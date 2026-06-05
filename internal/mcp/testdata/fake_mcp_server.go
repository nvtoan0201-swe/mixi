// fake_mcp_server is a scriptable MCP server used by the mcp package tests.
// It speaks JSONL on stdio and is driven by environment variables:
//
//	MIXI_FAKE_MCP_VERSION        protocol version to claim (default current)
//	MIXI_FAKE_MCP_EXIT_ON_START  "1" → exit(1) before reading anything
//
// Tools: echo (returns args), crash (exits without responding — simulates a
// kill -9 mid-call), sleep (stalls past any timeout).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func main() {
	if os.Getenv("MIXI_FAKE_MCP_EXIT_ON_START") == "1" {
		os.Exit(1)
	}
	version := os.Getenv("MIXI_FAKE_MCP_VERSION")
	if version == "" {
		version = "2025-06-18"
	}
	out := bufio.NewWriter(os.Stdout)
	reply := func(id int64, result any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 2<<20)
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil || req.ID == nil {
			continue // notification or noise
		}
		switch req.Method {
		case "initialize":
			reply(*req.ID, map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
				"serverInfo":      map[string]any{"name": "fake", "version": "0"},
			})
		case "tools/list":
			reply(*req.ID, map[string]any{"tools": []map[string]any{
				{"name": "echo", "description": "echo args back",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"msg": map[string]any{"type": "string"}}}},
				{"name": "crash", "description": "exit without responding",
					"inputSchema": map[string]any{"type": "object"}},
				{"name": "sleep", "description": "stall for ms",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"ms": map[string]any{"type": "number"}}}},
			}})
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			json.Unmarshal(req.Params, &p)
			switch p.Name {
			case "crash":
				os.Exit(7)
			case "sleep":
				var a struct{ Ms int }
				json.Unmarshal(p.Arguments, &a)
				time.Sleep(time.Duration(a.Ms) * time.Millisecond)
			}
			var a struct{ Msg string }
			json.Unmarshal(p.Arguments, &a)
			reply(*req.ID, map[string]any{"content": []map[string]any{
				{"type": "text", "text": fmt.Sprintf("echo: %s", a.Msg)},
			}})
		default:
			reply(*req.ID, map[string]any{})
		}
	}
}
