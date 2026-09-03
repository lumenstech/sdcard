package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/lumenstech/secure4k-sidecar/internal/alerts"
	"github.com/lumenstech/secure4k-sidecar/internal/device"
	"github.com/lumenstech/secure4k-sidecar/internal/inference"
	"github.com/lumenstech/secure4k-sidecar/internal/storage"
)

// FrameJob represents a single frame to process.
type FrameJob struct {
	DeviceID       string
	Timestamp      time.Time
	ImageData      []byte
	SidecarVersion string
	ReceivedAt     time.Time
}

// PipelineConfig holds dependencies for the processing pipeline.
type PipelineConfig struct {
	FrameStore  *storage.FrameStore
	DeviceStore *device.Store
	Inference   *inference.Service
	Alerts      *alerts.Service
	Workers     int
	QueueSize   int
}

// Pipeline processes incoming frames through:
//  1. Store frame to S3/local
//  2. Run inference (person/vehicle/animal detection)
//  3. If detection > threshold, create event + send alert
type Pipeline struct {
	cfg    PipelineConfig
	queue  chan *FrameJob
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func NewPipeline(cfg PipelineConfig) *Pipeline {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1000
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
	p.cancel()
	close(p.queue)
	p.wg.Wait()
	slog.Info("pipeline stopped")
}

func (p *Pipeline) Submit(job *FrameJob) error {
	select {
	case p.queue <- job:
		return nil
	default:
		return fmt.Errorf("pipeline queue full (%d/%d)", len(p.queue), cap(p.queue))
	}
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
		}
	}
}

func (p *Pipeline) processFrame(ctx context.Context, job *FrameJob) {
	start := time.Now()

	// Step 1: Store the raw frame
	frameKey, err := p.cfg.FrameStore.Store(ctx, job.DeviceID, job.Timestamp, job.ImageData)
	if err != nil {
		slog.Error("frame store failed",
			"device", job.DeviceID,
			"error", err,
		)
		return
	}

	// Step 2: Run inference
	result, err := p.cfg.Inference.Detect(ctx, job.ImageData)
	if err != nil {
		slog.Warn("inference failed, storing frame without detection",
			"device", job.DeviceID,
			"error", err,
		)
		// Store frame-only event (no detection)
		p.cfg.DeviceStore.CreateEvent(ctx, &device.Event{
			DeviceID:   job.DeviceID,
			FrameKey:   frameKey,
			Type:       "frame",
			Timestamp:  job.Timestamp,
			ReceivedAt: job.ReceivedAt,
		})
		return
	}

	// Step 3: Evaluate detections
	for _, det := range result.Detections {
		slog.Info("detection",
			"device", job.DeviceID,
			"class", det.Class,
			"confidence", det.Confidence,
			"bbox", det.BBox,
		)

		// Create event record
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

		if err := p.cfg.DeviceStore.CreateEvent(ctx, event); err != nil {
			slog.Error("event creation failed", "error", err)
			continue
		}

		// Step 4: Trigger alert for high-confidence person/vehicle detections
		if det.ShouldAlert() {
			dev, _ := p.cfg.DeviceStore.FindByID(ctx, job.DeviceID)
			alertMsg := formatAlert(dev, det, job.Timestamp)

			if err := p.cfg.Alerts.Send(ctx, dev, alertMsg, frameKey); err != nil {
				slog.Error("alert send failed",
					"device", job.DeviceID,
					"error", err,
				)
			} else {
				slog.Info("alert sent",
					"device", job.DeviceID,
					"class", det.Class,
					"confidence", det.Confidence,
				)
			}
		}
	}

	elapsed := time.Since(start)
	slog.Debug("frame processed",
		"device", job.DeviceID,
		"elapsed_ms", elapsed.Milliseconds(),
		"detections", len(result.Detections),
	)
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
		emoji,
		det.Class,
		location,
		ts.Format("3:04 PM"),
		det.Confidence*100,
	)
}
