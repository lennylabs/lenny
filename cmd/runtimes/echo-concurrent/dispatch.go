// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
)

// protocolError signals a non-recoverable inbound-format violation in the
// session front loop. The entrypoint maps it to the §15.4 protocol-error
// exit code (2). A per-slot echocore.ProtocolError is surfaced through
// this type too, so a malformed frame on any slot fails the whole runtime
// rather than silently wedging one slot.
type protocolError struct{ msg string }

func (e protocolError) Error() string { return "protocol error: " + e.msg }

// run drives the session dispatch loop until in reaches EOF, a shutdown
// frame arrives, or an unrecoverable error occurs.
//
// Each session-scoped frame's sessionId selects a session. The front loop
// demultiplexes those frames to per-session workers keyed on the
// identifier the adapter stamped, which it populates on every frame on
// every pod, so a session-scoped frame carrying none is a protocol error
// rather than a route to a pod-global session. Every worker runs an
// independent §28.5.3 echocore loop, so the per-frame message behavior is
// reused unchanged and each session keeps its own sequence counter. The
// protocol-level frames, heartbeat and shutdown, are pod-global and are
// answered by the front loop itself.
//
// spec: §5.2; §28.5.3 — dispatch loop keyed on sessionId
// over a single stdin channel.
func run(ctx context.Context, in io.Reader, out io.Writer, stderr io.Writer) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	d := newDemux(ctx, out, stderr)
	// closeAll drains every per-slot echocore loop. It is idempotent, so
	// the deferred call is a cleanup guard for the early-return paths; on
	// the normal path the loop below has already drained the workers and
	// this returns nil. A drain error surfaces only when the front loop
	// itself returned cleanly, so it never masks a front-loop error.
	defer func() {
		if cerr := d.closeAll(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), echocore.MaxFrameBytes)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			return protocolError{msg: fmt.Sprintf("malformed JSONL on input: %v", err)}
		}

		// A shutdown frame ends the loop for the whole pod. Forward it to
		// every active session so each echocore loop exits cleanly, then
		// let the deferred drain wait for the workers. The §28.5.3
		// shutdown is a pod-level signal.
		if env.Type == "shutdown" {
			d.broadcast(line)
			break
		}

		// spec: §28.5.3 — heartbeat is protocol-level and carries no
		// per-session identifier, so the pod answers it once, unstamped,
		// rather than routing it to a session.
		if env.Type == "heartbeat" {
			if err := d.writeFrame([]byte(`{"type":"heartbeat_ack"}`)); err != nil {
				return fmt.Errorf("write heartbeat_ack: %w", err)
			}
			continue
		}

		// spec: §28.5.3 — the adapter populates the per-session identifier
		// on every session-scoped frame on every pod, so a session-scoped
		// frame that carries none names no session this runtime may act
		// for. The rule is scoped to the session-scoped inbound set: a
		// frame of any other type keeps §15.4's unknown-type tolerance and
		// is dropped with a diagnostic rather than failing the runtime.
		if env.SessionID == "" {
			if !sessionScopedInboundTypes[env.Type] {
				fmt.Fprintf(stderr, "echo-concurrent: ignoring unaddressed %q frame\n", env.Type)
				continue
			}
			return protocolError{msg: fmt.Sprintf("session-scoped %q frame carries no sessionId", env.Type)}
		}

		if err := d.route(env.SessionID, line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return protocolError{msg: fmt.Sprintf("input read error: %v", err)}
	}

	// EOF or shutdown: the deferred closeAll drains every slot's echocore
	// loop and surfaces the first per-slot error.
	return nil
}

// sessionScopedInboundTypes is the §28.5.3 session-scoped set on the
// adapter-to-runtime direction: the inbound frame types that address a
// session and therefore carry the per-session identifier. Every other
// inbound type is protocol-level or unknown to this runtime, so it falls
// under §15.4's unknown-type tolerance rather than under the addressing
// rule. spec: §15.4; §28.5.3.
var sessionScopedInboundTypes = map[string]bool{
	"message":     true,
	"tool_result": true,
}

// demux owns the per-session worker map and the single shared output
// writer. It is the sessionId multiplexer: one echocore loop per session
// the pod has seen a frame for.
type demux struct {
	ctx    context.Context
	stderr io.Writer

	// outMu serialises writes to the real transport across all per-slot
	// workers, since several echocore loops write the single connection
	// concurrently.
	outMu sync.Mutex
	out   io.Writer

	mu       sync.Mutex
	slots    map[string]*slotWorker
	firstErr error
}

func newDemux(ctx context.Context, out io.Writer, stderr io.Writer) *demux {
	return &demux{
		ctx:    ctx,
		stderr: stderr,
		out:    out,
		slots:  make(map[string]*slotWorker),
	}
}

// route delivers an inbound frame to the worker for sessionID, creating
// the worker on first use.
func (d *demux) route(sessionID string, line []byte) error {
	w := d.worker(sessionID)
	return w.deliver(line)
}

// broadcast delivers a frame to every active worker. It is used for the
// pod-level shutdown frame so each session's echocore loop exits cleanly.
func (d *demux) broadcast(line []byte) {
	d.mu.Lock()
	workers := make([]*slotWorker, 0, len(d.slots))
	for _, w := range d.slots {
		workers = append(workers, w)
	}
	d.mu.Unlock()
	for _, w := range workers {
		// A shutdown delivery error is non-fatal: the worker is already
		// exiting. closeAll surfaces any echocore error.
		_ = w.deliver(line)
	}
}

// worker returns the worker for sessionID, starting it on first use.
func (d *demux) worker(sessionID string) *slotWorker {
	d.mu.Lock()
	defer d.mu.Unlock()
	if w, ok := d.slots[sessionID]; ok {
		return w
	}
	w := newSlotWorker(d.ctx, sessionID, d, d.stderr)
	d.slots[sessionID] = w
	return w
}

// recordErr keeps the first per-slot error so closeAll can surface it.
func (d *demux) recordErr(err error) {
	if err == nil {
		return
	}
	d.mu.Lock()
	if d.firstErr == nil {
		d.firstErr = err
	}
	d.mu.Unlock()
}

// closeAll closes every worker's inbound pipe and waits for its echocore
// loop to drain, returning the first per-session error. It is idempotent so
// the deferred call after a mid-loop error is safe.
func (d *demux) closeAll() error {
	d.mu.Lock()
	workers := make([]*slotWorker, 0, len(d.slots))
	for _, w := range d.slots {
		workers = append(workers, w)
	}
	d.slots = make(map[string]*slotWorker)
	d.mu.Unlock()

	for _, w := range workers {
		w.close()
	}
	for _, w := range workers {
		w.wait()
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	return d.firstErr
}

// writeFrame serialises a single outbound JSONL frame onto the shared
// transport. Per-session workers call it after stamping sessionId, so the
// one connection carries the interleaved per-session output. The front
// loop calls it directly for the pod-global heartbeat_ack.
func (d *demux) writeFrame(frame []byte) error {
	d.outMu.Lock()
	defer d.outMu.Unlock()
	if _, err := d.out.Write(frame); err != nil {
		return err
	}
	if len(frame) == 0 || frame[len(frame)-1] != '\n' {
		if _, err := d.out.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return nil
}
