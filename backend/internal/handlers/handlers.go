package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/lumenstech/secure4k-sidecar/internal/device"
)

// ---- Auth middleware -----------------------------------------

func AuthMiddleware(validKeys []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
			return
		}
		token := auth[7:]

		valid := false
		for _, key := range validKeys {
			if key != "" && subtle.ConstantTimeCompare([]byte(token), []byte(key)) == 1 {
				valid = true
				break
			}
		}
		if !valid {
			http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

// ---- Frame ingest -------------------------------------------

func IngestFrame(pipeline *Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.Header.Get("X-Device-ID")
		tsStr := r.Header.Get("X-Timestamp")
		version := r.Header.Get("X-Sidecar-Version")

		if deviceID == "" {
			http.Error(w, `{"error":"missing X-Device-ID"}`, http.StatusBadRequest)
			return
		}

		// Parse timestamp (unix epoch from filename)
		var ts time.Time
		if epoch, err := strconv.ParseInt(tsStr, 10, 64); err == nil {
			ts = time.Unix(epoch, 0)
		} else {
			ts = time.Now().UTC()
		}

		// Read frame body (limit to 5MB)
		body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
		if err != nil {
			slog.Error("failed to read frame body", "error", err, "device", deviceID)
			http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		if len(body) == 0 {
			http.Error(w, `{"error":"empty frame"}`, http.StatusBadRequest)
			return
		}

		// Submit to processing pipeline
		frame := &FrameJob{
			DeviceID:       deviceID,
			Timestamp:      ts,
			ImageData:      body,
			SidecarVersion: version,
			ReceivedAt:     time.Now().UTC(),
		}

		if err := pipeline.Submit(frame); err != nil {
			slog.Warn("pipeline full, frame dropped", "device", deviceID, "error", err)
			http.Error(w, `{"error":"pipeline full, retry later"}`, http.StatusServiceUnavailable)
			return
		}

		slog.Info("frame ingested",
			"device", deviceID,
			"size_bytes", len(body),
			"ts", ts.Format(time.RFC3339),
			"version", version,
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"status":"accepted","frame_ts":"%s"}`, ts.Format(time.RFC3339))
	}
}

// ---- Device registration ------------------------------------

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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}

		// Check for existing device by MAC (idempotent registration)
		existing, err := store.FindByMAC(r.Context(), req.MAC)
		if err == nil && existing != nil {
			slog.Info("device re-registered", "device_id", existing.ID, "mac", req.MAC)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"device_id": existing.ID,
				"status":    "existing",
			})
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
			http.Error(w, `{"error":"registration failed"}`, http.StatusInternalServerError)
			return
		}

		slog.Info("device registered", "device_id", dev.ID, "mac", req.MAC, "chip", req.ChipInfo)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"device_id": dev.ID,
			"status":    "registered",
		})
	}
}

// ---- Device heartbeat ---------------------------------------

type HeartbeatRequest struct {
	DeviceID      string `json:"device_id"`
	TS            int64  `json:"ts"`
	MemFreeKB     int    `json:"mem_free_kb"`
	DiskPct       int    `json:"disk_pct"`
	UptimeS       int    `json:"uptime_s"`
	BufferedFrames int   `json:"buffered_frames"`
	TempMC        int    `json:"temp_mc"`
	Version       string `json:"version"`
}

func DeviceHeartbeat(store *device.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req HeartbeatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}

		err := store.UpdateHeartbeat(r.Context(), req.DeviceID, &device.Heartbeat{
			Timestamp:      time.Unix(req.TS, 0),
			MemFreeKB:      req.MemFreeKB,
			DiskPct:        req.DiskPct,
			UptimeS:        req.UptimeS,
			BufferedFrames: req.BufferedFrames,
			TempMC:         req.TempMC,
			Version:        req.Version,
		})
		if err != nil {
			slog.Error("heartbeat update failed", "error", err, "device", req.DeviceID)
			http.Error(w, `{"error":"heartbeat failed"}`, http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok"}`)
	}
}

// ---- OTA check ----------------------------------------------

func OTACheck(bundlePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		currentVersion := r.Header.Get("X-Current-Version")
		deviceID := r.Header.Get("X-Device-ID")

		// TODO: Check bundlePath for latest version tarball
		// For now, return no update
		latestVersion := "0.1.0"

		slog.Debug("ota check", "device", deviceID, "current", currentVersion, "latest", latestVersion)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"version": latestVersion,
		})
	}
}

// ---- Dashboard endpoints ------------------------------------

func ListDevices(store *device.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		devices, err := store.List(r.Context())
		if err != nil {
			http.Error(w, `{"error":"failed to list devices"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"devices": devices,
			"count":   len(devices),
		})
	}
}

func ListEvents(store *device.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.URL.Query().Get("device_id")
		limitStr := r.URL.Query().Get("limit")
		limit := 50
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 200 {
			limit = l
		}

		events, err := store.ListEvents(r.Context(), deviceID, limit)
		if err != nil {
			http.Error(w, `{"error":"failed to list events"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"events": events,
			"count":  len(events),
		})
	}
}
