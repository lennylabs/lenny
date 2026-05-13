// SPDX-License-Identifier: MIT

// Command echo is the Basic-level reference adapter binary. It reads
// JSON Lines from stdin and writes JSON Lines to stdout, conforming to
// the contract published in schemas/lenny-adapter-jsonl.schema.json.
//
// The Basic level (spec §15.4.3) requires only the stdin/stdout channel.
// No adapter manifest, no MCP, no lifecycle channel. Echo serves three
// purposes:
//
//   - It is the reference implementation for runtime authors writing
//     their own Basic-level adapters.
//   - It is the target the Phase 2 contract tests
//     (tests/tier3_contract/adapter_jsonl/) exec to verify protocol
//     conformance end-to-end.
//   - It is the binary the conformance harness (cmd/lenny-compliance/)
//     exercises during its --level=basic battery.
//
// Behavioral contract (spec §15.4):
//
//   - `message` → emit a `response` whose output echoes the input parts
//     (with optional `[echo seq=N] ` prefix on text parts so multi-
//     message tests can correlate).
//   - `heartbeat` → emit `heartbeat_ack` within 10 seconds.
//   - `shutdown` → finish in-flight work, write a final response if any,
//     and exit cleanly within deadline_ms. On deadline expiry the
//     adapter does not self-kill; SIGKILL is the gateway's responsibility.
//   - Unknown message types are ignored (forward compatibility).
//   - stdin EOF → exit cleanly with code 0.
//
// Exit codes (spec §15.4):
//   0   success
//   1   runtime error (unexpected internal failure)
//   2   protocol error (malformed inbound JSONL the adapter cannot recover from)
//   137 SIGKILL (set by the OS, not by this binary)
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	exitOK            = 0
	exitRuntimeError  = 1
	exitProtocolError = 2
)

func main() {
	if err := run(os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var pe protocolError
		if errors.As(err, &pe) {
			os.Exit(exitProtocolError)
		}
		os.Exit(exitRuntimeError)
	}
	os.Exit(exitOK)
}

// run drives the echo adapter's main loop. It is exported via a private
// type so tests can drive it without spawning a subprocess.
func run(stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	w := newWriter(stdout)
	scanner := bufio.NewScanner(stdin)
	// Inbound lines can grow large. Cap at 50 MB matching the OutputPart
	// hard limit; anything larger is a protocol error.
	const maxBytes = 50 * 1024 * 1024
	scanner.Buffer(make([]byte, 64*1024), maxBytes)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var seq atomic.Uint64

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Parse the envelope. Only `type` is required to dispatch; the rest
		// is type-specific and unknown fields are ignored.
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			return protocolError{msg: fmt.Sprintf("malformed JSONL on stdin: %v", err)}
		}

		switch env.Type {
		case "message":
			if err := handleMessage(ctx, w, line, &seq); err != nil {
				return err
			}
		case "heartbeat":
			if err := w.write(heartbeatAck{Type: "heartbeat_ack"}); err != nil {
				return fmt.Errorf("write heartbeat_ack: %w", err)
			}
		case "shutdown":
			return handleShutdown(ctx, w, line, cancel)
		default:
			// Unknown type. Per the spec's forward-compatibility rule,
			// ignore silently and continue the loop. Surface the unknown
			// type on stderr so operators can see what showed up.
			fmt.Fprintf(stderr, "echo: ignoring unknown message type %q\n", env.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return protocolError{msg: fmt.Sprintf("stdin read error: %v", err)}
	}
	// stdin EOF: exit cleanly.
	return nil
}

// writer serialises JSONL writes to stdout. The adapter is single-
// goroutine today but the spec's heartbeat-ack-within-10s requirement
// will likely push us to a writer goroutine; the writer is already
// safe for that move.
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

// handleMessage echoes the inbound `message` back as a `response`. The
// spec's MessageEnvelope carries `input: [OutputPart]`; we emit
// `output: [OutputPart]` with the same parts, prefixing text parts with
// the per-session sequence number so multi-message tests can correlate.
func handleMessage(_ context.Context, w *writer, line []byte, seq *atomic.Uint64) error {
	var inbound struct {
		Type  string       `json:"type"`
		ID    string       `json:"id"`
		Input []outputPart `json:"input"`
	}
	if err := json.Unmarshal(line, &inbound); err != nil {
		return protocolError{msg: fmt.Sprintf("malformed message envelope: %v", err)}
	}

	n := seq.Add(1)
	out := make([]outputPart, 0, len(inbound.Input))
	for _, p := range inbound.Input {
		if p.Type == "text" && p.Inline != "" {
			out = append(out, outputPart{
				Type:    "text",
				Inline:  fmt.Sprintf("[echo seq=%d] %s", n, p.Inline),
				ID:      p.ID,
				MimeTyp: p.MimeTyp,
			})
		} else {
			// Non-text or ref'd parts: pass through verbatim.
			out = append(out, p)
		}
	}

	return w.write(response{
		Type:   "response",
		Output: out,
	})
}

// handleShutdown reads the deadline, gives ongoing work a moment to
// flush, then returns nil (clean exit). The cancel() lets any future
// goroutine work observe the shutdown.
func handleShutdown(_ context.Context, _ *writer, line []byte, cancel context.CancelFunc) error {
	var sd struct {
		Type       string `json:"type"`
		Reason     string `json:"reason"`
		DeadlineMS int    `json:"deadline_ms"`
	}
	if err := json.Unmarshal(line, &sd); err != nil {
		return protocolError{msg: fmt.Sprintf("malformed shutdown envelope: %v", err)}
	}
	cancel()
	// Echo's main loop is synchronous; there is no pending work to
	// flush. Real adapters would coordinate with their agent goroutines
	// here. Sleeping for the full deadline would only delay exit; we
	// return immediately. A tiny bounded sleep keeps the spec's
	// "graceful exit within deadline_ms" contract observable to tests
	// that watch the wall clock.
	d := time.Duration(sd.DeadlineMS) * time.Millisecond
	if d > 10*time.Millisecond {
		d = 10 * time.Millisecond
	}
	if d > 0 {
		time.Sleep(d)
	}
	return nil
}

// outputPart mirrors the wire JSON Schema for OutputPart, restricted to
// the fields Basic-level echo actually reads or writes. Unknown fields
// are tolerated on the inbound side via standard json decoder behaviour.
type outputPart struct {
	Type        string         `json:"type"`
	ID          string         `json:"id,omitempty"`
	Inline      string         `json:"inline,omitempty"`
	Ref         string         `json:"ref,omitempty"`
	MimeTyp     string         `json:"mimeType,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
	Status      string         `json:"status,omitempty"`
}

type response struct {
	Type   string       `json:"type"`
	Output []outputPart `json:"output"`
}

type heartbeatAck struct {
	Type string `json:"type"`
}

// protocolError signals a non-recoverable inbound-format violation.
// main maps this to exit code 2.
type protocolError struct{ msg string }

func (e protocolError) Error() string { return "protocol error: " + e.msg }
