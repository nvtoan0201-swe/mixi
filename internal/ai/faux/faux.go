// Package faux provides a scripted, always-compiled provider for end-to-end
// testing without network access. Model "faux/scripted" replays the turns
// described by the JSON file named in the MIXI_FAUX_SCRIPT environment
// variable; stream playback is delegated to agenttest so the wire behavior
// is identical to the loop's unit-test provider.
package faux

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/user/mixi-agent/internal/agent/agenttest"
	"github.com/user/mixi-agent/internal/ai"
)

// ScriptEnv names the environment variable holding the script file path.
const ScriptEnv = "MIXI_FAUX_SCRIPT"

// Script is the JSON document MIXI_FAUX_SCRIPT points at.
type Script struct {
	Turns []ScriptTurn `json:"turns"`
}

// ScriptTurn describes one provider stream in call order.
type ScriptTurn struct {
	// Text streamed as a single text block.
	Text string `json:"text,omitempty"`
	// ToolCalls streamed after the text; ends the turn with stopReason toolUse.
	ToolCalls []ScriptToolCall `json:"toolCalls,omitempty"`
	// Err terminates the stream with an error event carrying this message.
	Err string `json:"err,omitempty"`
	// WaitCtx blocks the stream until the run is aborted (SIGINT testing).
	WaitCtx bool `json:"waitCtx,omitempty"`
}

// ScriptToolCall scripts one tool invocation.
type ScriptToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Model returns the catalog entry for the scripted model. It is not in the
// ai built-in catalog: it exists only when asked for by name.
func Model() ai.Model {
	return ai.Model{
		API:           "faux",
		Provider:      "faux",
		ID:            "scripted",
		DisplayName:   "Faux Scripted",
		ContextWindow: 200_000,
		MaxOutput:     8_192,
	}
}

func init() { ai.Register(&provider{}) }

// provider loads the script on first use and replays it via agenttest.
type provider struct {
	once  sync.Once
	inner *agenttest.Provider
	err   error
}

func (p *provider) API() string { return "faux" }

func (p *provider) Stream(ctx context.Context, model ai.Model, c ai.Context, opts ai.StreamOptions) <-chan ai.StreamEvent {
	p.once.Do(p.load)
	if p.err != nil {
		return errorStream(model, p.err)
	}
	return p.inner.Stream(ctx, model, c, opts)
}

// load parses the script file once per process; the call counter inside the
// agenttest provider then advances across the whole run.
func (p *provider) load() {
	path := os.Getenv(ScriptEnv)
	if path == "" {
		p.err = fmt.Errorf("faux: model faux/scripted needs %s to point at a script file", ScriptEnv)
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		p.err = fmt.Errorf("faux: read script: %w", err)
		return
	}
	var s Script
	if err := json.Unmarshal(raw, &s); err != nil {
		p.err = fmt.Errorf("faux: parse script %s: %w", path, err)
		return
	}
	turns := make([]agenttest.Turn, len(s.Turns))
	for i, t := range s.Turns {
		turns[i] = agenttest.Turn{Text: t.Text, Err: t.Err, WaitCtx: t.WaitCtx}
		for _, tc := range t.ToolCalls {
			args := string(tc.Args)
			turns[i].ToolCalls = append(turns[i].ToolCalls, agenttest.ToolCallSpec{
				ID: tc.ID, Name: tc.Name, Args: args,
			})
		}
	}
	p.inner = agenttest.New(turns...)
}

// errorStream emits a single terminal error event, matching the provider
// contract so script problems surface as a failed turn, not a hang.
func errorStream(model ai.Model, err error) <-chan ai.StreamEvent {
	ch := make(chan ai.StreamEvent, 1)
	ch <- ai.EventError{Reason: ai.StopReasonError, Message: ai.AssistantMessage{
		API: model.API, Provider: model.Provider, Model: model.ID,
		StopReason: ai.StopReasonError, ErrorMessage: err.Error(),
	}}
	close(ch)
	return ch
}
