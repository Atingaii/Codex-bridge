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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

type Client struct {
	cfg            *config.Config
	version        string
	machineID      string
	hostname       string
	instance       string
	sessions       *SessionManager
	orchestrations *OrchestrationManager
	shutdown       chan struct{}
	shutdownOnce   chan struct{}
}

func NewClient(cfg *config.Config, version string) *Client {
	return &Client{cfg: cfg, version: version, shutdown: make(chan struct{}), shutdownOnce: make(chan struct{}, 1)}
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
	c.orchestrations = NewOrchestrationManager(c.cfg)

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
		if c.shutdownRequested() {
			return nil
		}
		err := c.connectOnce(ctx, token)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.shutdownRequested() {
			return nil
		}
		slog.Warn("[bridge] disconnected", "error", err, "retry_in", delay.String())
		timer := time.NewTimer(delay + time.Duration(rand.Int63n(int64(delay/2+1))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-c.shutdown:
			timer.Stop()
			return nil
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
	slog.Info("[bridge] connecting", "hub", c.cfg.Bridge.HubURL, "name", c.cfg.Bridge.Name, "machine_id", c.machineID)
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
		Name:         c.cfg.Bridge.Name,
		MachineID:    c.machineID,
		Hostname:     c.hostname,
		Version:      c.version,
		Instance:     c.instance,
		WorkingDirs:  DiscoverWorkingDirs(c.cfg),
		Capabilities: BridgeCapabilities(c.cfg),
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
	c.orchestrations.AttachOut(writec)
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
		c.orchestrations.DetachOut(writec)
		close(writeDone)
	}()
	for {
		select {
		case <-ctx.Done():
			c.sessions.CloseAll()
			c.orchestrations.CloseAll()
			return ctx.Err()
		case <-c.shutdown:
			c.sessions.CloseAll()
			c.orchestrations.CloseAll()
			return nil
		case err := <-done:
			return err
		case <-ticker.C:
			payload := protocol.HeartbeatPayload{
				TS:          time.Now().Unix(),
				WorkingDirs: DiscoverWorkingDirs(c.cfg),
			}
			select {
			case writec <- protocol.MustEnvelope(protocol.TypeHeartbeat, "", payload):
			default:
				return errors.New("bridge write queue full")
			}
		}
	}
}

func BridgeCapabilities(cfg *config.Config) *protocol.BridgeCapabilities {
	runner := strings.ToLower(strings.TrimSpace(cfg.Bridge.Runner))
	if runner == "" {
		runner = "echo"
	}
	reviewRequired := !bridgeBypassApprovalsAndSandbox(cfg)
	codexAvailable := commandAvailable(bridgeCodexPath(cfg))
	claudeAvailable := commandAvailable(bridgeClaudePath(cfg))
	caps := &protocol.BridgeCapabilities{
		Runner:         runner,
		Sandbox:        cfg.Bridge.Sandbox,
		ApprovalPolicy: cfg.Bridge.ApprovalPolicy,
		Chat:           map[string]protocol.BridgeCLICapability{},
		Orchestration:  map[string]protocol.BridgeCLICapability{},
		Metadata:       map[string]string{"approvalMode": approvalMode(cfg)},
	}
	caps.Chat["codex"] = protocol.BridgeCLICapability{
		Available:       codexAvailable && (runner == "codex-app-server" || runner == "codex-appserver" || runner == "app-server"),
		Execution:       runner,
		BrowserApproval: codexAvailable && (runner == "codex-app-server" || runner == "codex-appserver" || runner == "app-server"),
		ApprovalMode:    approvalMode(cfg),
	}
	caps.Orchestration["claude"] = protocol.BridgeCLICapability{
		Available:       claudeAvailable,
		Execution:       "claude --print",
		BrowserApproval: claudeAvailable && reviewRequired,
		ApprovalMode:    approvalMode(cfg),
	}
	caps.Orchestration["codex"] = protocol.BridgeCLICapability{
		Available:       codexAvailable,
		Execution:       codexOrchestrationExecution(cfg),
		BrowserApproval: codexAvailable && reviewRequired,
		ApprovalMode:    approvalMode(cfg),
	}
	return caps
}

func commandAvailable(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := exec.LookPath(path)
	return err == nil
}

func bridgeCodexPath(cfg *config.Config) string {
	if strings.TrimSpace(cfg.Bridge.CodexPath) == "" {
		return "codex"
	}
	return cfg.Bridge.CodexPath
}

func bridgeClaudePath(cfg *config.Config) string {
	if strings.TrimSpace(cfg.Bridge.ClaudePath) == "" {
		return "claude"
	}
	return cfg.Bridge.ClaudePath
}

func codexOrchestrationExecution(cfg *config.Config) string {
	if bridgeBypassApprovalsAndSandbox(cfg) {
		return "codex exec --json"
	}
	return "codex app-server"
}

func approvalMode(cfg *config.Config) string {
	if bridgeBypassApprovalsAndSandbox(cfg) {
		return "auto-execute"
	}
	return "review-required"
}

func bridgeBypassApprovalsAndSandbox(cfg *config.Config) bool {
	return strings.EqualFold(cfg.Bridge.ApprovalPolicy, "never") &&
		strings.EqualFold(cfg.Bridge.Sandbox, "danger-full-access")
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
	case protocol.TypeAgentShutdown:
		payload, _ := protocol.Decode[protocol.AgentShutdownPayload](env)
		c.requestShutdown(payload.Reason)
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
		go c.sessions.Prompt(ctx, env.Sid, payload, out)
	case protocol.TypeCancel:
		payload, _ := protocol.Decode[protocol.PromptPayload](env)
		c.sessions.Cancel(env.Sid, payload.RunID, payload.PromptID)
	case protocol.TypeApprovalResponse:
		payload, err := protocol.Decode[protocol.ApprovalResponsePayload](env)
		if err != nil {
			send(out, protocol.MustEnvelope(protocol.TypeError, env.Sid, protocol.ErrorPayload{Code: "BAD_APPROVAL_RESPONSE", Message: err.Error()}))
			return
		}
		if env.Sid == "" {
			if !c.orchestrations.ApprovalResponse(payload) {
				send(out, protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Code: "APPROVAL_NOT_FOUND", Message: "orchestration approval request not found"}))
			}
			return
		}
		if !c.sessions.ApprovalResponse(env.Sid, payload) {
			send(out, protocol.MustEnvelope(protocol.TypeError, env.Sid, protocol.ErrorPayload{Code: "APPROVAL_NOT_FOUND", Message: "approval request not found"}))
		}
	case protocol.TypeCloseSession:
		c.sessions.Close(env.Sid)
	case protocol.TypeOrchestrationStart:
		payload, err := protocol.Decode[protocol.OrchestrationStartPayload](env)
		if err != nil {
			send(out, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
				Kind:  "run.error",
				Error: err.Error(),
			}))
			return
		}
		c.orchestrations.Start(ctx, payload)
	case protocol.TypeOrchestrationCancel:
		payload, _ := protocol.Decode[protocol.OrchestrationCancelPayload](env)
		c.orchestrations.Cancel(payload.RunID)
	default:
		send(out, protocol.MustEnvelope(protocol.TypeError, env.Sid, protocol.ErrorPayload{Code: "BAD_TYPE", Message: "unsupported bridge frame"}))
	}
}

func (c *Client) requestShutdown(reason string) {
	select {
	case c.shutdownOnce <- struct{}{}:
		slog.Info("[bridge] shutdown requested by hub", "reason", reason)
		c.stopLocalUserService()
		close(c.shutdown)
	default:
	}
}

func (c *Client) shutdownRequested() bool {
	select {
	case <-c.shutdown:
		return true
	default:
		return false
	}
}

func (c *Client) stopLocalUserService() {
	serviceName := bridgeUserServiceName(c.cfg.Bridge.MachineIDFile)
	if serviceName == "" || !commandAvailable("systemctl") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "systemctl", "--user", "disable", "--now", serviceName).Run()
	_ = exec.CommandContext(ctx, "systemctl", "--user", "reset-failed", serviceName).Run()
	_ = exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()
}

func bridgeUserServiceName(machineIDFile string) string {
	base := filepath.Base(expandHome(strings.TrimSpace(machineIDFile)))
	if base == "" || base == "." || base == string(filepath.Separator) || base == "machine_id" {
		return ""
	}
	return "codex-bridge-" + base + ".service"
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
