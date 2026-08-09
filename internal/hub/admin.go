package hub

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
	"github.com/tencent/codex-bridge/internal/usagepricing"
)

type adminUsageTotals struct {
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheTokens      int64   `json:"cacheTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	EstimatedCostUSD float64 `json:"estimatedCostUsd"`
	CostKnown        bool    `json:"costKnown"`
	CallCount        int     `json:"callCount"`
}

type adminUserUsage struct {
	UserID            string `json:"userId"`
	Username          string `json:"username"`
	CreatedAt         int64  `json:"createdAt"`
	LastActiveAt      int64  `json:"lastActiveAt"`
	ActivityStatus    string `json:"activityStatus"`
	OnlineAgents      int    `json:"onlineAgents"`
	TotalAgents       int    `json:"totalAgents"`
	ChatSessions      int    `json:"chatSessions"`
	OrchestrationRuns int    `json:"orchestrationRuns"`
	RunningRuns       int    `json:"runningRuns"`
	adminUsageTotals
}

type adminUsageTrendPoint struct {
	Date              string `json:"date"`
	ActiveUsers       int    `json:"activeUsers"`
	ChatMessages      int    `json:"chatMessages"`
	OrchestrationRuns int    `json:"orchestrationRuns"`
	adminUsageTotals
	activeUserIDs map[string]struct{}
}

type adminUsageOverview struct {
	Days              int `json:"days"`
	TimezoneOffset    int `json:"timezoneOffset"`
	Users             int `json:"users"`
	ActiveUsers       int `json:"activeUsers"`
	OnlineUsers       int `json:"onlineUsers"`
	OnlineAgents      int `json:"onlineAgents"`
	TotalAgents       int `json:"totalAgents"`
	ChatSessions      int `json:"chatSessions"`
	OrchestrationRuns int `json:"orchestrationRuns"`
	RunningRuns       int `json:"runningRuns"`
	adminUsageTotals
	Trend []adminUsageTrendPoint `json:"trend"`
	Items []adminUserUsage       `json:"items"`
}

type adminConversationUsage struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	AgentName     string `json:"agentName"`
	Status        string `json:"status"`
	Mode          string `json:"mode,omitempty"`
	MaxTurns      int    `json:"maxTurns,omitempty"`
	ActivityCount int    `json:"activityCount"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
	adminUsageTotals
}

type adminUserUsageDetail struct {
	UserID         string `json:"userId"`
	Username       string `json:"username"`
	CreatedAt      int64  `json:"createdAt"`
	Days           int    `json:"days"`
	TimezoneOffset int    `json:"timezoneOffset"`
	adminUsageTotals
	Conversations []adminConversationUsage `json:"conversations"`
}

type adminConversationContent struct {
	ID     string                         `json:"id"`
	Kind   string                         `json:"kind"`
	Title  string                         `json:"title"`
	Prompt string                         `json:"prompt,omitempty"`
	Items  []adminConversationContentItem `json:"items"`
}

type adminConversationContentItem struct {
	Role      string `json:"role,omitempty"`
	Source    string `json:"source,omitempty"`
	Kind      string `json:"kind"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"createdAt"`
}

func (s *Server) handleAdminUsage(w http.ResponseWriter, r *http.Request, _ string) {
	days, timezoneOffset, cutoff, err := usageOverviewRange(r)
	if err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_RANGE", err.Error())
		return
	}
	snapshot, err := s.store.AdminUsageSnapshot(r.Context(), cutoff, timezoneOffset)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load administrator usage")
		return
	}
	overview := buildAdminUsageOverview(snapshot, days, timezoneOffset, time.Now().Unix(), s.pool.AgentOnline)
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"overview": overview})
}

