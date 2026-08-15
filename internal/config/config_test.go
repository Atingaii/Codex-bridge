package config

import "testing"

func TestDefaultRegistrationRequiresTurnstile(t *testing.T) {
	if !Default().Auth.Registration.RequireTurnstile {
		t.Fatal("default registration must require Turnstile")
	}
}
