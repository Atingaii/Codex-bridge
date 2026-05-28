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
	RunID     string              `json:"runId"`
	Mode      string              `json:"mode"`
	FirstCLI  string              `json:"firstCli,omitempty"`
	Prompt    string              `json:"prompt"`
	Context   string              `json:"context,omitempty"`
	Resume    bool                `json:"resume,omitempty"`
	PromptSeq int64               `json:"promptSeq,omitempty"`
	MaxTurns  int                 `json:"maxTurns,omitempty"`
	CWD       string              `json:"cwd,omitempty"`
	Files     []AttachmentPayload `json:"files,omitempty"`
}

type OrchestrationCancelPayload struct {
	RunID string `json:"runId"`
}

type OrchestrationEventPayload struct {
	ID        string         `json:"id,omitempty"`
	RunID     string         `json:"runId"`
	Seq       int64          `json:"seq,omitempty"`
	TurnID    string         `json:"turnId,omitempty"`
	Kind      string         `json:"kind"`
	Role      string         `json:"role,omitempty"`
	CLI       string         `json:"cli,omitempty"`
	Content   string         `json:"content,omitempty"`
	Status    string         `json:"status,omitempty"`
	Error     string         `json:"error,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt int64          `json:"createdAt,omitempty"`
}

type ToolEvent struct {
	ID       string `json:"id,omitempty"`
	Status   string `json:"status,omitempty"`
	Command  string `json:"command,omitempty"`
	Output   string `json:"output,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}
