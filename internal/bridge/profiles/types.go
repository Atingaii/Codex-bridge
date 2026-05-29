package profiles

import (
	"strings"
	"time"
)

const Default = "default"

func Formal() string {
	return "formal-" + "proof"
}

func Normalize(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case Formal():
		return Formal()
	default:
		return Default
	}
}

func IsFormal(value string) bool {
	return Normalize(value) == Formal()
}

type HandoffFields struct {
	Status   string
	Changed  string
	Verified string
	Next     string
	Risks    string
}

type Turn struct {
	TurnID        string
	Role          string
	CLI           string
	Msg           string
	Content       string
	Handoff       string
	HandoffFields HandoffFields
	Err           string
	Commands      []Command
}

type Command struct {
	ID          string
	Status      string
	Command     string
	Output      string
	ExitCode    *int
	StartedAt   time.Time
	CompletedAt time.Time
}

type WorkspaceChangeReport struct {
	Changed   []string
	Available bool
	Truncated bool
	Err       string
}

type Dimension struct {
	NameZH   string
	NameEN   string
	StatusZH string
	StatusEN string
	DetailZH string
	DetailEN string
}

type CommandFingerprint struct {
	CWD          string
	Command      string
	LastStatus   string
	LastExitCode *int
	RanAtLeast   time.Duration
	TimedOut     bool
	LastSeen     time.Time
}
