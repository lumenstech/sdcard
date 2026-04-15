// Package handlers implements the HTTP layer for the ingest service. It is
// deliberately built on stdlib net/http so the pilot has zero framework
// dependencies and predictable timeouts.
package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lumenstech/secure4k-sidecar/internal/device"
	"github.com/lumenstech/secure4k-sidecar/internal/ratelimit"
)

// MaxFrameBytes caps the body size of a frame upload. 5 MB matches the
// largest JPEG we'd realistically take from a 4K snapshot at quality 90.
const MaxFrameBytes int64 = 5 << 20

// MaxJSONBytes caps the body size of any JSON request (register, heartbeat).
const MaxJSONBytes int64 = 64 << 10

// AuthMiddleware enforces a bearer token from the configured allowlist using
// constant-time comparison. Empty tokens in the allowlist are ignored so an
// accidentally-empty VALID_API_KEYS env var can't accept "Bearer ".
func AuthMiddleware(validKeys []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || len(auth) <= 7 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing authorization"})
			return
		}
		token := auth[7:]

		valid := false
		for _, key := range validKeys {
			if key == "" {
				continue
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(key)) == 1 {
				valid = true
				break
			}
		}
		if !valid {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid api key"})
			return
		}
		next(w, r)
	}
}

// IngestFrame accepts a JPEG body with X-Device-ID and X-Timestamp headers
// and submits it to the processing pipeline. The handler is defensive:
//   - body size capped at MaxFrameBytes,
//   - per-device token-bucket rate limit applied before any work,
//   - submission to the pipeline is non-blocking; full queue returns 503.
func IngestFrame(pipeline *Pipeline, limiter *ratelimit.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.Header.Get("X-Device-ID")
		tsStr := r.Header.Get("X-Timestamp")
		version := r.Header.Get("X-Sidecar-Version")

		if deviceID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing X-Device-ID"})
			return
		}

		if limiter != nil && !limiter.Allow(deviceID) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
			return
		}

		var ts time.Time
		if epoch, err := strconv.ParseInt(tsStr, 10, 64); err == nil && epoch > 0 {
			ts = time.Unix(epoch, 0).UTC()
		} else {
			ts = time.Now().UTC()
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, MaxFrameBytes))
		_ = r.Body.Close()
		if err != nil {
			slog.Error("frame read failed", "error", err, "device", deviceID)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}
		if len(body) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty frame"})
			return
		}

		job := &FrameJob{
			DeviceID:       deviceID,
			Timestamp:      ts,
			ImageData:      body,
			SidecarVersion: version,
			ReceivedAt:     time.Now().UTC(),
		}

		if err := pipeline.Submit(job); err != nil {
			slog.Warn("pipeline full, frame dropped", "device", deviceID, "error", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "pipeline full, retry later"})
			return
		}

		slog.Info("frame ingested",
			"device", deviceID,
			"size_bytes", len(body),
			"ts", ts.Format(time.RFC3339),
			"version", version,
		)

		writeJSON(w, http.StatusAccepted, map[string]string{
			"status":   "accepted",
			"frame_ts": ts.Format(time.RFC3339),
		})
	}
}

// RegisterRequest is the JSON body posted by sidecar/register.sh.
type RegisterRequest struct {
	APIKey         string `json:"api_key"`
	MAC            string `json:"mac"`
	ChipInfo       string `json:"chip_info"`
	Kernel         string `json:"kernel"`
	MemKB          int    `json:"mem_kb"`
	SidecarVersion string `json:"sidecar_version"`
}

// RegisterDevice is idempotent on MAC. The store's INSERT ... ON CONFLICT (mac)
// path also acts as a backstop if FindByMAC races a second register.
func RegisterDevice(store device.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RegisterRequest
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if req.MAC == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing mac"})
			return
		}

		if existing, err := store.FindByMAC(r.Context(), req.MAC); err == nil && existing != nil {
			slog.Info("device re-registered", "device_id", existing.ID, "mac", req.MAC)
			writeJSON(w, http.StatusOK, map[string]string{
				"device_id": existing.ID,
				"status":    "existing",
			})
			return
		} else if err != nil && !errors.Is(err, device.ErrNotFound) {
			slog.Error("device lookup failed", "error", err, "mac", req.MAC)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
			return
		}

		dev, err := store.Create(r.Context(), &device.Device{
			MAC:            req.MAC,
			ChipInfo:       req.ChipInfo,
			Kernel:         req.Kernel,
			MemKB:          req.MemKB,
			SidecarVersion: req.SidecarVersion,
			Status:         "online",
			RegisteredAt:   time.Now().UTC(),
			LastHeartbeat:  time.Now().UTC(),
		})
		if err != nil {
			slog.Error("device registration failed", "error", err, "mac", req.MAC)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "registration failed"})
			return
		}

		slog.Info("device registered", "device_id", dev.ID, "mac", req.MAC, "chip", req.ChipInfo)
		writeJSON(w, http.StatusCreated, map[string]string{
			"device_id": dev.ID,
			"status":    "registered",
		})
	}
}

// HeartbeatRequest is the JSON body posted by upload_daemon.sh on its
// HEARTBEAT_EVERY tick.
type HeartbeatRequest struct {
	DeviceID       string `json:"device_id"`
	TS             int64  `json:"ts"`
	MemFreeKB      int    `json:"mem_free_kb"`
	DiskPct        int    `json:"disk_pct"`
	UptimeS        int    `json:"uptime_s"`
	BufferedFrames int    `json:"buffered_frames"`
	TempMC         int    `json:"temp_mc"`
	Version        string `json:"version"`
}

func DeviceHeartbeat(store device.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req HeartbeatRequest
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if req.DeviceID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing device_id"})
			return
		}

		ts := time.Unix(req.TS, 0).UTC()
		if req.TS <= 0 {
			ts = time.Now().UTC()
		}

		err := store.UpdateHeartbeat(r.Context(), req.DeviceID, &device.Heartbeat{
			Timestamp:      ts,
			MemFreeKB:      req.MemFreeKB,
			DiskPct:        req.DiskPct,
			UptimeS:        req.UptimeS,
			BufferedFrames: req.BufferedFrames,
			TempMC:         req.TempMC,
			Version:        req.Version,
		})
		if errors.Is(err, device.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not registered"})
			return
		}
		if err != nil {
			slog.Error("heartbeat update failed", "error", err, "device", req.DeviceID)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "heartbeat failed"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// ListDevices is a dashboard helper.
func ListDevices(store device.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		devices, err := store.List(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list devices"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"devices": devices,
			"count":   len(devices),
		})
	}
}

// ListEvents is a dashboard helper. Returns newest-first.
func ListEvents(store device.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		limit := 50
		if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 200 {
			limit = l
		}
		events, err := store.ListEvents(r.Context(), deviceID, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list events"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"events": events,
			"count":  len(events),
		})
	}
}

// ---- helpers ------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, MaxJSONBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}
