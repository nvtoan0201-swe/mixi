package ext

import (
	"encoding/json"
	"fmt"
	"time"
)

// supervise owns one extension's lifecycle: spawn → handshake → serve →
// crash → backoff restart, with the strike policy ending in Disabled.
func (h *Host) supervise(e *extension) {
	defer h.wg.Done()
	defer e.settle()
	crashes := 0
	for {
		if h.ctx.Err() != nil {
			h.stopProc(e, "session closed")
			return
		}
		start := time.Now()
		err := h.runOnce(e)
		h.stopProc(e, "restarting")
		e.failPending()
		if h.ctx.Err() != nil || e.getState() == StateDisabled {
			return
		}
		if time.Since(start) >= h.stableAfter {
			crashes = 0
		}
		crashes++
		e.settle() // first failure also resolves WaitInitial
		if crashes > len(h.backoff) {
			// Notice and cleanup land before the state flips so anyone
			// observing Disabled always finds the explanation already there.
			h.log.Error("ext: extension disabled after repeated crashes", "ext", e.name, "err", err)
			h.notify(fmt.Sprintf("Extension %q crashed %d times and was disabled.", e.name, crashes))
			h.clearStatus(e)
			e.setState(StateDisabled)
			return
		}
		e.setState(StateCrashed)
		h.log.Warn("ext: extension crashed, restarting", "ext", e.name, "attempt", crashes, "err", err)
		select {
		case <-time.After(h.backoff[crashes-1]):
		case <-h.ctx.Done():
			return
		}
	}
}

// runOnce runs one process incarnation to completion: handshake, then the
// read loop until the process dies or the extension is disabled.
func (h *Host) runOnce(e *extension) error {
	p, err := spawn(e.name, e.cfg, h.log, h.blockTimeout)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.proc = p
	e.state = StateStarting
	e.blockStrikes = 0
	e.mu.Unlock()

	hello, err := h.awaitHello(e, p)
	if err != nil {
		return err
	}
	// Tool conflicts resolve to the first-registered extension; handshakes
	// run in parallel, so adoption waits for every earlier-ordered
	// extension to settle, making "first" deterministic (sorted names).
	if e.firstHello() {
		h.waitEarlier(e)
	}
	if err := h.adopt(e, hello); err != nil {
		return err
	}
	p.send(Ready{Type: TypeReady, SessionID: h.ready.SessionID,
		Cwd: h.ready.Cwd, Mode: h.ready.Mode, Model: h.ready.Model})
	e.setState(StateReady)
	e.settle()

	// A restart mid-session re-announces session_start so the extension
	// can rebuild state it derived from it (status segments, …).
	h.bindMu.Lock()
	bound := h.api != nil
	h.bindMu.Unlock()
	if bound && e.subscribed(EvSessionStart) {
		e.enqueue(EventMsg{Type: TypeEvent, Event: EvSessionStart, Payload: []byte("{}")})
	}

	stopDrain := make(chan struct{})
	defer close(stopDrain)
	go h.drainQueue(e, p, stopDrain)
	// The read loop only ends when the process dies, so host shutdown must
	// kill the process to unblock it (kill is once-guarded — racing the
	// supervisor's own stopProc is fine).
	go func() {
		select {
		case <-h.ctx.Done():
			p.kill("session closed")
		case <-stopDrain:
		}
	}()
	return h.readLoop(e, p)
}

// awaitHello reads until the hello message or the handshake deadline.
func (h *Host) awaitHello(e *extension, p *proc) (*Hello, error) {
	deadline := time.NewTimer(h.helloWait)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			return nil, fmt.Errorf("ext %s: no hello within %s", e.name, h.helloWait)
		case <-h.ctx.Done():
			return nil, h.ctx.Err()
		case line, ok := <-p.lines:
			if !ok {
				return nil, fmt.Errorf("ext %s: exited before hello", e.name)
			}
			if line.err != nil {
				return nil, fmt.Errorf("ext %s: before hello: %w", e.name, line.err)
			}
			env, err := decodeEnvelope(line.msg)
			if err != nil || env.Type != TypeHello {
				h.log.Warn("ext: unexpected message before hello, ignoring", "ext", e.name, "err", err)
				continue
			}
			var hello Hello
			if err := json.Unmarshal(line.msg, &hello); err != nil {
				return nil, fmt.Errorf("ext %s: bad hello: %w", e.name, err)
			}
			if hello.Protocol != ProtocolVersion {
				return nil, fmt.Errorf("ext %s: protocol %d unsupported (host speaks %d)", e.name, hello.Protocol, ProtocolVersion)
			}
			return &hello, nil
		}
	}
}

