// Command permission_gate is a sample mixi extension: it subscribes to the
// blocking tool_call gate and vetoes bash commands that contain a
// destructive `rm -rf`. It demonstrates the hello handshake, blocking
// event responses, and the notify action.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type inbound struct {
	ID      int64           `json:"id"`
	Type    string          `json:"type"`
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

type gatePayload struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

type bashArgs struct {
	Command string `json:"command"`
}

func send(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

func main() {
	send(map[string]any{
		"type": "hello", "name": "permission-gate", "version": "1.0.0",
		"protocol": 1, "subscribe": []string{"tool_call"},
	})

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var msg inbound
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Type == "shutdown" {
			return
		}
		if msg.Type != "event" || msg.Event != "tool_call" {
			continue
		}
		var p gatePayload
		json.Unmarshal(msg.Payload, &p)
		resp := map[string]any{"type": "event_response", "id": msg.ID}
		if p.Tool == "bash" {
			var args bashArgs
			json.Unmarshal(p.Args, &args)
			if strings.Contains(args.Command, "rm -rf") {
				resp["block"] = map[string]string{
					"reason": "destructive `rm -rf` commands are not allowed by the permission-gate extension",
				}
			}
		}
		send(resp)
	}
}
