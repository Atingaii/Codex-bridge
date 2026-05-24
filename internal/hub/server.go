package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tencent/codex-bridge/internal/auth"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
	"github.com/tencent/codex-bridge/internal/web"
)

const accessCookieName = "cb_access"

const (
	loginRateLimitMax   = 8
	authRateLimitWindow = 10 * time.Minute
	maxPasswordBytes    = 256
)

const (
	permissionProfileReviewRequired = "review-required"
	permissionProfileAutoExecute    = "auto-execute"
)

type BuildInfo struct {
	Version   string `json:"version"`
	BuildTime string `json:"buildTime"`
}

type Server struct {
	cfg     *config.Config
	store   *store.Store
	signer  *auth.Signer
	pool    *Pool
	httpSrv *http.Server

	buffersMu sync.Mutex
	buffers   map[string]string

	rateMu sync.Mutex
	rates  map[string]rateBucket
}

type rateBucket struct {
	count int
	reset time.Time
}

func NewServer(cfg *config.Config, st *store.Store, build BuildInfo) *Server {
	s := &Server{
		cfg:     cfg,
		store:   st,
		signer:  auth.NewSigner(cfg.Auth.JWTSecret, cfg.Auth.AccessTokenTTL.Duration),
		pool:    NewPool(),
		buffers: make(map[string]string),
		rates:   make(map[string]rateBucket),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		serverutil.WriteJSON(w, http.StatusOK, map[string]any{
			"status":    "ok",
			"service":   cfg.App.Name,
			"version":   build.Version,
			"buildTime": build.BuildTime,
			"env":       cfg.App.Env,
		})
	})
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/register", s.handleRegister)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/me", s.withAuth(s.handleMe))
	mux.HandleFunc("GET /api/agents", s.withAuth(s.handleAgents))
	mux.HandleFunc("DELETE /api/agents/{agentID}", s.withAuth(s.handleDeleteAgent))
	mux.HandleFunc("POST /api/bridge-tokens", s.withAuth(s.handleCreateBridgeToken))
	mux.HandleFunc("GET /install.sh", s.handleInstallScript)
	mux.HandleFunc("GET /downloads/codex-bridge-linux-amd64", s.handleBridgeBinaryDownload)
	mux.HandleFunc("GET /api/sessions", s.withAdmin(s.handleListSessions))
	mux.HandleFunc("POST /api/sessions", s.withAdmin(s.handleCreateSession))
	mux.HandleFunc("PATCH /api/sessions/{sid}", s.withAdmin(s.handleUpdateSession))
	mux.HandleFunc("DELETE /api/sessions/{sid}", s.withAdmin(s.handleDeleteSession))
	mux.HandleFunc("GET /api/sessions/{sid}/messages", s.withAdmin(s.handleMessages))
	mux.HandleFunc("GET /api/sessions/{sid}/runs", s.withAdmin(s.handleRuns))
	mux.HandleFunc("GET /api/orchestrations", s.withAuth(s.handleListOrchestrations))
	mux.HandleFunc("POST /api/orchestrations", s.withAuth(s.handleCreateOrchestration))
	mux.HandleFunc("GET /api/orchestrations/{runID}", s.withAuth(s.handleGetOrchestration))
	mux.HandleFunc("GET /api/orchestrations/{runID}/events", s.withAuth(s.handleOrchestrationEvents))
	mux.HandleFunc("POST /api/orchestrations/{runID}/prompts", s.withAuth(s.handleContinueOrchestration))
	mux.HandleFunc("POST /api/orchestrations/{runID}/cancel", s.withAuth(s.handleCancelOrchestration))
	mux.HandleFunc("GET /ws/orchestrations", s.withAuth(s.handleOrchestrationWS))
	mux.HandleFunc("GET /ws/chat", s.withAdmin(s.handleBrowserWS))
	mux.HandleFunc("GET /api/agents/connect", s.handleBridgeWS)
	mux.Handle("GET /", s.staticHandler())

	addr := fmt.Sprintf("%s:%d", cfg.Gateway.Host, cfg.Gateway.Port)
	s.httpSrv = &http.Server{
		Addr:         addr,
		Handler:      securityHeaders(requestLogger(mux)),
		ReadTimeout:  cfg.Gateway.ReadTimeout.Duration,
		WriteTimeout: cfg.Gateway.WriteTimeout.Duration,
	}
	return s
}