// readLoop routes inbound messages until the process dies. Disable
// conditions (line cap, malformed flood) return a terminal error after
// marking the extension Disabled so supervise stops.
func (h *Host) readLoop(e *extension, p *proc) error {
	for line := range p.lines {
		if line.err != nil {
			if isLineTooLong(line.err) {
				h.disable(e, "wrote a line beyond the 1MiB cap")
				return line.err
			}
			return line.err // EOF / pipe error: crash path
		}
		env, err := decodeEnvelope(line.msg)
		if err != nil {
			if e.strikeMalformed(time.Now()) {
				h.disable(e, "flooded the host with malformed messages")
				return fmt.Errorf("ext %s: malformed flood", e.name)
			}
			h.log.Warn("ext: dropping malformed message", "ext", e.name, "err", err)
			continue
		}
		h.route(e, env, line.msg)
	}
	return fmt.Errorf("ext %s: stdout closed", e.name)
}

// route handles one decoded inbound message.
func (h *Host) route(e *extension, env envelope, raw []byte) {
	switch env.Type {
	case TypeEventResponse:
		var r EventResponse
		if err := json.Unmarshal(raw, &r); err == nil {
			e.resolve(env.ID, reply{eventResp: &r})
		}
	case TypeToolResult:
		var r ToolResultMsg
		if err := json.Unmarshal(raw, &r); err == nil {
			e.resolve(env.ID, reply{toolRes: &r})
		}
	case TypeAction:
		var a ActionMsg
		if err := json.Unmarshal(raw, &a); err != nil {
			return
		}
		h.bindMu.Lock()
		if h.api == nil {
			if len(h.actBuf) < 64 {
				h.actBuf = append(h.actBuf, bufferedAction{ext: e, msg: a})
			} else {
				h.log.Warn("ext: dropping pre-bind action, buffer full", "ext", e.name, "action", a.Action)
			}
			h.bindMu.Unlock()
			return
		}
		h.bindMu.Unlock()
		h.dispatchAction(e, a)
	default:
		h.log.Warn("ext: unknown message type, ignoring", "ext", e.name, "type", env.Type)
	}
}

// drainQueue writes queued non-blocking events to the process until it dies.
func (h *Host) drainQueue(e *extension, p *proc, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-h.ctx.Done():
			return
		case ev := <-e.queue:
			if err := p.send(ev); err != nil {
				return // readLoop observes the death; just stop writing
			}
		}
	}
}

// waitEarlier blocks until every extension ordered before e resolved its
// first handshake (adopted its tools or failed).
func (h *Host) waitEarlier(e *extension) {
	for _, name := range h.order {
		if name == e.name {
			return
		}
		select {
		case <-h.exts[name].settled:
		case <-h.ctx.Done():
			return
		}
	}
}

// stopProc kills the current process incarnation, if any.
func (h *Host) stopProc(e *extension, reason string) {
	e.mu.Lock()
	p := e.proc
	e.proc = nil
	e.mu.Unlock()
	if p != nil {
		p.kill(reason)
	}
}

// disable permanently stops the extension and tells the user. The notice
// and cleanup land before the state flips so anyone observing Disabled
// always finds the explanation already there.
func (h *Host) disable(e *extension, why string) {
	h.log.Error("ext: extension disabled", "ext", e.name, "why", why)
	h.notify(fmt.Sprintf("Extension %q was disabled: %s.", e.name, why))
	h.clearStatus(e)
	e.setState(StateDisabled)
}

// clearStatus removes the extension's footer status segment when it stops
// for good; the last value would otherwise stick in the UI indefinitely.
func (h *Host) clearStatus(e *extension) {
	h.bindMu.Lock()
	api := h.api
	h.bindMu.Unlock()
	if api != nil {
		api.SetStatus(e.name, "")
	}
}
