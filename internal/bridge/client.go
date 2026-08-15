package bridge

import (
	"context"
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
	cliConfig      *cliConfigManager
	shutdown       chan struct{}
	shutdownOnce   chan struct{}
	connectedAt    time.Time
}

func NewClient(cfg *config.Config, version string) *Client {
	return &Client{cfg: cfg, version: version, shutdown: make(chan struct{}), shutdownOnce: make(chan struct{}, 1)}
}

func (c *Client) Run(ctx context.Context) error {
	if c.cfg.Bridge.StrictWorkspace {
		if err := ValidateStrictWorkspaceSupport(); err != nil {
			return err
		}
	}
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
	c.cliConfig, err = newCLIConfigManager(c.cfg)
	if err != nil {
		return fmt.Errorf("initialize CLI configuration switcher: %w", err)
	}
	c.sessions = NewSessionManager(c.cfg)
	c.orchestrations = NewOrchestrationManager(c.cfg)
	c.orchestrations.SetCLIConfigManager(c.cliConfig)

	minDelay := c.cfg.Bridge.ReconnectMin.Duration
	maxDelay := c.cfg.Bridge.ReconnectMax.Duration
	if minDelay <= 0 {
		minDelay = 5 * time.Second
	}
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	delay := minDelay
	immediateRetryAvailable := true

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
		connectedFor := time.Duration(0)
		if !c.connectedAt.IsZero() {
			connectedFor = time.Since(c.connectedAt)
		}
		wait, nextDelay, nextImmediateRetryAvailable := bridgeReconnectDelay(
			delay, minDelay, maxDelay, connectedFor, immediateRetryAvailable,
		)
		if wait > 0 {
			wait += time.Duration(rand.Int63n(int64(wait/2 + 1)))
		}
		delay = nextDelay
		immediateRetryAvailable = nextImmediateRetryAvailable
		slog.Warn("[bridge] disconnected", "error", err, "retry_in", wait.String())
		if wait == 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-c.shutdown:
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func bridgeReconnectDelay(current, minDelay, maxDelay, connectedFor time.Duration, immediateRetryAvailable bool) (time.Duration, time.Duration, bool) {
	if minDelay <= 0 {
		minDelay = 5 * time.Second
	}
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	if current < minDelay {
		current = minDelay
	}
	if connectedFor >= 30*time.Second {
		current = minDelay
		immediateRetryAvailable = true
	}
	if connectedFor > 0 && immediateRetryAvailable {
		return 0, current, false
	}
	next := current * 2
	if next < current || next > maxDelay {
		next = maxDelay
	}
	return current, next, immediateRetryAvailable
}

func (c *Client) connectOnce(ctx context.Context, token string) error {
	c.connectedAt = time.Time{}
	if c.cliConfig == nil {
		manager, err := newCLIConfigManager(c.cfg)
		if err != nil {
			return fmt.Errorf("initialize CLI configuration switcher: %w", err)
		}
		c.cliConfig = manager
	}
	if c.orchestrations != nil {
		c.orchestrations.SetCLIConfigManager(c.cliConfig)
	}
	wsURL, err := c.bridgeURL(token)
	if err != nil {
		return err
	}
	slog.Info("[bridge] connecting", "hub", c.cfg.Bridge.HubURL, "name", c.cfg.Bridge.Name, "machine_id", c.machineID)
	header := http.Header{}
	ws, resp, err := dialHubWebSocket(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("%w: %s", err, resp.Status)
		}
		return err
	}
	defer ws.Close()

	capabilities := BridgeCapabilities(c.cfg)
	capabilities.ConfigSwitcher = c.cliConfig.capability()
	reg := protocol.RegisterPayload{
		Name:         c.cfg.Bridge.Name,
		MachineID:    c.machineID,
		Hostname:     c.hostname,
		Version:      c.version,
		Instance:     c.instance,
		WorkingDirs:  DiscoverWorkingDirs(c.cfg),
		Capabilities: capabilities,
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
	c.connectedAt = time.Now()
	readTimeout := bridgeWebSocketReadTimeout(c.cfg.Bridge.HeartbeatInterval.Duration)
	_ = ws.SetReadDeadline(time.Now().Add(readTimeout))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(readTimeout))
	})

	writec := make(chan protocol.Envelope, 128)
	writeDone := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		pingInterval := bridgeHeartbeatInterval(c.cfg.Bridge.HeartbeatInterval.Duration)
		pingTicker := time.NewTicker(pingInterval)
		defer pingTicker.Stop()
		for {
			select {
			case <-writeDone:
				return
			case <-pingTicker.C:
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					done <- fmt.Errorf("bridge websocket ping failed: %w", err)
					return
				}
			case env := <-writec:
				_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := ws.WriteJSON(env); err != nil {
					done <- fmt.Errorf("bridge websocket write failed: %w", err)
					return
				}
			}
		}
	}()
	// Start the writer before attaching managers. AttachOut may flush more
	// buffered events than fit in writec after a long disconnect, so the queue
	// must already have an active consumer.
	c.sessions.AttachOut(writec)
	c.orchestrations.AttachOut(writec)
	go func() {
		for {
			var env protocol.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				done <- fmt.Errorf("bridge websocket read failed: %w", err)
				return
			}
			_ = ws.SetReadDeadline(time.Now().Add(readTimeout))
			c.handleEnvelope(ctx, env, writec)
		}
	}()

	heartbeatInterval := bridgeHeartbeatInterval(c.cfg.Bridge.HeartbeatInterval.Duration)
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	defer func() {
		c.sessions.DetachOut(writec)
		c.orchestrations.DetachOut(writec)
		close(writeDone)
	}()
	// Detach before CloseAll on every exit path: the cancellation events that
	// CloseAll triggers must land in the managers' pending buffers (flushed on
	// reconnect), not in a write channel whose writer goroutine already died.
	for {
		select {
		case <-ctx.Done():
			c.sessions.DetachOut(writec)
			c.orchestrations.DetachOut(writec)
			c.sessions.CloseAll()
			c.orchestrations.CloseAll()
			return ctx.Err()
		case <-c.shutdown:
			c.sessions.DetachOut(writec)
			c.orchestrations.DetachOut(writec)
			c.sessions.CloseAll()
			c.orchestrations.CloseAll()
			return nil
		case err := <-done:
			c.sessions.DetachOut(writec)
			c.orchestrations.DetachOut(writec)
			return err
		case <-ticker.C:
			payload := protocol.HeartbeatPayload{
				TS:          time.Now().Unix(),
				WorkingDirs: DiscoverWorkingDirs(c.cfg),
			}
			select {
			case writec <- protocol.MustEnvelope(protocol.TypeHeartbeat, "", payload):
			default:
				// Transport Ping/Pong continues to prove liveness. A full
				// application queue is temporary backpressure, not a reason to
				// tear down the whole Bridge connection.
				slog.Warn("[bridge] heartbeat skipped: outbound queue full")
			}
		}
	}
}

