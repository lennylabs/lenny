// SPDX-License-Identifier: MIT

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
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// runFullBattery executes the Basic checks plus the §15.4.6 Full-level
// test categories. Each Full-level check spawns the runtime with a fake
// CH-RUNTIMEOPS server attached and asserts the matching event-pair
// behaviour. The fake lifecycle server uses a file-based Unix socket so
// the harness runs cross-platform; the spec permits both file-based and
// abstract socket addresses (§15.4.3 platform note).
func runFullBattery(binary string, timeout time.Duration, verbose bool) Report {
	r := Report{
		Harness:   "lenny-compliance/" + harnessVersion,
		Binary:    binary,
		Level:     "full",
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}

	for _, c := range basicCases() {
		detail, err := c.fn(binary, timeout, verbose)
		r.recordCheck(c.name, c.spec, detail, err)
	}
	ackWait := sessionStartedWait()
	for _, c := range fullCases() {
		detail, err := c.fn(binary, ackWait)
		r.recordCheck(c.name, c.spec, detail, err)
	}
	r.Summary.Total = len(r.Checks)
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return r
}

// fullCheck is one Full-level check. ackWait bounds its read of the
// session_started that answers its session_start; the battery passes
// sessionStartedWait, and the tests pass a short wait.
type fullCheck func(binary string, ackWait time.Duration) (string, error)

// fullCases is the §15.4.6 Full-level battery, run after the Basic one.
func fullCases() []struct {
	name string
	spec string
	fn   fullCheck
} {
	return []struct {
		name string
		spec string
		fn   fullCheck
	}{
		{"runtime_ops_handshake", "15.4.6", checkRuntimeOpsHandshake},
		{"checkpoint_quiesce_resume", "15.4.6", checkCheckpointQuiesce},
		{"interrupt_acknowledgement", "15.4.6", checkInterruptAck},
		{"credential_rotation_no_disruption", "15.4.6", checkCredentialRotation},
		{"deadline_signal_handling", "15.4.6", checkDeadlineSignal},
	}
}

func (r *Report) recordCheck(name, spec string, detail string, err error) {
	entry := Check{Name: name, Spec: spec}
	if err == nil {
		entry.Pass = true
		entry.Detail = detail
		r.Summary.Passed++
	} else {
		entry.Pass = false
		if detail != "" {
			entry.Detail = detail + " :: "
		}
		entry.Detail += err.Error()
		r.Summary.Failed++
	}
	r.Checks = append(r.Checks, entry)
}

// fakeAdapter spins up a Unix-socket listener that plays the adapter
// side of the CH-RUNTIMEOPS and writes a manifest pointing the
// runtime at it. The returned cleanup MUST be called.
type fakeAdapter struct {
	dir        string
	socketPath string
	manifest   string
	// credentialsPath is the session's own §6.1 credential file, the
	// path the manifest names and the path a credentials_rotated frame
	// carries. The harness roots the slot tree in its temp directory so
	// the battery exercises the manifest-resolved path rather than a
	// fixed location. spec: §4.7; §6.1.
	credentialsPath string
	listener        net.Listener
	// conn and connErr are written once by the accept goroutine before it
	// closes connReady, and read only after connReady is closed.
	conn      net.Conn
	connErr   error
	connReady chan struct{}
	// reader buffers conn across reads, so a second frame the runtime
	// writes in the same segment as the first is not lost.
	reader *bufio.Reader
}

// writeCredentialFile writes the per-session credential bundle the
// manifest and every credentials_rotated frame name, in the providers
// layout of the runtime credential file contract. The frame contract is
// that the adapter has already rewritten the file it names, so the harness
// writes it before the runtime resolves the path.
// spec: §4.7.11 (item 4, runtime credential file contract); §6.1.
func writeCredentialFile(path, provider string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create credential dir %s: %w", filepath.Dir(path), err)
	}
	body, err := credentialBundle(provider)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write credential file %s: %w", path, err)
	}
	return nil
}

