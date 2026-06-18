package hub

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

type shareResponse struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title,omitempty"`
	URL       string `json:"url,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type publicSessionResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type publicMessageResponse struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"createdAt"`
}

type publicOrchestrationRunResponse struct {
	ID                      string                    `json:"id"`
	Title                   string                    `json:"title"`
	Mode                    string                    `json:"mode"`
	WorkerPair              string                    `json:"workerPair,omitempty"`
	FirstCLI                string                    `json:"firstCli,omitempty"`
	Profile                 string                    `json:"profile,omitempty"`
	NativeContextCompaction string                    `json:"nativeContextCompaction,omitempty"`
	Prompt                  string                    `json:"prompt"`
	CWD                     string                    `json:"cwd,omitempty"`
	MaxTurns                int                       `json:"maxTurns"`
	Status                  string                    `json:"status"`
	Error                   string                    `json:"error,omitempty"`
	Files                   []store.OrchestrationFile `json:"files,omitempty"`
	CreatedAt               int64                     `json:"createdAt"`
	UpdatedAt               int64                     `json:"updatedAt"`
	FinishedAt              int64                     `json:"finishedAt,omitempty"`
}

type publicOrchestrationEventResponse struct {
	ID            string                  `json:"id"`
	RunID         string                  `json:"runId"`
	Seq           int64                   `json:"seq"`
	Kind          string                  `json:"kind"`
	Source        string                  `json:"source,omitempty"`
	Role          string                  `json:"role,omitempty"`
	CLI           string                  `json:"cli,omitempty"`
	TurnID        string                  `json:"turnId,omitempty"`
	Content       string                  `json:"content,omitempty"`
	Status        string                  `json:"status,omitempty"`
	Error         string                  `json:"error,omitempty"`
	CommandData   *protocol.CommandData   `json:"commandData,omitempty"`
	RunStartData  *protocol.RunStartData  `json:"runStartData,omitempty"`
	TurnStartData *protocol.TurnStartData `json:"turnStartData,omitempty"`
	RunEndData    *protocol.RunEndData    `json:"runEndData,omitempty"`
	RunConclusion *protocol.RunConclusion `json:"runConclusion,omitempty"`
	Data          map[string]any          `json:"data,omitempty"`
	CreatedAt     int64                   `json:"createdAt"`
}

func (s *Server) handleShareSession(w http.ResponseWriter, r *http.Request, uid string) {
	session, err := s.store.SessionByID(r.Context(), r.PathValue("sid"), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "session not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load session")
		return
	}
	share, err := s.store.CreateOrUpdateConversationShare(r.Context(), uid, store.ShareKindChat, session.ID, session.Title)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to create share")
		return
	}
	serverutil.WriteJSON(w, http.StatusCreated, map[string]any{"share": s.shareResponse(r, share)})
}

func (s *Server) handleShareOrchestration(w http.ResponseWriter, r *http.Request, uid string) {
	run, err := s.store.OrchestrationRunByID(r.Context(), r.PathValue("runID"), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration run")
		return
	}
	share, err := s.store.CreateOrUpdateConversationShare(r.Context(), uid, store.ShareKindOrchestration, run.ID, run.Title)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to create share")
		return
	}
	serverutil.WriteJSON(w, http.StatusCreated, map[string]any{"share": s.shareResponse(r, share)})
}

func (s *Server) handleRevokeShare(w http.ResponseWriter, r *http.Request, uid string) {
	if err := s.store.RevokeConversationShare(r.Context(), r.PathValue("shareID"), uid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "share not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to revoke share")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePublicShare(w http.ResponseWriter, r *http.Request) {
	share, err := s.store.ActiveConversationShareByID(r.Context(), r.PathValue("shareID"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "share not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load share")
		return
	}
	switch share.Kind {
	case store.ShareKindChat:
		s.handlePublicChatShare(w, r, share)
	case store.ShareKindOrchestration:
		s.handlePublicOrchestrationShare(w, r, share)
	default:
		serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "share not found")
	}
}

func (s *Server) handlePublicChatShare(w http.ResponseWriter, r *http.Request, share store.ConversationShare) {
	session, err := s.store.SessionByID(r.Context(), share.TargetID, share.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "share not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load shared session")
		return
	}
	messages, err := s.store.ListMessages(r.Context(), session.ID, 1000)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load shared messages")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{
		"share":    publicShareResponse(share),
		"session":  publicSession(session),
		"messages": publicMessages(messages),
	})
}

func (s *Server) handlePublicOrchestrationShare(w http.ResponseWriter, r *http.Request, share store.ConversationShare) {
	run, err := s.store.OrchestrationRunByID(r.Context(), share.TargetID, share.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "share not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load shared orchestration run")
		return
	}
	events, err := s.store.ListOrchestrationEvents(r.Context(), run.ID, 10000)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load shared orchestration events")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{
		"share":  publicShareResponse(share),
		"run":    publicOrchestrationRun(run),
		"events": publicOrchestrationEvents(events),
	})
}

func (s *Server) shareResponse(r *http.Request, share store.ConversationShare) shareResponse {
	resp := publicShareResponse(share)
	resp.URL = strings.TrimRight(s.publicBaseURL(r), "/") + "/share/" + share.ID
	return resp
}

func publicShareResponse(share store.ConversationShare) shareResponse {
	return shareResponse{
		ID:        share.ID,
		Kind:      share.Kind,
		Title:     share.Title,
		CreatedAt: share.CreatedAt,
		UpdatedAt: share.UpdatedAt,
	}
}

func publicSession(session store.Session) publicSessionResponse {
	return publicSessionResponse{
		ID:        session.ID,
		Title:     session.Title,
		CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt,
	}
}

