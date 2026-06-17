// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
)

// slotError wraps a per-slot echocore failure with the slot it came from.
// A per-slot echocore.ProtocolError (a malformed inbound body on the slot)
// is re-wrapped as the package-local protocolError so the entrypoint's
// errors.As(err, &protocolError{}) match succeeds and the runtime exits
// with the §15.4 protocol-error code (2) rather than the runtime-error
// code (1). Any other failure keeps the wrapped chain and maps to the
// runtime-error code. Failing closed on a malformed slot frame prevents a
// single slot from wedging a concurrent pod.
//
// spec: §15.4 — protocol-error exit code 2 for malformed inbound JSONL.
func slotError(slotID string, err error) error {
	var pe echocore.ProtocolError
	if errors.As(err, &pe) {
		return protocolError{msg: fmt.Sprintf("slot %q: %v", slotID, err)}
	}
	return fmt.Errorf("slot %q: %w", slotID, err)
}

// slotWorker runs one slot's §15.4.1 echo loop. It feeds inbound frames
// to an independent echocore.Run over a pipe, so each slot keeps its own
// sequence counter and its message/heartbeat/shutdown handling is the
// shared echocore behavior unchanged. Outbound frames are stamped with
// the slot's slotId by slotWriter before they reach the shared transport.
type slotWorker struct {
	slotID string

	pw        *io.PipeWriter
	done      chan struct{}
	closeOnce sync.Once
}

// newSlotWorker starts the slot's echocore loop. slotID "" is the
// whole-pod default session: its outbound frames carry no slotId, so
// echo-concurrent serves a maxConcurrentSessions: 1 pod through the same
// loop.
//
// The slot's cwd is derived from slotId via slotCwd
// (/workspace/slots/{slotId}/current/ per spec §6.4 line 384). It is an
// internal filesystem derivation the runtime would use for per-slot file
// operations; an echo runtime performs none, so the derivation is not
// plumbed onto the wire. The §15.4.1 outbound frame schema carries slotId
// alone for concurrent multiplexing, never cwd.
//
// spec: §6.4 line 384 — per-slot cwd /workspace/slots/{slotId}/current/;
// the runtime MUST NOT assume a global /workspace/current when
// maxConcurrentSessions > 1.
func newSlotWorker(ctx context.Context, slotID string, d *demux, stderr io.Writer) *slotWorker {
	pr, pw := io.Pipe()
	w := &slotWorker{
		slotID: slotID,
		pw:     pw,
		done:   make(chan struct{}),
	}
	// Derive the slot's cwd up front (spec §6.4 line 384). An echo runtime
	// performs no file operations, so the derivation only surfaces as a
	// diagnostic line on stderr; a runtime that read or wrote the workspace
	// would root every per-slot file operation at this path instead of the
	// global /workspace/current.
	if slotID != "" {
		fmt.Fprintf(stderr, "echo-concurrent: slot %q cwd %s\n", slotID, slotCwd(slotID))
	}
	go func() {
		defer close(w.done)
		// slotWriter stamps the slotId onto every outbound frame echocore
		// produces and forwards it to the shared transport. echocore is
		// driven unmodified; the slotId multiplexing lives entirely in the
		// front loop and this writer.
		sw := &slotWriter{slotID: slotID, out: d}
		err := echocore.Run(ctx, pr, sw, stderr)
		if err != nil {
			d.recordErr(slotError(slotID, err))
		}
		// Drain any unread inbound frames so a writer blocked on the pipe
		// after the loop exits (shutdown, protocol error) does not deadlock.
		_ = pr.CloseWithError(io.EOF)
	}()
	return w
}

// deliver writes one inbound frame to the slot's echocore loop. The frame
// is written with a trailing newline so the loop's scanner frames it.
func (w *slotWorker) deliver(line []byte) error {
	if _, err := w.pw.Write(line); err != nil {
		return protocolError{msg: fmt.Sprintf("slot %q: deliver: %v", w.slotID, err)}
	}
	if _, err := w.pw.Write([]byte{'\n'}); err != nil {
		return protocolError{msg: fmt.Sprintf("slot %q: deliver newline: %v", w.slotID, err)}
	}
	return nil
}

// close ends the slot's inbound stream so its echocore loop sees EOF and
// exits. It is safe to call more than once.
func (w *slotWorker) close() {
	w.closeOnce.Do(func() { _ = w.pw.Close() })
}

// wait blocks until the slot's echocore loop has drained.
func (w *slotWorker) wait() { <-w.done }

// slotCwd derives a slot's cwd from its slotId. A non-empty slotId yields
// the per-slot tree /workspace/slots/{slotId}/current/; the empty default
// session keeps the global /workspace/current path.
//
// spec: §6.4 line 384 — per-slot cwd derivation.
func slotCwd(slotID string) string {
	if slotID == "" {
		return "/workspace/current"
	}
	return "/workspace/slots/" + slotID + "/current/"
}

// slotWriter wraps the shared transport and stamps the slot's slotId onto
// every outbound JSONL frame echocore emits for a non-empty slot. echocore
// writes one JSON object per Write call (its json.Encoder.Encode), so
// slotWriter parses the object, injects slotId, and forwards the
// re-encoded frame. slotId is the only field the §15.4.1 outbound schema
// tags for concurrent multiplexing, so it is the only field stamped. The
// empty default session forwards frames unchanged.
type slotWriter struct {
	slotID string
	out    *demux
}

// Write stamps the slot fields onto a single outbound frame and forwards
// it. The frame is one JSON object terminated by a newline (echocore's
// encoder contract), so a single Write maps to a single frame.
func (s *slotWriter) Write(p []byte) (int, error) {
	if s.slotID == "" {
		// Whole-pod default session: no slotId on the wire (§15.4.1 line
		// 1459 — runtimes on a maxConcurrentSessions: 1 pod never see it).
		if err := s.out.writeFrame(p); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	stamped, err := s.stamp(p)
	if err != nil {
		return 0, err
	}
	if err := s.out.writeFrame(stamped); err != nil {
		return 0, err
	}
	return len(p), nil
}

// stamp injects the slotId into a single outbound JSONL frame. It decodes
// the object, sets slotId on every frame the §15.4.1 wire schema tags with
// it (response, tool_call), and re-encodes. A `heartbeat_ack` carries no
// content payload per the schema and is forwarded unchanged. A frame that
// is not a JSON object is forwarded unchanged so a future non-object frame
// is not dropped. slotId is the only field the schema adds for concurrent
// multiplexing; the per-slot cwd is an internal derivation (slotCwd) the
// runtime never emits on the wire.
//
// spec: §15.4.1 line 1451-1452 (slotId on response/tool_call).
func (s *slotWriter) stamp(frame []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(frame)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return frame, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, fmt.Errorf("stamp slot fields: decode outbound frame: %w", err)
	}
	var typ string
	if raw, ok := obj["type"]; ok {
		_ = json.Unmarshal(raw, &typ)
	}
	if typ == "heartbeat_ack" {
		// Protocol-level ack with no content payload; leave it untouched.
		return frame, nil
	}
	id, err := json.Marshal(s.slotID)
	if err != nil {
		return nil, fmt.Errorf("stamp slot fields: encode slotId: %w", err)
	}
	obj["slotId"] = id
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("stamp slot fields: re-encode outbound frame: %w", err)
	}
	return out, nil
}
