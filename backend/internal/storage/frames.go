// Package storage persists raw frames. The pilot writes locally first (fast,
// always-available) and then uploads asynchronously to S3/MinIO with bounded
// exponential backoff. If S3 is unreachable the frame is still safe on the
// local volume and the operator can replay later.
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type FrameStoreConfig struct {
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
	S3UseSSL    bool

	LocalPath string

	// UploadWorkers controls the size of the async S3 upload worker pool.
	UploadWorkers int
	// UploadQueueSize bounds backpressure: when full, frames are still stored
	// locally but not enqueued for S3 (operator can replay later).
	UploadQueueSize int

	// Optional, mainly for tests: short-circuit S3 entirely.
	DisableS3 bool
}

// FrameStore stores frames locally and (best-effort) on S3.
type FrameStore struct {
	cfg    FrameStoreConfig
	s3     *minio.Client
	useS3  bool
	queue  chan uploadJob
	wg     sync.WaitGroup
	closed chan struct{}
}

type uploadJob struct {
	key  string
	data []byte
}

// NewFrameStore wires the local directory and best-effort S3 client. Failure
// to reach S3 is logged but not fatal — the store still works as a
// local-only durable buffer.
func NewFrameStore(cfg FrameStoreConfig) (*FrameStore, error) {
	if cfg.LocalPath == "" {
		return nil, errors.New("LocalPath required")
	}
	if err := os.MkdirAll(cfg.LocalPath, 0o755); err != nil {
		return nil, fmt.Errorf("create local frame path: %w", err)
	}
	if cfg.UploadWorkers <= 0 {
		cfg.UploadWorkers = 2
	}
	if cfg.UploadQueueSize <= 0 {
		cfg.UploadQueueSize = 256
	}

	fs := &FrameStore{
		cfg:    cfg,
		queue:  make(chan uploadJob, cfg.UploadQueueSize),
		closed: make(chan struct{}),
	}

	if !cfg.DisableS3 && cfg.S3Endpoint != "" && cfg.S3Bucket != "" {
		host, secure, err := parseEndpoint(cfg.S3Endpoint)
		if err != nil {
			slog.Warn("s3 endpoint parse failed; running local-only", "error", err)
		} else {
			cli, err := minio.New(host, &minio.Options{
				Creds:  credentials.NewStaticV4(cfg.S3AccessKey, cfg.S3SecretKey, ""),
				Secure: secure || cfg.S3UseSSL,
				Region: cfg.S3Region,
			})
			if err != nil {
				slog.Warn("s3 client init failed; running local-only", "error", err)
			} else {
				fs.s3 = cli
				fs.useS3 = true
				// Best-effort bucket bootstrap.
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				exists, _ := cli.BucketExists(ctx, cfg.S3Bucket)
				if !exists {
					if err := cli.MakeBucket(ctx, cfg.S3Bucket, minio.MakeBucketOptions{Region: cfg.S3Region}); err != nil {
						slog.Warn("s3 bucket create failed; will retry on first upload", "error", err)
					}
				}
				for i := 0; i < cfg.UploadWorkers; i++ {
					fs.wg.Add(1)
					go fs.uploadWorker(i)
				}
			}
		}
	}

	if !fs.useS3 {
		slog.Info("frame store running local-only", "path", cfg.LocalPath)
	}
	return fs, nil
}

// Close drains the upload queue. Safe to call once.
func (fs *FrameStore) Close() {
	select {
	case <-fs.closed:
		return
	default:
	}
	close(fs.closed)
	close(fs.queue)
	fs.wg.Wait()
}

