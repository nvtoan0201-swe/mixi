package tools

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type stubTool struct {
	name string
	mode ExecMode
}

func (s stubTool) Name() string            { return s.name }
func (s stubTool) Description() string     { return "stub" }
func (s stubTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s stubTool) Mode() ExecMode          { return s.mode }
func (s stubTool) Execute(context.Context, json.RawMessage, chan<- ToolUpdate) (ToolResult, error) {
	return Text("ok"), nil
}

func TestRegistryRejectsDuplicateNames(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(stubTool{name: "read"}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register(stubTool{name: "read"}); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestRegistryOrderAndDefs(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"read", "write", "bash"} {
		if err := r.Register(stubTool{name: n}); err != nil {
			t.Fatal(err)
		}
	}
	defs := r.Defs()
	if len(defs) != 3 || defs[0].Name != "read" || defs[2].Name != "bash" {
		t.Fatalf("defs order wrong: %+v", defs)
	}
	if _, ok := r.Get("write"); !ok {
		t.Fatal("Get(write) missed")
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("Get(nope) should miss")
	}
}
