package bridge

import (
	"testing"
	"time"
)

func TestBridgeWebSocketReadTimeout(t *testing.T) {
	for _, tc := range []struct {
		heartbeat time.Duration
		want      time.Duration
	}{
		{0, 90 * time.Second},
		{15 * time.Second, 90 * time.Second},
		{30 * time.Second, 3 * time.Minute},
	} {
		if got := bridgeWebSocketReadTimeout(tc.heartbeat); got != tc.want {
			t.Fatalf("bridgeWebSocketReadTimeout(%s) = %s, want %s", tc.heartbeat, got, tc.want)
		}
	}
}

func TestBridgeHeartbeatIntervalDefaultsWhenUnset(t *testing.T) {
	if got := bridgeHeartbeatInterval(0); got != 15*time.Second {
		t.Fatalf("bridgeHeartbeatInterval(0) = %s, want 15s", got)
	}
	if got := bridgeHeartbeatInterval(2 * time.Second); got != 2*time.Second {
		t.Fatalf("bridgeHeartbeatInterval(2s) = %s, want 2s", got)
	}
}
