// Command fake_extension is a scriptable extension used by the host tests.
// FAKE_EXT_MODE selects the behavior:
//
//	echo      subscribe "*", append every received line to FAKE_EXT_LOG
//	slow      subscribe tool_call, never answer blocking events
//	blocker   subscribe tool_call, block tool FAKE_EXT_BLOCK_TOOL
//	mutator   subscribe tool_call, answer with mutate args FAKE_EXT_MUTATE
//	tool      register tool FAKE_EXT_TOOL (default fake_tool), echo args back
//	crash     exit(1) on the first event after ready
//	exit      exit(1) immediately (before hello)
//	no-hello  read forever without sending hello
//	bad-proto hello with protocol 99
//	garbage   after ready, write a single line beyond the 1MiB cap
//	malformed after ready, write 11 non-JSON lines
//	actions   after ready, fire one of each action; log responses
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

var logFile *os.File

func logLine(s string) {
	if logFile != nil {
		fmt.Fprintln(logFile, s)
		logFile.Sync()
	}
}

func send(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

type inbound struct {
	ID      int64           `json:"id"`
	Type    string          `json:"type"`
	Event   string          `json:"event"`
	Tool    string          `json:"tool"`
	Args    json.RawMessage `json:"args"`
	Action  string          `json:"action"`
	Error   string          `json:"error"`
	Payload json.RawMessage `json:"payload"`
}

func main() {
	mode := os.Getenv("FAKE_EXT_MODE")
	if path := os.Getenv("FAKE_EXT_LOG"); path != "" {
		logFile, _ = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	}
	if mode == "exit" {
		os.Exit(1)
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)

	if mode == "no-hello" {
		for sc.Scan() {
		}
		return
	}

	hello := map[string]any{
		"type": "hello", "name": "fake", "version": "0.0.1", "protocol": 1,
	}
	switch mode {
	case "echo", "actions":
		hello["subscribe"] = []string{"*"}
	case "slow", "blocker", "mutator", "crash":
		hello["subscribe"] = []string{"tool_call", "session_start", "session_shutdown"}
	case "tool":
		name := os.Getenv("FAKE_EXT_TOOL")
		if name == "" {
			name = "fake_tool"
		}
		hello["tools"] = []map[string]any{{
			"name": name, "description": "echoes its arguments",
			"schema": map[string]any{"type": "object"},
		}}
		hello["commands"] = []map[string]string{{"name": "fakecmd", "description": "a fake command"}}
		hello["subscribe"] = []string{"user_input"}
	case "bad-proto":
		hello["protocol"] = 99
	}
	send(hello)

	postReady := func() {
		switch mode {
		case "garbage":
			fmt.Println(strings.Repeat("x", 2<<20)) // 2MiB, past the cap
		case "malformed":
			for i := 0; i < 11; i++ {
				fmt.Println("not json at all", i)
			}
		case "actions":
			send(map[string]any{"type": "action", "id": 1, "action": "set_status",
				"params": map[string]string{"text": "hi"}})
			send(map[string]any{"type": "action", "id": 2, "action": "notify",
				"params": map[string]string{"text": "note", "level": "info"}})
			send(map[string]any{"type": "action", "id": 3, "action": "send_message",
				"params": map[string]string{"content": "steer me", "deliverAs": "steer"}})
			send(map[string]any{"type": "action", "id": 4, "action": "append_entry",
				"params": map[string]any{"customType": "fake.note", "data": map[string]int{"n": 1}}})
			send(map[string]any{"type": "action", "id": 5, "action": "ask_select",
				"params": map[string]any{"title": "pick", "options": []string{"a", "b"}}})
			send(map[string]any{"type": "action", "id": 6, "action": "register_tool",
				"params": map[string]any{}})
		}
	}

	for sc.Scan() {
		line := sc.Text()
		logLine(line)
		var msg inbound
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "ready":
			postReady()
		case "shutdown":
			return
		case "tool_call":
			send(map[string]any{"type": "tool_result", "id": msg.ID, "content": []map[string]string{
				{"type": "text", "text": "echo:" + string(msg.Args)}}})
		case "event":
			if mode == "crash" {
				os.Exit(1)
			}
			if msg.Event != "tool_call" {
				continue
			}
			switch mode {
			case "slow":
				// never answer: the host must time out
			case "blocker":
				resp := map[string]any{"type": "event_response", "id": msg.ID}
				var p struct {
					Tool string `json:"tool"`
				}
				json.Unmarshal(msg.Payload, &p)
				if p.Tool == os.Getenv("FAKE_EXT_BLOCK_TOOL") {
					resp["block"] = map[string]string{"reason": "blocked by fake"}
				}
				send(resp)
			case "mutator":
				send(map[string]any{"type": "event_response", "id": msg.ID,
					"mutate": map[string]any{"args": json.RawMessage(os.Getenv("FAKE_EXT_MUTATE"))}})
			default:
				send(map[string]any{"type": "event_response", "id": msg.ID})
			}
		}
	}
}
