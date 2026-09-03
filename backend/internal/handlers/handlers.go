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
	"time"

	"github.com/lumenstech/secure4k-sidecar/internal/device"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func AuthMiddleware(validKeys []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			writeJSONError(w, http.StatusUnauthorized, "missing authorization")
			return
		}
		token := auth[7:]
		valid := false
		for _, key := range validKeys {
			if key != "" && len(token) == len(key) && subtle.ConstantTimeCompare([]byte(token), []byte(key)) == 1 {
				valid = true
			}
		}
		if !valid {
			writeJSONError(w, http.StatusUnauthorized, "invalid api key")
			return
		}
		next(w, r)
	}
}

func IngestFrame(pipeline *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.Header.Get("X-Device-ID")
		if deviceID == "" {
			writeJSONError(w, http.StatusBadRequest, "missing X-Device-ID")
			return
		}
		tsStr := r.Header.Get("X-Timestamp")
		epoch, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil || epoch <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid X-Timestamp")
			return
		}
		ts := time.Unix(epoch, 0).UTC()

		const maxFrame = 5 << 20
		body, err := io.ReadAll(io.LimitReader(r.Body, maxFrame+1))
		_ = r.Body.Close()
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "failed to read body")
			return
		}
		if len(body) == 0 {
			writeJSONError(w, http.StatusBadRequest, "empty frame")
			return
		}
		if len(body) > maxFrame {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "frame exceeds 5MB limit")
			return
		}

		job := &FrameJob{DeviceID: deviceID, Timestamp: ts, ImageData: body, SidecarVersion: r.Header.Get("X-Sidecar-Version"), ReceivedAt: time.Now().UTC()}
		err = pipeline.Submit(r.Context(), job)
		switch {
		case err == nil:
			slog.Info("frame accepted", "device", deviceID, "size_bytes", len(body), "ts", ts)
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "frame_ts": ts.Format(time.RFC3339)})
		case errors.Is(err, ErrDuplicate):
			slog.Info("duplicate frame acknowledged", "device", deviceID, "ts", ts)
			writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate", "frame_ts": ts.Format(time.RFC3339)})
		case errors.Is(err, ErrRateLimited):
			w.Header().Set("Retry-After", "60")
			writeJSONError(w, http.StatusTooManyRequests, "device rate limit exceeded")
		case errors.Is(err, ErrQueueFull):
			w.Header().Set("Retry-After", "5")
			writeJSONError(w, http.StatusServiceUnavailable, "pipeline full, retry later")
		default:
			slog.Error("frame admission failed", "device", deviceID, "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "frame admission failed")
		}
	}
}

type RegisterRequest struct {
	APIKey         string `json:"api_key"`
	MAC            string `json:"mac"`
	ChipInfo       string `json:"chip_info"`
	Kernel         string `json:"kernel"`
	MemKB          int    `json:"mem_kb"`
	SidecarVersion string `json:"sidecar_version"`
}

func RegisterDevice(store *device.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RegisterRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if req.MAC == "" || req.MAC == "00:00:00:00:00:00" {
			writeJSONError(w, http.StatusBadRequest, "valid mac is required")
			return
		}
		if existing, err := store.FindByMAC(r.Context(), req.MAC); err == nil && existing != nil {
			writeJSON(w, http.StatusOK, map[string]string{"device_id": existing.ID, "status": "existing"})
			return
		}
		dev, err := store.Create(r.Context(), &device.Device{
			MAC: req.MAC, ChipInfo: req.ChipInfo, Kernel: req.Kernel, MemKB: req.MemKB,
			SidecarVersion: req.SidecarVersion, Status: "online", RegisteredAt: time.Now().UTC(), LastHeartbeat: time.Now().UTC(),
		})
		if err != nil {
			slog.Error("device registration failed", "error", err, "mac", req.MAC)
			writeJSONError(w, http.StatusInternalServerError, "registration failed")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"device_id": dev.ID, "status": "registered"})
	}
}

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

func DeviceHeartbeat(store *device.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req HeartbeatRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil || req.DeviceID == "" {
			writeJSONError(w, http.StatusBadRequest, "invalid heartbeat")
			return
		}
		stamp := time.Now().UTC()
		if req.TS > 0 {
			stamp = time.Unix(req.TS, 0).UTC()
		}
		err := store.UpdateHeartbeat(r.Context(), req.DeviceID, &device.Heartbeat{
			Timestamp: stamp, MemFreeKB: req.MemFreeKB, DiskPct: req.DiskPct, UptimeS: req.UptimeS,
			BufferedFrames: req.BufferedFrames, TempMC: req.TempMC, Version: req.Version,
		})
		if err != nil {
			slog.Error("heartbeat update failed", "error", err, "device", req.DeviceID)
			writeJSONError(w, http.StatusInternalServerError, "heartbeat failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func OTACheck(bundlePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentVersion := r.Header.Get("X-Current-Version")
		latestVersion := "0.1.0"
		if currentVersion == latestVersion {
			writeJSON(w, http.StatusOK, map[string]string{"version": currentVersion})
			return
		}
		// Pilot deliberately does not advertise an OTA artifact until a signed/checksummed
		// release bundle is provisioned. Returning current avoids unsafe partial updates.
		writeJSON(w, http.StatusOK, map[string]string{"version": currentVersion})
		_ = bundlePath
	}
}

func ListDevices(store *device.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		devices, err := store.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to list devices")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": devices, "count": len(devices)})
	}
}

func ListEvents(store *device.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 200 {
			limit = l
		}
		events, err := store.ListEvents(r.Context(), r.URL.Query().Get("device_id"), limit)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to list events")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events, "count": len(events)})
	}
}

var _ = fmt.Sprintf