func (s *Server) Run(ctx context.Context) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	errc := make(chan error, 1)
	go func() {
		slog.Info("[hub] listening", "addr", s.httpSrv.Addr)
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()

	select {
	case <-ctx.Done():
	case sig := <-quit:
		slog.Info("[hub] shutdown requested", "signal", sig.String())
	case err := <-errc:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpSrv.Shutdown(shutdownCtx)
}

func (s *Server) staticHandler() http.Handler {
	sub, err := fs.Sub(web.StaticFS, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
			http.NotFound(w, r)
			return
		}
		switch {
		case r.URL.Path == "/sw.js":
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
		case strings.HasSuffix(r.URL.Path, ".webmanifest"):
			w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
		case strings.HasSuffix(r.URL.Path, ".js"), strings.HasSuffix(r.URL.Path, ".css"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.URL.Path != "/" && !strings.Contains(filepathBase(r.URL.Path), ".") {
			if _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

func filepathBase(path string) string {
	i := strings.LastIndex(path, "/")
	if i >= 0 {
		return path[i+1:]
	}
	return path
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid login payload")
		return
	}
	username := normalizeUsername(req.Username)
	if !s.allowAuthAttempt(r, "login", username, loginRateLimitMax, authRateLimitWindow) {
		serverutil.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts, please try again later")
		return
	}
	if username == "" || req.Password == "" || len(req.Password) > maxPasswordBytes {
		serverutil.WriteError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid username or password")
		return
	}
	user, err := s.store.AuthenticateUser(r.Context(), username, req.Password)
	if err != nil {
		serverutil.WriteError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid username or password")
		return
	}
	token, expires, err := s.signer.Sign(user.ID)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "TOKEN_ERROR", "failed to issue token")
		return
	}
	user.IsAdmin = s.isAdminUser(user)
	http.SetCookie(w, &http.Cookie{
		Name:     accessCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secureCookie(r),
	})
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"user": user, "expiresAt": expires.Unix()})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	serverutil.WriteError(w, http.StatusForbidden, "REGISTRATION_DISABLED", "registration is disabled")
}

func normalizeUsername(username string) string {
	return strings.TrimSpace(username)
}

func (s *Server) allowAuthAttempt(r *http.Request, scope, username string, maxAttempts int, window time.Duration) bool {
	now := time.Now()
	key := scope + "|" + authClientIP(r) + "|" + strings.ToLower(username)

	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	for k, bucket := range s.rates {
		if now.After(bucket.reset) {
			delete(s.rates, k)
		}
	}
	bucket := s.rates[key]
	if bucket.reset.IsZero() || now.After(bucket.reset) {
		bucket = rateBucket{reset: now.Add(window)}
	}
	bucket.count++
	s.rates[key] = bucket
	return bucket.count <= maxAttempts
}

