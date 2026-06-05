package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildPermissionGateExt compiles the permission_gate sample extension once
// per test process; tests skip when no Go toolchain is available.
var permissionGateBin struct {
	once bool
	path string
}

func permissionGateExt(t *testing.T) string {
	t.Helper()
	if permissionGateBin.once {
		if permissionGateBin.path == "" {
			t.Skip("permission_gate extension unavailable (no Go toolchain)")
		}
		return permissionGateBin.path
	}
	permissionGateBin.once = true
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH — cannot build permission_gate extension")
	}
	dir, err := os.MkdirTemp("", "mixi-perm-gate")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "permission_gate")
	cmd := exec.Command(gobin, "build", "-o", bin, ".")
	cmd.Dir = filepath.Join("..", "..", "examples", "extensions", "permission_gate")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build permission_gate extension: %v\n%s", err, out)
	}
	permissionGateBin.path = bin
	return bin
}

// installExtension copies a built extension binary into the work dir's
// auto-discovery directory.
func installExtension(t *testing.T, work, bin string) {
	t.Helper()
	dir := filepath.Join(work, ".mixi", "extensions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.Base(bin)), raw, 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestE2EPermissionGateExtensionBlocksRmRf: the sample permission_gate
// extension, auto-discovered from .mixi/extensions/, vetoes a destructive
// bash call through the blocking tool_call gate — even in yolo mode, which
// only bypasses the built-in permission engine.
func TestE2EPermissionGateExtensionBlocksRmRf(t *testing.T) {
	work := t.TempDir()
	installExtension(t, work, permissionGateExt(t))
	marker := filepath.Join(work, "marker.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := fauxScript(t,
		toolCallTurn("call-1", "bash", map[string]any{"command": "rm -rf marker.txt"}),
		textTurn("done"),
	)
	code, stdout, stderr := runMixi(t, script, work, "",
		"-p", "delete marker.txt", "--model", "faux/scripted", "--no-save",
		"--permission-mode", "yolo", "--output", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "permission-gate") {
		t.Fatalf("tool result lacks the extension block reason:\n%s", stdout)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("rm -rf executed despite the extension block")
	}
}

// TestE2ENoExtensionsFlagSkipsDiscovery: --no-extensions leaves the gate
// unwired, so the same destructive call runs (yolo bypasses the engine).
func TestE2ENoExtensionsFlagSkipsDiscovery(t *testing.T) {
	work := t.TempDir()
	installExtension(t, work, permissionGateExt(t))
	marker := filepath.Join(work, "marker.txt")
	if err := os.WriteFile(marker, []byte("gone"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := fauxScript(t,
		toolCallTurn("call-1", "bash", map[string]any{"command": "rm -rf marker.txt"}),
		textTurn("done"),
	)
	code, _, stderr := runMixi(t, script, work, "",
		"-p", "delete marker.txt", "--model", "faux/scripted", "--no-save",
		"--permission-mode", "yolo", "--no-extensions")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("--no-extensions must not load extensions, rm should have run")
	}
}