// credentialBundle encodes a one-entry providers-layout credential bundle
// for provider.
// spec: §4.7.11 (item 4, runtime credential file contract).
func credentialBundle(provider string) ([]byte, error) {
	body, err := json.Marshal(map[string]any{"providers": []map[string]any{{
		"leaseId":      "lease_compliance_" + provider,
		"provider":     provider,
		"expiresAt":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"deliveryMode": "direct",
		"materializedConfig": map[string]any{
			"apiKey":  "sk-compliance-harness",
			"baseUrl": "https://api." + provider + ".example",
		},
	}}})
	if err != nil {
		return nil, fmt.Errorf("encode credential bundle: %w", err)
	}
	return body, nil
}

func newFakeAdapter() (*fakeAdapter, func(), error) {
	dir, err := os.MkdirTemp("", "lenny-compliance-full-*")
	if err != nil {
		return nil, nil, err
	}
	socketPath := filepath.Join(dir, "lifecycle.sock")
	manifest := filepath.Join(dir, "adapter-manifest.json")
	// spec: §6.1 — the credential file is written per session under
	// slots/{sessionId}/, so the harness names that path on the manifest
	// and on every credentials_rotated frame it sends.
	credentialsPath := filepath.Join(dir, "run", "lenny", "slots", complianceSessionID, "credentials.json")
	if err := writeCredentialFile(credentialsPath, "anthropic"); err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"sessionId":       complianceSessionID,
		"taskId":          complianceSessionID,
		"credentialsPath": credentialsPath,
		"runtimeOps":      map[string]any{"socket": socketPath},
		"mcpNonce":        "nonce_compliance_harness",
	})
	if err := os.WriteFile(manifest, body, 0o600); err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, fmt.Errorf("listen %s: %w", socketPath, err)
	}
	fa := &fakeAdapter{
		dir:             dir,
		socketPath:      socketPath,
		manifest:        manifest,
		credentialsPath: credentialsPath,
		listener:        l,
		connReady:       make(chan struct{}),
	}
	go func() {
		c, err := l.Accept()
		fa.conn = c
		fa.connErr = err
		close(fa.connReady)
	}()
	cleanup := func() {
		// Closing the listener ends a pending Accept, so connReady closes
		// and conn is safe to read.
		l.Close()
		<-fa.connReady
		if fa.conn != nil {
			fa.conn.Close()
		}
		os.RemoveAll(dir)
	}
	return fa, cleanup, nil
}

// waitConn blocks until the runtime dials the lifecycle socket and the
// listener accepts, or until the deadline elapses.
func (fa *fakeAdapter) waitConn(deadline time.Duration) error {
	select {
	case <-fa.connReady:
		return fa.connErr
	case <-time.After(deadline):
		return errors.New("runtime did not connect to the lifecycle socket within deadline")
	}
}

// send writes a JSON envelope to the runtime, terminated by a newline.
func (fa *fakeAdapter) send(v any) error {
	if fa.conn == nil {
		return errors.New("no connection")
	}
	enc := json.NewEncoder(fa.conn)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// recvJSONLine reads one newline-terminated JSON object from the
// runtime, parsing it into a map for convenience.
func (fa *fakeAdapter) recvJSONLine(deadline time.Duration) (map[string]any, error) {
	if fa.conn == nil {
		return nil, errors.New("no connection")
	}
	_ = fa.conn.SetReadDeadline(time.Now().Add(deadline))
	defer func() { _ = fa.conn.SetReadDeadline(time.Time{}) }()
	if fa.reader == nil {
		fa.reader = bufio.NewReader(fa.conn)
	}
	line, err := fa.reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, fmt.Errorf("decode %s: %w", string(line), err)
	}
	return m, nil
}

