package hub

import (
	"log/slog"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

const leaseIdleLeased = "leaseIdleLeased"

type browserSessionLease struct {
	sid     string
	agentID string
	state   string
	timer   *time.Timer
}

func (s *Server) tryReattach(sid string) bool {
	s.leasesMu.Lock()
	lease := s.leases[sid]
	if lease == nil {
		s.leasesMu.Unlock()
		return false
	}
	delete(s.leases, sid)
	if lease.timer != nil {
		lease.timer.Stop()
	}
	s.leasesMu.Unlock()
	return lease.state == leaseIdleLeased
}

func (s *Server) startBrowserLease(session store.Session) {
	if s.cfg.Hub.BrowserCloseSession {
		s.closeBrowserSessionAfterGrace(session)
		return
	}
	ttl := s.cfg.Hub.BrowserLeaseTTL.Duration
	if ttl <= 0 {
		return
	}

	lease := &browserSessionLease{
		sid:     session.ID,
		agentID: session.AgentID,
		state:   leaseIdleLeased,
	}
	lease.timer = time.AfterFunc(ttl, func() {
		s.expireBrowserLease(session.ID, session.AgentID)
	})

	s.leasesMu.Lock()
	if old := s.leases[session.ID]; old != nil && old.timer != nil {
		old.timer.Stop()
	}
	s.leases[session.ID] = lease
	s.leasesMu.Unlock()
}

func (s *Server) expireBrowserLease(sid, agentID string) {
	s.leasesMu.Lock()
	lease := s.leases[sid]
	if lease == nil || lease.agentID != agentID {
		s.leasesMu.Unlock()
		return
	}
	delete(s.leases, sid)
	s.leasesMu.Unlock()

	if s.pool.HasBrowser(sid) {
		return
	}
	if err := s.pool.SendToAgent(agentID, protocol.MustEnvelope(protocol.TypeCloseSession, sid, nil)); err != nil {
		slog.Debug("[hub] browser lease close_session failed", "sid", sid, "agentID", agentID, "error", err)
	}
}

func (s *Server) closeBrowserSessionAfterGrace(session store.Session) {
	grace := s.cfg.Hub.BrowserCloseGrace.Duration
	go func() {
		if grace > 0 {
			time.Sleep(grace)
		}
		if s.pool.HasBrowser(session.ID) {
			return
		}
		_ = s.pool.SendToAgent(session.AgentID, protocol.MustEnvelope(protocol.TypeCloseSession, session.ID, nil))
	}()
}
