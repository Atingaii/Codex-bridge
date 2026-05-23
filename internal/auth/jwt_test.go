package auth

import (
	"testing"
	"time"
)

func TestSignerRoundTrip(t *testing.T) {
	signer := NewSigner("test-secret-32-byte-minimum-value", time.Hour)
	token, expires, err := signer.Sign("usr_123")
	if err != nil {
		t.Fatal(err)
	}
	if !expires.After(time.Now()) {
		t.Fatalf("expires not in future: %v", expires)
	}
	uid, err := signer.Parse(token)
	if err != nil {
		t.Fatal(err)
	}
	if uid != "usr_123" {
		t.Fatalf("uid = %q", uid)
	}
}

func TestSignerRejectsWrongSecret(t *testing.T) {
	token, _, err := NewSigner("test-secret-32-byte-minimum-value", time.Hour).Sign("usr_123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSigner("different-test-secret-32-byte-min", time.Hour).Parse(token); err == nil {
		t.Fatal("expected parse error")
	}
}
