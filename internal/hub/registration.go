package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

const (
	registerRateLimitMax = 5
	minPasswordBytes     = 10
	turnstileAction      = "register"
	turnstileVerifyURL   = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
)

var registrationUsernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,31}$`)

type turnstileVerifier interface {
	Verify(context.Context, turnstileVerifyRequest) error
}

type turnstileVerifyRequest struct {
	Secret           string
	Token            string
	RemoteIP         string
	ExpectedHostname string
	ExpectedAction   string
}

type cloudflareTurnstileVerifier struct {
	client *http.Client
	url    string
}

type turnstileSiteverifyResponse struct {
	Success    bool     `json:"success"`
	Hostname   string   `json:"hostname"`
	Action     string   `json:"action"`
	ErrorCodes []string `json:"error-codes"`
}

func (v cloudflareTurnstileVerifier) Verify(ctx context.Context, req turnstileVerifyRequest) error {
	form := url.Values{
		"secret":   {req.Secret},
		"response": {req.Token},
	}
	if req.RemoteIP != "" {
		form.Set("remoteip", req.RemoteIP)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, v.url, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := v.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("turnstile siteverify status %d", resp.StatusCode)
	}
	var result turnstileSiteverifyResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&result); err != nil {
		return err
	}
	if !result.Success {
		return errors.New("turnstile rejected token")
	}
	if req.ExpectedAction != "" && result.Action != req.ExpectedAction {
		return errors.New("turnstile action mismatch")
	}
	if req.ExpectedHostname != "" && !strings.EqualFold(result.Hostname, req.ExpectedHostname) {
		return errors.New("turnstile hostname mismatch")
	}
	return nil
}

func (s *Server) registrationReady() bool {
	cfg := s.cfg.Auth.Registration
	return cfg.Enabled && strings.TrimSpace(cfg.TurnstileSiteKey) != "" && strings.TrimSpace(cfg.TurnstileSecret) != ""
}

func (s *Server) handleAuthConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{
		"registrationEnabled": s.registrationReady(),
		"turnstileSiteKey":    publicTurnstileSiteKey(s),
	})
}

func publicTurnstileSiteKey(s *Server) string {
	if !s.registrationReady() {
		return ""
	}
	return strings.TrimSpace(s.cfg.Auth.Registration.TurnstileSiteKey)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.registrationReady() {
		serverutil.WriteError(w, http.StatusForbidden, "REGISTRATION_DISABLED", "registration is disabled")
		return
	}
	var req struct {
		Username       string `json:"username"`
		Password       string `json:"password"`
		TurnstileToken string `json:"turnstileToken"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid registration payload")
		return
	}
	username := normalizeUsername(req.Username)
	if !s.allowAuthAttempt(r, "register", username, registerRateLimitMax, authRateLimitWindow) {
		serverutil.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts, please try again later")
		return
	}
	if !registrationUsernamePattern.MatchString(username) {
		serverutil.WriteError(w, http.StatusBadRequest, "INVALID_USERNAME", "username must be 3-32 lowercase letters, numbers, underscores, or hyphens")
		return
	}
	if len(req.Password) < minPasswordBytes || len(req.Password) > maxPasswordBytes {
		serverutil.WriteError(w, http.StatusBadRequest, "INVALID_PASSWORD", "password must be 10-256 bytes")
		return
	}
	if strings.TrimSpace(req.TurnstileToken) == "" {
		serverutil.WriteError(w, http.StatusBadRequest, "TURNSTILE_REQUIRED", "security verification is required")
		return
	}
	verifyCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := s.turnstile.Verify(verifyCtx, turnstileVerifyRequest{
		Secret:           s.cfg.Auth.Registration.TurnstileSecret,
		Token:            req.TurnstileToken,
		RemoteIP:         authClientIP(r),
		ExpectedHostname: strings.TrimSpace(s.cfg.Auth.Registration.TurnstileHostname),
		ExpectedAction:   turnstileAction,
	}); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "TURNSTILE_FAILED", "security verification failed")
		return
	}
	user, err := s.store.CreateUser(r.Context(), username, req.Password)
	if errors.Is(err, store.ErrConflict) {
		serverutil.WriteError(w, http.StatusConflict, "USERNAME_TAKEN", "username is already registered")
		return
	}
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to create account")
		return
	}
	s.writeAuthSession(w, r, user, http.StatusCreated)
}
