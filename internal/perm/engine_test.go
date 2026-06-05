package perm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
)

// scriptedAsker returns a fixed decision and records every request.
type scriptedAsker struct {
	decision AskDecision
	requests []AskRequest
}

func (s *scriptedAsker) Ask(_ context.Context, req AskRequest) (AskDecision, error) {
	s.requests = append(s.requests, req)
	return s.decision, nil
}

func newTestEngine(t *testing.T, cfg PolicyConfig, asker Asker, cwd string) *Engine {
	t.Helper()
	policy, err := NewPolicy(cfg)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return NewEngine(policy, asker, cwd)
}

func toolCall(name string, args map[string]any) ai.ToolCall {
	raw, _ := json.Marshal(args)
	return ai.ToolCall{ID: "t1", Name: name, Args: raw}
}

// callFor builds a representative in-cwd, non-secret call per category.
func callFor(cat Category, cwd string) ai.ToolCall {
	switch cat {
	case CatRead:
		return toolCall("read", map[string]any{"path": filepath.Join(cwd, "main.go")})
	case CatWrite:
		return toolCall("write", map[string]any{"path": filepath.Join(cwd, "out.txt"), "content": "x"})
	case CatExecute:
		return toolCall("bash", map[string]any{"command": "echo hi"})
	default:
		return toolCall("mcp__srv__tool", map[string]any{"q": 1})
	}
}

// TestModeCategoryDefaultMatrix verifies every mode × category default.
func TestModeCategoryDefaultMatrix(t *testing.T) {
	type outcome int
	const (
		oAllow outcome = iota // allowed without asking
		oAsk                  // asker consulted
		oDeny                 // denied without asking
	)
	matrix := map[Mode]map[Category]outcome{
		ModeYolo:     {CatRead: oAllow, CatWrite: oAllow, CatExecute: oAllow, CatMCP: oAllow},
		ModeAutoEdit: {CatRead: oAllow, CatWrite: oAllow, CatExecute: oAsk, CatMCP: oAsk},
		ModePrompt:   {CatRead: oAllow, CatWrite: oAsk, CatExecute: oAsk, CatMCP: oAsk},
		ModePlan:     {CatRead: oAllow, CatWrite: oDeny, CatExecute: oDeny, CatMCP: oDeny},
	}
	cwd := t.TempDir()
	for mode, byCat := range matrix {
		for cat, want := range byCat {
			t.Run(fmt.Sprintf("%s/%s", mode, cat), func(t *testing.T) {
				asker := &scriptedAsker{decision: AskDeny}
				e := newTestEngine(t, PolicyConfig{Mode: string(mode)}, asker, cwd)
				res := e.Decide(context.Background(), callFor(cat, cwd))
				asked := len(asker.requests) > 0
				switch want {
				case oAllow:
					if !res.Allow || asked {
						t.Fatalf("want plain allow, got allow=%v asked=%v (%s)", res.Allow, asked, res.Reason)
					}
				case oAsk:
					if !asked {
						t.Fatalf("want ask, asker never consulted (allow=%v %s)", res.Allow, res.Reason)
					}
					if res.Allow {
						t.Fatal("asker denied but call was allowed")
					}
				case oDeny:
					if res.Allow || asked {
						t.Fatalf("want plain deny, got allow=%v asked=%v", res.Allow, asked)
					}
					if !strings.Contains(res.Reason, string(mode)) {
						t.Fatalf("deny reason %q does not name the mode", res.Reason)
					}
				}
			})
		}
	}
}

func TestDenyBeatsAllow(t *testing.T) {
	cwd := t.TempDir()
	e := newTestEngine(t, PolicyConfig{
		Mode:  "yolo",
		Allow: []string{"bash(go*)"},
		Deny:  []string{"bash(go generate*)"},
	}, &scriptedAsker{}, cwd)
	if res := e.Decide(context.Background(), toolCall("bash", map[string]any{"command": "go generate ./..."})); res.Allow {
		t.Fatal("deny rule did not beat allow rule")
	}
	if res := e.Decide(context.Background(), toolCall("bash", map[string]any{"command": "go build ./..."})); !res.Allow {
		t.Fatalf("allow rule did not apply: %s", res.Reason)
	}
}

// Project deny merging happens in config (append-only); here we verify the
// engine end: a deny entry cannot be neutralized by any allow entry.
func TestProjectDenyCannotBeWeakenedByGlobalAllow(t *testing.T) {
	cwd := t.TempDir()
	e := newTestEngine(t, PolicyConfig{
		Mode:  "yolo",
		Allow: []string{"read(**/*)"},          // global allow-everything
		Deny:  []string{"read(**/secrets/**)"}, // project deny survives the merge
	}, &scriptedAsker{}, cwd)
	call := toolCall("read", map[string]any{"path": filepath.Join(cwd, "secrets", "key.pem")})
	if res := e.Decide(context.Background(), call); res.Allow {
		t.Fatal("broad allow weakened the deny rule")
	}
}

