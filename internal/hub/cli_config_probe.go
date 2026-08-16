package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	cliConfigHubProbeLimit  = 75 * time.Second
	cliConfigHubCallLimit   = 30 * time.Second
	maxCLIConfigAPIKeyBytes = 16 << 10
	maxHubDiscoveredModels  = 500
	maxHubModelIDBytes      = 256
)

// probeCLIConfigProvider performs the account-library probe without involving
// a Bridge. Its transport resolves and validates every dial target so a saved
// provider URL cannot turn the public Hub into an internal-network proxy.
func (s *Server) probeCLIConfigProvider(parent context.Context, cli, rawBase, model, key string) (string, string, []string, bool, error) {
	candidates, err := s.cliConfigProviderCandidates(rawBase)
	if err != nil {
		return "", "", nil, false, err
	}
	probeCtx, stop := context.WithTimeout(parent, cliConfigHubProbeLimit)
	defer stop()
	client := s.cliConfigProbeClient()
	var authFailed bool
	var messages []string
	for _, base := range candidates {
		ctx, cancel := context.WithTimeout(probeCtx, cliConfigHubCallLimit)
		models, status, fetchErr := hubFetchModels(ctx, client, base, key, cli)
		cancel()
		if fetchErr == nil && status >= 200 && status < 300 {
			if strings.TrimSpace(model) == "" {
				return base, hubProtocolForCLI(cli), models, true, nil
			}
			ctx, cancel = context.WithTimeout(probeCtx, cliConfigHubCallLimit)
			inferenceStatus, inferenceErr := hubProbeInference(ctx, client, base, model, key, cli)
			cancel()
			if inferenceErr == nil && inferenceStatus >= 200 && inferenceStatus < 300 {
				return base, hubProtocolForCLI(cli), models, true, nil
			}
			if inferenceStatus == http.StatusUnauthorized || inferenceStatus == http.StatusForbidden {
				authFailed = true
			}
			messages = appendProbeMessage(messages, base, inferenceStatus, inferenceErr, "inference")
			continue
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			authFailed = true
		}
		if strings.TrimSpace(model) != "" && status != http.StatusUnauthorized && status != http.StatusForbidden {
			ctx, cancel = context.WithTimeout(probeCtx, cliConfigHubCallLimit)
			inferenceStatus, inferenceErr := hubProbeInference(ctx, client, base, model, key, cli)
			cancel()
			if inferenceErr == nil && inferenceStatus >= 200 && inferenceStatus < 300 {
				return base, hubProtocolForCLI(cli), nil, false, nil
			}
			if inferenceStatus == http.StatusUnauthorized || inferenceStatus == http.StatusForbidden {
				authFailed = true
			}
		}
		messages = appendProbeMessage(messages, base, status, fetchErr, "")
	}
	if authFailed {
		return "", "", nil, false, errors.New("API Key authentication failed")
	}
	return "", "", nil, false, fmt.Errorf("connection test failed: %s", strings.Join(messages, "; "))
}

func appendProbeMessage(messages []string, base string, status int, err error, operation string) []string {
	if err != nil {
		message := strings.TrimSpace(err.Error())
		if len(message) > 180 {
			message = message[:180] + "..."
		}
		return append(messages, message)
	}
	if operation != "" {
		return append(messages, fmt.Sprintf("%s %s returned HTTP %d", base, operation, status))
	}
	return append(messages, fmt.Sprintf("%s returned HTTP %d", base, status))
}

func (s *Server) cliConfigProviderCandidates(raw string) ([]string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("Base URL must start with http:// or https://")
	}
	if u.Scheme != "https" && s.cfg.App.Env != "dev" && s.cfg.App.Env != "test" {
		return nil, errors.New("Base URL must use HTTPS")
	}
	if err := validateCLIConfigHost(u.Hostname()); err != nil {
		return nil, err
	}
	path := strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/messages", "/models"} {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimSuffix(path, suffix)
		}
	}
	u.Path, u.RawQuery, u.Fragment = path, "", ""
	base := strings.TrimRight(u.String(), "/")
	root := strings.TrimSuffix(base, "/v1")
	values, seen := []string{base, root + "/v1", root}, map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimRight(value, "/")
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out, nil
}

func (s *Server) cliConfigProbeClient() *http.Client {
	dialer := &net.Dialer{Timeout: cliConfigHubCallLimit}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if err := validateCLIConfigHost(host); err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !isPublicCLIConfigIP(ip) {
					return nil, errors.New("Base URL resolves to a non-public address")
				}
			}
			if len(ips) == 0 {
				return nil, errors.New("Base URL did not resolve to a public address")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	return &http.Client{Timeout: cliConfigHubCallLimit, Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func validateCLIConfigHost(host string) error {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" {
		return errors.New("Base URL host is required")
	}
	if ip, err := netip.ParseAddr(host); err == nil && !isPublicCLIConfigIP(ip) {
		return errors.New("Base URL must not use a private or local address")
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return errors.New("Base URL must not use a local address")
	}
	return nil
}

func isPublicCLIConfigIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	// RFC 6598 shared space and the non-routable/reserved IPv4 ranges are not
	// suitable provider destinations even though some are global-unicast.
	if ip.Is4() {
		return !netip.MustParsePrefix("100.64.0.0/10").Contains(ip) &&
			!netip.MustParsePrefix("192.0.0.0/24").Contains(ip) &&
			!netip.MustParsePrefix("192.0.2.0/24").Contains(ip) &&
			!netip.MustParsePrefix("198.18.0.0/15").Contains(ip) &&
			!netip.MustParsePrefix("198.51.100.0/24").Contains(ip) &&
			!netip.MustParsePrefix("203.0.113.0/24").Contains(ip) &&
			!netip.MustParsePrefix("240.0.0.0/4").Contains(ip)
	}
	return true
}

func hubFetchModels(ctx context.Context, client *http.Client, base, key, cli string) ([]string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/models", nil)
	if err != nil {
		return nil, 0, err
	}
	hubSetProviderHeaders(req, key, cli)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, nil
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
		return nil, resp.StatusCode, err
	}
	seen, models := map[string]bool{}, make([]string, 0, len(body.Data)+len(body.Models))
	for _, item := range append(body.Data, body.Models...) {
		id := strings.TrimSpace(item.ID)
		if id != "" && len(id) <= maxHubModelIDBytes && !seen[id] {
			seen[id] = true
			models = append(models, id)
			if len(models) == maxHubDiscoveredModels {
				break
			}
		}
	}
	sort.Strings(models)
	return models, resp.StatusCode, nil
}

func hubProbeInference(ctx context.Context, client *http.Client, base, model, key, cli string) (int, error) {
	endpoint, payload := strings.TrimRight(base, "/")+"/responses", map[string]any{"model": model, "input": "ping", "max_output_tokens": 16}
	if cli == "claude" {
		endpoint, payload = strings.TrimRight(base, "/")+"/messages", map[string]any{"model": model, "max_tokens": 1, "messages": []map[string]string{{"role": "user", "content": "ping"}}}
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	hubSetProviderHeaders(req, key, cli)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}

func hubSetProviderHeaders(req *http.Request, key, cli string) {
	req.Header.Set("Authorization", "Bearer "+key)
	if cli == "claude" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
}
func hubProtocolForCLI(cli string) string {
	if cli == "claude" {
		return "anthropic-compatible"
	}
	return "openai-compatible"
}
