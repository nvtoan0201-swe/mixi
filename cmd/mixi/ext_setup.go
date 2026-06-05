package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/config"
	"github.com/user/mixi-agent/internal/ext"
	"github.com/user/mixi-agent/internal/session"
	"github.com/user/mixi-agent/internal/tools"
)

// startExtensions discovers and boots the extension host, blocking
// (bounded) until first handshakes settle so extension tools reach the
// registry — and the system prompt — before the agent is built. Returns
// nil when no extensions are configured or discovered.
func startExtensions(rc *config.RuntimeConfig, cwd string, store session.Storage,
	reg *tools.Registry, log *slog.Logger, notify func(string)) (*ext.Host, error) {
	if rc.Flags.NoExtensions {
		return nil, nil
	}
	configured, err := ext.ParseConfigs(rc.Setting.Extensions)
	if err != nil {
		return nil, err
	}
	cfgs := ext.Discover(configured, ext.DiscoverDirs(cwd))
	if len(cfgs) == 0 {
		return nil, nil
	}
	host, err := ext.NewHost(ext.Options{
		Configs:  cfgs,
		Registry: reg,
		Log:      log,
		Notify:   notify,
		Ready: ext.ReadyInfo{
			SessionID: store.Header().ID,
			Cwd:       cwd,
			Mode:      string(rc.Mode),
			Model:     rc.Model.Provider + "/" + rc.Model.ID,
		},
	})
	if err != nil {
		return nil, err
	}
	host.Start(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	host.WaitInitial(ctx)
	return host, nil
}

// bindExtensions connects a started host to the live agent: the action API
// and the event stream. The returned func unsubscribes the host's bus tap.
func bindExtensions(host *ext.Host, a *agent.Agent, store session.Storage,
	notifier *agentNotifier, headless bool, errOut io.Writer, log *slog.Logger) func() {
	events, unsubscribe := a.Subscribe()
	host.Bind(&extAPI{
		agent: a, store: store, notifier: notifier,
		headless: headless, errOut: errOut, log: log,
	}, events)
	return unsubscribe
}

// extAPI implements ext.API against the live session. TUI surfaces route
// through the agent bus (the TUI renders EvNotice/EvStatus); headless mode
// writes notices to stderr and auto-answers selects.
type extAPI struct {
	agent    *agent.Agent
	store    session.Storage
	notifier *agentNotifier
	headless bool
	errOut   io.Writer
	log      *slog.Logger
}

func (x *extAPI) SendUserMessage(content, deliverAs string) error {
	msg := agent.CustomMessage{Content: content, Timestamp: time.Now().UnixMilli()}
	if deliverAs == "steer" {
		return x.agent.Steer(msg)
	}
	return x.agent.FollowUp(msg) // followUp | nextTurn
}

func (x *extAPI) AppendEntry(customType string, data json.RawMessage) error {
	return x.store.Append(&session.CustomEntry{CustomType: customType, Data: data})
}

func (x *extAPI) SetStatus(key, text string) error {
	if x.headless {
		return nil // no footer to draw on
	}
	x.notifier.publishEvent(agent.EvStatus{Key: key, Text: text})
	return nil
}

func (x *extAPI) Notify(text, level string) error {
	if x.headless {
		fmt.Fprintf(x.errOut, "mixi: extension notice [%s]: %s\n", level, text)
		return nil
	}
	x.notifier.notice(text)
	return nil
}

// AskSelect auto-answers with the first option. The interactive select
// modal is a later UI concern; extensions always get a valid choice.
func (x *extAPI) AskSelect(_ context.Context, title string, options []string) (string, error) {
	x.log.Warn("ext: ask_select auto-answered with the first option", "title", title, "choice", options[0])
	if !x.headless {
		x.notifier.notice(fmt.Sprintf("Extension asked %q — auto-selected %q.", title, options[0]))
	}
	return options[0], nil
}

// extensionFilters adapts the host into the tool-call filter slot only when
// a host exists (a typed-nil *Host inside the interface would be "non-nil").
func extensionFilters(host *ext.Host) []agent.ToolCallFilter {
	if host == nil {
		return nil
	}
	return []agent.ToolCallFilter{host}
}
