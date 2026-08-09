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

func TestBridgeReconnectDelayRetriesRegisteredConnectionImmediately(t *testing.T) {
	wait, next, immediate := bridgeReconnectDelay(20*time.Second, 5*time.Second, 30*time.Second, 10*time.Second, true)
	if wait != 0 || next != 20*time.Second || immediate {
		t.Fatalf("registered disconnect = (%s, %s, %t), want (0s, 20s, false)", wait, next, immediate)
	}
}

func TestBridgeReconnectDelayBacksOffAfterFailedReconnect(t *testing.T) {
	wait, next, immediate := bridgeReconnectDelay(5*time.Second, 5*time.Second, 30*time.Second, 0, false)
	if wait != 5*time.Second || next != 10*time.Second {
		t.Fatalf("first failed reconnect = (%s, %s), want (5s, 10s)", wait, next)
	}
	if immediate {
		t.Fatal("failed reconnect unexpectedly restored the immediate retry")
	}
	wait, next, immediate = bridgeReconnectDelay(next, 5*time.Second, 30*time.Second, 0, immediate)
	if wait != 10*time.Second || next != 20*time.Second {
		t.Fatalf("second failed reconnect = (%s, %s), want (10s, 20s)", wait, next)
	}
	wait, next, _ = bridgeReconnectDelay(30*time.Second, 5*time.Second, 30*time.Second, 0, immediate)
	if wait != 30*time.Second || next != 30*time.Second {
		t.Fatalf("capped reconnect = (%s, %s), want (30s, 30s)", wait, next)
	}
}

func TestBridgeReconnectDelayStableConnectionResetsBackoff(t *testing.T) {
	wait, next, immediate := bridgeReconnectDelay(30*time.Second, 5*time.Second, 30*time.Second, 30*time.Second, false)
	if wait != 0 || next != 5*time.Second || immediate {
		t.Fatalf("stable disconnect = (%s, %s, %t), want (0s, 5s, false)", wait, next, immediate)
	}
}

func TestBridgeReconnectDelayDoesNotLoopOnShortConnections(t *testing.T) {
	wait, next, immediate := bridgeReconnectDelay(5*time.Second, 5*time.Second, 30*time.Second, time.Millisecond, false)
	if wait != 5*time.Second || next != 10*time.Second || immediate {
		t.Fatalf("repeated short disconnect = (%s, %s, %t), want (5s, 10s, false)", wait, next, immediate)
	}
}
