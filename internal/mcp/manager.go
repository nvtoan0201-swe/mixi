package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/user/mixi-agent/internal/tools"
)

// State is one node of the server lifecycle machine.
type State string

const (
	StateConfigured   State = "configured"
	StateInitializing State = "initializing"
	StateReady        State = "ready"
	StateRestarting   State = "restarting"
	StateFailed       State = "failed" // disabled until manual reconnect
	StateClosed       State = "closed"
)

// Registrar receives adapted MCP tools; *tools.Registry satisfies it.
type Registrar interface {
	Register(t tools.Tool) error
}

// ManagerOptions configures the server fleet.
type ManagerOptions struct {
	Servers  map[string]ServerConfig
	Registry Registrar
	Log      *slog.Logger // nil = default
	// Notify surfaces user-facing notices (server disabled, …). May be nil.
	Notify func(text string)

	// Test seams; zero values select production behavior.
	NewTransport func(name string, cfg ServerConfig) (Transport, error)
	Backoff      []time.Duration
	StableAfter  time.Duration // uptime that resets the strike counter
}

// Manager supervises every configured MCP server: lifecycle, restarts,
// tool adoption, and call routing for adapted tools.
type Manager struct {
	reg          Registrar
	log          *slog.Logger
	notify       func(string)
	newTransport func(name string, cfg ServerConfig) (Transport, error)
	backoff      []time.Duration
	stableAfter  time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// servers is write-once before Start, read-only afterwards; per-server
	// mutable state locks under server.mu.
	servers map[string]*server
}

// server is the supervised state for one configured MCP server.
type server struct {
	name string
	cfg  ServerConfig

	mu      sync.Mutex
	state   State
	client  *Client
	current map[string]ToolInfo // tools the server offers right now
	known   map[string]ToolInfo // last-seen info per tool, never deleted
	adapted map[string]bool     // registry names already registered

	settleOnce sync.Once
	settled    chan struct{} // closed once the first READY-or-failure resolves
}

// NewManager validates the config (sanitized server-key collisions are a
// configuration error) and prepares the fleet without starting it.
func NewManager(opts ManagerOptions) (*Manager, error) {
	m := &Manager{
		reg:          opts.Registry,
		log:          opts.Log,
		notify:       opts.Notify,
		newTransport: opts.NewTransport,
		backoff:      opts.Backoff,
		stableAfter:  opts.StableAfter,
		servers:      map[string]*server{},
	}
	if m.log == nil {
		m.log = slog.Default()
	}
	if m.notify == nil {
		m.notify = func(string) {}
	}
	if m.newTransport == nil {
		m.newTransport = func(name string, cfg ServerConfig) (Transport, error) {
			return NewStdio(StdioOptions{
				Name: name, Command: cfg.Command, Args: cfg.Args, Env: cfg.Env,
				Log: m.log, WriteTimeout: cfg.Timeout(),
			})
		}
	}
	if len(m.backoff) == 0 {
		m.backoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	}
	if m.stableAfter <= 0 {
		m.stableAfter = 30 * time.Second
	}

	bySanitized := map[string]string{}
	for name, cfg := range opts.Servers {
		s := sanitizeName(name)
		if prev, dup := bySanitized[s]; dup {
			return nil, fmt.Errorf("mcp: servers %q and %q collide after name sanitizing (%q) — rename one", prev, name, s)
		}
		bySanitized[s] = name
		m.servers[name] = &server{
			name: name, cfg: cfg, state: StateConfigured,
			current: map[string]ToolInfo{}, known: map[string]ToolInfo{},
			adapted: map[string]bool{}, settled: make(chan struct{}),
		}
	}
	return m, nil
}

// Start launches one supervisor per server and returns immediately.
func (m *Manager) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)
	for _, s := range m.servers {
		m.wg.Add(1)
		go m.supervise(s, 0)
	}
}

// WaitInitial blocks until every server resolved its first handshake (READY
// or first failure) or ctx expires. Servers still settling keep going in
// the background either way.
func (m *Manager) WaitInitial(ctx context.Context) {
	for _, s := range m.servers {
		select {
		case <-s.settled:
		case <-ctx.Done():
			return
		}
	}
}

// Close shuts every server down and waits for the supervisors to exit.
func (m *Manager) Close() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

// ServerStatus is one row of the /mcp display.
type ServerStatus struct {
	Name  string
	State State
	Tools int
}

// Status reports every server sorted by name.
func (m *Manager) Status() []ServerStatus {
	out := make([]ServerStatus, 0, len(m.servers))
	for _, s := range m.servers {
		s.mu.Lock()
		out = append(out, ServerStatus{Name: s.name, State: s.state, Tools: len(s.current)})
		s.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Reconnect re-enables a FAILED server with a fresh strike counter.
func (m *Manager) Reconnect(name string) error {
	s, ok := m.servers[name]
	if !ok {
		return fmt.Errorf("mcp: no server %q", name)
	}
	if m.ctx == nil || m.ctx.Err() != nil {
		return fmt.Errorf("mcp: manager is shut down") // never race Close's wg.Wait
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateFailed {
		return fmt.Errorf("mcp: server %q is %s, not failed", name, s.state)
	}
	s.state = StateRestarting
	m.wg.Add(1)
	go m.supervise(s, 0)
	return nil
}

// callTool routes an adapted tool's Execute to the owning server.
func (m *Manager) callTool(ctx context.Context, srvName, origName string, args []byte) (tools.ToolResult, error) {
	s := m.servers[srvName]
	s.mu.Lock()
	st, cl := s.state, s.client
	_, offered := s.current[origName]
	s.mu.Unlock()

	if st != StateReady || cl == nil {
		return tools.Errorf("MCP server %q is %s; the tool is temporarily unavailable — retry shortly.", srvName, st), nil
	}
	if !offered {
		return tools.Errorf("Tool %q is no longer provided by MCP server %q.", origName, srvName), nil
	}
	content, isErr, err := cl.CallTool(ctx, origName, args)
	if errors.Is(err, ErrConnClosed) {
		return tools.Errorf("MCP server %q crashed during call. It is restarting; retry the tool if needed.", srvName), nil
	}
	if err != nil {
		return tools.ToolResult{}, err
	}
	return tools.ToolResult{Content: content, IsError: isErr}, nil
}

// toolInfo returns the last-known catalog entry for a tool.
func (m *Manager) toolInfo(srvName, origName string) (ToolInfo, bool) {
	s := m.servers[srvName]
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.known[origName]
	return info, ok
}

func (s *server) setState(st State) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
}

func (s *server) settle() {
	s.settleOnce.Do(func() { close(s.settled) })
}
