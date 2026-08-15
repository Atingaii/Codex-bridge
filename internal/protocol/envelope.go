package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	TypeRegister                      = "register"
	TypeRegistered                    = "registered"
	TypeHeartbeat                     = "heartbeat"
	TypeOpenSession                   = "open_session"
	TypeSessionOpened                 = "session_opened"
	TypePrompt                        = "prompt"
	TypeSessionUpdate                 = "session_update"
	TypePromptComplete                = "prompt_complete"
	TypeApprovalRequest               = "approval_request"
	TypeApprovalResponse              = "approval_response"
	TypeCancel                        = "cancel"
	TypeCloseSession                  = "close_session"
	TypeOrchestrationStart            = "orchestration_start"
	TypeOrchestrationEvent            = "orchestration_event"
	TypeOrchestrationCancel           = "orchestration_cancel"
	TypeOrchestrationUsageSyncRequest = "orchestration_usage_sync_request"
	TypeOrchestrationUsageSyncResult  = "orchestration_usage_sync_result"
	TypeCLIConfigTest                 = "cli_config_test"
	TypeCLIConfigApply                = "cli_config_apply"
	TypeCLIConfigReset                = "cli_config_reset"
	TypeCLIConfigResult               = "cli_config_result"
	TypeAgentShutdown                 = "agent_shutdown"
	TypeError                         = "error"
	TypeStatus                        = "status"
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
	Runner           string                         `json:"runner,omitempty"`
	Sandbox          string                         `json:"sandbox,omitempty"`
	ApprovalPolicy   string                         `json:"approvalPolicy,omitempty"`
	Chat             map[string]BridgeCLICapability `json:"chat,omitempty"`
	Orchestration    map[string]BridgeCLICapability `json:"orchestration,omitempty"`
	Metadata         map[string]string              `json:"metadata,omitempty"`
	ACP              *ACPCapability                 `json:"acp,omitempty"`
	DurableTaskGraph bool                           `json:"durableTaskGraph,omitempty"`
	UsageLedger      bool                           `json:"usageLedger,omitempty"`
	ConfigSwitcher   *CLIConfigSwitcherCapability   `json:"configSwitcher,omitempty"`
}

type CLIConfigSwitcherCapability struct {
	Version   int      `json:"version"`
	PublicKey string   `json:"publicKey"`
	KeyID     string   `json:"keyId"`
	CLIs      []string `json:"clis"`
}

type EncryptedSecret struct {
	EphemeralPublicKey string `json:"ephemeralPublicKey"`
	Salt               string `json:"salt"`
	IV                 string `json:"iv"`
	Ciphertext         string `json:"ciphertext"`
}

type CLIConfigRequest struct {
	RequestID        string   `json:"requestId"`
	CLI              string   `json:"cli"`
	Name             string   `json:"name,omitempty"`
	BaseURL          string   `json:"baseUrl,omitempty"`
	Model            string   `json:"model,omitempty"`
	ReasoningEffort  string   `json:"reasoningEffort,omitempty"`
	ReasoningLevels  []string `json:"reasoningLevels,omitempty"`
	ReasoningDefault string   `json:"reasoningDefault,omitempty"`
	// ClaudeContextWindow and ClaudeDisableUnknownModelWindowEnforcement are
	// reviewed, Hub-authorized settings. Bridge only materializes them.
	ClaudeContextWindow                        int             `json:"claudeContextWindow,omitempty"`
	ClaudeDisableUnknownModelWindowEnforcement bool            `json:"claudeDisableUnknownModelWindowEnforcement,omitempty"`
	Secret                                     EncryptedSecret `json:"secret,omitempty"`
}

