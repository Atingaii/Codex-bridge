package hub

import (
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
)

func TestResilientWebSocketReadTimeout(t *testing.T) {
	tests := []struct {
		configured time.Duration
		heartbeat  time.Duration
		want       time.Duration
	}{
		{0, 15 * time.Second, 90 * time.Second},
		{45 * time.Second, 15 * time.Second, 90 * time.Second},
		{2 * time.Minute, 15 * time.Second, 2 * time.Minute},
		{0, 30 * time.Second, 3 * time.Minute},
	}
	for _, tc := range tests {
		if got := resilientWebSocketReadTimeout(tc.configured, tc.heartbeat); got != tc.want {
			t.Fatalf("resilientWebSocketReadTimeout(%s, %s) = %s, want %s", tc.configured, tc.heartbeat, got, tc.want)
		}
	}
}

func TestBridgeReconnectGraceUsesEffectiveBridgeBackoff(t *testing.T) {
	tests := []struct {
		min  time.Duration
		max  time.Duration
		want time.Duration
	}{
		{0, 0, 6 * time.Second},
		{5 * time.Second, 30 * time.Second, 31 * time.Second},
		{10 * time.Second, 5 * time.Second, 11 * time.Second},
	}
	for _, tc := range tests {
		cfg := config.Default()
		cfg.Bridge.ReconnectMin = config.Duration{Duration: tc.min}
		cfg.Bridge.ReconnectMax = config.Duration{Duration: tc.max}
		s := &Server{cfg: &cfg}
		if got := s.bridgeReconnectGrace(); got != tc.want {
			t.Fatalf("bridgeReconnectGrace(%s, %s) = %s, want %s", tc.min, tc.max, got, tc.want)
		}
	}
}
