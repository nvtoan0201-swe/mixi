package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

// The nine built-in tool schemas the agent ships with.
var toolSchemas = map[string]string{
	"read":        `{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1}},"required":["path"]}`,
	"write":       `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`,
	"edit":        `{"type":"object","properties":{"path":{"type":"string"},"edits":{"type":"array","minItems":1,"items":{"type":"object","properties":{"oldText":{"type":"string","minLength":1},"newText":{"type":"string"}},"required":["oldText","newText"]}}},"required":["path","edits"]}`,
	"bash":        `{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"integer","minimum":1,"maximum":600},"background":{"type":"boolean"}},"required":["command"]}`,
	"bash_output": `{"type":"object","properties":{"job_id":{"type":"string"},"wait_ms":{"type":"integer","maximum":30000}},"required":["job_id"]}`,
	"kill_bash":   `{"type":"object","properties":{"job_id":{"type":"string"}},"required":["job_id"]}`,
	"grep":        `{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"glob":{"type":"string"},"ignoreCase":{"type":"boolean"},"literal":{"type":"boolean"},"context":{"type":"integer","maximum":10},"limit":{"type":"integer","default":100}},"required":["pattern"]}`,
	"find":        `{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"limit":{"type":"integer","default":1000}},"required":["pattern"]}`,
	"ls":          `{"type":"object","properties":{"path":{"type":"string","default":"."},"limit":{"type":"integer","default":500}},"required":[]}`,
}

var toolSampleArgs = map[string]string{
	"read":        `{"path":"main.go","offset":10,"limit":50}`,
	"write":       `{"path":"out.txt","content":"hello"}`,
	"edit":        `{"path":"main.go","edits":[{"oldText":"foo","newText":"bar"}]}`,
	"bash":        `{"command":"ls -la","timeout":30,"background":false}`,
	"bash_output": `{"job_id":"b1","wait_ms":500}`,
	"kill_bash":   `{"job_id":"b1"}`,
	"grep":        `{"pattern":"func main","path":".","ignoreCase":true,"context":2,"limit":50}`,
	"find":        `{"pattern":"*.go","path":"internal","limit":100}`,
	"ls":          `{"path":".","limit":20}`,
}

func TestBuiltinToolSchemasCompileAndValidate(t *testing.T) {
	for name, raw := range toolSchemas {
		t.Run(name, func(t *testing.T) {
			v, err := Compile(json.RawMessage(raw))
			if err != nil {
				t.Fatalf("Compile(%s): %v", name, err)
			}
			out, err := v.ValidateAndCoerce(name, json.RawMessage(toolSampleArgs[name]))
			if err != nil {
				t.Fatalf("ValidateAndCoerce(%s): %v", name, err)
			}
			if !json.Valid(out) {
				t.Fatalf("output not valid JSON: %s", out)
			}
		})
	}
}

func TestCompileCacheReturnsSameValidator(t *testing.T) {
	raw := json.RawMessage(toolSchemas["read"])
	v1, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := Compile(json.RawMessage(toolSchemas["read"]))
	if err != nil {
		t.Fatal(err)
	}
	if v1 != v2 {
		t.Fatal("identical schema bytes should hit the cache and share one *Validator")
	}
}

func TestCompileRejectsNonObjectRoot(t *testing.T) {
	for _, raw := range []string{
		`{"type":"string"}`,
		`{"properties":{"x":{"type":"string"}}}`, // no explicit type
		`[1,2,3]`,
		`true`,
		`{not json`,
	} {
		if _, err := Compile(json.RawMessage(raw)); err == nil {
			t.Errorf("Compile(%s): expected error, got nil", raw)
		}
	}
}

func TestValidateAndCoerceEmptyArgs(t *testing.T) {
	v, err := Compile(json.RawMessage(toolSchemas["ls"]))
	if err != nil {
		t.Fatal(err)
	}
	out, err := v.ValidateAndCoerce("ls", nil)
	if err != nil {
		t.Fatalf("empty args against no-required schema: %v", err)
	}
	if string(out) != "{}" {
		t.Fatalf("got %s, want {}", out)
	}
}

func TestValidateAndCoerceMalformedJSON(t *testing.T) {
	v, err := Compile(json.RawMessage(toolSchemas["read"]))
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.ValidateAndCoerce("read", json.RawMessage(`{"path": "x"`))
	if err == nil {
		t.Fatal("expected error for truncated JSON")
	}
	if !strings.Contains(err.Error(), `Validation failed for tool "read":`) ||
		!strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("unexpected error text:\n%s", err)
	}
}

func TestValidateAndCoerceConcurrent(t *testing.T) {
	v, err := Compile(json.RawMessage(toolSchemas["read"]))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				if _, err := v.ValidateAndCoerce("read", json.RawMessage(`{"path":"a","offset":"5"}`)); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
