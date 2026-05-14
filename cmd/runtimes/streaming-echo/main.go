// SPDX-License-Identifier: MIT

// Command streaming-echo is the Full-level reference adapter binary. It
// inherits the Basic-level stdin/stdout protocol from cmd/runtimes/echo
// (message → response, heartbeat → heartbeat_ack, shutdown → clean exit)
// and adds the lifecycle channel from spec §15.4.3 Full level:
//
//   - lifecycle_capabilities / lifecycle_support  — handshake on connect
//   - checkpoint_request / checkpoint_ready / checkpoint_complete
//   - interrupt_request / interrupt_acknowledged
//   - credentials_rotated  — re-read manifest credentials (no-op for echo)
//   - deadline_signal      — emit a final response and exit
//   - draining             — soft drain notification (logged, no behaviour
//     change for echo since it has no in-flight work)
//
// The lifecycle channel transport is a Unix socket whose path is taken
// from the adapter manifest (`/run/lenny/adapter-manifest.json` by
// default, or the path in $LENNY_ADAPTER_MANIFEST). The manifest's
// `lifecycleChannel.socket` field carries the path. Production deploys
// use a Linux abstract socket (path begins with `@`); on macOS, where
// abstract sockets are not supported (spec §15.4.3 platform note), a
// regular file-based socket path is used so the conformance harness can
// drive the runtime cross-platform.
//
// streaming-echo serves three purposes:
//   - Reference implementation for runtime authors writing their own
//     Full-level adapters.
//   - Target the Phase 2.8 contract and conformance tests exec.
//   - Binary the lenny-compliance --level full battery exercises.
//
// Exit codes (spec §15.4): identical to echo — 0 success, 1 runtime
// error, 2 protocol error.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	exitOK            = 0
	exitRuntimeError  = 1
	exitProtocolError = 2

	defaultManifestPath = "/run/lenny/adapter-manifest.json"
	runtimeVersion      = "streaming-echo/0.1.0"
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

func run(stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	stdoutWriter := newWriter(stdout)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Optional lifecycle channel. Resolved from the manifest path in
	// $LENNY_ADAPTER_MANIFEST (falling back to /run/lenny/adapter-manifest.json).
	// A missing manifest is not fatal: streaming-echo can be exercised in
	// the Basic-only test paths without a manifest. The lifecycle channel
	// is opened in a background goroutine so a slow connect does not block
	// stdin processing.
	manifest, manifestErr := loadManifest(os.Getenv("LENNY_ADAPTER_MANIFEST"))
	if manifestErr == nil && manifest.LifecycleChannel.Socket != "" {
		go runLifecycleChannel(ctx, manifest.LifecycleChannel.Socket, stdoutWriter, stderr, cancel)
	} else if manifestErr != nil {
		fmt.Fprintf(stderr, "streaming-echo: no adapter manifest (%v); lifecycle channel disabled\n", manifestErr)
	}

	scanner := bufio.NewScanner(stdin)
	const maxBytes = 50 * 1024 * 1024
	scanner.Buffer(make([]byte, 64*1024), maxBytes)

	var seq atomic.Uint64
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			return protocolError{msg: fmt.Sprintf("malformed JSONL on stdin: %v", err)}
		}
		switch env.Type {
		case "message":
			if err := handleMessage(ctx, stdoutWriter, line, &seq); err != nil {
				return err
			}
		case "heartbeat":
			if err := stdoutWriter.write(heartbeatAck{Type: "heartbeat_ack"}); err != nil {
				return fmt.Errorf("write heartbeat_ack: %w", err)
			}
		case "shutdown":
			return handleShutdown(stdoutWriter, line, cancel)
		default:
			fmt.Fprintf(stderr, "streaming-echo: ignoring unknown message type %q\n", env.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return protocolError{msg: fmt.Sprintf("stdin read error: %v", err)}
	}
	return nil
}

// adapterManifest is the subset of /run/lenny/adapter-manifest.json this
// runtime reads. Unknown fields on the file are ignored.
type adapterManifest struct {
	LifecycleChannel struct {
		Socket string `json:"socket"`
	} `json:"lifecycleChannel"`
	MCPNonce string `json:"mcpNonce"`
}