// spawn starts the runtime with LENNY_ADAPTER_MANIFEST set so it dials
// the fake adapter's lifecycle socket, and returns its stdin and a reader
// over its stdout. The caller MUST call reap to reap the child and then
// close the frame reader.
func (fa *fakeAdapter) spawn(ctx context.Context, binary string) (*exec.Cmd, io.WriteCloser, *frameReader, error) {
	cmd := exec.CommandContext(ctx, binary)
	cmd.Env = append(os.Environ(), "LENNY_ADAPTER_MANIFEST="+fa.manifest)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	outScan := bufio.NewScanner(stdout)
	outScan.Buffer(make([]byte, 64*1024), 50*1024*1024)
	return cmd, stdin, newFrameReader(outScan), nil
}

// reap closes stdin, waits up to deadline for the child to exit, then
// kills it if it has not. Returns the exit code.
func reap(cmd *exec.Cmd, stdin io.WriteCloser, deadline time.Duration) int {
	if stdin != nil {
		stdin.Close()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		return 0
	case <-time.After(deadline):
		_ = cmd.Process.Kill()
		<-done
		return -1
	}
}

// --- Full-level checks --------------------------------------------------

// fullRun is one Full check's runtime process: the fake adapter it dialed,
// its stdin, and the reader over its stdout.
type fullRun struct {
	fa     *fakeAdapter
	stdin  io.WriteCloser
	frames *frameReader
	// support is the capability list the runtime returned in
	// lifecycle_support.
	support []string
}

// startFullRun starts binary against a fresh fake adapter and completes
// the CH-RUNTIMEOPS capability handshake. The process context is ackWait
// plus fullCheckBudget, so a runtime that takes the whole session_started
// wait still has the former budget for the connection, the handshake, and
// the check's own exchange. The returned stop MUST be called; it reaps the
// process and releases the fake adapter.
func startFullRun(binary string, ackWait time.Duration) (*fullRun, func(), error) {
	fa, cleanup, err := newFakeAdapter()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), ackWait+fullCheckBudget)
	cmd, stdin, frames, err := fa.spawn(ctx, binary)
	if err != nil {
		cancel()
		cleanup()
		return nil, nil, err
	}
	stop := func() {
		reap(cmd, stdin, 2*time.Second)
		frames.close()
		cancel()
		cleanup()
	}
	run := &fullRun{fa: fa, stdin: stdin, frames: frames}
	if err := fa.waitConn(3 * time.Second); err != nil {
		stop()
		return nil, nil, err
	}
	if run.support, err = handshake(fa); err != nil {
		stop()
		return nil, nil, err
	}
	return run, stop, nil
}

// openSession writes the session_start that opens complianceSessionID and
// reads the session_started that answers it, under ackWait. A Full check
// calls it before it writes a session-scoped CH-RUNTIMEOPS frame, because
// that channel is a connection separate from the runtime's stdin, so only
// the acknowledgement shows the runtime holds the session the frame names.
//
// spec: §15.4.6 (Conformance Test Suite, Test categories by integration
// level), §28.5.3 (CH-RUNTIMEOPS, Messages).
func (r *fullRun) openSession(ackWait time.Duration) error {
	if err := writeLines(r.stdin, complianceSessionStart); err != nil {
		return err
	}
	return awaitSessionStarted(r.frames, complianceSessionID, complianceStartID, ackWait)
}

// supports reports whether the runtime declared capability in
// lifecycle_support.
func (r *fullRun) supports(capability string) bool {
	for _, c := range r.support {
		if c == capability {
			return true
		}
	}
	return false
}

