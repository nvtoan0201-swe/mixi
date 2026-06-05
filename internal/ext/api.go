package ext

import (
	"context"
	"encoding/json"
)

// API is the entire surface extension-originated actions can reach on the
// host, enumerated. The cmd wiring implements it against the agent,
// session store, and UI sink for the active mode.
type API interface {
	// SendUserMessage queues content for the agent. deliverAs selects the
	// queue: "steer" (inject before next turn), "followUp" (extend the run
	// when it would stop), or "nextTurn" (alias of followUp for idle
	// sessions).
	SendUserMessage(content string, deliverAs string) error
	// AppendEntry persists an extension-owned session entry; it never
	// enters LLM context.
	AppendEntry(customType string, data json.RawMessage) error
	// SetStatus sets a footer status segment keyed by the extension name.
	SetStatus(key, text string) error
	// Notify surfaces a user-visible notice (TUI toast / stderr headless).
	Notify(text, level string) error
	// AskSelect asks the user to pick one option; headless picks the first
	// option and WARNs.
	AskSelect(ctx context.Context, title string, options []string) (string, error)
}
