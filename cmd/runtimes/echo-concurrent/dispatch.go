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
// slotId front loop. The entrypoint maps it to the §15.4 protocol-error
// exit code (2). A per-slot echocore.ProtocolError is surfaced through
// this type too, so a malformed frame on any slot fails the whole runtime
// rather than silently wedging one slot.
type protocolError struct{ msg string }

func (e protocolError) Error() string { return "protocol error: " + e.msg }

// run drives the slotId dispatch loop until in reaches EOF, a shutdown
// frame arrives, or an unrecoverable error occurs.
//
// Each inbound frame's optional slotId selects a slot. The front loop
// demultiplexes frames to per-slot workers keyed on slotId; a frame with
// no slotId routes to the default whole-pod session (the empty-string
// key). Every worker runs an independent §15.4.1 echocore loop, so the
// per-frame message/heartbeat/shutdown behavior is reused unchanged and
// each slot keeps its own sequence counter.
//
// spec: §5.2 line 509, §15.4.1 line 1459 — dispatch loop keyed on slotId
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
			Type   string `json:"type"`
			SlotID string `json:"slotId"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			return protocolError{msg: fmt.Sprintf("malformed JSONL on input: %v", err)}
		}

		// A shutdown frame ends the loop for the whole pod. Forward it to
		// every active slot so each echocore loop exits cleanly, then let
		// the deferred drain wait for the workers. A shutdown carrying a
		// slotId still tears the whole pod down: the §15.4.1 shutdown is a
		// pod-level signal.
		if env.Type == "shutdown" {
			d.broadcast(line)
			break
		}

		if err := d.route(env.SlotID, line); err != nil {
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

// demux owns the per-slot worker map and the single shared output writer.
// It is the slotId multiplexer: one echocore loop per slotId, plus the
// empty-string default session for no-slotId frames.
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

// route delivers an inbound frame to the worker for slotID, creating the
// worker on first use. slotID "" is the whole-pod default session.
func (d *demux) route(slotID string, line []byte) error {
	w := d.worker(slotID)
	return w.deliver(line)
}

// broadcast delivers a frame to every active worker. It is used for the
// pod-level shutdown frame so each slot's echocore loop exits cleanly.
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

// worker returns the worker for slotID, starting it on first use.
func (d *demux) worker(slotID string) *slotWorker {
	d.mu.Lock()
	defer d.mu.Unlock()
	if w, ok := d.slots[slotID]; ok {
		return w
	}
	w := newSlotWorker(d.ctx, slotID, d, d.stderr)
	d.slots[slotID] = w
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
// loop to drain, returning the first per-slot error. It is idempotent so
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
// transport. Per-slot workers call it after stamping slotId, so the one
// connection carries the interleaved per-slot output.
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