// dialHubWebSocket keeps proxy support for installations that require it. A
// few proxy configurations return plaintext HTTP to a CONNECT/TLS request;
// retry the Hub control channel directly only for that unambiguous failure.
// CLI provider requests retain their inherited proxy environment.
func dialHubWebSocket(ctx context.Context, wsURL string, header http.Header) (*websocket.Conn, *http.Response, error) {
	ws, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err == nil || !hubProxyReturnedPlaintextTLS(err) {
		return ws, resp, err
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	slog.Warn("[bridge] Hub proxy returned plaintext during TLS handshake; retrying Hub connection directly")
	directDialer := *websocket.DefaultDialer
	directDialer.Proxy = nil
	return directDialer.DialContext(ctx, wsURL, header)
}

func hubProxyReturnedPlaintextTLS(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "first record does not look like a tls handshake")
}

func bridgeHeartbeatInterval(configured time.Duration) time.Duration {
	if configured <= 0 {
		return 15 * time.Second
	}
	return configured
}

func bridgeWebSocketReadTimeout(heartbeat time.Duration) time.Duration {
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	timeout := 6 * heartbeat
	if timeout < 90*time.Second {
		timeout = 90 * time.Second
	}
	return timeout
}

func BridgeCapabilities(cfg *config.Config) *protocol.BridgeCapabilities {
	runner := strings.ToLower(strings.TrimSpace(cfg.Bridge.Runner))
	if runner == "" {
		runner = "echo"
	}
	reviewRequired := !bridgeNoApprovalExecution(cfg)
	codexAvailable := commandAvailable(bridgeCodexPath(cfg))
	claudeAvailable := commandAvailable(bridgeClaudePath(cfg))
	caps := &protocol.BridgeCapabilities{
		Runner:                 runner,
		Sandbox:                cfg.Bridge.Sandbox,
		ApprovalPolicy:         cfg.Bridge.ApprovalPolicy,
		Chat:                   map[string]protocol.BridgeCLICapability{},
		Orchestration:          map[string]protocol.BridgeCLICapability{},
		Metadata:               map[string]string{"approvalMode": approvalMode(cfg)},
		DurableTaskGraph:       cfg.Bridge.DurableTaskGraph,
		UsageLedger:            true,
		IsolatedWorkerProfiles: true,
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
	if runner == "acp" {
		acpCLI := strings.ToLower(strings.TrimSpace(cfg.Bridge.ACP.CLI))
		if acpCLI != "codex" {
			acpCLI = "claude"
		}
		adapter := strings.TrimSpace(cfg.Bridge.ACP.ClaudeCommand)
		if acpCLI == "codex" {
			adapter = strings.TrimSpace(cfg.Bridge.ACP.CodexCommand)
		}
		adapterAvailable := commandAvailable(adapter)
		caps.ACP = &protocol.ACPCapability{
			Available:    adapterAvailable,
			LoadSession:  true,
			NativeResume: cfg.Bridge.ACP.PreferNativeResume,
		}
		// The interactive long session executes through the ACP adapter, so the
		// selected CLI's chat capability reflects the adapter, not codex exec.
		caps.Chat[acpCLI] = protocol.BridgeCLICapability{
			Available:       adapterAvailable,
			Execution:       "acp:" + adapter,
			BrowserApproval: adapterAvailable,
			ApprovalMode:    approvalMode(cfg),
		}
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
	if bridgeNoApprovalExecution(cfg) {
		return "codex exec --json"
	}
	return "codex app-server"
}

func approvalMode(cfg *config.Config) string {
	if cfg.Bridge.StrictWorkspace {
		return "strict-workspace"
	}
	if bridgeBypassApprovalsAndSandbox(cfg) {
		return "auto-execute"
	}
	return "review-required"
}

func bridgeNoApprovalExecution(cfg *config.Config) bool {
	return cfg.Bridge.StrictWorkspace || bridgeBypassApprovalsAndSandbox(cfg)
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
		if err := c.sessions.Open(env.Sid, payload.RemoteThreadID, payload.CWD, out); err != nil {
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
		c.orchestrations.Start(payload)
	case protocol.TypeOrchestrationCancel:
		payload, _ := protocol.Decode[protocol.OrchestrationCancelPayload](env)
		c.orchestrations.Cancel(payload.RunID)
	case protocol.TypeOrchestrationUsageSyncRequest:
		payload, err := protocol.Decode[protocol.OrchestrationUsageSyncRequest](env)
		if err != nil || payload.RunID == "" {
			return
		}
		go func() {
			result := scanOrchestrationUsage(payload)
			send(out, protocol.MustEnvelope(protocol.TypeOrchestrationUsageSyncResult, "", result))
		}()
	case protocol.TypeCLIConfigTest, protocol.TypeCLIConfigApply, protocol.TypeCLIConfigReset:
		payload, err := protocol.Decode[protocol.CLIConfigRequest](env)
		if err != nil || payload.RequestID == "" {
			return
		}
		go func() {
			result := c.cliConfig.handle(ctx, env.Type, payload)
			send(out, protocol.MustEnvelope(protocol.TypeCLIConfigResult, "", result))
		}()
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