func TestFlagRulesOutrankConfigRules(t *testing.T) {
	cwd := t.TempDir()
	ctx := context.Background()
	// --allow beats a settings deny.
	e := newTestEngine(t, PolicyConfig{
		Mode:      "prompt",
		Deny:      []string{"bash(npm*)"},
		FlagAllow: []string{"bash(npm test*)"},
	}, &scriptedAsker{decision: AskDeny}, cwd)
	if res := e.Decide(ctx, toolCall("bash", map[string]any{"command": "npm test"})); !res.Allow {
		t.Fatalf("--allow did not outrank settings deny: %s", res.Reason)
	}
	// --deny beats a settings allow.
	e = newTestEngine(t, PolicyConfig{
		Mode:     "yolo",
		Allow:    []string{"bash(npm*)"},
		FlagDeny: []string{"bash(npm publish*)"},
	}, &scriptedAsker{}, cwd)
	if res := e.Decide(ctx, toolCall("bash", map[string]any{"command": "npm publish"})); res.Allow {
		t.Fatal("--deny did not outrank settings allow")
	}
}

func TestDenyPatternsScreenBash(t *testing.T) {
	cwd := t.TempDir()
	cfg := PolicyConfig{
		Mode:         "yolo",
		DenyPatterns: []string{`rm\s+(-rf?|--recursive)\s+/`},
	}
	// Yolo skips the dangerous-pattern screen (baselines are non-yolo).
	e := newTestEngine(t, cfg, &scriptedAsker{}, cwd)
	if res := e.Decide(context.Background(), toolCall("bash", map[string]any{"command": "rm -rf /"})); !res.Allow {
		t.Fatal("yolo mode should skip denyPatterns")
	}
	cfg.Mode = "prompt"
	cfg.Allow = []string{"bash(rm*)"}
	e = newTestEngine(t, cfg, &scriptedAsker{decision: AskAllow}, cwd)
	res := e.Decide(context.Background(), toolCall("bash", map[string]any{"command": "rm -rf /"}))
	if res.Allow {
		t.Fatal("dangerous pattern was not denied")
	}
	if !strings.Contains(res.Reason, "dangerous pattern") {
		t.Fatalf("reason %q does not explain the dangerous pattern", res.Reason)
	}
}

func TestWriteOutsideCwdForcesAsk(t *testing.T) {
	cwd := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.txt")
	call := toolCall("write", map[string]any{"path": outside, "content": "x"})
	ctx := context.Background()

	// Even an allow-everything rule cannot pre-approve the escape.
	asker := &scriptedAsker{decision: AskDeny}
	e := newTestEngine(t, PolicyConfig{Mode: "prompt", Allow: []string{"write(**/*)"}}, asker, cwd)
	if res := e.Decide(ctx, call); res.Allow {
		t.Fatal("outside-cwd write allowed without asking")
	}
	if len(asker.requests) != 1 {
		t.Fatalf("asker consulted %d times, want 1", len(asker.requests))
	}
	// Yolo skips the baseline.
	e = newTestEngine(t, PolicyConfig{Mode: "yolo"}, &scriptedAsker{}, cwd)
	if res := e.Decide(ctx, call); !res.Allow {
		t.Fatalf("yolo should skip the outside-cwd baseline: %s", res.Reason)
	}
}

