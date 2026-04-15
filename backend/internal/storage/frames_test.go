package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreAndRetrieveLocal(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFrameStore(FrameStoreConfig{LocalPath: dir, DisableS3: true})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	ts := time.Unix(1700001234, 0).UTC()
	key, err := fs.Store(context.Background(), "dev_x", ts, []byte("hello"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if key == "" {
		t.Fatal("empty key")
	}
	if _, err := os.Stat(filepath.Join(dir, key)); err != nil {
		t.Fatalf("expected file at %s, err=%v", key, err)
	}
	data, err := fs.Retrieve(context.Background(), key)
	if err != nil || string(data) != "hello" {
		t.Fatalf("retrieve: %v %s", err, data)
	}
}

func TestStoreIdempotentSameSize(t *testing.T) {
	dir := t.TempDir()
	fs, _ := NewFrameStore(FrameStoreConfig{LocalPath: dir, DisableS3: true})
	defer fs.Close()
	ts := time.Unix(1700001234, 0).UTC()

	k1, _ := fs.Store(context.Background(), "dev_x", ts, []byte("hello"))
	k2, _ := fs.Store(context.Background(), "dev_x", ts, []byte("hello"))
	if k1 != k2 {
		t.Fatalf("expected same key, got %s vs %s", k1, k2)
	}
}

func TestStoreRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	fs, _ := NewFrameStore(FrameStoreConfig{LocalPath: dir, DisableS3: true})
	defer fs.Close()
	if _, err := fs.Store(context.Background(), "", time.Now(), []byte("x")); err == nil {
		t.Fatal("expected error for empty deviceID")
	}
	if _, err := fs.Store(context.Background(), "d", time.Now(), nil); err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestSanitizeIDPreventsTraversal(t *testing.T) {
	dir := t.TempDir()
	fs, _ := NewFrameStore(FrameStoreConfig{LocalPath: dir, DisableS3: true})
	defer fs.Close()
	key, err := fs.Store(context.Background(), "../escape", time.Now(), []byte("x"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	abs, _ := filepath.Abs(filepath.Join(dir, key))
	rootAbs, _ := filepath.Abs(dir)
	if !filepath.HasPrefix(abs, rootAbs) {
		t.Fatalf("escaped root: %s outside %s", abs, rootAbs)
	}
}