// checkRuntimeOpsHandshake drives the CH-RUNTIMEOPS opening category: the
// runtime completes the lifecycle_capabilities / lifecycle_support
// exchange and then answers a session_start with a session_started that
// carries the same sessionId and startId and no error. It is named for the
// channel because the naming law gives a channel one identifier on every
// carrier, the Go symbol included.
//
// spec: §15.4.6 (Conformance Test Suite, CH-RUNTIMEOPS opening), §28.5.3
// (CH-MSGSOCK, Outbound: session_started), §28.1.
func checkRuntimeOpsHandshake(binary string, ackWait time.Duration) (string, error) {
	run, stop, err := startFullRun(binary, ackWait)
	if err != nil {
		return "", err
	}
	defer stop()
	if len(run.support) == 0 {
		return "", errors.New("lifecycle_support.capabilities is empty")
	}
	if err := run.openSession(ackWait); err != nil {
		return "", err
	}
	return fmt.Sprintf("supported=%d capabilities; session_start acknowledged", len(run.support)), nil
}

// checkCheckpointQuiesce drives checkpoint_request for the open session
// and requires checkpoint_ready for the same checkpointId.
//
// spec: §15.4.6 (Conformance Test Suite, checkpoint quiesce/resume).
func checkCheckpointQuiesce(binary string, ackWait time.Duration) (string, error) {
	run, stop, err := startFullRun(binary, ackWait)
	if err != nil {
		return "", err
	}
	defer stop()
	if err := run.openSession(ackWait); err != nil {
		return "", err
	}
	cpID := "ckpt_" + randomID()
	if err := run.fa.send(map[string]any{
		"type":         "checkpoint_request",
		"sessionId":    complianceSessionID,
		"checkpointId": cpID,
		"deadlineMs":   5000,
	}); err != nil {
		return "", err
	}
	reply, err := run.fa.recvJSONLine(3 * time.Second)
	if err != nil {
		return "", err
	}
	if reply["type"] != "checkpoint_ready" || reply["checkpointId"] != cpID {
		return "", fmt.Errorf("expected checkpoint_ready with id %q, got %v", cpID, reply)
	}
	// Complete the checkpoint so the runtime can resume.
	_ = run.fa.send(map[string]any{"type": "checkpoint_complete", "sessionId": complianceSessionID, "checkpointId": cpID, "status": "ok"})
	return "checkpoint_request → checkpoint_ready round-trip", nil
}

// checkInterruptAck drives interrupt_request for the open session and
// requires interrupt_acknowledged for the same interruptId.
//
// spec: §15.4.6 (Conformance Test Suite, interrupt acknowledgement).
func checkInterruptAck(binary string, ackWait time.Duration) (string, error) {
	run, stop, err := startFullRun(binary, ackWait)
	if err != nil {
		return "", err
	}
	defer stop()
	if err := run.openSession(ackWait); err != nil {
		return "", err
	}
	intID := "int_" + randomID()
	if err := run.fa.send(map[string]any{
		"type":        "interrupt_request",
		"sessionId":   complianceSessionID,
		"interruptId": intID,
		"deadlineMs":  2000,
	}); err != nil {
		return "", err
	}
	reply, err := run.fa.recvJSONLine(3 * time.Second)
	if err != nil {
		return "", err
	}
	if reply["type"] != "interrupt_acknowledged" || reply["interruptId"] != intID {
		return "", fmt.Errorf("expected interrupt_acknowledged with id %q, got %v", intID, reply)
	}
	return "interrupt_request → interrupt_acknowledged round-trip", nil
}

// rereadWait bounds the credential rotation check's wait for the runtime to
// open the credential file credentials_rotated names.
const rereadWait = 3 * time.Second

// errNoCredentialReread marks a rotation the runtime acknowledged, or
// left unanswered, without opening the credential file the frame named.
var errNoCredentialReread = errors.New("runtime did not re-read the credential file credentials_rotated names")

