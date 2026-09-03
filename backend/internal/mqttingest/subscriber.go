package mqttingest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/lumenstech/secure4k-sidecar/internal/handlers"
)

type Config struct {
	BrokerURL string
	Topic     string
}

type Subscriber struct {
	cfg      Config
	pipeline *handlers.Pipeline
	client   mqtt.Client
}

type framePayload struct {
	DeviceID string `json:"device_id"`
	TS       int64  `json:"ts"`
	Frame    string `json:"frame"`
}

func New(cfg Config, pipeline *handlers.Pipeline) *Subscriber {
	if cfg.Topic == "" {
		cfg.Topic = "secure4k/frames/+"
	}
	return &Subscriber{cfg: cfg, pipeline: pipeline}
}

func (s *Subscriber) Start(ctx context.Context) error {
	if s.cfg.BrokerURL == "" {
		return nil
	}
	opts := mqtt.NewClientOptions().AddBroker(s.cfg.BrokerURL)
	opts.SetClientID(fmt.Sprintf("secure4k-ingest-%d", time.Now().UnixNano()))
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(2 * time.Second)
	opts.SetConnectTimeout(8 * time.Second)
	opts.SetKeepAlive(30 * time.Second)
	opts.SetPingTimeout(5 * time.Second)
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		slog.Warn("mqtt connection lost", "error", err)
	})
	s.client = mqtt.NewClient(opts)
	if token := s.client.Connect(); !token.WaitTimeout(10*time.Second) {
		return errors.New("mqtt connect timeout")
	} else if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt connect: %w", err)
	}
	if token := s.client.Subscribe(s.cfg.Topic, 1, s.handleMessage); !token.WaitTimeout(10*time.Second) {
		return errors.New("mqtt subscribe timeout")
	} else if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt subscribe: %w", err)
	}
	slog.Info("mqtt ingest subscribed", "broker", s.cfg.BrokerURL, "topic", s.cfg.Topic)
	return nil
}

func (s *Subscriber) handleMessage(_ mqtt.Client, msg mqtt.Message) {
	var payload framePayload
	if err := json.Unmarshal(msg.Payload(), &payload); err != nil {
		slog.Warn("mqtt frame rejected", "topic", msg.Topic(), "error", err)
		return
	}
	if payload.DeviceID == "" || payload.TS <= 0 || payload.Frame == "" {
		slog.Warn("mqtt frame rejected; missing fields", "topic", msg.Topic())
		return
	}
	image, err := base64.StdEncoding.DecodeString(payload.Frame)
	if err != nil || len(image) == 0 || len(image) > 5<<20 {
		slog.Warn("mqtt frame rejected; invalid image", "device", payload.DeviceID, "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = s.pipeline.Submit(ctx, &handlers.FrameJob{
		DeviceID: payload.DeviceID, Timestamp: time.Unix(payload.TS, 0).UTC(), ImageData: image, ReceivedAt: time.Now().UTC(),
	})
	if errors.Is(err, handlers.ErrDuplicate) {
		slog.Debug("mqtt duplicate acknowledged", "device", payload.DeviceID, "ts", payload.TS)
		return
	}
	if err != nil {
		slog.Warn("mqtt frame admission failed", "device", payload.DeviceID, "error", err)
	}
}

func (s *Subscriber) Stop() {
	if s.client != nil && s.client.IsConnected() {
		s.client.Disconnect(500)
	}
}