type CLIConfigResult struct {
	RequestID     string            `json:"requestId"`
	OK            bool              `json:"ok"`
	CLI           string            `json:"cli,omitempty"`
	BaseURL       string            `json:"baseUrl,omitempty"`
	Protocol      string            `json:"protocol,omitempty"`
	Models        []string          `json:"models,omitempty"`
	ModelsListed  bool              `json:"modelsListed,omitempty"`
	ModelMetadata *CLIModelMetadata `json:"modelMetadata,omitempty"`
	AppliedModel  string            `json:"appliedModel,omitempty"`
	Message       string            `json:"message,omitempty"`
	Error         string            `json:"error,omitempty"`
	ConfigChanged bool              `json:"configChanged,omitempty"`
}

// CLIModelMetadata is sourced only from the Hub's reviewed model catalog.
// An unknown provider alias intentionally has no reasoning levels.
type CLIModelMetadata struct {
	Model                    string   `json:"model"`
	Reviewed                 bool     `json:"reviewed"`
	SupportedReasoningLevels []string `json:"supportedReasoningLevels,omitempty"`
	DefaultReasoningLevel    string   `json:"defaultReasoningLevel,omitempty"`
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
	RunID                   string                          `json:"runId"`
	Mode                    string                          `json:"mode"`
	WorkerPair              string                          `json:"workerPair,omitempty"`
	FirstCLI                string                          `json:"firstCli,omitempty"`
	Prompt                  string                          `json:"prompt"`
	Context                 string                          `json:"context,omitempty"`
	Resume                  bool                            `json:"resume,omitempty"`
	PromptSeq               int64                           `json:"promptSeq,omitempty"`
	MaxTurns                int                             `json:"maxTurns,omitempty"`
	MaxTurnsRequested       int                             `json:"maxTurnsRequested,omitempty"`
	Round                   int                             `json:"round,omitempty"`
	MaxRounds               int                             `json:"maxRounds,omitempty"`
	CWD                     string                          `json:"cwd,omitempty"`
	Files                   []AttachmentPayload             `json:"files,omitempty"`
	CodexThreadID           string                          `json:"codexThreadId,omitempty"`
	CodexThreadIDs          map[string]string               `json:"codexThreadIds,omitempty"`
	ClaudeStarted           bool                            `json:"claudeStarted,omitempty"`
	ClaudeSessionIDs        map[string]string               `json:"claudeSessionIds,omitempty"`
	ClaudeStartedSlots      map[string]bool                 `json:"claudeStartedSlots,omitempty"`
	RunCWD                  string                          `json:"runCwd,omitempty"`
	Profile                 string                          `json:"profile,omitempty"`
	NativeContextCompaction string                          `json:"nativeContextCompaction,omitempty"`
	PlanWorkspace           bool                            `json:"planWorkspace,omitempty"`
	TaskGraph               *TaskGraphPayload               `json:"taskGraph,omitempty"`
	WorkerProfiles          map[string]WorkerProfileBinding `json:"workerProfiles,omitempty"`
}

// WorkerProfileBinding is an immutable Hub-authorized worker snapshot. The
// credential remains encrypted end-to-end for the selected Bridge.
type WorkerProfileBinding struct {
	PresetID                                   string          `json:"presetId"`
	CLI                                        string          `json:"cli"`
	Name                                       string          `json:"name"`
	BaseURL                                    string          `json:"baseUrl"`
	Model                                      string          `json:"model"`
	ReasoningEffort                            string          `json:"reasoningEffort,omitempty"`
	ReasoningLevels                            []string        `json:"reasoningLevels,omitempty"`
	ReasoningDefault                           string          `json:"reasoningDefault,omitempty"`
	ClaudeContextWindow                        int             `json:"claudeContextWindow,omitempty"`
	ClaudeDisableUnknownModelWindowEnforcement bool            `json:"claudeDisableUnknownModelWindowEnforcement,omitempty"`
	Secret                                     EncryptedSecret `json:"secret"`
}

type TaskGraphPayload struct {
	ID            string        `json:"id"`
	Generation    int           `json:"generation"`
	Round         int           `json:"round"`
	MaxRounds     int           `json:"maxRounds"`
	ParallelLimit int           `json:"parallelLimit"`
	Tasks         []TaskPayload `json:"tasks"`
}

