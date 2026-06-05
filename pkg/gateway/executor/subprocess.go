// SPDX-License-Identifier: MIT

package executor

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/task"
)

// SubprocessExecutor runs a §15.4 Basic-level runtime binary as a
// child process and speaks the §15.4.1 adapter JSONL protocol over
// the process's stdin/stdout. It is the bridge between the gateway's
// in-process Executor abstraction and a real runtime binary
// (cmd/runtimes/echo and any Basic-level adapter built against the
// same contract).
//
// Each session gets its own child process, spawned lazily on the
// first Send and torn down on Close (stdin EOF triggers the §15.4
// clean-exit path). Concurrent Sends against one session are
// serialised; different sessions run independent processes.
//
// This is NOT the production pod-backed executor — there is no
// Kubernetes, no warm pool, no adapter manifest, no lifecycle
// channel. It is the `make run` developer-loop executor and the
// substrate the conformance harness exercises.
type SubprocessExecutor struct {
	binPath string
	timeout time.Duration

	mu    sync.Mutex
	procs map[string]*subprocessSession
}

// SubprocessOptions configures a SubprocessExecutor.
type SubprocessOptions struct {
	// BinPath is the runtime binary to exec. Required.
	BinPath string

	// SendTimeout bounds how long Send waits for a response line.
	// Zero defaults to 30 s.
	SendTimeout time.Duration
}

// NewSubprocessExecutor returns a SubprocessExecutor.
func NewSubprocessExecutor(opts SubprocessOptions) *SubprocessExecutor {
	timeout := opts.SendTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &SubprocessExecutor{
		binPath: opts.BinPath,
		timeout: timeout,
		procs:   map[string]*subprocessSession{},
	}
}

// subprocessSession holds the child-process state for one session.
type subprocessSession struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	// stderr captures the runtime's stderr tail so a non-zero exit can be
	// folded into a §15.4.1 RUNTIME_CRASH error.
	stderr *capBuffer

	// waitOnce memoizes cmd.Wait so the §15.4.1 RUNTIME_CRASH probe in
	// readResponse and the teardown in Close can both read the exit
	// status without double-waiting the process.
	waitOnce sync.Once
	waitErr  error
}

// wait reaps the child process exactly once and returns the same exit
// error to every caller.
func (s *subprocessSession) wait() error {
	s.waitOnce.Do(func() { s.waitErr = s.cmd.Wait() })
	return s.waitErr
}

// capBuffer is a concurrency-safe writer that retains only the last `cap`
// bytes written. The runtime's stderr is the most diagnostic at the tail
// (the final panic / log lines), so the head is discarded when the
// capture overflows.
type capBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func (c *capBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf = append(c.buf, p...)
	if len(c.buf) > c.cap {
		c.buf = c.buf[len(c.buf)-c.cap:]
	}
	return len(p), nil
}

func (c *capBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}

// maxStderrCapture bounds the per-session stderr ring buffer.
const maxStderrCapture = 8 * 1024

// exitCodeOf extracts the process exit code from a cmd.Wait error. ok is
// false when err is not an *exec.ExitError (e.g. an I/O error reaping the
// process), so the caller does not misclassify it as a clean or crashed
// exit.
func exitCodeOf(err error) (code int, ok bool) {
	if err == nil {
		return 0, true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), true
	}
	return 0, false
}

