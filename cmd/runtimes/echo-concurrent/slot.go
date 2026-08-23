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

// slotError wraps a per-session echocore failure with the session it came
// from.
// A per-slot echocore.ProtocolError (a malformed inbound body on the slot)
// is re-wrapped as the package-local protocolError so the entrypoint's
// errors.As(err, &protocolError{}) match succeeds and the runtime exits
// with the §15.4 protocol-error code (2) rather than the runtime-error
// code (1). Any other failure keeps the wrapped chain and maps to the
// runtime-error code. Failing closed on a malformed slot frame prevents a
// single session from wedging a concurrent pod.
//
// spec: §15.4 — protocol-error exit code 2 for malformed inbound JSONL.
func slotError(sessionID string, err error) error {
	var pe echocore.ProtocolError
	if errors.As(err, &pe) {
		return protocolError{msg: fmt.Sprintf("session %q: %v", sessionID, err)}
	}
	return fmt.Errorf("session %q: %w", sessionID, err)
}

// slotWorker runs one session's §28.5.3 echo loop. It feeds inbound
// frames to an independent echocore.Run over a pipe, so each session
// keeps its own sequence counter and its message handling is the shared
// echocore behavior unchanged. Outbound frames are stamped with the
// session's sessionId by slotWriter before they reach the shared
// transport.
type slotWorker struct {
	sessionID string

	pw        *io.PipeWriter
	done      chan struct{}
	closeOnce sync.Once
}

// newSlotWorker starts the session's echocore loop.
//
// The session's cwd is derived from its identifier via slotCwd
// (/workspace/slots/{sessionId}/current/ per spec §6.4). It is an
// internal filesystem derivation the runtime would use for per-session
// file operations; an echo runtime performs none, so the derivation is
// not plumbed onto the wire. The §28.5.3 outbound frame schema carries
// sessionId alone for multiplexing, never cwd.
//
// spec: §6.4 — per-slot cwd /workspace/slots/{sessionId}/current/ on
// every pod, whatever the pool's concurrency.
func newSlotWorker(ctx context.Context, sessionID string, d *demux, stderr io.Writer) *slotWorker {
	pr, pw := io.Pipe()
	w := &slotWorker{
		sessionID: sessionID,
		pw:        pw,
		done:      make(chan struct{}),
	}
	// Derive the session's cwd up front (spec §6.4). An echo runtime
	// performs no file operations, so the derivation only surfaces as a
	// diagnostic line on stderr; a runtime that read or wrote the
	// workspace would root every per-session file operation at this path.
	fmt.Fprintf(stderr, "echo-concurrent: session %q cwd %s\n", sessionID, slotCwd(sessionID))
	go func() {
		defer close(w.done)
		// slotWriter stamps the sessionId onto every outbound frame
		// echocore produces and forwards it to the shared transport.
		// echocore is driven unmodified; the multiplexing lives entirely
		// in the front loop and this writer.
		sw := &slotWriter{sessionID: sessionID, out: d}
		err := echocore.Run(ctx, pr, sw, stderr)
		if err != nil {
			d.recordErr(slotError(sessionID, err))
		}
		// Drain any unread inbound frames so a writer blocked on the pipe
		// after the loop exits (shutdown, protocol error) does not deadlock.
		_ = pr.CloseWithError(io.EOF)
	}()
	return w
}

// deliver writes one inbound frame to the session's echocore loop. The
// frame is written with a trailing newline so the loop's scanner frames
// it.
func (w *slotWorker) deliver(line []byte) error {
	if _, err := w.pw.Write(line); err != nil {
		return protocolError{msg: fmt.Sprintf("session %q: deliver: %v", w.sessionID, err)}
	}
	if _, err := w.pw.Write([]byte{'\n'}); err != nil {
		return protocolError{msg: fmt.Sprintf("session %q: deliver newline: %v", w.sessionID, err)}
	}
	return nil
}

// close ends the session's inbound stream so its echocore loop sees EOF
// and exits. It is safe to call more than once.
func (w *slotWorker) close() {
	w.closeOnce.Do(func() { _ = w.pw.Close() })
}

// wait blocks until the session's echocore loop has drained.
func (w *slotWorker) wait() { <-w.done }

// slotCwd derives a session's cwd from its identifier: the per-slot tree
// /workspace/slots/{sessionId}/current/. Every session is bound to a slot
// on every pod, so there is one layout and no pod-global alternative.
//
// spec: §6.4; §28.5.3 — per-slot cwd derivation.
func slotCwd(sessionID string) string {
	return "/workspace/slots/" + sessionID + "/current/"
}

// slotWriter wraps the shared transport and stamps the session's
// sessionId onto every outbound JSONL frame echocore emits. echocore
// writes one JSON object per Write call (its json.Encoder.Encode), so
// slotWriter parses the object, injects sessionId, and forwards the
// re-encoded frame. sessionId is the only field the §28.5.3 outbound
// schema adds for multiplexing, so it is the only field stamped.
type slotWriter struct {
	sessionID string
	out       *demux
}

// Write stamps the session address onto a single outbound frame and
// forwards it. The frame is one JSON object terminated by a newline
// (echocore's encoder contract), so a single Write maps to a single
// frame.
func (s *slotWriter) Write(p []byte) (int, error) {
	stamped, err := s.stamp(p)
	if err != nil {
		return 0, err
	}
	if err := s.out.writeFrame(stamped); err != nil {
		return 0, err
	}
	return len(p), nil
}

// stamp injects the sessionId into a single outbound JSONL frame. It
// decodes the object, sets sessionId on every session-scoped frame, and
// re-encodes. A `heartbeat_ack` is protocol-level, carries no per-session
// identifier by design, and is forwarded unchanged. A frame that is not a
// JSON object is forwarded unchanged so a future non-object frame is not
// dropped. sessionId is the only field the schema adds for multiplexing;
// the per-session cwd is an internal derivation (slotCwd) the runtime
// never emits on the wire.
//
// spec: §28.5.3.
func (s *slotWriter) stamp(frame []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(frame)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return frame, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, fmt.Errorf("stamp session address: decode outbound frame: %w", err)
	}
	var typ string
	if raw, ok := obj["type"]; ok {
		_ = json.Unmarshal(raw, &typ)
	}
	if typ == "heartbeat_ack" {
		// Protocol-level ack with no content payload; leave it untouched.
		return frame, nil
	}
	id, err := json.Marshal(s.sessionID)
	if err != nil {
		return nil, fmt.Errorf("stamp session address: encode sessionId: %w", err)
	}
	obj["sessionId"] = id
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("stamp session address: re-encode outbound frame: %w", err)
	}
	return out, nil
}