func authClientIP(r *http.Request) string {
	for _, header := range []string{"CF-Connecting-IP", "X-Real-IP"} {
		value := strings.TrimSpace(r.Header.Get(header))
		if value != "" {
			return value
		}
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if i := strings.Index(forwarded, ","); i >= 0 {
			forwarded = forwarded[:i]
		}
		if forwarded = strings.TrimSpace(forwarded); forwarded != "" {
			return forwarded
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     accessCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.secureCookie(r),
	})
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) secureCookie(r *http.Request) bool {
	if s.cfg.Hub.CookieSecure {
		return true
	}
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, uid string) {
	user, err := s.store.UserByID(r.Context(), uid)
	if err != nil {
		serverutil.WriteError(w, http.StatusUnauthorized, "INVALID_TOKEN", "invalid token")
		return
	}
	user.IsAdmin = s.isAdminUser(user)
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request, uid string) {
	agents, err := s.visibleAgents(r.Context(), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusUnauthorized, "INVALID_TOKEN", "invalid token")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to list agents")
		return
	}
	out := make([]agentResponse, 0, len(agents))
	for i := range agents {
		agents[i].Online = s.pool.AgentOnline(agents[i].ID)
		item := agentResponse{Agent: agents[i]}
		if caps, ok := s.pool.AgentCapabilities(agents[i].ID); ok {
			item.Capabilities = caps
		}
		out = append(out, item)
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"agents": out})
}

type agentResponse struct {
	store.Agent
	Capabilities *protocol.BridgeCapabilities `json:"capabilities,omitempty"`
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request, uid string) {
	agentID := r.PathValue("agentID")
	agent, err := s.visibleAgentByID(r.Context(), uid, agentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "agent not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load agent")
		return
	}
	if err := s.store.DeleteAgent(r.Context(), agent.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "agent not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to delete agent")
		return
	}
	if err := s.store.RevokeEnrollTokensForMachine(r.Context(), agent.MachineID); err != nil {
		slog.Warn("[hub] revoke agent enroll token failed", "agent_id", agent.ID, "error", err)
	}
	s.pool.DisconnectAgent(agent.ID)
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true, "agentId": agent.ID})
}