// Send implements Executor. It lazily spawns the child process for
// sessionID, writes one §15.4.1 message envelope per Message, and
// collects the response envelopes.
func (e *SubprocessExecutor) Send(ctx context.Context, sessionID string, messages []Message) (Response, error) {
	sess, err := e.session(sessionID)
	if err != nil {
		return Response{}, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	var out []OutputPart
	var envAnn map[string]any
	for _, m := range messages {
		env := messageEnvelope{
			SchemaVersion: 1,
			Type:          "message",
			ID:            newMessageID(),
			From:          resolveFromBlock(m),
			Input:         []wireOutputPart{{Type: "text", Inline: m.Content}},
		}
		line, err := json.Marshal(env)
		if err != nil {
			return Response{}, err
		}
		if _, err := sess.stdin.Write(append(line, '\n')); err != nil {
			return Response{}, fmt.Errorf("executor: write to runtime: %w", err)
		}
		parts, ann, err := e.readResponse(ctx, sess)
		if err != nil {
			return Response{}, err
		}
		out = append(out, parts...)
		envAnn = mergeAnnotations(envAnn, ann)
	}
	return Response{Parts: out, Annotations: envAnn}, nil
}

// Start eagerly spawns the runtime child process for sessionID without
// sending a message. The §6.1 pod-warm flow starts the runtime at
// session start, so the §4.7 adapter calls Start from StartSession and
// the process is live before the first message arrives. Start is
// idempotent: a second call for an already-started session is a no-op.
func (e *SubprocessExecutor) Start(_ context.Context, sessionID string) error {
	_, err := e.session(sessionID)
	return err
}

// WriteEnvelope writes a pre-encoded §15.4.1 message envelope to the
// session's runtime stdin, terminated by a newline. The session must
// already be started; WriteEnvelope does not spawn the process. It is
// the raw-delivery path the §4.7 adapter's SendMessage uses, where the
// gateway has already encoded the envelope and the adapter forwards it
// verbatim.
func (e *SubprocessExecutor) WriteEnvelope(sessionID string, envelope []byte) error {
	e.mu.Lock()
	sess, ok := e.procs[sessionID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("executor: session %s has no running runtime", sessionID)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if _, err := sess.stdin.Write(append(envelope, '\n')); err != nil {
		return fmt.Errorf("executor: write envelope to runtime: %w", err)
	}
	return nil
}

// Interrupt signals the session's runtime process: a hard interrupt
// sends SIGKILL, a clean interrupt sends SIGTERM. The signal is
// delivered without taking the session's stdin/stdout lock, so an
// interrupt reaches a runtime that is busy with an in-flight Send.
func (e *SubprocessExecutor) Interrupt(_ context.Context, sessionID string, hard bool) error {
	e.mu.Lock()
	sess, ok := e.procs[sessionID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("executor: session %s has no running runtime", sessionID)
	}
	if hard {
		if err := sess.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("executor: kill runtime: %w", err)
		}
		return nil
	}
	if err := sess.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("executor: signal runtime: %w", err)
	}
	return nil
}

