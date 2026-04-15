package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OTAManifest describes the latest sidecar bundle the backend is serving.
//
// The backend reads `<OTA_BUNDLE_PATH>/manifest.json` if present, falling
// back to "no update available" otherwise. Sidecars compare the version
// against their own and, on mismatch, download `URL` and verify against
// `SHA256` before applying.
type OTAManifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Notes   string `json:"notes,omitempty"`
}

// OTACheck returns the current manifest. A missing manifest returns a 200 with
// the device's reported current version echoed back, so the sidecar treats it
// as "no update" rather than as a failure.
func OTACheck(bundlePath string) http.HandlerFunc {
	cache := &otaCache{path: filepath.Join(bundlePath, "manifest.json")}
	return func(w http.ResponseWriter, r *http.Request) {
		current := r.Header.Get("X-Current-Version")
		deviceID := r.Header.Get("X-Device-ID")

		manifest := cache.read()
		if manifest == nil {
			writeJSON(w, http.StatusOK, OTAManifest{Version: current})
			return
		}

		slog.Debug("ota check", "device", deviceID, "current", current, "latest", manifest.Version)
		writeJSON(w, http.StatusOK, manifest)
	}
}

// OTADownload streams the bundle file from disk. The route is mounted at
// /v1/ota/bundle/{name} and only files under bundlePath are served.
func OTADownload(bundlePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Defence against path traversal: only basename allowed.
		name := filepath.Base(r.PathValue("name"))
		if name == "" || name == "." || name == "/" {
			http.Error(w, "bad name", http.StatusBadRequest)
			return
		}
		full := filepath.Join(bundlePath, name)
		f, err := os.Open(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = io.Copy(w, f)
	}
}

// otaCache rereads the manifest file at most every otaRefresh to avoid
// hammering the disk on every sidecar poll. It also recomputes the bundle's
// sha256 lazily if SHA256 is empty in the manifest.
type otaCache struct {
	path     string
	mu       sync.Mutex
	manifest *OTAManifest
	loaded   time.Time
}

const otaRefresh = 30 * time.Second

func (c *otaCache) read() *OTAManifest {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.manifest != nil && time.Since(c.loaded) < otaRefresh {
		return c.manifest
	}

	data, err := os.ReadFile(c.path)
	if err != nil {
		c.manifest = nil
		c.loaded = time.Now()
		return nil
	}

	m := &OTAManifest{}
	if err := jsonUnmarshal(data, m); err != nil {
		slog.Warn("ota manifest parse failed", "error", err)
		c.manifest = nil
		c.loaded = time.Now()
		return nil
	}
	if m.Version == "" {
		c.manifest = nil
		c.loaded = time.Now()
		return nil
	}
	if m.SHA256 == "" && m.URL != "" && strings.HasPrefix(m.URL, "/v1/ota/bundle/") {
		bundleFile := filepath.Join(filepath.Dir(c.path), filepath.Base(m.URL))
		if sum, err := sha256File(bundleFile); err == nil {
			m.SHA256 = sum
		}
	}
	c.manifest = m
	c.loaded = time.Now()
	return c.manifest
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