func (s *Server) handleCreateBridgeToken(w http.ResponseWriter, r *http.Request, uid string) {
	var req struct {
		Label             string `json:"label"`
		TTL               string `json:"ttl"`
		PermissionProfile string `json:"permissionProfile"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid bridge token payload")
		return
	}
	ttl := 24 * time.Hour
	if strings.TrimSpace(req.TTL) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(req.TTL))
		if err != nil || parsed <= 0 || parsed > 7*24*time.Hour {
			serverutil.WriteError(w, http.StatusBadRequest, "BAD_TTL", "ttl must be between 1s and 168h")
			return
		}
		ttl = parsed
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "CLI endpoint"
	}
	if runes := []rune(label); len(runes) > 80 {
		label = string(runes[:80])
	}
	value := store.NewToken("enr")
	expiresAt := time.Now().Add(ttl)
	if err := s.store.CreateEnrollTokenForUser(r.Context(), value, uid, label, &expiresAt); err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to create bridge token")
		return
	}
	hubURL := s.publicBaseURL(r)
	profile := normalizePermissionProfile(req.PermissionProfile)
	if profile == "" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_PERMISSION_PROFILE", "permissionProfile must be review-required or auto-execute")
		return
	}
	installCommand := s.bridgeInstallCommand(hubURL)
	connectCommand := s.bridgeConnectCommand(hubURL, value, profile)
	setupCommand := bridgeSetupCommand(installCommand, connectCommand)
	profiles := s.bridgePermissionProfiles(hubURL, value, installCommand)
	serverutil.WriteJSON(w, http.StatusCreated, map[string]any{
		"token":              value,
		"expiresAt":          expiresAt.Unix(),
		"label":              label,
		"hubUrl":             hubURL,
		"downloadUrl":        strings.TrimSpace(s.cfg.Hub.BridgeDownloadURL),
		"permissionProfile":  profile,
		"permissionProfiles": profiles,
		"setupCommand":       setupCommand,
		"installCommand":     installCommand,
		"connectCommand":     connectCommand,
		"commands":           []string{setupCommand},
	})
}

func normalizePermissionProfile(profile string) string {
	switch strings.TrimSpace(strings.ToLower(profile)) {
	case "", permissionProfileReviewRequired:
		return permissionProfileReviewRequired
	case permissionProfileAutoExecute:
		return permissionProfileAutoExecute
	default:
		return ""
	}
}

func (s *Server) bridgePermissionProfiles(hubURL, token, installCommand string) []map[string]string {
	return []map[string]string{
		s.bridgePermissionProfile(hubURL, token, installCommand, permissionProfileReviewRequired),
		s.bridgePermissionProfile(hubURL, token, installCommand, permissionProfileAutoExecute),
	}
}

func (s *Server) bridgePermissionProfile(hubURL, token, installCommand, profile string) map[string]string {
	connectCommand := s.bridgeConnectCommand(hubURL, token, profile)
	return map[string]string{
		"id":             profile,
		"setupCommand":   bridgeSetupCommand(installCommand, connectCommand),
		"connectCommand": connectCommand,
	}
}

func (s *Server) bridgeInstallCommand(hubURL string) string {
	return fmt.Sprintf("curl -fsSL %s | sh", shellQuote(strings.TrimRight(hubURL, "/")+"/install.sh"))
}

func bridgeSetupCommand(installCommand, connectCommand string) string {
	return installCommand + " && { " + connectCommand + "; }"
}

func (s *Server) bridgeConnectCommand(hubURL, token, permissionProfile string) string {
	hubArg := ""
	if hubURL != strings.TrimRight(s.cfg.Bridge.HubURL, "/") {
		hubArg = " --hub " + shellQuote(hubURL)
	}
	permissionArg := bridgePermissionArgs(permissionProfile)
	startScript := []string{
		shellQuote("#!/bin/sh"),
		shellQuote("set -eu"),
		shellExpandWord(`CB_HASH="$CB_HASH"`),
		shellExpandWord(`CB_HOME="$CB_HOME"`),
		shellQuote(`CB_PROXY_ENV=$(cat "$CB_HOME/services/${CB_HASH}.env" 2>/dev/null || true)`),
		shellQuote(`if [ -n "$CB_PROXY_ENV" ]; then set -a; . "$CB_HOME/services/${CB_HASH}.env"; set +a; fi`),
		shellQuote(`CB_CWD=$(cat "$CB_HOME/services/${CB_HASH}.cwd")`),
		shellQuote(`CB_NAME=$(cat "$CB_HOME/services/${CB_HASH}.name")`),
		shellQuote(fmt.Sprintf(`exec "$HOME/.local/bin/codex-bridge" connect%s%s --cwd "$CB_CWD" --name "$CB_NAME" --machine-id-file "$CB_HOME/machines/${CB_HASH}" %s`, hubArg, permissionArg, shellQuote(token))),
	}
	serviceFile := []string{
		shellQuote("[Unit]"),
		shellExpandWord("Description=Codex Bridge endpoint for $CB_CWD"),
		shellQuote("After=network-online.target"),
		shellQuote("Wants=network-online.target"),
		shellQuote(""),
		shellQuote("[Service]"),
		shellQuote("Type=simple"),
		shellExpandWord("ExecStart=%h/.codex-bridge/services/${CB_HASH}.sh"),
		shellQuote("Restart=always"),
		shellQuote("RestartSec=5"),
		shellExpandWord("StandardOutput=append:%h/.codex-bridge/logs/${CB_HASH}.log"),
		shellExpandWord("StandardError=append:%h/.codex-bridge/logs/${CB_HASH}.log"),
		shellQuote(""),
		shellQuote("[Install]"),
		shellQuote("WantedBy=default.target"),
	}
	return shellJoin(
		`CB_CWD="${PWD:-.}"`,
		`CB_DIR="$(basename "$CB_CWD")"`,
		`CB_HASH="$(printf '%s' "$CB_CWD" | cksum | awk '{print $1}')"`,
		`CB_NAME="${HOSTNAME:-cli}-${CB_DIR}-${CB_HASH}"`,
		`CB_HOME="$HOME/.codex-bridge"`,
		`CB_LOG_DIR="$CB_HOME/logs"`,
		`CB_LOG="$CB_LOG_DIR/${CB_HASH}.log"`,
		`CB_START="$CB_HOME/services/${CB_HASH}.sh"`,
		`CB_SERVICE_DIR="$HOME/.config/systemd/user"`,
		`CB_SERVICE_NAME="codex-bridge-${CB_HASH}.service"`,
		`mkdir -p "$CB_HOME/machines" "$CB_LOG_DIR" "$CB_HOME/services" "$CB_SERVICE_DIR"`,
		`printf '%s\n' "$CB_CWD" > "$CB_HOME/services/${CB_HASH}.cwd"`,
		`printf '%s\n' "$CB_NAME" > "$CB_HOME/services/${CB_HASH}.name"`,
		`: > "$CB_HOME/services/${CB_HASH}.env"`,
		`for CB_ENV_NAME in HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY http_proxy https_proxy all_proxy no_proxy; do eval "CB_ENV_VALUE=\${$CB_ENV_NAME:-}"; if [ -n "$CB_ENV_VALUE" ]; then printf '%s=%s\n' "$CB_ENV_NAME" "$(printf '%s' "$CB_ENV_VALUE" | sed "s/'/'\\\\''/g; 1s/^/'/; \$s/\$/'/")" >> "$CB_HOME/services/${CB_HASH}.env"; fi; done`,
		fmt.Sprintf(`printf '%%s\n' %s > "$CB_START"`, strings.Join(startScript, " ")),
		`chmod 700 "$CB_START"`,
		`: > "$CB_LOG"`,
		`CB_STARTED=0`,
		fmt.Sprintf(`if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then printf '%%s\n' %s > "$CB_SERVICE_DIR/$CB_SERVICE_NAME"; if systemctl --user daemon-reload && systemctl --user enable "$CB_SERVICE_NAME" && systemctl --user restart "$CB_SERVICE_NAME"; then sleep 2; if systemctl --user is-active --quiet "$CB_SERVICE_NAME"; then loginctl enable-linger "$(id -un)" >/dev/null 2>&1 || true; CB_WAIT=0; while [ "$CB_WAIT" -lt 10 ] && ! grep -q '\[bridge\] connected' "$CB_LOG" 2>/dev/null; do sleep 1; CB_WAIT=$((CB_WAIT + 1)); done; if grep -q '\[bridge\] connected' "$CB_LOG" 2>/dev/null; then echo "codex-bridge connected: $CB_SERVICE_NAME log=$CB_LOG"; else echo "codex-bridge service started but Hub connection is not confirmed. log=$CB_LOG" >&2; tail -n 40 "$CB_LOG" >&2 2>/dev/null || true; fi; CB_STARTED=1; else echo "codex-bridge user service did not stay active; falling back to nohup" >&2; systemctl --user status "$CB_SERVICE_NAME" --no-pager >&2 || true; if command -v journalctl >/dev/null 2>&1; then journalctl --user -u "$CB_SERVICE_NAME" -n 30 --no-pager >&2 || true; fi; systemctl --user stop "$CB_SERVICE_NAME" >/dev/null 2>&1 || true; fi; fi; fi`, strings.Join(serviceFile, " ")),
		`if [ "$CB_STARTED" != "1" ]; then nohup "$CB_START" > "$CB_LOG" 2>&1 & CB_PID=$!; CB_WAIT=0; while [ "$CB_WAIT" -lt 10 ] && ! grep -q '\[bridge\] connected' "$CB_LOG" 2>/dev/null; do sleep 1; CB_WAIT=$((CB_WAIT + 1)); done; if grep -q '\[bridge\] connected' "$CB_LOG" 2>/dev/null; then echo "codex-bridge connected in background: pid=$CB_PID log=$CB_LOG"; else echo "codex-bridge started in background but Hub connection is not confirmed: pid=$CB_PID log=$CB_LOG" >&2; tail -n 40 "$CB_LOG" >&2 2>/dev/null || true; fi; fi`,
	)
}

func bridgePermissionArgs(profile string) string {
	switch profile {
	case permissionProfileAutoExecute:
		return " --runner codex --sandbox danger-full-access --approval-policy never"
	default:
		return " --runner codex-app-server --sandbox workspace-write --approval-policy untrusted"
	}
}

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	downloadURL := s.bridgeDownloadURL(r)
	if downloadURL == "" {
		serverutil.WriteError(w, http.StatusInternalServerError, "DOWNLOAD_NOT_CONFIGURED", "bridge download url is not configured")
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, `#!/bin/sh
set -eu

BIN_DIR="${HOME}/.local/bin"
BIN="${BIN_DIR}/codex-bridge"
TMP="${BIN}.tmp.$$"
DOWNLOAD_URL=%s

mkdir -p "$BIN_DIR"
cleanup() {
  rm -f "$TMP"
}
trap cleanup EXIT HUP INT TERM
if command -v curl >/dev/null 2>&1; then
  curl -fL --retry 3 -o "$TMP" "$DOWNLOAD_URL"
elif command -v wget >/dev/null 2>&1; then
  wget -O "$TMP" "$DOWNLOAD_URL"
else
  echo "curl or wget is required" >&2
  exit 1
fi
chmod +x "$TMP"
mv -f "$TMP" "$BIN"
trap - EXIT HUP INT TERM
echo "installed $BIN"
`, shellQuote(downloadURL))
}

func (s *Server) handleBridgeBinaryDownload(w http.ResponseWriter, r *http.Request) {
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		serverutil.WriteError(w, http.StatusInternalServerError, "BINARY_NOT_FOUND", "failed to locate bridge binary")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="codex-bridge-linux-amd64"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, exe)
}

func (s *Server) bridgeDownloadURL(r *http.Request) string {
	if downloadURL := strings.TrimSpace(s.cfg.Hub.BridgeDownloadURL); downloadURL != "" {
		return downloadURL
	}
	return s.publicBaseURL(r) + "/downloads/codex-bridge-linux-amd64"
}

func (s *Server) publicBaseURL(r *http.Request) string {
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		proto = strings.TrimSpace(r.URL.Scheme)
	}
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	if i := strings.Index(proto, ","); i >= 0 {
		proto = strings.TrimSpace(proto[:i])
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if i := strings.Index(host, ","); i >= 0 {
		host = strings.TrimSpace(host[:i])
	}
	return strings.TrimRight(proto+"://"+host, "/")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func shellExpandWord(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func shellJoin(commands ...string) string {
	return strings.Join(commands, "; ")
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request, uid string) {
	sessions, err := s.store.ListSessions(r.Context(), uid, 100)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to list sessions")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request, uid string) {
	var req struct {
		AgentID string `json:"agentId"`
		Title   string `json:"title"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid session payload")
		return
	}
	if req.AgentID == "" {
		agents, err := s.visibleAgents(r.Context(), uid)
		if err != nil || len(agents) == 0 {
			serverutil.WriteError(w, http.StatusConflict, "NO_AGENT", "no bridge agent has enrolled yet")
			return
		}
		for _, agent := range agents {
			if s.pool.AgentOnline(agent.ID) {
				req.AgentID = agent.ID
				break
			}
		}
		if req.AgentID == "" {
			req.AgentID = agents[0].ID
		}
	}
	if _, err := s.visibleAgentByID(r.Context(), uid, req.AgentID); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_AGENT", "agent not found")
		return
	}
	sess, err := s.store.CreateSession(r.Context(), uid, req.AgentID, req.Title)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to create session")
		return
	}
	serverutil.WriteJSON(w, http.StatusCreated, map[string]any{"session": sess})
}