type TaskPayload struct {
	ID            string   `json:"id"`
	AttemptID     string   `json:"attemptId"`
	Name          string   `json:"name"`
	Role          string   `json:"role"`
	WorkerSlot    string   `json:"workerSlot,omitempty"`
	PayloadDigest string   `json:"payloadDigest"`
	Dependencies  []string `json:"dependencies,omitempty"`
}

type TaskAttemptRef struct {
	GraphID       string `json:"graphId"`
	TaskID        string `json:"taskId"`
	AttemptID     string `json:"attemptId"`
	Name          string `json:"name,omitempty"`
	Role          string `json:"role,omitempty"`
	WorkerSlot    string `json:"workerSlot,omitempty"`
	Round         int    `json:"round,omitempty"`
	MaxRounds     int    `json:"maxRounds,omitempty"`
	PayloadDigest string `json:"payloadDigest"`
}

type OrchestrationCancelPayload struct {
	RunID string `json:"runId"`
}

type OrchestrationUsageSession struct {
	CLI        string `json:"cli"`
	WorkerSlot string `json:"workerSlot,omitempty"`
	SessionID  string `json:"sessionId"`
	// Isolated prevents a post-run ledger scan from falling back to the
	// operator's global CLI home for a profile-bound worker.
	Isolated bool `json:"isolated,omitempty"`
}

type OrchestrationUsageSyncRequest struct {
	RunID    string                      `json:"runId"`
	Sessions []OrchestrationUsageSession `json:"sessions"`
}

type OrchestrationUsageEvent struct {
	EventID          string `json:"eventId"`
	CLI              string `json:"cli"`
	WorkerSlot       string `json:"workerSlot,omitempty"`
	SessionID        string `json:"sessionId"`
	OccurredAt       int64  `json:"occurredAt,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	InputTokens      int64  `json:"inputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	ReasoningTokens  int64  `json:"reasoningTokens,omitempty"`
}

type OrchestrationUsageSessionResult struct {
	CLI        string `json:"cli"`
	WorkerSlot string `json:"workerSlot,omitempty"`
	SessionID  string `json:"sessionId"`
	Status     string `json:"status"`
	EventCount int    `json:"eventCount"`
	Error      string `json:"error,omitempty"`
}

type OrchestrationUsageSyncResult struct {
	RunID     string                            `json:"runId"`
	Status    string                            `json:"status"`
	ScannedAt int64                             `json:"scannedAt"`
	Sessions  []OrchestrationUsageSessionResult `json:"sessions"`
	Events    []OrchestrationUsageEvent         `json:"events"`
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
	TurnEndData    *TurnEndData    `json:"turnEndData,omitempty"`
	RunEndData     *RunEndData     `json:"runEndData,omitempty"`
	BridgeNoteData *BridgeNoteData `json:"bridgeNoteData,omitempty"`
	RunConclusion  *RunConclusion  `json:"runConclusion,omitempty"`
	Data           map[string]any  `json:"data,omitempty"`
	CreatedAt      int64           `json:"createdAt,omitempty"`
	Task           *TaskAttemptRef `json:"task,omitempty"`
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
	CWD                     string `json:"cwd,omitempty"`
	Mode                    string `json:"mode,omitempty"`
	WorkerPair              string `json:"workerPair,omitempty"`
	FirstCLI                string `json:"firstCli,omitempty"`
	MaxTurnsRequested       int    `json:"maxTurnsRequested,omitempty"`
	MaxTurnsApplied         int    `json:"maxTurnsApplied,omitempty"`
	Round                   int    `json:"round,omitempty"`
	MaxRounds               int    `json:"maxRounds,omitempty"`
	PromptSeq               int64  `json:"promptSeq,omitempty"`
	Profile                 string `json:"profile,omitempty"`
	NativeContextCompaction string `json:"nativeContextCompaction,omitempty"`
}