// Store writes the frame to local disk synchronously and enqueues an async
// upload to S3 (best-effort). Returns the canonical storage key.
//
// Key format: {device_id}/{date}/{unix}.jpg — stable for retrieval and
// human-grep friendly.
func (fs *FrameStore) Store(ctx context.Context, deviceID string, ts time.Time, data []byte) (string, error) {
	if deviceID == "" {
		return "", errors.New("deviceID required")
	}
	if len(data) == 0 {
		return "", errors.New("empty frame")
	}
	deviceID = sanitizeID(deviceID)
	dateDir := ts.UTC().Format("2006-01-02")
	key := fmt.Sprintf("%s/%s/%d.jpg", deviceID, dateDir, ts.UTC().Unix())

	dir := filepath.Join(fs.cfg.LocalPath, deviceID, dateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create frame dir: %w", err)
	}
	full := filepath.Join(fs.cfg.LocalPath, key)

	// Idempotent local write: if the file already exists with the same size,
	// don't rewrite. This makes sidecar retries cheap.
	if info, err := os.Stat(full); err == nil && info.Size() == int64(len(data)) {
		fs.enqueueUpload(key, data)
		return key, nil
	}

	tmp := full + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", fmt.Errorf("write frame: %w", err)
	}
	if err := os.Rename(tmp, full); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("rename frame: %w", err)
	}

	fs.enqueueUpload(key, data)
	return key, nil
}

func (fs *FrameStore) enqueueUpload(key string, data []byte) {
	if !fs.useS3 {
		return
	}
	// Copy data because the caller may reuse the buffer.
	cp := make([]byte, len(data))
	copy(cp, data)
	select {
	case fs.queue <- uploadJob{key: key, data: cp}:
	default:
		slog.Warn("s3 upload queue full, frame stored locally only", "key", key)
	}
}

// Retrieve looks the key up locally first, then falls back to S3.
func (fs *FrameStore) Retrieve(ctx context.Context, key string) ([]byte, error) {
	full := filepath.Join(fs.cfg.LocalPath, key)
	if data, err := os.ReadFile(full); err == nil {
		return data, nil
	}
	if !fs.useS3 {
		return nil, fmt.Errorf("retrieve: not found locally and S3 disabled")
	}
	obj, err := fs.s3.GetObject(ctx, fs.cfg.S3Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("s3 get: %w", err)
	}
	defer obj.Close()
	return io.ReadAll(obj)
}

// Cleanup removes local files older than retention. S3 copies are kept.
// Returns the count removed.
func (fs *FrameStore) Cleanup(ctx context.Context, retention time.Duration) (int, error) {
	cutoff := time.Now().Add(-retention)
	removed := 0
	err := filepath.Walk(fs.cfg.LocalPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			if rmErr := os.Remove(path); rmErr == nil {
				removed++
			}
		}
		return nil
	})
	return removed, err
}

func (fs *FrameStore) uploadWorker(id int) {
	defer fs.wg.Done()
	for job := range fs.queue {
		fs.uploadWithRetry(job)
	}
}

func (fs *FrameStore) uploadWithRetry(job uploadJob) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	const maxAttempts = 6 // ~1+2+4+8+16+30 = ~61s of attempts then give up.

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_, err := fs.s3.PutObject(ctx, fs.cfg.S3Bucket, job.key,
			bytes.NewReader(job.data), int64(len(job.data)),
			minio.PutObjectOptions{ContentType: "image/jpeg"})
		cancel()
		if err == nil {
			return
		}
		slog.Warn("s3 upload failed, retrying",
			"key", job.key, "attempt", attempt, "error", err)

		select {
		case <-fs.closed:
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	slog.Error("s3 upload abandoned after max retries; frame remains on local disk only",
		"key", job.key)
}

func parseEndpoint(s string) (string, bool, error) {
	if !strings.Contains(s, "://") {
		return s, false, nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", false, err
	}
	host := u.Host
	if host == "" {
		host = u.Path
	}
	return host, u.Scheme == "https", nil
}

// sanitizeID strips path separators so a malicious device_id can't escape the
// storage root.
func sanitizeID(id string) string {
	id = strings.ReplaceAll(id, "/", "_")
	id = strings.ReplaceAll(id, "..", "_")
	return id
}