func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request, uid string) {
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid session payload")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "session title is required")
		return
	}
	sess, err := s.store.UpdateSessionTitle(r.Context(), r.PathValue("sid"), uid, req.Title)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "session not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to update session")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"session": sess})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request, uid string) {
	sid := r.PathValue("sid")
	session, err := s.store.SessionByID(r.Context(), sid, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "session not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load session")
		return
	}
	if err := s.store.DeleteSession(r.Context(), sid, uid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "session not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to delete session")
		return
	}
	s.clearAssistantBuffer(sid)
	_ = s.pool.SendToAgent(session.AgentID, protocol.MustEnvelope(protocol.TypeCloseSession, sid, nil))
	s.pool.BroadcastToBrowsers(sid, protocol.MustEnvelope(protocol.TypeError, sid, protocol.ErrorPayload{
		Code:    "SESSION_DELETED",
		Message: "session deleted",
	}))
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request, uid string) {
	sid := r.PathValue("sid")
	if _, err := s.store.SessionByID(r.Context(), sid, uid); err != nil {
		serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "session not found")
		return
	}
	messages, err := s.store.ListMessages(r.Context(), sid, 500)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to list messages")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"messages": messages})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request, uid string) {
	sid := r.PathValue("sid")
	if _, err := s.store.SessionByID(r.Context(), sid, uid); err != nil {
		serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "session not found")
		return
	}
	runs, err := s.store.ListRuns(r.Context(), sid, 200)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to list runs")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) withAuth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := s.userIDFromRequest(r)
		if err != nil {
			serverutil.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required")
			return
		}
		next(w, r, uid)
	}
}

