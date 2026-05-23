package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
}

func NewServer(cfg *config.Config, st *store.Store, build BuildInfo) *Server {
	s := &Server{
		cfg:     cfg,
		store:   st,
		signer:  auth.NewSigner(cfg.Auth.JWTSecret, cfg.Auth.AccessTokenTTL.Duration),
		pool:    NewPool(),
		buffers: make(map[string]string),
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
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/me", s.withAuth(s.handleMe))
	mux.HandleFunc("GET /api/agents", s.withAuth(s.handleAgents))
	mux.HandleFunc("GET /api/sessions", s.withAuth(s.handleListSessions))
	mux.HandleFunc("POST /api/sessions", s.withAuth(s.handleCreateSession))
	mux.HandleFunc("PATCH /api/sessions/{sid}", s.withAuth(s.handleUpdateSession))
	mux.HandleFunc("DELETE /api/sessions/{sid}", s.withAuth(s.handleDeleteSession))
	mux.HandleFunc("GET /api/sessions/{sid}/messages", s.withAuth(s.handleMessages))
	mux.HandleFunc("GET /api/sessions/{sid}/runs", s.withAuth(s.handleRuns))
	mux.HandleFunc("GET /ws/chat", s.withAuth(s.handleBrowserWS))
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
		fileServer.ServeHTTP(w, r)
	})
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
	user, err := s.store.AuthenticateUser(r.Context(), req.Username, req.Password)
	if err != nil {
		serverutil.WriteError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid username or password")
		return
	}
	token, expires, err := s.signer.Sign(user.ID)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "TOKEN_ERROR", "failed to issue token")
		return
	}
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
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request, uid string) {
	agents, err := s.store.ListAgents(r.Context())
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to list agents")
		return
	}
	for i := range agents {
		agents[i].Online = s.pool.AgentOnline(agents[i].ID)
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"agents": agents})
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
		agents, err := s.store.ListAgents(r.Context())
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
	if _, err := s.store.AgentByID(r.Context(), req.AgentID); err != nil {
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
