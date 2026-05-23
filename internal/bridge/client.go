package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

type Client struct {
	cfg       *config.Config
	version   string
	machineID string
	hostname  string
	instance  string
	sessions  *SessionManager
}

func NewClient(cfg *config.Config, version string) *Client {
	return &Client{cfg: cfg, version: version}
}

func (c *Client) Run(ctx context.Context) error {
	machineID, err := loadMachineID(c.cfg.Bridge.MachineIDFile)
	if err != nil {
		return err
	}
	token, err := loadToken(c.cfg.Bridge.Token, c.cfg.Bridge.TokenFile)
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	c.machineID = machineID
	c.hostname = hostname
	c.instance = store.NewID("bin")
	c.sessions = NewSessionManager(c.cfg)

	minDelay := c.cfg.Bridge.ReconnectMin.Duration
	maxDelay := c.cfg.Bridge.ReconnectMax.Duration
	if minDelay <= 0 {
		minDelay = 5 * time.Second
	}
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	delay := minDelay

	for {
		err := c.connectOnce(ctx, token)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("[bridge] disconnected", "error", err, "retry_in", delay.String())
		timer := time.NewTimer(delay + time.Duration(rand.Int63n(int64(delay/2+1))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

func (c *Client) connectOnce(ctx context.Context, token string) error {
	wsURL, err := c.bridgeURL(token)
	if err != nil {
		return err
	}
	header := http.Header{}
	ws, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("%w: %s", err, resp.Status)
		}
		return err
	}
	defer ws.Close()

	reg := protocol.RegisterPayload{
		Name:      c.cfg.Bridge.Name,
		MachineID: c.machineID,
		Hostname:  c.hostname,
		Version:   c.version,
		Instance:  c.instance,
	}
	if err := ws.WriteJSON(protocol.MustEnvelope(protocol.TypeRegister, "", reg)); err != nil {
		return err
	}
	var ack protocol.Envelope
	if err := ws.ReadJSON(&ack); err != nil {
		return err
	}
	if ack.Type == protocol.TypeError {
		payload, _ := protocol.Decode[protocol.ErrorPayload](ack)
		return fmt.Errorf("register rejected: %s", payload.Message)
	}
	if ack.Type != protocol.TypeRegistered {
		return fmt.Errorf("unexpected register response %q", ack.Type)
	}
	registered, err := protocol.Decode[protocol.RegisteredPayload](ack)
	if err != nil {
		return err
	}
	slog.Info("[bridge] connected", "agent_id", registered.AgentID, "hub", c.cfg.Bridge.HubURL, "runner", c.cfg.Bridge.Runner)

	writec := make(chan protocol.Envelope, 128)
	c.sessions.AttachOut(writec)
	writeDone := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		for {
			select {
			case <-writeDone:
				return
			case env := <-writec:
				if err := ws.WriteJSON(env); err != nil {
					done <- err
					return
				}
			}
		}
	}()
	go func() {
		for {
			var env protocol.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				done <- err
				return
			}
			c.handleEnvelope(ctx, env, writec)
		}
	}()

	ticker := time.NewTicker(c.cfg.Bridge.HeartbeatInterval.Duration)
	defer ticker.Stop()
	defer func() {
		c.sessions.DetachOut(writec)
		close(writeDone)
	}()
	for {
		select {
		case <-ctx.Done():
			c.sessions.CloseAll()
			return ctx.Err()
		case err := <-done:
			return err
		case <-ticker.C:
			select {
			case writec <- protocol.MustEnvelope(protocol.TypeHeartbeat, "", map[string]any{"ts": time.Now().Unix()}):
			default:
				return errors.New("bridge write queue full")
			}
		}
	}
}

func (c *Client) bridgeURL(token string) (string, error) {
	base, err := url.Parse(c.cfg.Bridge.HubURL)
	if err != nil {
		return "", err
	}
	switch base.Scheme {
	case "https":
		base.Scheme = "wss"
	case "http":
		base.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported hub scheme %q", base.Scheme)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/api/agents/connect"
	q := base.Query()
	q.Set("token", token)
	base.RawQuery = q.Encode()
	return base.String(), nil
}

func (c *Client) handleEnvelope(ctx context.Context, env protocol.Envelope, out chan<- protocol.Envelope) {
	switch env.Type {
	case protocol.TypeHeartbeat:
		return
	case protocol.TypeOpenSession:
		payload, _ := protocol.Decode[protocol.OpenSessionPayload](env)
		if err := c.sessions.Open(env.Sid, payload.RemoteThreadID, out); err != nil {
			send(out, protocol.MustEnvelope(protocol.TypeError, env.Sid, protocol.ErrorPayload{Code: "OPEN_FAILED", Message: err.Error()}))
		}
	case protocol.TypePrompt:
		payload, err := protocol.Decode[protocol.PromptPayload](env)
		if err != nil {
			send(out, protocol.MustEnvelope(protocol.TypeError, env.Sid, protocol.ErrorPayload{Code: "BAD_PROMPT", Message: err.Error()}))
			return
		}
		go c.sessions.Prompt(ctx, env.Sid, payload.Content, payload.RunID, payload.PromptID, out)
	case protocol.TypeCancel:
		payload, _ := protocol.Decode[protocol.PromptPayload](env)
		c.sessions.Cancel(env.Sid, payload.RunID, payload.PromptID)
	case protocol.TypeCloseSession:
		c.sessions.Close(env.Sid)
	default:
		send(out, protocol.MustEnvelope(protocol.TypeError, env.Sid, protocol.ErrorPayload{Code: "BAD_TYPE", Message: "unsupported bridge frame"}))
	}
}

func send(out chan<- protocol.Envelope, env protocol.Envelope) bool {
	select {
	case out <- env:
		return true
	default:
		slog.Warn("[bridge] outbound queue full", "type", env.Type, "sid", env.Sid)
		return false
	}
}