func (s *Server) withAdmin(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return s.withAuth(func(w http.ResponseWriter, r *http.Request, uid string) {
		user, err := s.store.UserByID(r.Context(), uid)
		if err != nil {
			serverutil.WriteError(w, http.StatusUnauthorized, "INVALID_TOKEN", "invalid token")
			return
		}
		if !s.isAdminUser(user) {
			serverutil.WriteError(w, http.StatusForbidden, "ADMIN_ONLY", "admin account required")
			return
		}
		next(w, r, uid)
	})
}

func (s *Server) isAdminUser(user store.User) bool {
	admin := strings.TrimSpace(s.cfg.Auth.BootstrapUsername)
	if admin == "" {
		admin = "admin"
	}
	return strings.EqualFold(user.Username, admin)
}

func (s *Server) visibleAgents(ctx context.Context, uid string) ([]store.Agent, error) {
	user, err := s.store.UserByID(ctx, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	if s.isAdminUser(user) {
		return s.store.ListAgents(ctx)
	}
	return s.store.ListAgentsForUser(ctx, uid, false)
}

func (s *Server) visibleAgentByID(ctx context.Context, uid, agentID string) (store.Agent, error) {
	user, err := s.store.UserByID(ctx, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Agent{}, store.ErrNotFound
		}
		return store.Agent{}, err
	}
	if s.isAdminUser(user) {
		return s.store.AgentByID(ctx, agentID)
	}
	return s.store.AgentByIDForUser(ctx, agentID, uid, false)
}

func (s *Server) userIDFromRequest(r *http.Request) (string, error) {
	var token string
	if cookie, err := r.Cookie(accessCookieName); err == nil {
		token = cookie.Value
	}
	if token == "" {
		raw := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(raw), "bearer ") {
			token = strings.TrimSpace(raw[7:])
		}
	}
	if token == "" {
		return "", errors.New("missing token")
	}
	return s.signer.Parse(token)
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(lw, r)
		query := r.URL.RawQuery
		if query != "" && strings.Contains(strings.ToLower(query), "token") {
			query = "[REDACTED]"
		}
		slog.Info("[http] request",
			"method", r.Method,
			"path", r.URL.Path,
			"query", query,
			"status", lw.status,
			"latency", time.Since(start).String(),
			"remote", r.RemoteAddr,
		)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com; connect-src 'self' ws: wss:; img-src 'self' data: blob:; base-uri 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

func (w *loggingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
