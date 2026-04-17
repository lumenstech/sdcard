package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type FrameStoreConfig struct {
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
	LocalPath   string
}

type FrameStore struct {
	cfg FrameStoreConfig
	// TODO: Add S3 client (minio-go) when deploying to cloud
	// For pilot, use local filesystem storage
}

func NewFrameStore(cfg FrameStoreConfig) (*FrameStore, error) {
	// Ensure local path exists
	if err := os.MkdirAll(cfg.LocalPath, 0755); err != nil {
		return nil, fmt.Errorf("create local frame path: %w", err)
	}
	return &FrameStore{cfg: cfg}, nil
}

// Store saves a frame and returns the storage key.
// Key format: {device_id}/{date}/{timestamp}.jpg
func (fs *FrameStore) Store(ctx context.Context, deviceID string, ts time.Time, data []byte) (string, error) {
	dateDir := ts.Format("2006-01-02")
	key := fmt.Sprintf("%s/%s/%d.jpg", deviceID, dateDir, ts.Unix())

	// Local storage for pilot
	fullPath := filepath.Join(fs.cfg.LocalPath, deviceID, dateDir)
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		return "", fmt.Errorf("create frame dir: %w", err)
	}

	filename := filepath.Join(fullPath, fmt.Sprintf("%d.jpg", ts.Unix()))
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return "", fmt.Errorf("write frame: %w", err)
	}

	// TODO: Upload to S3 in parallel for cloud backup
	// client.PutObject(ctx, fs.cfg.S3Bucket, key, bytes.NewReader(data), ...)

	return key, nil
}

// Retrieve gets a frame by storage key.
func (fs *FrameStore) Retrieve(ctx context.Context, key string) ([]byte, error) {
	fullPath := filepath.Join(fs.cfg.LocalPath, key)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("read frame: %w", err)
	}
	return data, nil
}

// Cleanup removes frames older than the retention period.
func (fs *FrameStore) Cleanup(ctx context.Context, retention time.Duration) (int, error) {
	cutoff := time.Now().Add(-retention)
	removed := 0

	err := filepath.Walk(fs.cfg.LocalPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(path)
			removed++
		}
		return nil
	})

	return removed, err
}
