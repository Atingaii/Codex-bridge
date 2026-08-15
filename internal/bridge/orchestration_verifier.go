package bridge

import (
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

const (
	verifierVerdictPass     = "pass"
	verifierVerdictContinue = "continue"
)

// evaluateOrchestrationVerdict is deliberately local and deterministic. It
// never executes a command or starts another model process; it only judges the
// completed turn and the command lifecycle evidence already emitted by the
// worker CLI.
func evaluateOrchestrationVerdict(mode, profile string, durable bool, history []orchestrationTurn) protocol.VerifierVerdict {
	if len(history) == 0 {
		return verifierQuorum("no completed turn is available", nil)
	}
	record := history[len(history)-1]
	packet := record.Relay
	handoff := protocol.VerifierCheck{Name: "handoff", Status: verifierVerdictPass, Reason: "structured resolved final handoff has no remaining action or risk"}
	if !packet.Structured {
		handoff = verifierCheckContinue("handoff", "worker did not return a structured handoff")
	}
	if packet.Status != "resolved" || packet.To != "user" || packet.Intent != "final" {
		handoff = verifierCheckContinue("handoff", "worker has not made a resolved final claim to the user")
	}
	if !machineExplicitNone(packet.Next) || !machineExplicitNone(packet.Risks) {
		handoff = verifierCheckContinue("handoff", "worker reported remaining work or risks")
	}
	evidence := protocol.VerifierCheck{Name: "evidence", Status: verifierVerdictPass, Reason: "successful command evidence was recorded"}
	if !relayHasSuccessfulCommand(record.Tools) {
		evidence = verifierCheckContinue("evidence", "no successful command evidence was recorded in this turn")
	}
	if normalizeOrchestrationProfile(profile) == "formal-proof" && !relayHasSuccessfulFormalCheck(record.Tools) {
		evidence = verifierCheckContinue("evidence", "formal-proof completion requires a successful recognized proof checker")
	}
	independence := protocol.VerifierCheck{Name: "independence", Status: verifierVerdictPass, Reason: "independent reviewer boundary and participant evidence are present"}
	if durable {
		if record.Role != store.TaskRoleReviewer {
			independence = verifierCheckContinue("independence", "only the durable graph reviewer can finish the run early")
		}
	} else {
		if mode == "collaboration" && record.Role != "reviewer" {
			independence = verifierCheckContinue("independence", "collaboration completion requires a reviewer verdict")
		}
		if mode == "debate" && record.Role != "critic" {
			independence = verifierCheckContinue("independence", "debate completion requires a critic verdict")
		}
		if relayParticipantCount(history) < 2 {
			independence = verifierCheckContinue("independence", "independent confirmation requires two worker participants")
		}
	}
	return verifierQuorum("independent worker review and command evidence confirmed completion", []protocol.VerifierCheck{handoff, evidence, independence})
}

func verifierPass(record orchestrationTurn, reason string) protocol.VerifierVerdict {
	evidence := []string{"structured resolved final handoff", "successful command evidence"}
	if relayHasSuccessfulFormalCheck(record.Tools) {
		evidence = append(evidence, "successful recognized proof checker")
	}
	return protocol.VerifierVerdict{Status: verifierVerdictPass, Reason: reason, Evidence: evidence}
}

func verifierCheckContinue(name, reason string) protocol.VerifierCheck {
	return protocol.VerifierCheck{Name: name, Status: verifierVerdictContinue, Reason: reason}
}

func verifierQuorum(reason string, checks []protocol.VerifierCheck) protocol.VerifierVerdict {
	if len(checks) == 0 {
		checks = []protocol.VerifierCheck{{Name: "handoff", Status: verifierVerdictContinue, Reason: reason}, {Name: "evidence", Status: verifierVerdictContinue, Reason: reason}, {Name: "independence", Status: verifierVerdictContinue, Reason: reason}}
	}
	for _, check := range checks {
		if check.Status != verifierVerdictPass {
			return protocol.VerifierVerdict{Status: verifierVerdictContinue, Reason: check.Reason, Checkers: checks}
		}
	}
	return protocol.VerifierVerdict{
		Status:   verifierVerdictPass,
		Reason:   reason,
		Evidence: []string{"structured resolved final handoff", "successful command evidence", "independent reviewer boundary"},
		Checkers: checks,
	}
}

func verifierContinue(reason string) protocol.VerifierVerdict {
	return protocol.VerifierVerdict{Status: verifierVerdictContinue, Reason: strings.TrimSpace(reason)}
}

func verifierVerdictEvent(turnID string, record orchestrationTurn, verdict protocol.VerifierVerdict) protocol.OrchestrationEventPayload {
	content := "Verifier: " + verdict.Reason
	if len(verdict.Checkers) > 0 {
		parts := make([]string, 0, len(verdict.Checkers))
		for _, check := range verdict.Checkers {
			parts = append(parts, check.Name+": "+check.Status)
		}
		content = "Verifier (" + strings.Join(parts, ", ") + "): " + verdict.Reason
	}
	return protocol.OrchestrationEventPayload{
		Kind:    "verifier.verdict",
		Source:  "verifier",
		Role:    "verifier",
		CLI:     record.CLI,
		TurnID:  turnID,
		Status:  verdict.Status,
		Content: content,
		Data: map[string]any{
			"verdict": verdict,
		},
	}
}