// Output streams every line the session's runtime writes to stdout as
// a channel of §15.4.1 JSONL frames. The channel closes when the
// runtime's stdout reaches EOF; ctx cancellation stops the reader so a
// consumer that stops draining does not leak the goroutine. Output
// must be consumed by a single caller — the adapter's Attach stream —
// because it drains the shared stdout scanner.
func (e *SubprocessExecutor) Output(ctx context.Context, sessionID string) (<-chan []byte, error) {
	e.mu.Lock()
	sess, ok := e.procs[sessionID]
	e.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("executor: session %s has no running runtime", sessionID)
	}
	ch := make(chan []byte)
	go func() {
		defer close(ch)
		for sess.stdout.Scan() {
			line := append([]byte(nil), sess.stdout.Bytes()...)
			select {
			case ch <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// readResponse scans stdout for the next `response` envelope. The
// echo runtime may interleave `heartbeat_ack` and `status` frames;
// those are skipped. Bounded by the executor's SendTimeout.
func (e *SubprocessExecutor) readResponse(ctx context.Context, sess *subprocessSession) ([]OutputPart, map[string]any, error) {
	type result struct {
		parts []OutputPart
		ann   map[string]any
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		for sess.stdout.Scan() {
			var env responseEnvelope
			if err := json.Unmarshal(sess.stdout.Bytes(), &env); err != nil {
				// Skip unparseable lines (forward compatibility).
				continue
			}
			if env.Type != "response" {
				// heartbeat_ack, status, etc. — keep scanning.
				continue
			}
			parts, ann := ingestResponse(env)
			ch <- result{parts: parts, ann: ann}
			return
		}
		if err := sess.stdout.Err(); err != nil {
			ch <- result{err: fmt.Errorf("executor: read from runtime: %w", err)}
			return
		}
		// spec: §15.4.1 line 1889 — the runtime closed stdout before
		// emitting a `response`. If it exited non-zero, synthesize a
		// RUNTIME_CRASH from the exit code and the captured stderr; a
		// clean (code 0) exit with no response is a protocol error, not a
		// crash.
		if code, ok := exitCodeOf(sess.wait()); ok && code != 0 {
			ch <- result{err: task.RuntimeCrash(code, sess.stderr.String())}
			return
		}
		ch <- result{err: fmt.Errorf("executor: runtime closed stdout before responding")}
	}()

	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-time.After(e.timeout):
		return nil, nil, fmt.Errorf("executor: runtime did not respond within %s", e.timeout)
	case r := <-ch:
		return r.parts, r.ann, r.err
	}
}

// session returns the child-process state for sessionID, spawning
// the process on first use.
func (e *SubprocessExecutor) session(sessionID string) (*subprocessSession, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.procs[sessionID]; ok {
		return s, nil
	}
	cmd := exec.Command(e.binPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("executor: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("executor: stdout pipe: %w", err)
	}
	// Capture stderr so a non-zero exit without a `response` can be turned
	// into a §15.4.1 RUNTIME_CRASH carrying the runtime's diagnostics.
	stderr := &capBuffer{cap: maxStderrCapture}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("executor: start runtime %q: %w", e.binPath, err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	s := &subprocessSession{cmd: cmd, stdin: stdin, stdout: scanner, stderr: stderr}
	e.procs[sessionID] = s
	return s, nil
}

// Close implements Executor. It closes the child's stdin (which the
// §15.4 contract treats as a clean-exit signal) and waits for the
// process to exit.
func (e *SubprocessExecutor) Close(_ context.Context, sessionID string) error {
	e.mu.Lock()
	sess, ok := e.procs[sessionID]
	if ok {
		delete(e.procs, sessionID)
	}
	e.mu.Unlock()
	if !ok {
		return nil
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	_ = sess.stdin.Close()
	// Give the process a moment to exit cleanly; kill if it lingers. The
	// memoized wait reaps the process at most once, so a prior
	// RUNTIME_CRASH probe in readResponse and this teardown never race a
	// double cmd.Wait.
	done := make(chan error, 1)
	go func() { done <- sess.wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = sess.cmd.Process.Kill()
		<-done
	}
	return nil
}

// ----- §15.4.1 wire types -----

type messageEnvelope struct {
	SchemaVersion int              `json:"schemaVersion"`
	Type          string           `json:"type"`
	ID            string           `json:"id"`
	From          fromBlock        `json:"from"`
	Input         []wireOutputPart `json:"input"`
}

type fromBlock struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// resolveFromBlock returns the §15.4.1 from-object for an outbound
// message. A message carrying an explicit From (an inter-session
// lenny/send_message whose sender the gateway authenticated) is stamped
// verbatim; a message with no attribution defaults to the gateway-client
// identity used for top-level client turns. The runtime never supplies
// `from`; the gateway is authoritative. spec: §15.4.1 lines 1696-1707.
// F-13.5.11.
func resolveFromBlock(m Message) fromBlock {
	if m.From.Kind != "" {
		return fromBlock{Kind: m.From.Kind, ID: m.From.ID}
	}
	return fromBlock{Kind: "client", ID: "client_gateway"}
}

type wireOutputPart struct {
	Type          string         `json:"type"`
	Inline        string         `json:"inline,omitempty"`
	Ref           string         `json:"ref,omitempty"`
	SchemaVersion int            `json:"schemaVersion,omitempty"`
	Annotations   map[string]any `json:"annotations,omitempty"`
}

type responseEnvelope struct {
	Type   string           `json:"type"`
	Text   string           `json:"text,omitempty"`
	Output []wireOutputPart `json:"output,omitempty"`
}

// newMessageID returns a §15.4.1-shaped message id. The schema
// pattern is `^msg_[0-9A-HJKMNP-TV-Z]{26}$` (Crockford base32, ULID
// style). The minimal executor generates 26 Crockford-base32
// characters from 130 random bits.
func newMessageID() string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var raw [26]byte
	_, _ = rand.Read(raw[:])
	out := make([]byte, 26)
	for i := range out {
		out[i] = alphabet[int(raw[i])&31]
	}
	return "msg_" + string(out)
}
