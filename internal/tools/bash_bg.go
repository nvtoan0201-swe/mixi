package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type bashOutputTool struct{ jobs *JobTable }

func (t *bashOutputTool) Name() string { return "bash_output" }

func (t *bashOutputTool) Description() string {
	return "Read new output from a background bash job since the last read."
}

func (t *bashOutputTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"job_id":{"type":"string","description":"Job id returned by bash with background=true"},
"wait_ms":{"type":"integer","maximum":30000,"description":"Wait up to this long for new output"}
},"required":["job_id"]}`)
}

func (t *bashOutputTool) Mode() ExecMode { return ExecParallel }

func (t *bashOutputTool) Execute(ctx context.Context, args json.RawMessage, _ chan<- ToolUpdate) (ToolResult, error) {
	var a struct {
		JobID  string `json:"job_id"`
		WaitMS int    `json:"wait_ms"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{}, fmt.Errorf("bash_output: bad arguments: %w", err)
	}
	job, ok := t.jobs.get(a.JobID)
	if !ok {
		return Errorf("no background job %q; live jobs: %s", a.JobID, t.jobs.liveIDs()), nil
	}
	if a.WaitMS > 0 {
		t.waitForOutput(ctx, job, time.Duration(a.WaitMS)*time.Millisecond)
	}

	t.jobs.mu.Lock()
	delta, cursor, err := job.acc.readFrom(job.cursor)
	if err == nil {
		job.cursor = cursor
	}
	t.jobs.mu.Unlock()
	if err != nil {
		return ToolResult{}, fmt.Errorf("bash_output %s: %w", a.JobID, err)
	}

	out, truncated := tailTruncate(delta, MaxLines, MaxBytes)
	if out == "" {
		out = "(no new output)"
	}
	if truncated {
		out = "[earlier output truncated]\n" + out
	}
	if job.finished() {
		out += fmt.Sprintf("\n(exited with code %d)", job.exitCode)
	} else {
		out += "\n(running)"
	}
	return Text(out), nil
}

// waitForOutput blocks until the job produces bytes past its cursor, exits,
// or the wait budget / context runs out.
func (t *bashOutputTool) waitForOutput(ctx context.Context, job *Job, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) && !job.finished() {
		// cursor is guarded by the table mutex; concurrent bash_output
		// calls on one job would otherwise race this read.
		t.jobs.mu.Lock()
		cursor := job.cursor
		t.jobs.mu.Unlock()
		if job.acc.snapshot().Total > cursor {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-job.done:
			return
		case <-time.After(25 * time.Millisecond):
		}
	}
}

type killBashTool struct{ jobs *JobTable }

func (t *killBashTool) Name() string { return "kill_bash" }

func (t *killBashTool) Description() string {
	return "Terminate a background bash job (SIGTERM, then SIGKILL after 2s) and return its final output."
}

func (t *killBashTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"job_id":{"type":"string","description":"Job id to terminate"}
},"required":["job_id"]}`)
}

func (t *killBashTool) Mode() ExecMode { return ExecParallel }

func (t *killBashTool) Execute(ctx context.Context, args json.RawMessage, _ chan<- ToolUpdate) (ToolResult, error) {
	var a struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{}, fmt.Errorf("kill_bash: bad arguments: %w", err)
	}
	job, ok := t.jobs.get(a.JobID)
	if !ok {
		return Errorf("no background job %q; live jobs: %s", a.JobID, t.jobs.liveIDs()), nil
	}
	already := job.finished()
	if !already {
		terminateGroup(job.cmd, job.done)
		<-job.done
	}
	tail, _ := tailTruncate(job.acc.snapshot().Tail, MaxLines, MaxBytes)
	t.jobs.remove(a.JobID)

	verb := "killed"
	if already {
		verb = fmt.Sprintf("had already exited with code %d", job.exitCode)
	}
	if tail == "" {
		tail = "(no output)"
	}
	return Text(fmt.Sprintf("job %s %s; final output:\n%s", a.JobID, verb, tail)), nil
}
