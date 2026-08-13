// Package rollout evaluates named, user-scoped feature rollout policies.
package rollout

import (
	"hash/fnv"
	"strconv"
	"strings"
)

const (
	FeatureStrictWorkspace            = "strict-workspace"
	FeatureOrchestrationPlanWorkspace = "orchestration-plan-workspace"
)

var registeredFeatures = []string{
	FeatureStrictWorkspace,
	FeatureOrchestrationPlanWorkspace,
}

// RegisteredFeatures returns the user-visible capabilities that may be exposed
// to clients. A feature must be registered here before it can appear in
// user.features.
func RegisteredFeatures() []string {
	return append([]string(nil), registeredFeatures...)
}

type Subject struct {
	ID       string
	Username string
	Admin    bool
}

type Evaluator struct {
	policies map[string]string
}

func New(policies map[string]string) Evaluator {
	copy := make(map[string]string, len(policies))
	for feature, policy := range policies {
		feature = normalizeFeature(feature)
		if feature != "" {
			copy[feature] = strings.TrimSpace(strings.ToLower(policy))
		}
	}
	return Evaluator{policies: copy}
}

func (e Evaluator) Enabled(feature string, subject Subject) bool {
	feature = normalizeFeature(feature)
	policy := e.policies[feature]
	switch policy {
	case "all", "on", "enabled":
		return true
	case "admin":
		return subject.Admin
	case "off", "disabled", "":
		return false
	}
	if raw, ok := strings.CutPrefix(policy, "percent:"); ok {
		percent, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || percent <= 0 {
			return false
		}
		if percent >= 100 {
			return true
		}
		if strings.TrimSpace(subject.ID) == "" {
			return false
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(feature + "\x00" + subject.ID))
		return int(h.Sum32()%100) < percent
	}
	if raw, ok := strings.CutPrefix(policy, "users:"); ok {
		username := strings.TrimSpace(subject.Username)
		if username == "" {
			return false
		}
		for _, candidate := range strings.Split(raw, ",") {
			if configured := strings.TrimSpace(candidate); configured != "" && strings.EqualFold(configured, username) {
				return true
			}
		}
	}
	return false
}

func (e Evaluator) EnabledFeatures(features []string, subject Subject) []string {
	enabled := make([]string, 0, len(features))
	for _, feature := range features {
		if e.Enabled(feature, subject) {
			enabled = append(enabled, normalizeFeature(feature))
		}
	}
	return enabled
}

func normalizeFeature(feature string) string {
	return strings.TrimSpace(strings.ToLower(feature))
}
