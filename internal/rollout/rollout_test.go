package rollout

import "testing"

func TestEvaluatorPolicies(t *testing.T) {
	e := New(map[string]string{
		"off":      "off",
		"admins":   "admin",
		"everyone": "all",
		"selected": "users:alice, Bob",
		"none":     "percent:0",
		"full":     "percent:100",
	})
	member := Subject{ID: "usr_member", Username: "alice"}
	admin := Subject{ID: "usr_admin", Username: "root", Admin: true}
	for feature, want := range map[string]bool{"off": false, "admins": false, "everyone": true, "selected": true, "none": false, "full": true} {
		if got := e.Enabled(feature, member); got != want {
			t.Errorf("member feature %q = %v, want %v", feature, got, want)
		}
	}
	if !e.Enabled("admins", admin) {
		t.Fatal("admin policy rejected administrator")
	}
}

func TestPercentageRolloutIsStable(t *testing.T) {
	e := New(map[string]string{"candidate": "percent:37"})
	subject := Subject{ID: "usr_stable", Username: "tester"}
	want := e.Enabled("candidate", subject)
	for i := 0; i < 100; i++ {
		if got := e.Enabled("candidate", subject); got != want {
			t.Fatalf("percentage assignment changed: got %v, want %v", got, want)
		}
	}
	if e.Enabled("candidate", Subject{}) {
		t.Fatal("anonymous subject entered percentage rollout")
	}
}

func TestMalformedAndEmptyPoliciesFailClosed(t *testing.T) {
	e := New(map[string]string{
		"bad-percent": "percent:nope",
		"empty-users": "users:",
		"unknown":     "sometimes",
	})
	for _, feature := range []string{"bad-percent", "empty-users", "unknown", "missing"} {
		if e.Enabled(feature, Subject{}) {
			t.Fatalf("feature %q unexpectedly enabled", feature)
		}
	}
}

func TestRegisteredFeaturesAreNormalizedAndUnique(t *testing.T) {
	features := RegisteredFeatures()
	seen := make(map[string]bool, len(features))
	for _, feature := range features {
		if feature == "" || feature != normalizeFeature(feature) {
			t.Fatalf("feature registry contains invalid key %q", feature)
		}
		if seen[feature] {
			t.Fatalf("feature registry contains duplicate key %q", feature)
		}
		seen[feature] = true
	}
	features[0] = "mutated"
	if RegisteredFeatures()[0] == "mutated" {
		t.Fatal("registered feature list leaked mutable package state")
	}
}

func TestPlanWorkspaceAdminRollout(t *testing.T) {
	e := New(map[string]string{FeatureOrchestrationPlanWorkspace: "admin"})
	if !e.Enabled(FeatureOrchestrationPlanWorkspace, Subject{ID: "usr_admin", Username: "admin", Admin: true}) {
		t.Fatal("plan workspace admin rollout rejected administrator")
	}
	if e.Enabled(FeatureOrchestrationPlanWorkspace, Subject{ID: "usr_member", Username: "member"}) {
		t.Fatal("plan workspace admin rollout admitted regular user")
	}
}
