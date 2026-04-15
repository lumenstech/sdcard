package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/lumenstech/secure4k-sidecar/internal/alerts"
	"github.com/lumenstech/secure4k-sidecar/internal/device"
	"github.com/lumenstech/secure4k-sidecar/internal/inference"
	"github.com/lumenstech/secure4k-sidecar/internal/storage"
)

// FrameJob is a single frame submitted by the HTTP or MQTT ingest layer.
type FrameJob struct {
	DeviceID       string
	Timestamp      time.Time
	ImageData      []byte
	SidecarVersion string
	ReceivedAt     time.Time
}

// PipelineConfig wires the pipeline's dependencies.
type PipelineConfig struct {
	FrameStore  *storage.FrameStore
	DeviceStore device.Store
	Inference   *inference.Service
	Alerts      *alerts.Service

	Workers   int
	QueueSize int

	// FrameTimeout bounds the total processing time per frame so a stuck
	// downstream (slow Ollama, slow alert) cannot block a worker forever.
	FrameTimeout time.Duration
}

// Pipeline processes frames in three steps: store, infer, emit event/alert.
//
// Reliability guarantees:
//   - Submit is non-blocking; queue full returns an error to the caller.
//   - Each frame runs inside a per-frame context with FrameTimeout.
//   - Inference failures degrade gracefully: the frame is still stored and a
//     "frame" event is recorded so operators can see something arrived.
//   - Event creation is idempotent at the store layer; ErrDuplicateEvent is
//     treated as success and never re-triggers an alert.
//   - Stop drains the queue (close + wg.Wait) so in-flight frames complete.
type Pipeline struct {
	cfg    PipelineConfig
	queue  chan *FrameJob
	wg     sync.WaitGroup
	cancel context.CancelFunc

	// queue depth gauge for /healthz
	depthMu sync.RWMutex
	depth   int
}

func NewPipeline(cfg PipelineConfig) *Pipeline {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1000
	}
	if cfg.FrameTimeout <= 0 {
		cfg.FrameTimeout = 45 * time.Second
	}
	return &Pipeline{
		cfg:   cfg,
		queue: make(chan *FrameJob, cfg.QueueSize),
	}
}

func (p *Pipeline) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	for i := 0; i < p.cfg.Workers; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}
	slog.Info("pipeline started", "workers", p.cfg.Workers, "queue_size", p.cfg.QueueSize)
}

func (p *Pipeline) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	close(p.queue)
	p.wg.Wait()
	slog.Info("pipeline stopped")
}

// Submit enqueues a frame. Non-blocking: returns an error if the queue is full
// so the caller (HTTP or MQTT) can shed load gracefully (HTTP 503).
func (p *Pipeline) Submit(job *FrameJob) error {
	select {
	case p.queue <- job:
		p.bump(1)
		return nil
	default:
		return fmt.Errorf("pipeline queue full (%d/%d)", len(p.queue), cap(p.queue))
	}
}

// Depth returns the current queue depth for healthchecks.
func (p *Pipeline) Depth() int {
	p.depthMu.RLock()
	defer p.depthMu.RUnlock()
	return p.depth
}

func (p *Pipeline) bump(delta int) {
	p.depthMu.Lock()
	p.depth += delta
	p.depthMu.Unlock()
}

func (p *Pipeline) worker(ctx context.Context, id int) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-p.queue:
			if !ok {
				return
			}
			p.processFrame(ctx, job)
			p.bump(-1)
		}
	}
}

func (p *Pipeline) processFrame(parent context.Context, job *FrameJob) {
	ctx, cancel := context.WithTimeout(parent, p.cfg.FrameTimeout)
	defer cancel()
	start := time.Now()

	frameKey, err := p.cfg.FrameStore.Store(ctx, job.DeviceID, job.Timestamp, job.ImageData)
	if err != nil {
		slog.Error("frame store failed", "device", job.DeviceID, "error", err)
		return
	}

	// Inference is fail-open: any error (Ollama down, parse failure, timeout)
	// records the frame and exits without panicking the worker.
	result, infErr := p.cfg.Inference.Detect(ctx, job.ImageData)
	if infErr != nil {
		slog.Warn("inference unavailable, recording frame-only event",
			"device", job.DeviceID, "error", infErr)
		p.recordEvent(ctx, &device.Event{
			DeviceID:   job.DeviceID,
			FrameKey:   frameKey,
			Type:       "frame",
			Timestamp:  job.Timestamp,
			ReceivedAt: job.ReceivedAt,
		})
		return
	}

	if len(result.Detections) == 0 {
		// Still record a frame event so operators see liveness.
		p.recordEvent(ctx, &device.Event{
			DeviceID:    job.DeviceID,
			FrameKey:    frameKey,
			Type:        "frame",
			Timestamp:   job.Timestamp,
			ReceivedAt:  job.ReceivedAt,
			ProcessedAt: time.Now().UTC(),
		})
		slog.Debug("frame processed: no detections",
			"device", job.DeviceID, "elapsed_ms", time.Since(start).Milliseconds())
		return
	}

	for _, det := range result.Detections {
		event := &device.Event{
			DeviceID:    job.DeviceID,
			FrameKey:    frameKey,
			Type:        "detection",
			Class:       det.Class,
			Confidence:  det.Confidence,
			BBox:        det.BBox,
			Timestamp:   job.Timestamp,
			ReceivedAt:  job.ReceivedAt,
			ProcessedAt: time.Now().UTC(),
		}

		dup := p.recordEvent(ctx, event)
		if dup {
			// The same detection has already been processed (sidecar retry).
			// Skip alerting to avoid double-notifying the customer.
			continue
		}

		if det.ShouldAlert() {
			dev, _ := p.cfg.DeviceStore.FindByID(ctx, job.DeviceID)
			msg := formatAlert(dev, det, job.Timestamp)
			if err := p.cfg.Alerts.Send(ctx, dev, msg, frameKey); err != nil {
				slog.Error("alert send failed",
					"device", job.DeviceID, "class", det.Class, "error", err)
			} else {
				slog.Info("alert sent",
					"device", job.DeviceID, "class", det.Class, "confidence", det.Confidence)
			}
		}
	}

	slog.Debug("frame processed",
		"device", job.DeviceID,
		"elapsed_ms", time.Since(start).Milliseconds(),
		"detections", len(result.Detections),
		"inference_ms", result.InferenceMS,
	)
}

// recordEvent writes the event and returns true if it was a duplicate (so the
// caller can skip downstream side effects like alerting).
func (p *Pipeline) recordEvent(ctx context.Context, e *device.Event) bool {
	if err := p.cfg.DeviceStore.CreateEvent(ctx, e); err != nil {
		if errors.Is(err, device.ErrDuplicateEvent) {
			slog.Debug("duplicate event suppressed",
				"device", e.DeviceID, "type", e.Type, "class", e.Class)
			return true
		}
		slog.Error("event creation failed", "error", err, "device", e.DeviceID)
	}
	return false
}

func formatAlert(dev *device.Device, det inference.Detection, ts time.Time) string {
	location := "Unknown location"
	if dev != nil && dev.Label != "" {
		location = dev.Label
	}
	emoji := "⚠️"
	switch det.Class {
	case "person":
		emoji = "🚨"
	case "vehicle", "car", "truck":
		emoji = "🚗"
	case "animal", "dog", "cat":
		emoji = "🐾"
	}
	return fmt.Sprintf(
		"%s %s detected at %s — %s (%.0f%% confidence)",
		emoji, det.Class, location,
		ts.Format("3:04 PM"), det.Confidence*100,
	)
}
