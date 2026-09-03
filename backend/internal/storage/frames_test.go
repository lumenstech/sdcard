package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreLocalFirstAndRetrieve(t *testing.T) {
	root := t.TempDir()
	store, err := NewFrameStore(FrameStoreConfig{LocalPath: root})
	if err != nil { t.Fatal(err) }
	defer store.Close()
	data := []byte("jpeg-test-data")
	ts := time.Unix(1700000000, 0).UTC()
	key, err := store.Store(context.Background(), "dev_test", ts, data)
	if err != nil { t.Fatal(err) }
	if key != "dev_test/2023-11-14/1700000000.jpg" { t.Fatalf("unexpected key %s", key) }
	got, err := store.Retrieve(context.Background(), key)
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(got, data) { t.Fatal("retrieved bytes differ") }
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(key))); err != nil { t.Fatalf("local evidence missing: %v", err) }
}

func TestRetrieveRejectsTraversal(t *testing.T) {
	store, err := NewFrameStore(FrameStoreConfig{LocalPath: t.TempDir()})
	if err != nil { t.Fatal(err) }
	defer store.Close()
	if _, err := store.Retrieve(context.Background(), "../../etc/passwd"); err == nil { t.Fatal("expected traversal rejection") }
}
