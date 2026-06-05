package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/perm"
	"github.com/user/mixi-agent/internal/session"
)

// commandSpec is one slash command. The table is the registration point
// extensions will append to when the extension host lands.
type commandSpec struct {
	name string
	help string
	run  func(m *rootModel, arg string) tea.Cmd
}

// compactDoneMsg reports the result of an async /compact.
type compactDoneMsg struct{ err error }

func commandTable() []commandSpec {
	return []commandSpec{
		{"/compact", "compact context now [focus]", cmdCompact},
		{"/cost", "session token/cost totals", cmdCost},
		{"/ext", "extension status", cmdExt},
		{"/fork", "fork session (use --resume <id> --fork <entry>)", cmdSessionHint},
		{"/mcp", "MCP server status; /mcp reconnect <name>", cmdMCP},
		{"/mode", "cycle or set permission mode", cmdMode},
		{"/model", "cycle or set model", cmdModel},
		{"/name", "name this session", cmdName},
		{"/new", "new session (restart mixi)", cmdSessionHint},
		{"/permissions", "show rules and session grants", cmdPermissions},
		{"/pin", "pin the latest entry: /pin <label>", cmdPin},
		{"/quit", "exit", func(*rootModel, string) tea.Cmd { return tea.Quit }},
		{"/resume", "resume a session (use mixi -c or --resume)", cmdSessionHint},
		{"/tree", "show the session entry tree", cmdTree},
	}
}

func cmdModel(m *rootModel, arg string) tea.Cmd {
	models := m.deps.Models
	if len(models) == 0 {
		m.transcript.notice("No model catalog available.", true)
		return nil
	}
	cur := m.deps.Agent.Model()
	next := models[0]
	if arg != "" {
		found := false
		for _, c := range models {
			if c.ID == arg || c.Provider+"/"+c.ID == arg {
				next, found = c, true
				break
			}
		}
		if !found {
			m.transcript.notice(fmt.Sprintf("Unknown model %q. Known: %s", arg, modelIDs(models)), true)
			return nil
		}
	} else {
		for i, c := range models {
			if c.Provider == cur.Provider && c.ID == cur.ID {
				next = models[(i+1)%len(models)]
				break
			}
		}
	}
	m.deps.Agent.SetModel(next)
	m.status.model = next
	m.appendEntry(&session.ModelChangeEntry{Provider: next.Provider, ModelID: next.ID})
	m.transcript.notice("Model: "+next.Provider+"/"+next.ID, false)
	return nil
}

func modelIDs(models []ai.Model) string {
	ids := make([]string, len(models))
	for i, c := range models {
		ids[i] = c.Provider + "/" + c.ID
	}
	return strings.Join(ids, ", ")
}

func cmdCompact(m *rootModel, arg string) tea.Cmd {
	if m.running {
		m.transcript.notice("Cannot compact during a run.", true)
		return nil
	}
	if m.deps.Compactor == nil {
		m.transcript.notice("Compaction is not available in this session.", true)
		return nil
	}
	comp := m.deps.Compactor
	return func() tea.Msg { return compactDoneMsg{err: comp.Compact(context.Background(), arg)} }
}

func cmdCost(m *rootModel, _ string) tea.Cmd {
	m.transcript.notice(fmt.Sprintf("Session cost: $%.4f • last context: %d tokens", m.status.cost, m.status.ctxTokens), false)
	return nil
}

// permModeCycle is the /mode rotation order.
var permModeCycle = []perm.Mode{perm.ModePlan, perm.ModePrompt, perm.ModeAutoEdit, perm.ModeYolo}

func cmdMode(m *rootModel, arg string) tea.Cmd {
	if m.deps.Engine == nil {
		m.transcript.notice("No permission engine in this session.", true)
		return nil
	}
	cur := m.deps.Engine.Mode()
	next := cur
	if arg != "" {
		found := false
		for _, md := range permModeCycle {
			if string(md) == arg {
				next, found = md, true
				break
			}
		}
		if !found {
			m.transcript.notice(fmt.Sprintf("Unknown mode %q (plan|prompt|auto-edit|yolo)", arg), true)
			return nil
		}
	} else {
		for i, md := range permModeCycle {
			if md == cur {
				next = permModeCycle[(i+1)%len(permModeCycle)]
				break
			}
		}
	}
	m.deps.Engine.SetMode(next)
	m.status.permMode = string(next)
	m.transcript.notice("Permission mode: "+string(next), false)
	return nil
}