// checkCredentialRotation asserts the runtime re-reads the credential file
// credentials_rotated names for the open session, and then acknowledges
// the rotation with the frame's leaseId. The harness replaces the file
// with a named pipe before it writes the frame, so the runtime's open of
// the path for reading is the observable event of the re-read: it depends
// on no reply text and on no file access time. Once the runtime opens the
// pipe the harness writes the rotated bundle and closes the pipe, so the
// runtime's read ends at end of file. A runtime that does not declare
// credential_rotation has no category to satisfy.
//
// spec: §15.4.6 (Conformance Test Suite, credential rotation handling),
// §28.5.3 (CH-RUNTIMEOPS, credentials_rotated).
func checkCredentialRotation(binary string, ackWait time.Duration) (string, error) {
	run, stop, err := startFullRun(binary, ackWait)
	if err != nil {
		return "", err
	}
	defer stop()
	if !run.supports("credential_rotation") {
		return "runtime does not declare credential_rotation; category not applicable", nil
	}
	if err := run.openSession(ackWait); err != nil {
		return "", err
	}
	path := run.fa.credentialsPath
	if err := replaceWithPipe(path); err != nil {
		return "", err
	}
	leaseID := "lease_" + randomID()
	if err := run.fa.send(map[string]any{
		"type":            "credentials_rotated",
		"sessionId":       complianceSessionID,
		"provider":        "anthropic",
		"credentialsPath": path,
		"leaseId":         leaseID,
	}); err != nil {
		return "", err
	}
	if err := serveRotatedBundle(path, "anthropic", rereadWait); err != nil {
		return "", err
	}
	reply, err := run.fa.recvJSONLine(3 * time.Second)
	if err != nil {
		return "", fmt.Errorf("no credentials_acknowledged after the re-read: %w", err)
	}
	if reply["type"] != "credentials_acknowledged" || reply["leaseId"] != leaseID {
		return "", fmt.Errorf("expected credentials_acknowledged with leaseId %q, got %v", leaseID, reply)
	}
	return "credential file re-read; credentials_rotated → credentials_acknowledged round-trip", nil
}

// replaceWithPipe replaces the file at path with a named pipe.
func replaceWithPipe(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove credential file %s: %w", path, err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return fmt.Errorf("create named pipe at %s: %w", path, err)
	}
	return nil
}

// serveRotatedBundle opens the named pipe at path for writing without
// blocking, retrying until a reader has opened it or wait elapses, then
// writes provider's bundle and closes the pipe. An open that never
// succeeds means no reader opened the path, and the error wraps
// errNoCredentialReread.
func serveRotatedBundle(path, provider string, wait time.Duration) error {
	body, err := credentialBundle(provider)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			_, werr := f.Write(body)
			cerr := f.Close()
			if werr != nil {
				return fmt.Errorf("write rotated bundle to %s: %w", path, werr)
			}
			if cerr != nil {
				return fmt.Errorf("close rotated bundle pipe %s: %w", path, cerr)
			}
			return nil
		}
		// ENXIO is the open of a pipe for writing that no reader holds.
		if !errors.Is(err, syscall.ENXIO) {
			return fmt.Errorf("open credential pipe %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w (%s, within %s)", errNoCredentialReread, path, wait)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// deadlineRemainingMs is the remainingMs the deadline check writes on
// deadline_approaching, and the bound on the runtime's response.
const deadlineRemainingMs = 3000

// checkDeadlineSignal drives the deadline signal handling category. After
// the session_started read it writes a message for the session and then
// deadline_approaching naming the session, before it reads the response.
// The runtime writes the response to that message before remainingMs
// elapses, writes no other response for the session, and still answers a
// later heartbeat, which shows the process is alive. A runtime that does
// not declare deadline_signal has no category to satisfy.
//
// spec: §15.4.6 (Conformance Test Suite, deadline signal handling),
// §28.5.3 (CH-RUNTIMEOPS, deadline_approaching), §4.7.10 (Runtime process
// lifetime).
func checkDeadlineSignal(binary string, ackWait time.Duration) (string, error) {
	run, stop, err := startFullRun(binary, ackWait)
	if err != nil {
		return "", err
	}
	defer stop()
	if !run.supports("deadline_signal") {
		return "runtime does not declare deadline_signal; category not applicable", nil
	}
	if err := run.openSession(ackWait); err != nil {
		return "", err
	}
	msg := `{"type":"message","id":"msg_01J9X0ZW1ZF7K8Q1V2T3M4N5D1","from":{"kind":"client","id":"client_alice"},"sessionId":"` +
		complianceSessionID + `","input":[{"type":"text","inline":"ping"}]}`
	if err := writeLines(run.stdin, msg); err != nil {
		return "", err
	}
	if err := run.fa.send(map[string]any{
		"type":        "deadline_approaching",
		"sessionId":   complianceSessionID,
		"remainingMs": deadlineRemainingMs,
		"trigger":     "session_age",
	}); err != nil {
		return "", err
	}
	if err := awaitDeadlineResponse(run.frames, deadlineRemainingMs*time.Millisecond); err != nil {
		return "", err
	}
	if err := writeLines(run.stdin, `{"type":"heartbeat","ts":1}`); err != nil {
		return "", err
	}
	if err := awaitAckWithoutResponse(run.frames, 3*time.Second); err != nil {
		return "", err
	}
	return "response before remainingMs, no further response, heartbeat still answered", nil
}

// awaitDeadlineResponse reads frames until the response to the session's
// message, skipping session_started frames, and fails when it does not
// arrive within wait or does not carry the session's sessionId.
func awaitDeadlineResponse(frames *frameReader, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		line, err := frames.next(time.Until(deadline))
		if err != nil {
			return fmt.Errorf("no response to the session's message before remainingMs (%s) elapsed: %w", wait, err)
		}
		f, ok := decodeSessionFrame(line)
		if !ok || f.Type == "session_started" {
			continue
		}
		if f.Type != "response" || f.SessionID != complianceSessionID {
			return fmt.Errorf("expected the response for session %s, got %s", complianceSessionID, line)
		}
		return nil
	}
}

