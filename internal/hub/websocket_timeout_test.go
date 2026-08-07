package hub

import (
	"testing"
	"time"
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
