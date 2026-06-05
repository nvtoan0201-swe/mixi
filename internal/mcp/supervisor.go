package mcp

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// maxStrikes is how many consecutive failures a server may accumulate
// before it is disabled (FAILED) pending manual reconnect.
const maxStrikes = 3

// supervise drives one server through its lifecycle until close or FAILED:
//
//	CONFIGURED → INITIALIZING → READY ⇄ RESTARTING → FAILED / CLOSED
//
// strikes carries the consecutive-failure count across restarts; a stable
// uptime resets it.
func (m *Manager) supervise(s *server, strikes int) {
	defer m.wg.Done()
	for {
		if m.ctx.Err() != nil {
			s.setState(StateClosed)
			s.settle()
			return
		}
		s.setState(StateInitializing)

		tr, err := m.newTransport(s.name, s.cfg)
		if err != nil {
			// A missing or unrunnable binary will not fix itself with retries.
			m.fail(s, fmt.Sprintf("MCP server %q failed to start: %v", s.name, err))
			return
		}
		cl := NewClient(ClientOptions{
			ServerName:  s.name,
			Transport:   tr,
			CallTimeout: s.cfg.Timeout(),
			Log:         m.log,
			// Fired from the client's reader goroutine: re-list on a fresh
			// goroutine so the response can actually be routed.
			OnToolsChanged: func() { go m.relist(s) },
		})

		hctx, cancel := context.WithTimeout(m.ctx, s.cfg.Timeout())
		err = cl.Initialize(hctx)
		var infos []ToolInfo
		if err == nil {
			infos, err = cl.ListTools(hctx)
		}
		cancel()
		if err != nil {
			cl.Close()
			m.log.Warn("mcp: handshake failed", "server", s.name, "err", err)
			strikes++
			s.settle()
			if strikes > maxStrikes {
				m.fail(s, fmt.Sprintf("MCP server %q failed %d handshakes; disabled (reconnect via /mcp)", s.name, strikes))
				return
			}
			s.setState(StateRestarting)
			if !m.sleepBackoff(strikes) {
				s.setState(StateClosed)
				return
			}
			continue
		}

		s.mu.Lock()
		s.client = cl
		s.mu.Unlock()
		m.adoptTools(s, infos)
		s.setState(StateReady)
		s.settle()
		m.log.Info("mcp: server ready", "server", s.name,
			"protocol", cl.NegotiatedVersion(), "tools", len(infos))
		readyAt := time.Now()

		select {
		case <-m.ctx.Done():
			cl.Close()
			s.setState(StateClosed)
			return
		case <-cl.Done(): // EOF or crash; the client already failed pending calls
			cl.Close()
			s.mu.Lock()
			s.client = nil
			s.mu.Unlock()
			if time.Since(readyAt) >= m.stableAfter {
				strikes = 0
			}
			strikes++
			m.log.Warn("mcp: server connection lost", "server", s.name, "strikes", strikes)
			if strikes > maxStrikes {
				m.fail(s, fmt.Sprintf("MCP server %q crashed %d times in a row; disabled (reconnect via /mcp)", s.name, strikes))
				return
			}
			s.setState(StateRestarting)
			if !m.sleepBackoff(strikes) {
				s.setState(StateClosed)
				return
			}
		}
	}
}

// fail disables the server and tells the user; only /mcp reconnect or a
// process restart bring it back. The notice goes out before the state
// flips so anyone unblocked by FAILED/settled already sees it.
func (m *Manager) fail(s *server, msg string) {
	m.log.Error("mcp: server disabled", "server", s.name, "reason", msg)
	m.notify(msg)
	s.setState(StateFailed)
	s.settle()
}

// sleepBackoff waits the strike-indexed backoff; false means the manager
// closed while waiting.
func (m *Manager) sleepBackoff(strikes int) bool {
	idx := strikes - 1
	if idx >= len(m.backoff) {
		idx = len(m.backoff) - 1
	}
	select {
	case <-m.ctx.Done():
		return false
	case <-time.After(m.backoff[idx]):
		return true
	}
}

// relist refreshes the server's tool catalog after a list_changed
// notification.
func (m *Manager) relist(s *server) {
	s.mu.Lock()
	cl := s.client
	s.mu.Unlock()
	if cl == nil {
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, s.cfg.Timeout())
	defer cancel()
	infos, err := cl.ListTools(ctx)
	if err != nil {
		m.log.Warn("mcp: re-list after list_changed failed", "server", s.name, "err", err)
		return
	}
	m.adoptTools(s, infos)
}

// adoptTools replaces the server's current catalog and registers adapters
// for names not seen before. Existing adapters pick up refreshed schemas
// and descriptions live; tools that disappeared stay registered but error
// when called — the LLM-visible tool list never churns mid-run.
func (m *Manager) adoptTools(s *server, infos []ToolInfo) {
	current := make(map[string]ToolInfo, len(infos))
	for _, info := range infos {
		if _, dup := current[info.Name]; dup {
			m.log.Warn("mcp: duplicate tool name from server, last wins", "server", s.name, "tool", info.Name)
		}
		current[info.Name] = checkSchema(m, s.name, info)
	}

	s.mu.Lock()
	added, removed := diffNames(s.current, current)
	s.current = current
	var newTools []ToolInfo
	for name, info := range current {
		s.known[name] = info
		regName := RegisteredToolName(s.name, name)
		if !s.adapted[regName] {
			s.adapted[regName] = true
			newTools = append(newTools, info)
		}
	}
	s.mu.Unlock()

	if len(added)+len(removed) > 0 {
		m.log.Info("mcp: tool set changed", "server", s.name, "added", added, "removed", removed)
	}
	for _, info := range newTools {
		t := &adaptedTool{m: m, server: s.name, orig: info.Name,
			regName: RegisteredToolName(s.name, info.Name)}
		if err := m.reg.Register(t); err != nil {
			m.log.Warn("mcp: tool registration rejected", "server", s.name, "tool", t.regName, "err", err)
		}
	}
}

// diffNames reports catalog membership changes for the restart/re-list log.
func diffNames(old, new map[string]ToolInfo) (added, removed []string) {
	for name := range new {
		if _, ok := old[name]; !ok {
			added = append(added, name)
		}
	}
	for name := range old {
		if _, ok := new[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}