func (s *Server) handleAdminUserUsage(w http.ResponseWriter, r *http.Request, _ string) {
	days, timezoneOffset, cutoff, err := usageOverviewRange(r)
	if err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_RANGE", err.Error())
		return
	}
	snapshot, err := s.store.AdminUserDetailSnapshot(r.Context(), r.PathValue("userID"), cutoff)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "user not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load administrator user detail")
		return
	}
	detail := buildAdminUserUsageDetail(snapshot, days, timezoneOffset)
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"detail": detail})
}

func (s *Server) handleAdminConversationContent(w http.ResponseWriter, r *http.Request, _ string) {
	content, err := s.store.AdminConversationContent(
		r.Context(), r.PathValue("userID"), r.PathValue("kind"), r.PathValue("conversationID"),
	)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "conversation not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load administrator conversation content")
		return
	}
	response := adminConversationContent{
		ID: content.ID, Kind: content.Kind, Title: content.Title, Prompt: content.Prompt,
		Items: make([]adminConversationContentItem, 0, len(content.Items)),
	}
	for _, item := range content.Items {
		response.Items = append(response.Items, adminConversationContentItem{
			Role: item.Role, Source: item.Source, Kind: item.Kind, Content: item.Content, CreatedAt: item.CreatedAt,
		})
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"conversation": response})
}

func buildAdminUserUsageDetail(snapshot store.AdminUserDetailSnapshot, days, timezoneOffset int) adminUserUsageDetail {
	detail := adminUserUsageDetail{
		UserID: snapshot.User.ID, Username: snapshot.User.Username, CreatedAt: snapshot.User.CreatedAt,
		Days: days, TimezoneOffset: timezoneOffset, adminUsageTotals: adminUsageTotals{CostKnown: true},
	}
	items := make(map[string]*adminConversationUsage, len(snapshot.Conversations))
	for _, raw := range snapshot.Conversations {
		item := adminConversationUsage{
			ID: raw.ID, Kind: raw.Kind, Title: raw.Title, AgentName: raw.AgentName, Status: raw.Status,
			Mode: raw.Mode, MaxTurns: raw.MaxTurns, ActivityCount: raw.ActivityCount,
			CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt, adminUsageTotals: adminUsageTotals{CostKnown: true},
		}
		items[item.ID] = &item
	}
	for _, event := range snapshot.UsageEvents {
		item := items[event.ConversationID]
		if item == nil {
			continue
		}
		usage, ok := normalizeAdminUsageEvent(event.AdminUsageEvent)
		if ok {
			addAdminUsage(&item.adminUsageTotals, usage)
		}
	}
	for _, item := range items {
		item.TotalTokens = item.InputTokens + item.OutputTokens + item.CacheTokens
		if item.CallCount == 0 {
			item.CostKnown = false
		}
		addAdminUsage(&detail.adminUsageTotals, item.adminUsageTotals)
		detail.Conversations = append(detail.Conversations, *item)
	}
	detail.TotalTokens = detail.InputTokens + detail.OutputTokens + detail.CacheTokens
	if detail.CallCount == 0 {
		detail.CostKnown = false
	}
	sort.Slice(detail.Conversations, func(i, j int) bool {
		if detail.Conversations[i].UpdatedAt == detail.Conversations[j].UpdatedAt {
			return detail.Conversations[i].ID < detail.Conversations[j].ID
		}
		return detail.Conversations[i].UpdatedAt > detail.Conversations[j].UpdatedAt
	})
	return detail
}