// awaitAckWithoutResponse reads frames until heartbeat_ack, skipping
// session_started frames. A response read first is a second response for
// the session after deadline_approaching, and the end of stdout means the
// runtime exited on the signal.
func awaitAckWithoutResponse(frames *frameReader, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		line, err := frames.next(time.Until(deadline))
		if errors.Is(err, errStdoutClosed) {
			return fmt.Errorf("runtime exited after deadline_approaching: %w", err)
		}
		if err != nil {
			return fmt.Errorf("no heartbeat_ack after deadline_approaching: %w", err)
		}
		f, ok := decodeSessionFrame(line)
		if !ok || f.Type == "session_started" {
			continue
		}
		switch f.Type {
		case "heartbeat_ack":
			return nil
		case "response":
			return fmt.Errorf("runtime wrote a second response after deadline_approaching: %s", line)
		}
	}
}

// handshake performs the lifecycle_capabilities exchange and returns the
// capabilities the runtime declared in lifecycle_support. It is shared by
// every Full-level check.
func handshake(fa *fakeAdapter) ([]string, error) {
	if err := fa.send(map[string]any{
		"type":            "lifecycle_capabilities",
		"protocolVersion": "1.0",
		"capabilities":    []string{"checkpoint", "interrupt", "credential_rotation", "deadline_signal"},
	}); err != nil {
		return nil, err
	}
	reply, err := fa.recvJSONLine(3 * time.Second)
	if err != nil {
		return nil, err
	}
	if reply["type"] != "lifecycle_support" {
		return nil, fmt.Errorf("expected lifecycle_support, got %v", reply)
	}
	raw, _ := reply["capabilities"].([]any)
	support := make([]string, 0, len(raw))
	for _, c := range raw {
		if name, ok := c.(string); ok {
			support = append(support, name)
		}
	}
	return support, nil
}

// randomID returns a short random ID for use in checkpoint and interrupt
// envelopes. It does not need cryptographic strength — uniqueness within
// a single test run is enough.
func randomID() string {
	return strings.ReplaceAll(fmt.Sprintf("%016x", time.Now().UnixNano()), " ", "")
}
