// SPDX-License-Identifier: MIT

// Package echocore is the Basic-level §15.4.1 echo loop, factored out
// of cmd/runtimes/echo so it can run under either §4.7 deployment
// model from a single implementation.
//
// The loop is the §15.4.1 contract: a `message` is echoed back as a
// `response` carrying the same input parts (text parts prefixed with a
// per-loop sequence number so multi-message tests can correlate), a
// `heartbeat` is answered with `heartbeat_ack`, a `shutdown` triggers a
// clean exit, unknown types are ignored, and inbound EOF exits cleanly.
//
// echocore reads an io.Reader and writes an io.Writer; it is indifferent
// to the byte transport behind them. The sidecar reference runtime
// (cmd/runtimes/echo) supplies a stdin/stdout or abstract-socket
// transport; the embedded reference runtime (cmd/runtimes/echo-embedded)
// supplies the adapter's in-process pipe. The §15.4.1 framing is
// identical in every case.
package echocore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// ProtocolError signals a non-recoverable inbound-format violation. A
// runtime entrypoint maps it to the §15.4 protocol-error exit code (2).
type ProtocolError struct{ Msg string }

// Error implements error.
func (e ProtocolError) Error() string { return "protocol error: " + e.Msg }

// MaxFrameBytes caps an inbound JSONL frame at the §15.4.1 MessagePart
// hard limit; a larger frame is a protocol error.
const MaxFrameBytes = 50 * 1024 * 1024

// Run drives the §15.4.1 echo loop until in reaches EOF, a shutdown
// frame arrives, or an unrecoverable error occurs. ctx lets shutdown
// propagate to any goroutine work; the synchronous loop itself returns
// promptly. Diagnostic lines for unknown frame types are written to
// stderr.
func Run(ctx context.Context, in io.Reader, out io.Writer, stderr io.Writer) error {
	w := newWriter(out)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), MaxFrameBytes)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var seq atomic.Uint64
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			return ProtocolError{Msg: fmt.Sprintf("malformed JSONL on input: %v", err)}
		}
		switch env.Type {
		case "message":
			if err := handleMessage(w, line, &seq); err != nil {
				return err
			}
		case "heartbeat":
			if err := w.write(heartbeatAck{Type: "heartbeat_ack"}); err != nil {
				return fmt.Errorf("write heartbeat_ack: %w", err)
			}
		case "shutdown":
			return handleShutdown(w, line, cancel)
		default:
			fmt.Fprintf(stderr, "echocore: ignoring unknown message type %q\n", env.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return ProtocolError{Msg: fmt.Sprintf("input read error: %v", err)}
	}
	return nil
}

// writer serialises JSONL writes so a future writer goroutine is safe.
type writer struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func newWriter(w io.Writer) *writer {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &writer{enc: enc}
}

func (w *writer) write(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(v)
}

// handleMessage echoes the inbound `message` back as a `response`,
// prefixing text parts with the per-loop sequence number.
func handleMessage(w *writer, line []byte, seq *atomic.Uint64) error {
	var inbound struct {
		Type  string        `json:"type"`
		ID    string        `json:"id"`
		Input []messagePart `json:"input"`
	}
	if err := json.Unmarshal(line, &inbound); err != nil {
		return ProtocolError{Msg: fmt.Sprintf("malformed message envelope: %v", err)}
	}
	n := seq.Add(1)
	out := make([]messagePart, 0, len(inbound.Input))
	for _, p := range inbound.Input {
		// §15.4.1 producer obligation: schemaVersion is set on every
		// emitted MessagePart.
		if p.SchemaVersion == 0 {
			p.SchemaVersion = 1
		}
		if p.Type == "text" && p.Inline != "" {
			out = append(out, messagePart{
				SchemaVersion: 1,
				Type:          "text",
				Inline:        fmt.Sprintf("[echo seq=%d] %s", n, p.Inline),
				ID:            p.ID,
				MimeTyp:       p.MimeTyp,
			})
		} else {
			out = append(out, p)
		}
	}
	return w.write(response{Type: "response", Output: out})
}

// handleShutdown reads the deadline, gives ongoing work a bounded moment
// to flush, and returns nil for a clean exit.
func handleShutdown(_ *writer, line []byte, cancel context.CancelFunc) error {
	var sd struct {
		Type       string `json:"type"`
		Reason     string `json:"reason"`
		DeadlineMS int    `json:"deadline_ms"`
	}
	if err := json.Unmarshal(line, &sd); err != nil {
		return ProtocolError{Msg: fmt.Sprintf("malformed shutdown envelope: %v", err)}
	}
	cancel()
	d := time.Duration(sd.DeadlineMS) * time.Millisecond
	if d > 10*time.Millisecond {
		d = 10 * time.Millisecond
	}
	if d > 0 {
		time.Sleep(d)
	}
	return nil
}

// messagePart mirrors the §15.4.1 MessagePart wire JSON, restricted to the
// fields Basic-level echo reads or writes. schemaVersion is mandatory.
type messagePart struct {
	SchemaVersion int            `json:"schemaVersion"`
	Type          string         `json:"type"`
	ID            string         `json:"id,omitempty"`
	Inline        string         `json:"inline,omitempty"`
	Ref           string         `json:"ref,omitempty"`
	MimeTyp       string         `json:"mimeType,omitempty"`
	Annotations   map[string]any `json:"annotations,omitempty"`
	Status        string         `json:"status,omitempty"`
}

type response struct {
	Type   string        `json:"type"`
	Output []messagePart `json:"output"`
}

type heartbeatAck struct {
	Type string `json:"type"`
}