func cmdPermissions(m *rootModel, _ string) tea.Cmd {
	if m.deps.Engine == nil {
		m.transcript.notice("No permission engine in this session.", true)
		return nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Permission mode: %s\n", m.deps.Engine.Mode())
	grants := m.deps.Engine.Grants()
	if len(grants) == 0 {
		sb.WriteString("Session grants: none")
	} else {
		sb.WriteString("Session grants:\n  " + strings.Join(grants, "\n  "))
	}
	m.transcript.notice(sb.String(), false)
	return nil
}

func cmdPin(m *rootModel, arg string) tea.Cmd {
	if arg == "" {
		m.transcript.notice("Usage: /pin <label>", true)
		return nil
	}
	leaf := m.deps.Store.LeafID()
	if leaf == "" {
		m.transcript.notice("Nothing to pin yet.", true)
		return nil
	}
	m.appendEntry(&session.LabelEntry{TargetID: leaf, Label: arg})
	m.transcript.notice(fmt.Sprintf("Pinned %s as %q", leaf, arg), false)
	return nil
}

func cmdName(m *rootModel, arg string) tea.Cmd {
	if arg == "" {
		m.transcript.notice("Usage: /name <session name>", true)
		return nil
	}
	m.appendEntry(&session.SessionInfoEntry{Name: arg})
	m.transcript.notice("Session named: "+arg, false)
	return nil
}

func cmdTree(m *rootModel, _ string) tea.Cmd {
	entries := m.deps.Store.Entries()
	if len(entries) == 0 {
		m.transcript.notice("Session is empty.", false)
		return nil
	}
	leaf := m.deps.Store.LeafID()
	var sb strings.Builder
	sb.WriteString("Session tree (oldest first):\n")
	for _, e := range entries {
		marker := "  "
		if e.EntryID() == leaf {
			marker = "▶ "
		}
		fmt.Fprintf(&sb, "%s%s %s %s\n", marker, e.EntryID(), e.Type(), entrySummary(e))
	}
	m.transcript.notice(strings.TrimRight(sb.String(), "\n"), false)
	return nil
}

// entrySummary gives each tree row a one-glance description.
func entrySummary(e session.Entry) string {
	switch v := e.(type) {
	case *session.MessageEntry:
		return string(v.Message.MsgRole())
	case *session.LabelEntry:
		return fmt.Sprintf("%q → %s", v.Label, v.TargetID)
	case *session.SessionInfoEntry:
		return fmt.Sprintf("name=%q", v.Name)
	case *session.ModelChangeEntry:
		return v.Provider + "/" + v.ModelID
	case *session.CompactionEntry:
		return fmt.Sprintf("kept from %s", v.FirstKeptEntryID)
	}
	return ""
}

func cmdMCP(m *rootModel, arg string) tea.Cmd {
	fleet := m.deps.MCP
	if fleet == nil {
		m.transcript.notice("No MCP servers configured (add mcpServers to settings.json).", false)
		return nil
	}
	if name, ok := strings.CutPrefix(arg, "reconnect "); ok {
		name = strings.TrimSpace(name)
		if err := fleet.Reconnect(name); err != nil {
			m.transcript.notice("MCP reconnect: "+err.Error(), true)
		} else {
			m.transcript.notice("Reconnecting MCP server "+name+"…", false)
		}
		return nil
	}
	var b strings.Builder
	b.WriteString("MCP servers:")
	for _, st := range fleet.Status() {
		fmt.Fprintf(&b, "\n  %-20s %-12s %d tools", st.Name, st.State, st.Tools)
	}
	b.WriteString("\nReconnect a failed server: /mcp reconnect <name>")
	m.transcript.notice(b.String(), false)
	return nil
}

func cmdExt(m *rootModel, _ string) tea.Cmd {
	fleet := m.deps.Ext
	if fleet == nil {
		m.transcript.notice("No extensions running (add extensions to settings.json or drop executables in .mixi/extensions/).", false)
		return nil
	}
	var b strings.Builder
	b.WriteString("Extensions:")
	for _, st := range fleet.Status() {
		fmt.Fprintf(&b, "\n  %-20s %-12s %d tools", st.Name, st.State, st.Tools)
	}
	m.transcript.notice(b.String(), false)
	return nil
}

// extensionCommands projects extension-registered commands into the slash
// command table; invoking one dispatches a user_input event to its owner.
func extensionCommands(fleet ExtFleet) []commandSpec {
	var specs []commandSpec
	for _, c := range fleet.Commands() {
		name := c.Name
		specs = append(specs, commandSpec{
			name: "/" + name,
			help: c.Description + " (ext:" + c.Ext + ")",
			run: func(m *rootModel, arg string) tea.Cmd {
				if err := m.deps.Ext.DispatchCommand(name, arg); err != nil {
					m.transcript.notice(err.Error(), true)
				}
				return nil
			},
		})
	}
	return specs
}

// cmdSessionHint covers session switching, which requires a process-level
// rebuild; the CLI flags are the supported path for now.
func cmdSessionHint(m *rootModel, _ string) tea.Cmd {
	m.transcript.notice("Session switching from inside the TUI is not available yet.\nUse: mixi (new) • mixi -c (continue recent) • mixi --resume <id> [--fork <entry>]", false)
	return nil
}