func publicOrchestrationRun(run store.OrchestrationRun) publicOrchestrationRunResponse {
	return publicOrchestrationRunResponse{
		ID:                      run.ID,
		Title:                   run.Title,
		Mode:                    run.Mode,
		WorkerPair:              run.WorkerPair,
		FirstCLI:                run.FirstCLI,
		Profile:                 run.Profile,
		NativeContextCompaction: run.NativeContextCompaction,
		Prompt:                  run.Prompt,
		CWD:                     run.CWD,
		MaxTurns:                run.MaxTurns,
		Status:                  run.Status,
		Error:                   run.Error,
		Files:                   run.Files,
		CreatedAt:               run.CreatedAt,
		UpdatedAt:               run.UpdatedAt,
		FinishedAt:              run.FinishedAt,
	}
}

func publicMessages(messages []store.Message) []publicMessageResponse {
	out := make([]publicMessageResponse, 0, len(messages))
	for _, msg := range messages {
		out = append(out, publicMessageResponse{
			ID:        msg.ID,
			Role:      msg.Role,
			Content:   msg.Content,
			CreatedAt: msg.CreatedAt,
		})
	}
	return out
}

func publicOrchestrationEvents(events []store.OrchestrationEvent) []publicOrchestrationEventResponse {
	out := make([]publicOrchestrationEventResponse, 0, len(events))
	for _, event := range events {
		if publicOrchestrationEventHidden(event) {
			continue
		}
		out = append(out, publicOrchestrationEventResponse{
			ID:            event.ID,
			RunID:         event.RunID,
			Seq:           event.Seq,
			Kind:          event.Kind,
			Source:        publicOrchestrationEventSource(event),
			Role:          event.Role,
			CLI:           event.CLI,
			TurnID:        event.TurnID,
			Content:       event.Content,
			Status:        event.Status,
			Error:         event.Error,
			CommandData:   publicCommandData(event.CommandData),
			RunStartData:  publicRunStartData(event.RunStartData),
			TurnStartData: publicTurnStartData(event.TurnStartData),
			RunEndData:    publicRunEndData(event.RunEndData),
			RunConclusion: event.RunConclusion,
			Data:          publicOrchestrationEventData(event.Data),
			CreatedAt:     event.CreatedAt,
		})
	}
	return out
}

func publicOrchestrationEventHidden(event store.OrchestrationEvent) bool {
	// turn.end is lifecycle, not an internal log row: failed turns carry
	// severity "error" but the public timeline still needs the turn closed.
	if event.Kind == "turn.end" {
		return false
	}
	if event.Severity != "" {
		return true
	}
	if event.Source == "bridge" && event.Kind != "run.start" && event.Kind != "turn.start" && event.Kind != "run.end" && event.Kind != "run.error" && event.Kind != "run.cancelled" && event.Kind != "run.conclusion" {
		return true
	}
	return false
}

func publicOrchestrationEventSource(event store.OrchestrationEvent) string {
	if event.Source == "cli" || event.Source == "bridge" || event.Source == "user" {
		return event.Source
	}
	switch event.Kind {
	case "user.message":
		return "user"
	case "run.start", "turn.start", "run.end", "run.error", "run.cancelled", "run.conclusion":
		return "bridge"
	default:
		return "cli"
	}
}

func publicCommandData(data *protocol.CommandData) *protocol.CommandData {
	if data == nil {
		return nil
	}
	copy := *data
	return &copy
}

func publicRunStartData(data *protocol.RunStartData) *protocol.RunStartData {
	if data == nil {
		return nil
	}
	copy := *data
	copy.CWD = ""
	return &copy
}

// publicRunEndData keeps only the worker pair. Native resume commands, thread
// and session ids, transcript paths, and the run cwd describe the Bridge
// host's filesystem and live CLI state — none of that belongs in an anonymous
// share.
func publicRunEndData(data *protocol.RunEndData) *protocol.RunEndData {
	if data == nil {
		return nil
	}
	return &protocol.RunEndData{WorkerPair: data.WorkerPair}
}

func publicTurnStartData(data *protocol.TurnStartData) *protocol.TurnStartData {
	if data == nil {
		return nil
	}
	copy := *data
	copy.PromptText = ""
	return &copy
}

func publicOrchestrationEventData(data map[string]any) map[string]any {
	if len(data) == 0 {
		return nil
	}
	allowed := map[string]struct{}{
		"command":       {},
		"output":        {},
		"status":        {},
		"exitCode":      {},
		"id":            {},
		"name":          {},
		"input":         {},
		"startedAt":     {},
		"completedAt":   {},
		"durationMs":    {},
		"files":         {},
		"source":        {},
		"contentKind":   {},
		"eventType":     {},
		"agent":         {},
		"target":        {},
		"jobId":         {},
		"paneId":        {},
		"state":         {},
		"health":        {},
		"tail":          {},
		"lastUpdatedAt": {},
	}
	out := make(map[string]any)
	for key, value := range data {
		if _, ok := allowed[key]; !ok {
			continue
		}
		if key == "files" {
			out[key] = publicEventFiles(value)
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func publicEventFiles(value any) []store.OrchestrationFile {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]store.OrchestrationFile, 0, len(raw))
	for _, item := range raw {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := record["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		mimeType, _ := record["mimeType"].(string)
		var size int64
		switch v := record["size"].(type) {
		case int64:
			size = v
		case int:
			size = int64(v)
		case float64:
			size = int64(v)
		}
		out = append(out, store.OrchestrationFile{Name: name, MimeType: strings.TrimSpace(mimeType), Size: size})
	}
	return out
}
