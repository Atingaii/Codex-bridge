package protocol

import (
	"encoding/json"
	"fmt"
)

const (
	TypeRegister            = "register"
	TypeRegistered          = "registered"
	TypeHeartbeat           = "heartbeat"
	TypeOpenSession         = "open_session"
	TypeSessionOpened       = "session_opened"
	TypePrompt              = "prompt"
	TypeSessionUpdate       = "session_update"
	TypePromptComplete      = "prompt_complete"
	TypeApprovalRequest     = "approval_request"
	TypeApprovalResponse    = "approval_response"
	TypeCancel              = "cancel"
	TypeCloseSession        = "close_session"
	TypeOrchestrationStart  = "orchestration_start"
	TypeOrchestrationEvent  = "orchestration_event"
	TypeOrchestrationCancel = "orchestration_cancel"
	TypeAgentShutdown       = "agent_shutdown"
	TypeError               = "error"
	TypeStatus              = "status"
)

type Envelope struct {
	Type    string          `json:"type"`
	Sid     string          `json:"sid,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func NewEnvelope(typ, sid string, payload any) (Envelope, error) {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, err
		}
		raw = b
	}
	return Envelope{Type: typ, Sid: sid, Payload: raw}, nil
}

func MustEnvelope(typ, sid string, payload any) Envelope {
	env, err := NewEnvelope(typ, sid, payload)
	if err != nil {
		panic(err)
	}
	return env
}

func Decode[T any](env Envelope) (T, error) {
	var out T
	if len(env.Payload) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(env.Payload, &out); err != nil {
		return out, fmt.Errorf("decode %s payload: %w", env.Type, err)
	}
	return out, nil
}

type RegisterPayload struct {
	Name         string              `json:"name"`
	MachineID    string              `json:"machineId"`
	Hostname     string              `json:"hostname"`
	Version      string              `json:"version"`
	Instance     string              `json:"instance,omitempty"`
	WorkingDirs  []string            `json:"workingDirs,omitempty"`
	Capabilities *BridgeCapabilities `json:"capabilities,omitempty"`
}

type RegisteredPayload struct {
	AgentID string `json:"agentId"`
}

type HeartbeatPayload struct {
	TS          int64    `json:"ts,omitempty"`
	WorkingDirs []string `json:"workingDirs,omitempty"`
}

type AgentShutdownPayload struct {
	Reason string `json:"reason,omitempty"`
}

type BridgeCapabilities struct {
	Runner         string                         `json:"runner,omitempty"`
	Sandbox        string                         `json:"sandbox,omitempty"`
	ApprovalPolicy string                         `json:"approvalPolicy,omitempty"`
	Chat           map[string]BridgeCLICapability `json:"chat,omitempty"`
	Orchestration  map[string]BridgeCLICapability `json:"orchestration,omitempty"`
	Metadata       map[string]string              `json:"metadata,omitempty"`
	ACP            *ACPCapability                 `json:"acp,omitempty"`
}

// ACPCapability advertises whether the endpoint can run an Agent Client
// Protocol adapter for interactive long sessions and whether those sessions can
// be resumed from the native CLI in the workspace (target B). It is nil when the
// endpoint does not use the ACP runner so existing endpoints stay unaffected.
type ACPCapability struct {
	Available bool `json:"available"`
	// LoadSession reports whether the adapter advertised session/load support so
	// the Bridge can resume an ACP session it previously opened.
	LoadSession bool `json:"loadSession"`
	// NativeResume reports whether a local `resume` command can be offered for
	// sessions opened through this endpoint (target B).
	NativeResume bool `json:"nativeResume"`
}

type BridgeCLICapability struct {
	Available       bool   `json:"available"`
	Execution       string `json:"execution,omitempty"`
	BrowserApproval bool   `json:"browserApproval"`
	ApprovalMode    string `json:"approvalMode,omitempty"`
}

type OpenSessionPayload struct {
	Sid            string `json:"sid"`
	CWD            string `json:"cwd,omitempty"`
	RemoteThreadID string `json:"remoteThreadId,omitempty"`
}

type SessionOpenedPayload struct {
	RemoteThreadID string `json:"remoteThreadId,omitempty"`
	Runner         string `json:"runner,omitempty"`
	// NativeResumeID is the underlying CLI's own session id used for local
	// takeover (target B). It is the same value as RemoteThreadID for Claude and
	// a separately resolved id for Codex. Empty when no native resume is
	// available; never fabricated.
	NativeResumeID string `json:"nativeResumeId,omitempty"`
	// NativeResumeCommand is a ready-to-copy command that continues this same
	// conversation in the native CLI from the workspace, e.g.
	// `claude --resume <id>`. Empty when unavailable.
	NativeResumeCommand string `json:"nativeResumeCommand,omitempty"`
}

type PromptPayload struct {
	Content     string              `json:"content"`
	PromptID    string              `json:"promptId,omitempty"`
	RunID       string              `json:"runId,omitempty"`
	Attachments []AttachmentPayload `json:"attachments,omitempty"`
}

type AttachmentPayload struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
	Data     string `json:"data"`
}

type SessionUpdatePayload struct {
	Delta    string     `json:"delta,omitempty"`
	Content  string     `json:"content,omitempty"`
	RunID    string     `json:"runId,omitempty"`
	PromptID string     `json:"promptId,omitempty"`
	Event    string     `json:"event,omitempty"`
	Tool     *ToolEvent `json:"tool,omitempty"`
}

type PromptCompletePayload struct {
	Content        string          `json:"content,omitempty"`
	Usage          json.RawMessage `json:"usage,omitempty"`
	RemoteThreadID string          `json:"remoteThreadId,omitempty"`
	RunID          string          `json:"runId,omitempty"`
	PromptID       string          `json:"promptId,omitempty"`
	// NativeResumeID and NativeResumeCommand mirror SessionOpenedPayload so the
	// browser can refresh the local-takeover command after a turn (the native id
	// can become resolvable only once the CLI has written its rollout). Both are
	// optional and never fabricated.
	NativeResumeID      string `json:"nativeResumeId,omitempty"`
	NativeResumeCommand string `json:"nativeResumeCommand,omitempty"`
}

type ErrorPayload struct {
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
	RunID    string `json:"runId,omitempty"`
	PromptID string `json:"promptId,omitempty"`
}

type ApprovalRequestPayload struct {
	RequestID string          `json:"requestId"`
	Kind      string          `json:"kind"`
	Command   string          `json:"command,omitempty"`
	CWD       string          `json:"cwd,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	ThreadID  string          `json:"threadId,omitempty"`
	TurnID    string          `json:"turnId,omitempty"`
	ItemID    string          `json:"itemId,omitempty"`
	RunID     string          `json:"runId,omitempty"`
	PromptID  string          `json:"promptId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type ApprovalResponsePayload struct {
	RequestID string `json:"requestId"`
	Decision  string `json:"decision"`
}

type OrchestrationStartPayload struct {
	RunID             string              `json:"runId"`
	Mode              string              `json:"mode"`
	FirstCLI          string              `json:"firstCli,omitempty"`
	Prompt            string              `json:"prompt"`
	Context           string              `json:"context,omitempty"`
	Resume            bool                `json:"resume,omitempty"`
	PromptSeq         int64               `json:"promptSeq,omitempty"`
	MaxTurns          int                 `json:"maxTurns,omitempty"`
	MaxTurnsRequested int                 `json:"maxTurnsRequested,omitempty"`
	CWD               string              `json:"cwd,omitempty"`
	Files             []AttachmentPayload `json:"files,omitempty"`
	CodexThreadID     string              `json:"codexThreadId,omitempty"`
	ClaudeStarted     bool                `json:"claudeStarted,omitempty"`
	RunCWD            string              `json:"runCwd,omitempty"`
	Profile           string              `json:"profile,omitempty"`
}

type OrchestrationCancelPayload struct {
	RunID string `json:"runId"`
}

type OrchestrationEventPayload struct {
	ID             string          `json:"id,omitempty"`
	RunID          string          `json:"runId"`
	Seq            int64           `json:"seq,omitempty"`
	TurnID         string          `json:"turnId,omitempty"`
	Kind           string          `json:"kind"`
	Source         string          `json:"source,omitempty"`
	Severity       string          `json:"severity,omitempty"`
	Role           string          `json:"role,omitempty"`
	CLI            string          `json:"cli,omitempty"`
	Content        string          `json:"content,omitempty"`
	Status         string          `json:"status,omitempty"`
	Error          string          `json:"error,omitempty"`
	CommandData    *CommandData    `json:"commandData,omitempty"`
	RunStartData   *RunStartData   `json:"runStartData,omitempty"`
	TurnStartData  *TurnStartData  `json:"turnStartData,omitempty"`
	RunEndData     *RunEndData     `json:"runEndData,omitempty"`
	BridgeNoteData *BridgeNoteData `json:"bridgeNoteData,omitempty"`
	RunConclusion  *RunConclusion  `json:"runConclusion,omitempty"`
	Data           map[string]any  `json:"data,omitempty"`
	CreatedAt      int64           `json:"createdAt,omitempty"`
}

type ToolEvent struct {
	ID       string `json:"id,omitempty"`
	Status   string `json:"status,omitempty"`
	Command  string `json:"command,omitempty"`
	Output   string `json:"output,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

type CommandData struct {
	ID                    string `json:"id,omitempty"`
	Command               string `json:"command,omitempty"`
	Input                 string `json:"input,omitempty"`
	Output                string `json:"output,omitempty"`
	Name                  string `json:"name,omitempty"`
	Status                string `json:"status,omitempty"`
	ExitCode              *int   `json:"exitCode,omitempty"`
	StartedAt             int64  `json:"startedAt,omitempty"`
	CompletedAt           int64  `json:"completedAt,omitempty"`
	DurationMs            int64  `json:"durationMs,omitempty"`
	PID                   int    `json:"pid,omitempty"`
	PGID                  int    `json:"pgid,omitempty"`
	WillSuppressOnFailure bool   `json:"willSuppressOnFailure,omitempty"`
}

type RunStartData struct {
	CWD               string `json:"cwd,omitempty"`
	Mode              string `json:"mode,omitempty"`
	FirstCLI          string `json:"firstCli,omitempty"`
	MaxTurnsRequested int    `json:"maxTurnsRequested,omitempty"`
	MaxTurnsApplied   int    `json:"maxTurnsApplied,omitempty"`
	PromptSeq         int64  `json:"promptSeq,omitempty"`
	Profile           string `json:"profile,omitempty"`
}

type TurnStartData struct {
	CLI        string `json:"cli,omitempty"`
	Turn       int    `json:"turn,omitempty"`
	MaxTurns   int    `json:"maxTurns,omitempty"`
	PromptText string `json:"promptText,omitempty"`
	Profile    string `json:"profile,omitempty"`
	ResumeMode string `json:"resumeMode,omitempty"`
}

type RunEndData struct {
	CodexThreadID   string `json:"codexThreadId,omitempty"`
	ClaudeSessionID string `json:"claudeSessionId,omitempty"`
}

type BridgeNoteData struct {
	Category     string `json:"category,omitempty"`
	Command      string `json:"command,omitempty"`
	AfterSeconds int    `json:"afterSeconds,omitempty"`
	InjectedText string `json:"injectedText,omitempty"`
}

type RunConclusion struct {
	Outcome              string   `json:"outcome"`
	Summary              string   `json:"summary"`
	BuildOrAuditCommands []string `json:"buildOrAuditCommands,omitempty"`
	UnmetObligations     []string `json:"unmetObligations,omitempty"`
	EvidenceRefs         []string `json:"evidenceRefs,omitempty"`
}
