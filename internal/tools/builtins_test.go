package tools

import (
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/schema"
)

func registerAll(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	jobs, err := RegisterBuiltins(r, Options{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(jobs.KillAll)
	return r
}

func TestRegisterBuiltinsAllNinePresent(t *testing.T) {
	r := registerAll(t)
	want := []string{"read", "write", "edit", "bash", "bash_output", "kill_bash", "grep", "find", "ls"}
	if got := len(r.All()); got != len(want) {
		t.Fatalf("registered %d tools, want %d", got, len(want))
	}
	for _, name := range want {
		if _, ok := r.Get(name); !ok {
			t.Errorf("tool %q not registered", name)
		}
	}
}

// Every schema literal must compile under the project validator and
// accept/reject representative arguments.
func TestAllToolSchemasCompileAndValidate(t *testing.T) {
	r := registerAll(t)
	valid := map[string]string{
		"read":        `{"path":"f.txt","offset":1,"limit":10}`,
		"write":       `{"path":"f.txt","content":""}`,
		"edit":        `{"path":"f.txt","edits":[{"oldText":"a","newText":"b"}]}`,
		"bash":        `{"command":"ls","timeout":30,"background":false}`,
		"bash_output": `{"job_id":"b1","wait_ms":500}`,
		"kill_bash":   `{"job_id":"b1"}`,
		"grep":        `{"pattern":"x","ignoreCase":true,"context":2,"limit":5}`,
		"find":        `{"pattern":"*.go","path":"src","limit":10}`,
		"ls":          `{"path":".","limit":5}`,
	}
	invalid := map[string]string{
		"read":        `{"offset":1}`,                    // missing path
		"write":       `{"path":"f.txt"}`,                // missing content
		"edit":        `{"path":"f.txt","edits":[]}`,     // minItems 1
		"bash":        `{"command":"ls","timeout":9999}`, // > max 600
		"bash_output": `{"wait_ms":100}`,                 // missing job_id
		"kill_bash":   `{}`,                              // missing job_id
		"grep":        `{"pattern":"x","context":99}`,    // > max 10
		"find":        `{}`,                              // missing pattern
		"ls":          `{"limit":0}`,                     // < minimum 1
	}
	for _, tool := range r.All() {
		name := tool.Name()
		v, err := schema.Compile(tool.Schema())
		if err != nil {
			t.Errorf("%s: schema does not compile: %v", name, err)
			continue
		}
		if _, err := v.ValidateAndCoerce(name, []byte(valid[name])); err != nil {
			t.Errorf("%s: valid args rejected: %v", name, err)
		}
		if _, err := v.ValidateAndCoerce(name, []byte(invalid[name])); err == nil {
			t.Errorf("%s: invalid args %s accepted", name, invalid[name])
		}
	}
}

func TestToolDescriptionsNonEmpty(t *testing.T) {
	for _, tool := range registerAll(t).All() {
		if strings.TrimSpace(tool.Description()) == "" {
			t.Errorf("%s: empty description", tool.Name())
		}
	}
}
