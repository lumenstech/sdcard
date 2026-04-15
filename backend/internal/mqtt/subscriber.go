// Package mqtt subscribes to the MQTT broker and forwards frame messages to
// the same Pipeline used by the HTTP ingest path. Connection failures are
// logged and retried automatically by the paho client; any panic in a
// message callback is recovered so a single bad payload cannot kill the
// subscriber.
package mqtt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	mqttlib "github.com/eclipse/paho.mqtt.golang"

	"github.com/lumenstech/secure4k-sidecar/internal/handlers"
)

type Config struct {
	BrokerURL string // tcp://host:1883 or ssl://host:8883
	Topic     string // typically secure4k/frames/+
	ClientID  string
	Username  string
	Password  string
}

type Subscriber struct {
	cfg      Config
	client   mqttlib.Client
	pipeline *handlers.Pipeline
}

type framePayload struct {
	DeviceID string `json:"device_id"`
	TS       int64  `json:"ts"`
	Frame    string `json:"frame"` // base64 JPEG
	Version  string `json:"version,omitempty"`
}

// New connects to the broker and subscribes. The returned Subscriber must be
// closed at shutdown.
func New(cfg Config, pipeline *handlers.Pipeline) (*Subscriber, error) {
	if cfg.BrokerURL == "" {
		return nil, errors.New("BrokerURL required")
	}
	if cfg.Topic == "" {
		cfg.Topic = "secure4k/frames/+"
	}
	if cfg.ClientID == "" {
		cfg.ClientID = fmt.Sprintf("ingest-%d", time.Now().UnixNano())
	}

	opts := mqttlib.NewClientOptions().
		AddBroker(cfg.BrokerURL).
		SetClientID(cfg.ClientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectTimeout(10 * time.Second).
		SetMaxReconnectInterval(60 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetPingTimeout(10 * time.Second)

	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
	}
	if cfg.Password != "" {
		opts.SetPassword(cfg.Password)
	}

	opts.OnConnect = func(c mqttlib.Client) {
		slog.Info("mqtt connected", "broker", cfg.BrokerURL, "topic", cfg.Topic)
	}
	opts.OnConnectionLost = func(c mqttlib.Client, err error) {
		slog.Warn("mqtt connection lost", "error", err)
	}

	sub := &Subscriber{cfg: cfg, pipeline: pipeline}

	cli := mqttlib.NewClient(opts)
	if tok := cli.Connect(); tok.WaitTimeout(10*time.Second) && tok.Error() != nil {
		return nil, fmt.Errorf("mqtt connect: %w", tok.Error())
	} else if tok.Error() == nil && !cli.IsConnected() {
		// Connection still pending; auto-reconnect will retry. That's fine.
	}
	sub.client = cli

	if tok := cli.Subscribe(cfg.Topic, 1, sub.onMessage); tok.WaitTimeout(10*time.Second) && tok.Error() != nil {
		cli.Disconnect(250)
		return nil, fmt.Errorf("mqtt subscribe: %w", tok.Error())
	}

	return sub, nil
}

func (s *Subscriber) Close() {
	if s.client != nil && s.client.IsConnected() {
		s.client.Disconnect(500)
	}
}

func (s *Subscriber) onMessage(_ mqttlib.Client, msg mqttlib.Message) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("mqtt callback panic recovered",
				"topic", msg.Topic(), "panic", r, "stack", string(debug.Stack()))
		}
	}()

	var p framePayload
	if err := json.Unmarshal(msg.Payload(), &p); err != nil {
		slog.Warn("mqtt payload parse failed", "topic", msg.Topic(), "error", err)
		return
	}
	if p.DeviceID == "" {
		slog.Warn("mqtt payload missing device_id", "topic", msg.Topic())
		return
	}

	imgData, err := base64.StdEncoding.DecodeString(p.Frame)
	if err != nil || len(imgData) == 0 {
		slog.Warn("mqtt payload bad base64", "device", p.DeviceID, "error", err)
		return
	}
	if int64(len(imgData)) > handlers.MaxFrameBytes {
		slog.Warn("mqtt frame too large, dropping", "device", p.DeviceID, "size", len(imgData))
		return
	}

	ts := time.Unix(p.TS, 0).UTC()
	if p.TS <= 0 {
		ts = time.Now().UTC()
	}

	job := &handlers.FrameJob{
		DeviceID:       p.DeviceID,
		Timestamp:      ts,
		ImageData:      imgData,
		SidecarVersion: p.Version,
		ReceivedAt:     time.Now().UTC(),
	}
	if err := s.pipeline.Submit(job); err != nil {
		slog.Warn("mqtt frame dropped, pipeline full", "device", p.DeviceID)
	}
}

// Probe is a small helper used by tests/diagnostics.
func (s *Subscriber) Probe(ctx context.Context) error {
	if s.client == nil {
		return errors.New("not initialized")
	}
	if !s.client.IsConnected() {
		return errors.New("disconnected")
	}
	return nil
}