func buildAdminUsageOverview(snapshot store.AdminUsageSnapshot, days, timezoneOffset int, now int64, online func(string) bool) adminUsageOverview {
	overview := adminUsageOverview{Days: days, TimezoneOffset: timezoneOffset, Users: len(snapshot.Users)}
	trend := map[string]*adminUsageTrendPoint{}
	users := make(map[string]*adminUserUsage, len(snapshot.Users))
	for _, raw := range snapshot.Users {
		item := adminUserUsage{
			UserID: raw.UserID, Username: raw.Username, CreatedAt: raw.CreatedAt, LastActiveAt: raw.LastActiveAt,
			ChatSessions: raw.ChatSessions, OrchestrationRuns: raw.OrchestrationRuns, RunningRuns: raw.RunningRuns,
		}
		for _, agentID := range raw.AgentIDs {
			item.TotalAgents++
			if online(agentID) {
				item.OnlineAgents++
			}
		}
		item.ActivityStatus = adminActivityStatus(item.OnlineAgents, item.LastActiveAt, now)
		item.CostKnown = true
		users[item.UserID] = &item
	}

	for _, event := range snapshot.UsageEvents {
		usage, ok := normalizeAdminUsageEvent(event)
		if !ok {
			continue
		}
		item := users[event.UserID]
		if item == nil {
			continue
		}
		addAdminUsage(&item.adminUsageTotals, usage)
		point := adminTrendPoint(trend, overviewDate(event.OccurredAt, timezoneOffset))
		addAdminUsage(&point.adminUsageTotals, usage)
		point.activeUserIDs[event.UserID] = struct{}{}
	}
	for _, event := range snapshot.ActivityEvents {
		point := adminTrendPoint(trend, overviewDate(event.OccurredAt, timezoneOffset))
		point.activeUserIDs[event.UserID] = struct{}{}
		switch event.Kind {
		case "chat_message":
			point.ChatMessages += event.Count
		case "orchestration_run":
			point.OrchestrationRuns += event.Count
		}
	}
	if days > 0 {
		nowLocal := time.Unix(now, 0).Add(-time.Duration(timezoneOffset) * time.Minute).UTC()
		for offset := days - 1; offset >= 0; offset-- {
			date := nowLocal.AddDate(0, 0, -offset).Format("2006-01-02")
			adminTrendPoint(trend, date)
		}
	}

	for _, item := range users {
		item.TotalTokens = item.InputTokens + item.OutputTokens + item.CacheTokens
		if item.CallCount == 0 {
			item.CostKnown = false
		}
		overview.TotalAgents += item.TotalAgents
		overview.OnlineAgents += item.OnlineAgents
		overview.ChatSessions += item.ChatSessions
		overview.OrchestrationRuns += item.OrchestrationRuns
		overview.RunningRuns += item.RunningRuns
		if item.OnlineAgents > 0 {
			overview.OnlineUsers++
		}
		if item.LastActiveAt >= now-24*60*60 {
			overview.ActiveUsers++
		}
		addAdminUsage(&overview.adminUsageTotals, item.adminUsageTotals)
		overview.Items = append(overview.Items, *item)
	}
	overview.TotalTokens = overview.InputTokens + overview.OutputTokens + overview.CacheTokens
	if overview.CallCount == 0 {
		overview.CostKnown = false
	}
	for _, point := range trend {
		point.ActiveUsers = len(point.activeUserIDs)
		point.TotalTokens = point.InputTokens + point.OutputTokens + point.CacheTokens
		if point.CallCount == 0 {
			point.CostKnown = false
		}
		overview.Trend = append(overview.Trend, *point)
	}
	sort.Slice(overview.Trend, func(i, j int) bool { return overview.Trend[i].Date < overview.Trend[j].Date })
	sort.Slice(overview.Items, func(i, j int) bool {
		if overview.Items[i].LastActiveAt == overview.Items[j].LastActiveAt {
			return strings.ToLower(overview.Items[i].Username) < strings.ToLower(overview.Items[j].Username)
		}
		return overview.Items[i].LastActiveAt > overview.Items[j].LastActiveAt
	})
	return overview
}

func adminTrendPoint(trend map[string]*adminUsageTrendPoint, date string) *adminUsageTrendPoint {
	point := trend[date]
	if point == nil {
		point = &adminUsageTrendPoint{Date: date, activeUserIDs: map[string]struct{}{}, adminUsageTotals: adminUsageTotals{CostKnown: true}}
		trend[date] = point
	}
	return point
}

