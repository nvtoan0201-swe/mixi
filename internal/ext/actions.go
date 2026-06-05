package ext

import (
	"context"
	"encoding/json"
	"fmt"
)

// Action parameter shapes (the wire side of the API interface).
type sendMessageParams struct {
	Content   string `json:"content"`
	DeliverAs string `json:"deliverAs"` // steer | followUp | nextTurn
}

type appendEntryParams struct {
	CustomType string          `json:"customType"`
	Data       json.RawMessage `json:"data"`
}

type setStatusParams struct {
	Text string `json:"text"`
}

type notifyParams struct {
	Text  string `json:"text"`
	Level string `json:"level"`
}

type askSelectParams struct {
	Title   string   `json:"title"`
	Options []string `json:"options"`
}

type registerCommandParams struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// dispatchAction executes one extension action against the host API and
// answers with an action_response carrying the result or the error.
func (h *Host) dispatchAction(e *extension, msg ActionMsg) {
	result, err := h.runAction(e, msg)
	resp := ActionResponse{Type: TypeActionResponse, ID: msg.ID}
	if err != nil {
		resp.Error = err.Error()
		h.log.Warn("ext: action failed", "ext", e.name, "action", msg.Action, "err", err)
	} else {
		resp.Result = result
	}
	e.sendDirect(resp)
}

// runAction maps one wire action onto the API surface.
func (h *Host) runAction(e *extension, msg ActionMsg) (json.RawMessage, error) {
	h.bindMu.Lock()
	api := h.api
	h.bindMu.Unlock()
	if api == nil {
		return nil, fmt.Errorf("host is not bound yet")
	}
	switch msg.Action {
	case "send_message":
		var p sendMessageParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil, fmt.Errorf("send_message params: %w", err)
		}
		switch p.DeliverAs {
		case "steer", "followUp", "nextTurn":
		default:
			return nil, fmt.Errorf("send_message: unknown deliverAs %q", p.DeliverAs)
		}
		return nil, api.SendUserMessage(p.Content, p.DeliverAs)
	case "append_entry":
		var p appendEntryParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil, fmt.Errorf("append_entry params: %w", err)
		}
		if p.CustomType == "" {
			return nil, fmt.Errorf("append_entry: missing customType")
		}
		return nil, api.AppendEntry(p.CustomType, p.Data)
	case "set_status":
		var p setStatusParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil, fmt.Errorf("set_status params: %w", err)
		}
		// The segment key is the extension name: one segment per extension,
		// no cross-extension clobbering.
		return nil, api.SetStatus(e.name, p.Text)
	case "notify":
		var p notifyParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil, fmt.Errorf("notify params: %w", err)
		}
		return nil, api.Notify(p.Text, p.Level)
	case "ask_select":
		var p askSelectParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil, fmt.Errorf("ask_select params: %w", err)
		}
		if len(p.Options) == 0 {
			return nil, fmt.Errorf("ask_select: no options")
		}
		choice, err := api.AskSelect(context.Background(), p.Title, p.Options)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"choice": choice})
	case "register_command":
		var p registerCommandParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil, fmt.Errorf("register_command params: %w", err)
		}
		if !toolNameOK.MatchString(p.Name) {
			return nil, fmt.Errorf("register_command: invalid name %q", p.Name)
		}
		e.mu.Lock()
		e.commands = append(e.commands, CommandDef{Name: p.Name, Description: p.Description})
		e.mu.Unlock()
		return nil, nil
	case "register_tool":
		// Tools are part of the LLM-visible contract fixed at hello; late
		// registration would churn the tool list mid-run.
		return nil, fmt.Errorf("register_tool is hello-only")
	}
	return nil, fmt.Errorf("unknown action %q", msg.Action)
}
