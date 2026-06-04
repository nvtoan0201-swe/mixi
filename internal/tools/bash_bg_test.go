//go:build unix

package tools

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// bgTools wires bash + bash_output + kill_bash around one job table.
func bgTools(t *testing.T) (*bashTool, *bashOutputTool, *killBashTool) {
	t.Helper()
	jobs := NewJobTable()
	t.Cleanup(jobs.KillAll)
	cwd := t.TempDir()
	return &bashTool{cwd: cwd, jobs: jobs}, &bashOutputTool{jobs: jobs}, &killBashTool{jobs: jobs}
}

func TestBackgroundJobLifecycle(t *testing.T) {
	bash, bgOut, _ := bgTools(t)
	res := execTool(t, bash, `{"command":"echo one; sleep 0.3; echo two","background":true}`)
	msg := resultText(t, res)
	if !strings.HasPrefix(msg, "started background job b1 (pid ") {
		t.Fatalf("msg=%q", msg)
	}

	out := resultText(t, execTool(t, bgOut, `{"job_id":"b1","wait_ms":2000}`))
	if !strings.Contains(out, "one") {
		t.Fatalf("first read missing early output: %q", out)
	}

	// Poll until exit; cursor semantics mean "two" appears exactly once.
	deadline := time.Now().Add(5 * time.Second)
	var sawTwo, exited bool
	for time.Now().Before(deadline) && !exited {
		out = resultText(t, execTool(t, bgOut, `{"job_id":"b1","wait_ms":1000}`))
		if strings.Count(out, "two") > 1 {
			t.Fatalf("delta re-delivered old output: %q", out)
		}
		if strings.Contains(out, "two") {
			if sawTwo {
				t.Fatalf("cursor did not advance; got 'two' twice")
			}
			sawTwo = true
		}
		exited = strings.Contains(out, "(exited with code 0)")
	}
	if !sawTwo || !exited {
		t.Fatalf("sawTwo=%v exited=%v last=%q", sawTwo, exited, out)
	}
}

func TestBashOutputUnknownJob(t *testing.T) {
	bash, bgOut, _ := bgTools(t)
	execTool(t, bash, `{"command":"sleep 5","background":true}`)
	res := execTool(t, bgOut, `{"job_id":"b9"}`)
	out := resultText(t, res)
	if !res.IsError || !strings.Contains(out, `no background job "b9"`) || !strings.Contains(out, "b1") {
		t.Fatalf("out=%q", out)
	}
}

func TestKillBashTerminatesJob(t *testing.T) {
	bash, bgOut, kill := bgTools(t)
	res := execTool(t, bash, `{"command":"echo started; sleep 60","background":true}`)
	if !strings.Contains(resultText(t, res), "b1") {
		t.Fatalf("res=%q", resultText(t, res))
	}
	// Let the shell actually emit its output before killing, otherwise the
	// group dies before echo runs and "final output" is legitimately empty.
	if out := resultText(t, execTool(t, bgOut, `{"job_id":"b1","wait_ms":3000}`)); !strings.Contains(out, "started") {
		t.Fatalf("job produced no output before kill: %q", out)
	}
	start := time.Now()
	out := resultText(t, execTool(t, kill, `{"job_id":"b1"}`))
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("kill took %v", d)
	}
	if !strings.Contains(out, "job b1 killed") || !strings.Contains(out, "started") {
		t.Fatalf("out=%q", out)
	}
	// Job removed: second kill reports unknown id.
	res = execTool(t, kill, `{"job_id":"b1"}`)
	if !res.IsError {
		t.Fatal("killed job should be gone from the table")
	}
}

func TestKillBashAlreadyExited(t *testing.T) {
	bash, bgOut, kill := bgTools(t)
	execTool(t, bash, `{"command":"echo bye","background":true}`)
	// Wait for natural exit.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(resultText(t, execTool(t, bgOut, `{"job_id":"b1","wait_ms":500}`)), "exited") {
			break
		}
	}
	out := resultText(t, execTool(t, kill, `{"job_id":"b1"}`))
	if !strings.Contains(out, "had already exited with code 0") {
		t.Fatalf("out=%q", out)
	}
}

func TestJobIDsIncrement(t *testing.T) {
	bash, _, kill := bgTools(t)
	for i := 1; i <= 3; i++ {
		res := execTool(t, bash, `{"command":"sleep 30","background":true}`)
		want := fmt.Sprintf("b%d", i)
		if !strings.Contains(resultText(t, res), want) {
			t.Fatalf("job %d: %q", i, resultText(t, res))
		}
	}
	for i := 1; i <= 3; i++ {
		execTool(t, kill, fmt.Sprintf(`{"job_id":"b%d"}`, i))
	}
}
