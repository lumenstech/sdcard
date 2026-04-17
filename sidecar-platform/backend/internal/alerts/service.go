package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/lumenstech/secure4k-sidecar/internal/device"
)

type Config struct {
	SequenceNowURL string
	SequenceNowKey string
	DefaultChannel string // whatsapp, sms, email
}

type Service struct {
	cfg    Config
	client *http.Client
}

func NewService(cfg Config) *Service {
	return &Service{
		cfg: cfg,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Send dispatches an alert via SequenceNow.
func (s *Service) Send(ctx context.Context, dev *device.Device, message string, frameKey string) error {
	if s.cfg.SequenceNowKey == "" {
		slog.Warn("alert skipped: no SequenceNow API key configured")
		return nil
	}

	channel := s.cfg.DefaultChannel
	recipient := ""

	if dev != nil {
		if dev.AlertChannel != "" {
			channel = dev.AlertChannel
		}
		if dev.AlertPhone != "" {
			recipient = dev.AlertPhone
		} else if dev.OwnerPhone != "" {
			recipient = dev.OwnerPhone
		}
	}

	if recipient == "" {
		return fmt.Errorf("no recipient configured for device %s", dev.ID)
	}

	switch channel {
	case "whatsapp":
		return s.sendWhatsApp(ctx, recipient, message, frameKey)
	case "sms":
		return s.sendSMS(ctx, recipient, message)
	case "slack":
		if dev != nil && dev.AlertSlack != "" {
			return s.sendSlack(ctx, dev.AlertSlack, message, frameKey)
		}
		return fmt.Errorf("no slack webhook configured")
	default:
		return s.sendWhatsApp(ctx, recipient, message, frameKey)
	}
}

func (s *Service) sendWhatsApp(ctx context.Context, phone, message, frameKey string) error {
	payload := map[string]interface{}{
		"to":      phone,
		"message": message,
	}

	// If we have a frame, include the image URL
	if frameKey != "" {
		payload["media_url"] = fmt.Sprintf("%s/v1/frames/%s", s.cfg.SequenceNowURL, frameKey)
	}

	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/v1/whatsapp/send", s.cfg.SequenceNowURL),
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create whatsapp request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", s.cfg.SequenceNowKey))

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("whatsapp error %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (s *Service) sendSMS(ctx context.Context, phone, message string) error {
	payload := map[string]string{
		"to":      phone,
		"message": message,
	}

	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/v1/sms/send", s.cfg.SequenceNowURL),
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", s.cfg.SequenceNowKey))

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sms error %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (s *Service) sendSlack(ctx context.Context, webhookURL, message, frameKey string) error {
	payload := map[string]string{
		"text": message,
	}

	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("slack error %d", resp.StatusCode)
	}

	return nil
}
