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
	LocalPath   string
}

type uploadJob struct {
	key  string
	data []byte
}

type FrameStore struct {
	cfg    FrameStoreConfig
	client *minio.Client
	queue  chan uploadJob
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewFrameStore(cfg FrameStoreConfig) (*FrameStore, error) {
	if cfg.LocalPath == "" {
		return nil, errors.New("local frame path is required")
	}
	if err := os.MkdirAll(cfg.LocalPath, 0755); err != nil {
		return nil, fmt.Errorf("create local frame path: %w", err)
	}

	fs := &FrameStore{cfg: cfg, queue: make(chan uploadJob, 256)}
	if cfg.S3Endpoint != "" && cfg.S3Bucket != "" {
		u, err := url.Parse(cfg.S3Endpoint)
		if err != nil {
			return nil, fmt.Errorf("parse S3 endpoint: %w", err)
		}
		endpoint := u.Host
		if endpoint == "" {
			endpoint = strings.TrimPrefix(strings.TrimPrefix(cfg.S3Endpoint, "http://"), "https://")
		}
		client, err := minio.New(endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.S3AccessKey, cfg.S3SecretKey, ""),
			Secure: u.Scheme == "https",
			Region: cfg.S3Region,
		})
		if err != nil {
			return nil, fmt.Errorf("create S3 client: %w", err)
		}
		fs.client = client
	}

	ctx, cancel := context.WithCancel(context.Background())
	fs.cancel = cancel
	fs.wg.Add(1)
	go fs.uploadWorker(ctx)
	return fs, nil
}

func (fs *FrameStore) Close() {
	if fs.cancel != nil {
		fs.cancel()
	}
	fs.wg.Wait()
}

// Store writes to local durable storage first, then queues a bounded S3 upload.
// If the S3 queue is saturated the local evidence remains intact and the loss of
// remote redundancy is visible in logs rather than dropping the frame.
func (fs *FrameStore) Store(ctx context.Context, deviceID string, ts time.Time, data []byte) (string, error) {
	dateDir := ts.UTC().Format("2006-01-02")
	key := fmt.Sprintf("%s/%s/%d.jpg", deviceID, dateDir, ts.Unix())
	fullPath := filepath.Join(fs.cfg.LocalPath, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", fmt.Errorf("create frame dir: %w", err)
	}

	tmp := fullPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return "", fmt.Errorf("write temporary frame: %w", err)
	}
	if err := os.Rename(tmp, fullPath); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("commit frame: %w", err)
	}

	if fs.client != nil {
		job := uploadJob{key: key, data: append([]byte(nil), data...)}
		select {
		case fs.queue <- job:
		default:
			slog.Error("s3 upload queue full; frame retained locally", "key", key, "queue_len", len(fs.queue))
		}
	}
	return key, nil
}

func (fs *FrameStore) uploadWorker(ctx context.Context) {
	defer fs.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-fs.queue:
			fs.uploadWithRetry(ctx, job)
		}
	}
}

func (fs *FrameStore) uploadWithRetry(parent context.Context, job uploadJob) {
	if fs.client == nil {
		return
	}
	backoff := time.Second
	for attempt := 1; attempt <= 5; attempt++ {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		_, err := fs.client.PutObject(ctx, fs.cfg.S3Bucket, job.key, bytes.NewReader(job.data), int64(len(job.data)), minio.PutObjectOptions{ContentType: "image/jpeg"})
		cancel()
		if err == nil {
			slog.Debug("frame uploaded to s3", "key", job.key, "attempt", attempt)
			return
		}
		slog.Warn("s3 upload failed", "key", job.key, "attempt", attempt, "error", err)
		if attempt == 5 {
			break
		}
		select {
		case <-parent.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 8*time.Second {
			backoff *= 2
		}
	}
	slog.Error("s3 upload exhausted retries; frame retained locally", "key", job.key)
}

func (fs *FrameStore) Retrieve(ctx context.Context, key string) ([]byte, error) {
	cleanKey := filepath.Clean(filepath.FromSlash(key))
	if strings.HasPrefix(cleanKey, "..") || filepath.IsAbs(cleanKey) {
		return nil, errors.New("invalid frame key")
	}
	fullPath := filepath.Join(fs.cfg.LocalPath, cleanKey)
	if data, err := os.ReadFile(fullPath); err == nil {
		return data, nil
	}
	if fs.client == nil {
		return nil, fmt.Errorf("frame not found locally and s3 is disabled: %s", key)
	}
	getCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	obj, err := fs.client.GetObject(getCtx, fs.cfg.S3Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get frame from s3: %w", err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("read s3 frame: %w", err)
	}
	return data, nil
}

func (fs *FrameStore) S3Health(ctx context.Context) error {
	if fs.client == nil {
		return errors.New("s3 disabled")
	}
	healthCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	exists, err := fs.client.BucketExists(healthCtx, fs.cfg.S3Bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("bucket %s does not exist", fs.cfg.S3Bucket)
	}
	return nil
}

func (fs *FrameStore) Cleanup(ctx context.Context, retention time.Duration) (int, error) {
	cutoff := time.Now().Add(-retention)
	removed := 0
	err := filepath.Walk(fs.cfg.LocalPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !info.ModTime().Before(cutoff) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		removed++
		return nil
	})
	return removed, err
}