func TestSymlinkEscapeForcesAsk(t *testing.T) {
	cwd := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(cwd, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	asker := &scriptedAsker{decision: AskDeny}
	e := newTestEngine(t, PolicyConfig{Mode: "auto-edit"}, asker, cwd)
	call := toolCall("write", map[string]any{"path": filepath.Join(link, "x.txt"), "content": "x"})
	if res := e.Decide(context.Background(), call); res.Allow {
		t.Fatal("write through an escaping symlink was allowed")
	}
	if len(asker.requests) != 1 {
		t.Fatal("symlink escape did not force an ask")
	}
}

func TestSecretReadForcesAsk(t *testing.T) {
	cwd := t.TempDir()
	ctx := context.Background()
	for _, name := range []string{".env", ".env.local", "id_rsa", "credentials.json"} {
		asker := &scriptedAsker{decision: AskDeny}
		e := newTestEngine(t, PolicyConfig{Mode: "prompt"}, asker, cwd)
		call := toolCall("read", map[string]any{"path": filepath.Join(cwd, name)})
		if res := e.Decide(ctx, call); res.Allow {
			t.Errorf("secret read %s allowed without asking", name)
		}
		if len(asker.requests) != 1 {
			t.Errorf("secret read %s did not ask (asks=%d)", name, len(asker.requests))
		}
	}
	// grep targeting a secret file is screened like read (it returns
	// contents); a plain read stays free.
	asker := &scriptedAsker{decision: AskDeny}
	e := newTestEngine(t, PolicyConfig{Mode: "prompt"}, asker, cwd)
	call := toolCall("grep", map[string]any{"pattern": ".", "path": filepath.Join(cwd, ".env")})
	if res := e.Decide(ctx, call); res.Allow || len(asker.requests) != 1 {
		t.Errorf("grep over a secret file was not forced to ask (allow=%v asks=%d)", res.Allow, len(asker.requests))
	}
	e = newTestEngine(t, PolicyConfig{Mode: "prompt"}, &scriptedAsker{decision: AskDeny}, cwd)
	if res := e.Decide(ctx, toolCall("read", map[string]any{"path": filepath.Join(cwd, "main.go")})); !res.Allow {
		t.Fatalf("plain read denied: %s", res.Reason)
	}
}

func TestAskAlwaysRecordsGrant(t *testing.T) {
	cwd := t.TempDir()
	ctx := context.Background()
	asker := &scriptedAsker{decision: AskAlways}
	e := newTestEngine(t, PolicyConfig{Mode: "prompt"}, asker, cwd)

	call := toolCall("bash", map[string]any{"command": "go test ./..."})
	if res := e.Decide(ctx, call); !res.Allow {
		t.Fatalf("always answer did not allow: %s", res.Reason)
	}
	grants := e.Grants()
	if len(grants) != 1 || grants[0] != "bash(go test*)" {
		t.Fatalf("grants = %v, want [bash(go test*)]", grants)
	}
	// Sibling invocation passes without consulting the asker again.
	sibling := toolCall("bash", map[string]any{"command": "go test -run TestX ./internal/..."})
	if res := e.Decide(ctx, sibling); !res.Allow {
		t.Fatalf("grant did not cover sibling: %s", res.Reason)
	}
	if len(asker.requests) != 1 {
		t.Fatalf("asker consulted %d times, want 1", len(asker.requests))
	}
	// A different command family still asks.
	other := toolCall("bash", map[string]any{"command": "go build ./..."})
	e.Decide(ctx, other)
	if len(asker.requests) != 2 {
		t.Fatal("grant leaked beyond its two-token prefix")
	}
}

func TestFileGrantGeneralizesToDirectory(t *testing.T) {
	cwd := t.TempDir()
	sub := filepath.Join(cwd, "out")
	info := CallInfo{Tool: "write", Category: CatWrite, Path: filepath.Join(sub, "a.txt"), Cwd: cwd}
	r := generalize(info)
	if r.raw != "write("+sub+"/**)" {
		t.Fatalf("generalize = %s", r.raw)
	}
	if !r.Matches(CallInfo{Tool: "write", Category: CatWrite, Path: filepath.Join(sub, "deep", "b.txt"), Cwd: cwd}) {
		t.Fatal("directory grant does not cover deeper files")
	}
	if r.Matches(CallInfo{Tool: "write", Category: CatWrite, Path: filepath.Join(cwd, "other.txt"), Cwd: cwd}) {
		t.Fatal("directory grant leaked to sibling directory")
	}
}

func TestHeadlessAsker(t *testing.T) {
	cwd := t.TempDir()
	ctx := context.Background()
	call := toolCall("bash", map[string]any{"command": "echo hi"})

	e := newTestEngine(t, PolicyConfig{Mode: "prompt"}, HeadlessAsker{}, cwd)
	res := e.Decide(ctx, call)
	if res.Allow {
		t.Fatal("headless prompt mode allowed an execute call")
	}
	for _, want := range []string{"Permission denied", "approval", "--permission-mode"} {
		if !strings.Contains(res.Reason, want) {
			t.Errorf("headless deny reason %q missing %q", res.Reason, want)
		}
	}
	// Explicit auto-edit loosens asks headlessly.
	e = newTestEngine(t, PolicyConfig{Mode: "auto-edit"}, HeadlessAsker{AllowAll: true}, cwd)
	if res := e.Decide(ctx, call); !res.Allow {
		t.Fatalf("explicit auto-edit headless still denied: %s", res.Reason)
	}
}

func TestMCPRules(t *testing.T) {
	cwd := t.TempDir()
	ctx := context.Background()
	e := newTestEngine(t, PolicyConfig{
		Mode:  "prompt",
		Allow: []string{"mcp__github__get_*"},
		Deny:  []string{"mcp__github__delete_repo"},
	}, &scriptedAsker{decision: AskDeny}, cwd)

	if res := e.Decide(ctx, toolCall("mcp__github__get_issue", nil)); !res.Allow {
		t.Fatalf("mcp glob allow failed: %s", res.Reason)
	}
	if res := e.Decide(ctx, toolCall("mcp__github__delete_repo", nil)); res.Allow {
		t.Fatal("mcp exact deny failed")
	}
	if res := e.Decide(ctx, toolCall("mcp__github__create_issue", nil)); res.Allow {
		t.Fatal("unmatched mcp call should fall to prompt-mode ask→deny")
	}
}
