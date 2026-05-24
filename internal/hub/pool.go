package hub

import (
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tencent/codex-bridge/internal/protocol"
)

var ErrAgentOffline = errors.New("agent offline")

type Pool struct {
	mu                    sync.RWMutex
	agents                map[string]*BridgeConn
	browsers              map[string]map[*BrowserConn]struct{}
	orchestrationBrowsers map[string]map[*BrowserConn]struct{}
}

func NewPool() *Pool {
	return &Pool{
		agents:                make(map[string]*BridgeConn),
		browsers:              make(map[string]map[*BrowserConn]struct{}),
		orchestrationBrowsers: make(map[string]map[*BrowserConn]struct{}),
	}
}

func (p *Pool) RegisterAgent(conn *BridgeConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if old := p.agents[conn.agentID]; old != nil && old != conn {
		old.Close()
	}
	p.agents[conn.agentID] = conn
}

func (p *Pool) UnregisterAgent(agentID string, conn *BridgeConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.agents[agentID] == conn {
		delete(p.agents, agentID)
	}
}

func (p *Pool) AgentOnline(agentID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.agents[agentID] != nil
}

func (p *Pool) DisconnectAgent(agentID string) {
	p.mu.Lock()
	conn := p.agents[agentID]
	if conn != nil {
		delete(p.agents, agentID)
	}
	p.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func (p *Pool) ShutdownAgent(agentID, reason string) error {
	return p.ShutdownAgentWithGrace(agentID, reason, 500*time.Millisecond)
}

func (p *Pool) ShutdownAgentWithGrace(agentID, reason string, grace time.Duration) error {
	p.mu.Lock()
	conn := p.agents[agentID]
	if conn != nil {
		delete(p.agents, agentID)
	}
	p.mu.Unlock()
	if conn == nil {
		return ErrAgentOffline
	}
	if err := conn.Send(protocol.MustEnvelope(protocol.TypeAgentShutdown, "", protocol.AgentShutdownPayload{Reason: reason})); err != nil {
		conn.Close()
		return err
	}
	go func() {
		if grace > 0 {
			time.Sleep(grace)
		}
		conn.Close()
	}()
	return nil
}

func (p *Pool) SendToAgent(agentID string, env protocol.Envelope) error {
	p.mu.RLock()
	conn := p.agents[agentID]
	p.mu.RUnlock()
	if conn == nil {
		return ErrAgentOffline
	}
	return conn.Send(env)
}

func (p *Pool) AddBrowser(sid string, conn *BrowserConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	set := p.browsers[sid]
	if set == nil {
		set = make(map[*BrowserConn]struct{})
		p.browsers[sid] = set
	}
	set[conn] = struct{}{}
}

func (p *Pool) RemoveBrowser(sid string, conn *BrowserConn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	set := p.browsers[sid]
	if set == nil {
		return false
	}
	delete(set, conn)
	if len(set) == 0 {
		delete(p.browsers, sid)
		return true
	}
	return false
}

func (p *Pool) HasBrowser(sid string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.browsers[sid]) > 0
}

func (p *Pool) BroadcastToBrowsers(sid string, env protocol.Envelope) {
	p.mu.RLock()
	var conns []*BrowserConn
	for conn := range p.browsers[sid] {
		conns = append(conns, conn)
	}
	p.mu.RUnlock()
	for _, conn := range conns {
		_ = conn.Send(env)
	}
}

func (p *Pool) AddOrchestrationBrowser(runID string, conn *BrowserConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	set := p.orchestrationBrowsers[runID]
	if set == nil {
		set = make(map[*BrowserConn]struct{})
		p.orchestrationBrowsers[runID] = set
	}
	set[conn] = struct{}{}
}

func (p *Pool) RemoveOrchestrationBrowser(runID string, conn *BrowserConn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	set := p.orchestrationBrowsers[runID]
	if set == nil {
		return
	}
	delete(set, conn)
	if len(set) == 0 {
		delete(p.orchestrationBrowsers, runID)
	}
}

func (p *Pool) BroadcastToOrchestrationBrowsers(runID string, env protocol.Envelope) {
	p.mu.RLock()
	var conns []*BrowserConn
	for conn := range p.orchestrationBrowsers[runID] {
		conns = append(conns, conn)
	}
	p.mu.RUnlock()
	for _, conn := range conns {
		_ = conn.Send(env)
	}
}

type wsSender struct {
	ws   *websocket.Conn
	send chan protocol.Envelope
	done chan struct{}
	once sync.Once
}

func newWSSender(ws *websocket.Conn, queue int) wsSender {
	if queue <= 0 {
		queue = 64
	}
	return wsSender{
		ws:   ws,
		send: make(chan protocol.Envelope, queue),
		done: make(chan struct{}),
	}
}

func (s *wsSender) Send(env protocol.Envelope) error {
	select {
	case <-s.done:
		return websocket.ErrCloseSent
	case s.send <- env:
		return nil
	default:
		return errors.New("websocket send queue full")
	}
}

func (s *wsSender) WriteLoop() {
	for {
		select {
		case <-s.done:
			return
		case env := <-s.send:
			_ = s.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := s.ws.WriteJSON(env); err != nil {
				s.Close()
				return
			}
		}
	}
}

func (s *wsSender) Close() {
	s.once.Do(func() {
		close(s.done)
		if s.ws != nil {
			_ = s.ws.Close()
		}
	})
}

type BridgeConn struct {
	agentID      string
	capabilities *protocol.BridgeCapabilities
	wsSender
}

func NewBridgeConn(agentID string, ws *websocket.Conn, queue int, capabilities *protocol.BridgeCapabilities) *BridgeConn {
	return &BridgeConn{agentID: agentID, capabilities: capabilities, wsSender: newWSSender(ws, queue)}
}

func (p *Pool) AgentCapabilities(agentID string) (*protocol.BridgeCapabilities, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	conn := p.agents[agentID]
	if conn == nil || conn.capabilities == nil {
		return nil, false
	}
	return conn.capabilities, true
}

type BrowserConn struct {
	sid string
	wsSender
}

func NewBrowserConn(sid string, ws *websocket.Conn, queue int) *BrowserConn {
	return &BrowserConn{sid: sid, wsSender: newWSSender(ws, queue)}
}
