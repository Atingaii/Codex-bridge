package hub

import (
	"net/netip"
	"testing"
)

func TestCLIConfigProbeRejectsNonPublicDestinations(t *testing.T) {
	for _, host := range []string{"localhost", "api.localhost", "127.0.0.1", "10.0.0.1", "169.254.169.254", "[::1]", "[fc00::1]"} {
		if err := validateCLIConfigHost(host); err == nil {
			t.Fatalf("validateCLIConfigHost(%q) accepted a non-public destination", host)
		}
	}
	for _, raw := range []string{"100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1"} {
		ip := netip.MustParseAddr(raw)
		if isPublicCLIConfigIP(ip) {
			t.Fatalf("isPublicCLIConfigIP(%q) = true", raw)
		}
	}
	if !isPublicCLIConfigIP(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public address rejected")
	}
}

func TestCLIConfigProviderCandidatesRejectHTTPOutsideDevelopment(t *testing.T) {
	s, _ := newAuthTestServer(t)
	s.cfg.App.Env = "prod"
	if _, err := s.cliConfigProviderCandidates("http://api.example.com/v1"); err == nil {
		t.Fatal("production candidate generation accepted HTTP")
	}
	if _, err := s.cliConfigProviderCandidates("https://127.0.0.1/v1"); err == nil {
		t.Fatal("candidate generation accepted loopback")
	}
}