func loadManifest(path string) (adapterManifest, error) {
	if path == "" {
		path = defaultManifestPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return adapterManifest{}, err
	}
	var m adapterManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return adapterManifest{}, fmt.Errorf("manifest %s: %w", path, err)
	}
	return m, nil
}

// runLifecycleChannel dials the lifecycle Unix socket, completes the
// capabilities/support handshake, and processes incoming events from the
// adapter side. Each event response is written back on the same socket.
//
// Connection failures are logged but not fatal — the spec permits the
// adapter side of the channel to be temporarily unavailable. A failed
// connect on first try gets one retry pause and then gives up.
func runLifecycleChannel(ctx context.Context, socket string, stdoutWriter *writer, stderr io.Writer, cancel context.CancelFunc) {
	conn, err := dialLifecycleSocket(ctx, socket)
	if err != nil {
		fmt.Fprintf(stderr, "streaming-echo: lifecycle channel dial failed: %v\n", err)
		return
	}
	defer conn.Close()

	// Reader/writer over the same connection. Newline-delimited JSON
	// matches the JSONL stdin/stdout shape so reusing the encoder is
	// straightforward.
	reader := bufio.NewReader(conn)
	w := newWriter(conn)

	// Handshake. Wait for the adapter's lifecycle_capabilities, then
	// reply with lifecycle_support naming the events streaming-echo
	// implements. Anything else on the first frame is a protocol error.
	first, err := readJSONLine(reader)
	if err != nil {
		fmt.Fprintf(stderr, "streaming-echo: lifecycle handshake read: %v\n", err)
		return
	}
	var caps struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(first, &caps); err != nil || caps.Type != "lifecycle_capabilities" {
		fmt.Fprintf(stderr, "streaming-echo: lifecycle handshake: expected lifecycle_capabilities, got %s\n", string(first))
		return
	}
	if err := w.write(map[string]any{
		"type": "lifecycle_support",
		"supported": []string{
			"checkpoint",
			"interrupt",
			"credential_rotation",
			"deadline_signal",
			"draining",
		},
		"runtime_version": runtimeVersion,
	}); err != nil {
		fmt.Fprintf(stderr, "streaming-echo: lifecycle support write: %v\n", err)
		return
	}

	// Main event loop. Each inbound frame dispatches to a handler that
	// writes the matching outbound frame. Unknown event types are
	// ignored (forward compat).
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line, err := readJSONLine(reader)
		if err != nil {
			if err == io.EOF || errors.Is(err, net.ErrClosed) {
				return
			}
			fmt.Fprintf(stderr, "streaming-echo: lifecycle read: %v\n", err)
			return
		}
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			fmt.Fprintf(stderr, "streaming-echo: lifecycle: malformed frame: %v\n", err)
			continue
		}
		switch env.Type {
		case "checkpoint_request":
			handleCheckpoint(line, w, stderr)
		case "interrupt_request":
			handleInterrupt(line, stdoutWriter, w, stderr)
		case "credentials_rotated":
			handleCredentialsRotated(line, stderr)
		case "deadline_signal":
			handleDeadline(line, stdoutWriter, stderr, cancel)
			return
		case "draining":
			fmt.Fprintln(stderr, "streaming-echo: lifecycle: draining notification received")
		default:
			fmt.Fprintf(stderr, "streaming-echo: lifecycle: ignoring unknown event type %q\n", env.Type)
		}
	}
}

