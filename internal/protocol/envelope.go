package protocol

import (
	"encoding/json"
	"fmt"
)

const (
	TypeRegister       = "register"
	TypeRegistered     = "registered"
	TypeHeartbeat      = "heartbeat"
	TypeOpenSession    = "open_session"
	TypeSessionOpened  = "session_opened"
	TypePrompt         = "prompt"
	TypeSessionUpdate  = "session_update"
	TypePromptComplete = "prompt_complete"
	TypeCancel         = "cancel"
	TypeCloseSession   = "close_session"
	TypeError          = "error"
	TypeStatus         = "status"
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
	Name      string `json:"name"`
	MachineID string `json:"machineId"`
	Hostname  string `json:"hostname"`
	Version   string `json:"version"`
	Instance  string `json:"instance,omitempty"`
}

type RegisteredPayload struct {
	AgentID string `json:"agentId"`
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
	Content  string `json:"content"`
	PromptID string `json:"promptId,omitempty"`
	RunID    string `json:"runId,omitempty"`
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

type ToolEvent struct {
	ID       string `json:"id,omitempty"`
	Status   string `json:"status,omitempty"`
	Command  string `json:"command,omitempty"`
	Output   string `json:"output,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}