type TurnStartData struct {
	StartedAt       int64  `json:"startedAt,omitempty"`
	CLI             string `json:"cli,omitempty"`
	WorkerSlot      string `json:"workerSlot,omitempty"`
	PresetName      string `json:"presetName,omitempty"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	Turn            int    `json:"turn,omitempty"`
	MaxTurns        int    `json:"maxTurns,omitempty"`
	Round           int    `json:"round,omitempty"`
	MaxRounds       int    `json:"maxRounds,omitempty"`
	PromptText      string `json:"promptText,omitempty"`
	Profile         string `json:"profile,omitempty"`
	ResumeMode      string `json:"resumeMode,omitempty"`
}

// TurnEndData records elapsed wall-clock time for one CLI turn. It is emitted
// by Bridge so follow-up prompts append independently timed turns to the same
// orchestration run.
type TurnEndData struct {
	StartedAt   int64 `json:"startedAt,omitempty"`
	CompletedAt int64 `json:"completedAt,omitempty"`
	DurationMs  int64 `json:"durationMs,omitempty"`
}

type RunEndData struct {
	CodexThreadID      string             `json:"codexThreadId,omitempty"`
	CodexThreadIDs     map[string]string  `json:"codexThreadIds,omitempty"`
	ClaudeSessionID    string             `json:"claudeSessionId,omitempty"`
	ClaudeSessionIDs   map[string]string  `json:"claudeSessionIds,omitempty"`
	WorkerPair         string             `json:"workerPair,omitempty"`
	NativeResume       []NativeResumeInfo `json:"nativeResume,omitempty"`
	CodexNativeResume  *NativeResumeInfo  `json:"codexNativeResume,omitempty"`
	ClaudeNativeResume *NativeResumeInfo  `json:"claudeNativeResume,omitempty"`
	TerminalReason     string             `json:"terminalReason,omitempty"`
	VerifierVerdict    *VerifierVerdict   `json:"verifierVerdict,omitempty"`
}

type VerifierVerdict struct {
	Status   string          `json:"status"`
	Reason   string          `json:"reason"`
	Evidence []string        `json:"evidence,omitempty"`
	Checkers []VerifierCheck `json:"checkers,omitempty"`
}

// VerifierCheck is one bounded deterministic adjudication role. A passed
// verdict requires every checker to pass.
type VerifierCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type BridgeNoteData struct {
	Category     string `json:"category,omitempty"`
	Command      string `json:"command,omitempty"`
	AfterSeconds int    `json:"afterSeconds,omitempty"`
	InjectedText string `json:"injectedText,omitempty"`
}

type NativeResumeInfo struct {
	CLI              string `json:"cli,omitempty"`
	WorkerSlot       string `json:"workerSlot,omitempty"`
	ID               string `json:"id,omitempty"`
	Command          string `json:"command,omitempty"`
	CWD              string `json:"cwd,omitempty"`
	TranscriptPath   string `json:"transcriptPath,omitempty"`
	Visible          bool   `json:"visible"`
	VisibilityReason string `json:"visibilityReason,omitempty"`
}

const (
	NativeContextCompactionOff       = "off"
	NativeContextCompactionAfterTurn = "after-turn"
	WorkerPairClaudeCodex            = "claude-codex"
	WorkerPairCodexCodex             = "codex-codex"
	WorkerPairClaudeClaude           = "claude-claude"
)

func NormalizeNativeContextCompaction(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case NativeContextCompactionAfterTurn:
		return NativeContextCompactionAfterTurn
	default:
		return NativeContextCompactionOff
	}
}

func NormalizeOrchestrationWorkerPair(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case WorkerPairCodexCodex:
		return WorkerPairCodexCodex
	case WorkerPairClaudeClaude:
		return WorkerPairClaudeClaude
	default:
		return WorkerPairClaudeCodex
	}
}

type RunConclusion struct {
	Outcome              string   `json:"outcome"`
	Summary              string   `json:"summary"`
	BuildOrAuditCommands []string `json:"buildOrAuditCommands,omitempty"`
	UnmetObligations     []string `json:"unmetObligations,omitempty"`
	EvidenceRefs         []string `json:"evidenceRefs,omitempty"`
}