func dialLifecycleSocket(ctx context.Context, socket string) (net.Conn, error) {
	// Linux abstract socket addresses start with @ and are translated to
	// a leading NUL when dialled. The net package handles this by passing
	// the @-prefixed name unchanged to the syscall; the kernel resolves
	// it. On macOS, abstract sockets are not supported — the path is
	// taken as-is on the filesystem.
	addr := socket
	if strings.HasPrefix(socket, "@") {
		// net.Dial expects "\x00..." for abstract sockets when not on
		// Linux; on Linux net handles the @ prefix natively. Strip the
		// @ and let the kernel decide — this matches the Go stdlib
		// behaviour for unix-abstract addresses.
		addr = "\x00" + socket[1:]
	}
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", addr)
	if err == nil {
		return conn, nil
	}
	// One bounded retry. Helps the test harness that may not have its
	// listener up at the instant the runtime starts.
	select {
	case <-time.After(200 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return d.DialContext(ctx, "unix", addr)
}

func readJSONLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if len(line) > 0 {
		// Trim the trailing newline before returning.
		return line[:len(line)-1], err
	}
	return line, err
}

func handleCheckpoint(line []byte, w *writer, stderr io.Writer) {
	var req struct {
		Type         string `json:"type"`
		CheckpointID string `json:"checkpoint_id"`
		DeadlineMS   int    `json:"deadline_ms"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		fmt.Fprintf(stderr, "streaming-echo: checkpoint_request decode: %v\n", err)
		return
	}
	// Quiesce: streaming-echo has no streaming output yet, so we are
	// quiescent immediately. A real Full-level runtime would flush
	// pending output and wait for in-flight tool calls to settle.
	_ = w.write(map[string]any{
		"type":          "checkpoint_ready",
		"checkpoint_id": req.CheckpointID,
	})
}

func handleInterrupt(line []byte, stdoutWriter, w *writer, stderr io.Writer) {
	var req struct {
		Type        string `json:"type"`
		InterruptID string `json:"interrupt_id"`
		DeadlineMS  int    `json:"deadline_ms"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		fmt.Fprintf(stderr, "streaming-echo: interrupt_request decode: %v\n", err)
		return
	}
	// Acknowledge the interrupt; echo has no work to interrupt so it
	// reaches the "safe stop point" trivially. A real runtime would
	// stop emitting output before sending the ack.
	_ = w.write(map[string]any{
		"type":         "interrupt_acknowledged",
		"interrupt_id": req.InterruptID,
	})
}

func handleCredentialsRotated(line []byte, stderr io.Writer) {
	// streaming-echo has no upstream credentials to refresh; the event
	// is acknowledged via lifecycle_support advertising credential_
	// rotation, and the runtime would now re-read the manifest if it
	// kept credentials in memory.
	fmt.Fprintf(stderr, "streaming-echo: credentials rotated (no-op): %s\n", strings.TrimSpace(string(line)))
}

func handleDeadline(line []byte, stdoutWriter *writer, stderr io.Writer, cancel context.CancelFunc) {
	var req struct {
		Type       string `json:"type"`
		DeadlineMS int    `json:"deadline_ms"`
	}
	_ = json.Unmarshal(line, &req)
	// Emit a final response with the DEADLINE_EXCEEDED marker per
	// spec §15.4.6 Full-level deadline-signal-handling expectation,
	// then signal the stdin loop to exit.
	_ = stdoutWriter.write(map[string]any{
		"type": "response",
		"output": []map[string]any{
			{"type": "text", "inline": "deadline reached"},
		},
		"error": map[string]any{"code": "DEADLINE_EXCEEDED"},
	})
	cancel()
}

// writer / stdin helpers below are identical to cmd/runtimes/echo. They
// are duplicated rather than shared because the runtime binaries are
// intentionally simple and self-contained; runtime authors copying one
// of these into a new project want a single file, not a shared util.

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
				Inline:  fmt.Sprintf("[streaming-echo seq=%d] %s", n, p.Inline),
				ID:      p.ID,
				MimeTyp: p.MimeTyp,
			})
		} else {
			out = append(out, p)
		}
	}
	return w.write(response{Type: "response", Output: out})
}

func handleShutdown(_ *writer, line []byte, cancel context.CancelFunc) error {
	var sd struct {
		Type       string `json:"type"`
		Reason     string `json:"reason"`
		DeadlineMS int    `json:"deadline_ms"`
	}
	if err := json.Unmarshal(line, &sd); err != nil {
		return protocolError{msg: fmt.Sprintf("malformed shutdown envelope: %v", err)}
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

type protocolError struct{ msg string }

func (e protocolError) Error() string { return "protocol error: " + e.msg }
