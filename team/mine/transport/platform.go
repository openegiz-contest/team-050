package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/openegiz-contest/team-050/team/mine/domain"
)

func Env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type Platform struct {
	User     string
	Base     string
	Password string
	Client   *http.Client
}

func NewPlatform() *Platform {
	return &Platform{User: "ditto", Base: Env("DITTO_URL", "http://localhost:8080"), Password: os.Getenv("DITTO_PASSWORD"), Client: &http.Client{Timeout: 20 * time.Second}}
}
func (p *Platform) Request(ctx context.Context, method, path string, value any) ([]byte, int, error) {
	var body io.Reader
	if value != nil {
		b, e := json.Marshal(value)
		if e != nil {
			return nil, 0, e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.Base, "/")+path, body)
	if e != nil {
		return nil, 0, e
	}
	req.SetBasicAuth(p.User, p.Password)
	req.Header.Set("Content-Type", "application/json")
	if command, ok := value.(domain.Command); ok {
		req.Header.Set("correlation-id", command.ID)
	}
	resp, e := p.Client.Do(req)
	if e != nil {
		return nil, 0, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if e == nil && resp.StatusCode >= 400 {
		e = fmt.Errorf("Ditto HTTP %d", resp.StatusCode)
	}
	return b, resp.StatusCode, e
}
func (p *Platform) Get(ctx context.Context, name string) (domain.Thing, error) {
	b, _, e := p.Request(ctx, "GET", "/api/2/things/"+domain.ThingID(name), nil)
	var t domain.Thing
	if e == nil {
		e = json.Unmarshal(b, &t)
	}
	return t, e
}

type Envelope struct {
	Topic   string          `json:"topic"`
	Path    string          `json:"path"`
	Headers map[string]any  `json:"headers,omitempty"`
	Value   json.RawMessage `json:"value"`
}

func Connect(id string, onConnect func(mqtt.Client)) (mqtt.Client, error) {
	opts := mqtt.NewClientOptions().AddBroker(Env("MQTT_URL", "tcp://localhost:1883")).SetClientID(id).SetAutoReconnect(true).SetConnectRetry(false).SetKeepAlive(20 * time.Second).SetPingTimeout(10 * time.Second).SetOrderMatters(true).SetOnConnectHandler(onConnect)
	client := mqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		return nil, fmt.Errorf("MQTT connect timeout")
	}
	return client, token.Error()
}
func Publish(client mqtt.Client, topic string, body any) error {
	b, e := json.Marshal(body)
	if e != nil {
		return e
	}
	t := client.Publish(topic, 1, false, b)
	if !t.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("MQTT publish timeout")
	}
	return t.Error()
}
func PublishFeatures(client mqtt.Client, t domain.Thing) error {
	b, _ := json.Marshal(t.Features)
	return Publish(client, "telemetry/team050/"+domain.Name(t.ID), Envelope{Topic: domain.Namespace + "/" + domain.Name(t.ID) + "/things/twin/commands/modify", Path: "/features", Value: b})
}
func PublishResult(client mqtt.Client, r domain.Result) error {
	b, _ := json.Marshal(r)
	return Publish(client, "telemetry/team050/control-01", Envelope{Topic: domain.Namespace + "/control-01/things/live/messages/command_result", Path: "/outbox/messages/command_result", Headers: map[string]any{"content-type": "application/json", "correlation-id": r.ID}, Value: b})
}
