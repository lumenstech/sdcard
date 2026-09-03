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

var (
	ErrDuplicate   = errors.New("duplicate frame")
	ErrRateLimited = errors.New("device rate limit exceeded")
	ErrQueueFull   = errors.New("pipeline queue full")
)

type FrameJob struct {
	DeviceID       string
	Timestamp      time.Time
	ImageData      []byte
	SidecarVersion string
	ReceivedAt     time.Time
}

type PipelineConfig struct {
	FrameStore         *storage.FrameStore
	DeviceStore        *device.Store
	Inference          *inference.Service
	Alerts             *alerts.Service
	Workers            int
	QueueSize          int
	RateLimitPerMinute int
}

type rateWindow struct {
	started time.Time
	count   int
}

type Pipeline struct {
	cfg      PipelineConfig
	queue    chan *FrameJob
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once
	rateMu   sync.Mutex
	rates    map[string]rateWindow
}

func NewPipeline(cfg PipelineConfig) *Pipeline {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1000
	}
	if cfg.RateLimitPerMinute <= 0 {
		cfg.RateLimitPerMinute = 120
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Pipeline{
		cfg:    cfg,
		queue:  make(chan *FrameJob, cfg.QueueSize),
		ctx:    ctx,
		cancel: cancel,
		rates:  make(map[string]rateWindow),
	}
}

func (p *Pipeline) Start() {
	for i := 0; i < p.cfg.Workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
	slog.Info("pipeline started", "workers", p.cfg.Workers, "queue_size", p.cfg.QueueSize, "rate_limit_per_minute", p.cfg.RateLimitPerMinute)
}

// Stop closes admission and drains accepted work before cancelling the shared context.
func (p *Pipeline) Stop() {
	p.stopOnce.Do(func() {
		close(p.queue)
		p.wg.Wait()
		p.cancel()
		slog.Info("pipeline stopped")
	})
}

func (p *Pipeline) allowDevice(deviceID string, now time.Time) bool {
	p.rateMu.Lock()
	defer p.rateMu.Unlock()
	window := p.rates[deviceID]
	if window.started.IsZero() || now.Sub(window.started) >= time.Minute {
		window = rateWindow{started: now, count: 0}
	}
	if window.count >= p.cfg.RateLimitPerMinute {
		p.rates[deviceID] = window
		return false
	}
	window.count++
	p.rates[deviceID] = window
	if len(p.rates) > 4096 {
		cutoff := now.Add(-2 * time.Minute)
		for id, candidate := range p.rates {
			if candidate.started.Before(cutoff) {
				delete(p.rates, id)
			}
		}
	}
	return true
}

// Submit applies shared admission policy for every transport: bounded per-device
// rate, durable idempotency claim, and a bounded in-memory processing queue.
func (p *Pipeline) Submit(ctx context.Context, job *FrameJob) error {
	if job == nil || job.DeviceID == "" {
		return errors.New("invalid frame job")
	}
	if !p.allowDevice(job.DeviceID, time.Now()) {
		return ErrRateLimited
	}
	claimed, err := p.cfg.DeviceStore.ClaimFrame(ctx, job.DeviceID, job.Timestamp)
	if err != nil {
		return err
	}
	if !claimed {
		return ErrDuplicate
	}
	select {
	case p.queue <- job:
		return nil
	default:
		_ = p.cfg.DeviceStore.ReleaseFrame(ctx, job.DeviceID, job.Timestamp)
		return fmt.Errorf("%w (%d/%d)", ErrQueueFull, len(p.queue), cap(p.queue))
	}
}

func (p *Pipeline) worker(id int) {
	defer p.wg.Done()
	for job := range p.queue {
		p.processFrame(p.ctx, job)
	}
}

func (p *Pipeline) processFrame(ctx context.Context, job *FrameJob) {
	start := time.Now()
	frameKey, err := p.cfg.FrameStore.Store(ctx, job.DeviceID, job.Timestamp, job.ImageData)
	if err != nil {
		slog.Error("frame store failed", "device", job.DeviceID, "error", err)
		return
	}

	result, err := p.cfg.Inference.Detect(ctx, job.ImageData)
	if err != nil {
		slog.Warn("inference unavailable; frame persisted without detection", "device", job.DeviceID, "error", err)
		if eventErr := p.cfg.DeviceStore.CreateEvent(ctx, &device.Event{
			DeviceID: job.DeviceID, FrameKey: frameKey, Type: "frame",
			Timestamp: job.Timestamp, ReceivedAt: job.ReceivedAt, ProcessedAt: time.Now().UTC(),
		}); eventErr != nil {
			slog.Error("frame event creation failed", "device", job.DeviceID, "error", eventErr)
		}
		return
	}

	if len(result.Detections) == 0 {
		if err := p.cfg.DeviceStore.CreateEvent(ctx, &device.Event{
			DeviceID: job.DeviceID, FrameKey: frameKey, Type: "frame",
			Timestamp: job.Timestamp, ReceivedAt: job.ReceivedAt, ProcessedAt: time.Now().UTC(),
		}); err != nil {
			slog.Error("frame event creation failed", "device", job.DeviceID, "error", err)
		}
	}

	for _, det := range result.Detections {
		event := &device.Event{
			DeviceID: job.DeviceID, FrameKey: frameKey, Type: "detection",
			Class: det.Class, Confidence: det.Confidence, BBox: det.BBox,
			Timestamp: job.Timestamp, ReceivedAt: job.ReceivedAt, ProcessedAt: time.Now().UTC(),
		}
		if err := p.cfg.DeviceStore.CreateEvent(ctx, event); err != nil {
			slog.Error("event creation failed", "device", job.DeviceID, "error", err)
			continue
		}
		if !det.ShouldAlert() {
			continue
		}
		dev, err := p.cfg.DeviceStore.FindByID(ctx, job.DeviceID)
		if err != nil || dev == nil {
			slog.Warn("alert skipped; device lookup failed", "device", job.DeviceID, "error", err)
			continue
		}
		if err := p.cfg.Alerts.Send(ctx, dev, formatAlert(dev, det, job.Timestamp), frameKey); err != nil {
			slog.Error("alert send failed", "device", job.DeviceID, "error", err)
		} else {
			slog.Info("alert sent", "device", job.DeviceID, "class", det.Class, "confidence", det.Confidence)
		}
	}

	slog.Debug("frame processed", "device", job.DeviceID, "elapsed_ms", time.Since(start).Milliseconds(), "detections", len(result.Detections))
}

func formatAlert(dev *device.Device, det inference.Detection, ts time.Time) string {
	location := "Unknown location"
	if dev != nil && dev.Label != "" {
		location = dev.Label
	}
	emoji := "warning"
	switch det.Class {
	case "person":
		emoji = "person"
	case "vehicle", "car", "truck":
		emoji = "vehicle"
	case "animal", "dog", "cat":
		emoji = "animal"
	}
	return fmt.Sprintf("%s: %s detected at %s - %s (%.0f%% confidence)", emoji, det.Class, location, ts.Format("3:04 PM"), det.Confidence*100)
}