func adminActivityStatus(onlineAgents int, lastActiveAt, now int64) string {
	if onlineAgents > 0 {
		return "online"
	}
	age := now - lastActiveAt
	if age <= 24*60*60 {
		return "active"
	}
	if age <= 7*24*60*60 {
		return "idle"
	}
	return "inactive"
}

func addAdminUsage(target *adminUsageTotals, usage adminUsageTotals) {
	if usage.CallCount <= 0 {
		return
	}
	hadUsage := target.CallCount > 0
	target.InputTokens += usage.InputTokens
	target.OutputTokens += usage.OutputTokens
	target.CacheTokens += usage.CacheTokens
	target.TotalTokens += usage.TotalTokens
	target.EstimatedCostUSD += usage.EstimatedCostUSD
	target.CallCount += usage.CallCount
	if !hadUsage {
		target.CostKnown = usage.CostKnown
	} else {
		target.CostKnown = target.CostKnown && usage.CostKnown
	}
}

func normalizeAdminUsageEvent(event store.AdminUsageEvent) (adminUsageTotals, bool) {
	cli, model := event.CLI, event.Model
	input, output, cacheRead, cacheWrite := event.InputTokens, event.OutputTokens, event.CacheReadTokens, event.CacheWriteTokens
	cost, costKnown := float64(0), false
	if event.UsageJSON != "" {
		if event.Legacy {
			var data map[string]any
			if json.Unmarshal([]byte(event.UsageJSON), &data) != nil {
				return adminUsageTotals{}, false
			}
			usage := orchestrationUsageFromData(data)
			if usage.CLI != "" {
				cli = usage.CLI
			}
			model, input, output, cacheRead, cacheWrite = usage.Model, usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens
			cost, costKnown = usage.EstimatedCostUSD, usage.CostKnown
		} else {
			var data map[string]any
			if json.Unmarshal([]byte(event.UsageJSON), &data) != nil {
				return adminUsageTotals{}, false
			}
			cli, model, input, output, cacheRead, cacheWrite = adminUsageFields(cli, data)
		}
	}
	if input+output+cacheRead+cacheWrite <= 0 {
		return adminUsageTotals{}, false
	}
	if !event.Normalized && strings.EqualFold(cli, "codex") && input >= cacheRead {
		input -= cacheRead
	}
	if !costKnown {
		quote, ok := usagepricing.EstimateNormalized(cli, model, input, output, cacheRead, cacheWrite)
		if ok {
			cost, costKnown = quote.CostUSD, true
		}
	}
	calls := event.CallCount
	if calls <= 0 {
		calls = 1
	}
	return adminUsageTotals{InputTokens: input, OutputTokens: output, CacheTokens: cacheRead + cacheWrite, TotalTokens: input + output + cacheRead + cacheWrite, EstimatedCostUSD: cost, CostKnown: costKnown, CallCount: calls}, true
}

func adminUsageFields(defaultCLI string, data map[string]any) (string, string, int64, int64, int64, int64) {
	outer := data
	if nested, ok := data["usage"].(map[string]any); ok {
		data = nested
	}
	if nested, ok := data["last"].(map[string]any); ok {
		data = nested
	}
	number := func(keys ...string) int64 {
		for _, key := range keys {
			switch value := data[key].(type) {
			case float64:
				if value >= 0 && value <= math.MaxInt64 {
					return int64(value)
				}
			case json.Number:
				if value, err := value.Int64(); err == nil && value >= 0 {
					return value
				}
			}
		}
		return 0
	}
	text := func(values ...any) string {
		for _, value := range values {
			if value, ok := value.(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	cli := text(outer["cli"], defaultCLI)
	model := text(outer["model"], data["model"])
	return cli, model,
		number("input_tokens", "inputTokens"), number("output_tokens", "outputTokens"),
		number("cached_input_tokens", "cache_read_input_tokens", "cachedInputTokens", "cacheReadTokens"),
		number("cache_write_input_tokens", "cache_creation_input_tokens", "cacheWriteInputTokens", "cacheWriteTokens")
}
