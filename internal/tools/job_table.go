package tools

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

// JobTable tracks background bash jobs (ids b1, b2, ...). Shared by bash,
// bash_output and kill_bash; the agent calls KillAll on shutdown so no
// orphan process groups survive the session.
type JobTable struct {
	mu   sync.Mutex
	jobs map[string]*Job
	next int
}

// Job is one background command. exitCode is valid once done is closed.
type Job struct {
	ID       string
	cmd      *exec.Cmd
	acc      *accumulator
	done     chan struct{}
	exitCode int
	cursor   int64 // bash_output read position, guarded by table mu
}

func NewJobTable() *JobTable {
	return &JobTable{jobs: map[string]*Job{}}
}

// add registers a started command and watches it to record the exit code.
// pipes must complete before cmd.Wait (stdout/stderr pipe contract).
func (jt *JobTable) add(cmd *exec.Cmd, acc *accumulator, pipes *sync.WaitGroup) *Job {
	jt.mu.Lock()
	jt.next++
	job := &Job{ID: fmt.Sprintf("b%d", jt.next), cmd: cmd, acc: acc, done: make(chan struct{})}
	jt.jobs[job.ID] = job
	jt.mu.Unlock()
	go func() {
		pipes.Wait()
		job.exitCode = commandExitCode(cmd.Wait())
		close(job.done)
	}()
	return job
}

func (jt *JobTable) get(id string) (*Job, bool) {
	jt.mu.Lock()
	defer jt.mu.Unlock()
	j, ok := jt.jobs[id]
	return j, ok
}

func (jt *JobTable) remove(id string) {
	jt.mu.Lock()
	defer jt.mu.Unlock()
	if j, ok := jt.jobs[id]; ok {
		j.acc.close()
		delete(jt.jobs, id)
	}
}

// liveIDs lists registered job ids, sorted, for unknown-id error messages.
func (jt *JobTable) liveIDs() string {
	jt.mu.Lock()
	defer jt.mu.Unlock()
	ids := make([]string, 0, len(jt.jobs))
	for id := range jt.jobs {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return "none"
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

// KillAll terminates every job's process group; called on agent shutdown.
func (jt *JobTable) KillAll() {
	jt.mu.Lock()
	jobs := make([]*Job, 0, len(jt.jobs))
	for _, j := range jt.jobs {
		jobs = append(jobs, j)
	}
	jt.jobs = map[string]*Job{}
	jt.mu.Unlock()
	for _, j := range jobs {
		terminateGroup(j.cmd, j.done)
		<-j.done
		j.acc.close()
	}
}

// finished reports whether the job has exited (non-blocking).
func (j *Job) finished() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}
