package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestOTACheckNoManifestReturnsCurrent(t *testing.T) {
	dir := t.TempDir()
	h := OTACheck(dir)
	r := httptest.NewRequest("GET", "/v1/ota/check", nil)
	r.Header.Set("X-Current-Version", "0.1.0")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	var got OTAManifest
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.Version != "0.1.0" || got.URL != "" {
		t.Fatalf("expected echoed version with no URL, got %+v", got)
	}
}

func TestOTACheckServesManifestAndComputesSum(t *testing.T) {
	dir := t.TempDir()
	bundleData := []byte("fake-tarball")
	bundlePath := filepath.Join(dir, "sidecar-0.2.0.tar.gz")
	if err := os.WriteFile(bundlePath, bundleData, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := OTAManifest{
		Version: "0.2.0",
		URL:     "/v1/ota/bundle/sidecar-0.2.0.tar.gz",
	}
	mb, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o644); err != nil {
		t.Fatal(err)
	}

	h := OTACheck(dir)
	r := httptest.NewRequest("GET", "/v1/ota/check", nil)
	r.Header.Set("X-Current-Version", "0.1.0")
	w := httptest.NewRecorder()
	h(w, r)

	var got OTAManifest
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.Version != "0.2.0" {
		t.Fatalf("version: %+v", got)
	}
	sum := sha256.Sum256(bundleData)
	if got.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 mismatch: got %s want %s", got.SHA256, hex.EncodeToString(sum[:]))
	}
}

func TestOTADownloadServesFile(t *testing.T) {
	dir := t.TempDir()
	bundleData := []byte("data")
	if err := os.WriteFile(filepath.Join(dir, "x.tar.gz"), bundleData, 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ota/bundle/{name}", OTADownload(dir))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/ota/bundle/x.tar.gz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("err=%v code=%d", err, resp.StatusCode)
	}
	defer resp.Body.Close()
}

func TestOTADownloadRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ota/bundle/{name}", OTADownload(dir))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	// {name} captures a single segment; a traversal attempt resolves to basename.
	resp, _ := http.Get(srv.URL + "/v1/ota/bundle/anything")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
