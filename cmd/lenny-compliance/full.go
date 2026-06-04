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
	"time"
)

// runFullBattery executes the Basic checks plus the §15.4.6 Full-level
// test categories. Each Full-level check spawns the runtime with a fake
// lifecycle-channel server attached and asserts the matching event-pair
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

	basic := basicCases()
	full := []struct {
		name string
		spec string
		fn   func(string, time.Duration, bool) (string, error)
	}{
		{"lifecycle_channel_opening", "15.4.6", checkLifecycleHandshake},
		{"checkpoint_quiesce_resume", "15.4.6", checkCheckpointQuiesce},
		{"interrupt_acknowledgement", "15.4.6", checkInterruptAck},
		{"credential_rotation_no_disruption", "15.4.6", checkCredentialRotation},
		{"deadline_signal_handling", "15.4.6", checkDeadlineSignal},
	}

	for _, c := range basic {
		detail, err := c.fn(binary, timeout, verbose)
		r.recordCheck(c.name, c.spec, detail, err)
	}
	for _, c := range full {
		detail, err := c.fn(binary, timeout, verbose)
		r.recordCheck(c.name, c.spec, detail, err)
	}
	r.Summary.Total = len(r.Checks)
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return r
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
// side of the lifecycle channel and writes a manifest pointing the
// runtime at it. The returned cleanup MUST be called.
type fakeAdapter struct {
	dir        string
	socketPath string
	manifest   string
	listener   net.Listener
	conn       net.Conn
	connErr    error
	connReady  chan struct{}
}

func newFakeAdapter() (*fakeAdapter, func(), error) {
	dir, err := os.MkdirTemp("", "lenny-compliance-full-*")
	if err != nil {
		return nil, nil, err
	}
	socketPath := filepath.Join(dir, "lifecycle.sock")
	manifest := filepath.Join(dir, "adapter-manifest.json")
	body, _ := json.Marshal(map[string]any{
		"lifecycleChannel": map[string]any{"socket": socketPath},
		"mcpNonce":         "nonce_compliance_harness",
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
		dir:        dir,
		socketPath: socketPath,
		manifest:   manifest,
		listener:   l,
		connReady:  make(chan struct{}),
	}
	go func() {
		c, err := l.Accept()
		fa.conn = c
		fa.connErr = err
		close(fa.connReady)
	}()
	cleanup := func() {
		if fa.conn != nil {
			fa.conn.Close()
		}
		l.Close()
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
	defer fa.conn.SetReadDeadline(time.Time{})
	r := bufio.NewReader(fa.conn)
	line, err := r.ReadBytes('\n')
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
// the fake adapter's lifecycle socket. The caller MUST call cmd.Wait
// (or cmd.Process.Kill, then Wait) to reap the child.
func (fa *fakeAdapter) spawn(ctx context.Context, binary string) (*exec.Cmd, io.WriteCloser, *bufio.Scanner, *bufio.Scanner, error) {
	cmd := exec.CommandContext(ctx, binary)
	cmd.Env = append(os.Environ(), "LENNY_ADAPTER_MANIFEST="+fa.manifest)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, nil, err
	}
	outScan := bufio.NewScanner(stdout)
	outScan.Buffer(make([]byte, 64*1024), 50*1024*1024)
	errScan := bufio.NewScanner(stderr)
	return cmd, stdin, outScan, errScan, nil
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

func checkLifecycleHandshake(binary string, _ time.Duration, _ bool) (string, error) {
	fa, cleanup, err := newFakeAdapter()
	if err != nil {
		return "", err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, stdin, _, _, err := fa.spawn(ctx, binary)
	if err != nil {
		return "", err
	}
	defer reap(cmd, stdin, 2*time.Second)

	if err := fa.waitConn(3 * time.Second); err != nil {
		return "", err
	}
	if err := fa.send(map[string]any{
		"type":            "lifecycle_capabilities",
		"protocolVersion": "1.0",
		"capabilities":    []string{"checkpoint", "interrupt", "credential_rotation", "deadline_signal"},
	}); err != nil {
		return "", err
	}
	reply, err := fa.recvJSONLine(3 * time.Second)
	if err != nil {
		return "", err
	}
	if reply["type"] != "lifecycle_support" {
		return "", fmt.Errorf("expected lifecycle_support, got %v", reply)
	}
	capabilities, _ := reply["capabilities"].([]any)
	if len(capabilities) == 0 {
		return "", errors.New("lifecycle_support.capabilities is empty")
	}
	return fmt.Sprintf("supported=%d capabilities", len(capabilities)), nil
}

func checkCheckpointQuiesce(binary string, _ time.Duration, _ bool) (string, error) {
	fa, cleanup, err := newFakeAdapter()
	if err != nil {
		return "", err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, stdin, _, _, err := fa.spawn(ctx, binary)
	if err != nil {
		return "", err
	}
	defer reap(cmd, stdin, 2*time.Second)

	if err := fa.waitConn(3 * time.Second); err != nil {
		return "", err
	}
	if err := handshake(fa); err != nil {
		return "", err
	}
	cpID := "ckpt_" + randomID()
	if err := fa.send(map[string]any{
		"type":         "checkpoint_request",
		"checkpointId": cpID,
		"deadlineMs":   5000,
	}); err != nil {
		return "", err
	}
	reply, err := fa.recvJSONLine(3 * time.Second)
	if err != nil {
		return "", err
	}
	if reply["type"] != "checkpoint_ready" || reply["checkpointId"] != cpID {
		return "", fmt.Errorf("expected checkpoint_ready with id %q, got %v", cpID, reply)
	}
	// Complete the checkpoint so the runtime can resume.
	_ = fa.send(map[string]any{"type": "checkpoint_complete", "checkpointId": cpID, "status": "ok"})
	return "checkpoint_request → checkpoint_ready round-trip", nil
}

func checkInterruptAck(binary string, _ time.Duration, _ bool) (string, error) {
	fa, cleanup, err := newFakeAdapter()
	if err != nil {
		return "", err
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, stdin, _, _, err := fa.spawn(ctx, binary)
	if err != nil {
		return "", err
	}
	defer reap(cmd, stdin, 2*time.Second)

	if err := fa.waitConn(3 * time.Second); err != nil {
		return "", err
	}
	if err := handshake(fa); err != nil {
		return "", err
	}
	intID := "int_" + randomID()
	if err := fa.send(map[string]any{
		"type":        "interrupt_request",
		"interruptId": intID,
		"deadlineMs":  2000,
	}); err != nil {
		return "", err
	}
	reply, err := fa.recvJSONLine(3 * time.Second)
	if err != nil {
		return "", err
	}
	if reply["type"] != "interrupt_acknowledged" || reply["interruptId"] != intID {
		return "", fmt.Errorf("expected interrupt_acknowledged with id %q, got %v", intID, reply)
	}
	return "interrupt_request → interrupt_acknowledged round-trip", nil
}

func checkCredentialRotation(binary string, _ time.Duration, _ bool) (string, error) {
	fa, cleanup, err := newFakeAdapter()
	if err != nil {
		return "", err
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, stdin, _, _, err := fa.spawn(ctx, binary)
	if err != nil {
		return "", err
	}
	defer reap(cmd, stdin, 2*time.Second)

	if err := fa.waitConn(3 * time.Second); err != nil {
		return "", err
	}
	if err := handshake(fa); err != nil {
		return "", err
	}
	leaseID := "lease_" + randomID()
	if err := fa.send(map[string]any{
		"type":            "credentials_rotated",
		"provider":        "anthropic",
		"credentialsPath": "/run/lenny/credentials.json",
		"leaseId":         leaseID,
	}); err != nil {
		return "", err
	}
	// §4.7: the runtime rebinds the credential and replies
	// credentials_acknowledged carrying the same leaseId.
	reply, err := fa.recvJSONLine(3 * time.Second)
	if err != nil {
		return "", err
	}
	if reply["type"] != "credentials_acknowledged" || reply["leaseId"] != leaseID {
		return "", fmt.Errorf("expected credentials_acknowledged with leaseId %q, got %v", leaseID, reply)
	}
	return "credentials_rotated → credentials_acknowledged round-trip", nil
}

func checkDeadlineSignal(binary string, _ time.Duration, _ bool) (string, error) {
	fa, cleanup, err := newFakeAdapter()
	if err != nil {
		return "", err
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, stdin, outScan, _, err := fa.spawn(ctx, binary)
	if err != nil {
		return "", err
	}
	defer reap(cmd, stdin, 2*time.Second)

	if err := fa.waitConn(3 * time.Second); err != nil {
		return "", err
	}
	if err := handshake(fa); err != nil {
		return "", err
	}
	// §15.4.6 "deadline signal handling" is exercised via the §4.7
	// terminate frame: the runtime emits a final response and exits.
	if err := fa.send(map[string]any{
		"type":       "terminate",
		"deadlineMs": 1000,
		"reason":     "session_complete",
	}); err != nil {
		return "", err
	}
	// The runtime is required to emit a final response on stdout. Read
	// up to one stdout line with a deadline.
	type outResult struct {
		line string
		err  error
	}
	ch := make(chan outResult, 1)
	go func() {
		if outScan.Scan() {
			ch <- outResult{line: outScan.Text()}
			return
		}
		ch <- outResult{err: outScan.Err()}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return "", r.err
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(r.line), &resp); err != nil {
			return "", fmt.Errorf("final response not JSON: %v (line %q)", err, r.line)
		}
		if resp["type"] != "response" {
			return "", fmt.Errorf("expected final response type=response, got %v", resp)
		}
		return "deadline_signal → final response emitted", nil
	case <-time.After(3 * time.Second):
		return "", errors.New("runtime did not emit a final response within 3s of terminate")
	}
}

// handshake performs the lifecycle_capabilities exchange. It is shared
// by every full-level check.
func handshake(fa *fakeAdapter) error {
	if err := fa.send(map[string]any{
		"type":            "lifecycle_capabilities",
		"protocolVersion": "1.0",
		"capabilities":    []string{"checkpoint", "interrupt", "credential_rotation", "deadline_signal"},
	}); err != nil {
		return err
	}
	reply, err := fa.recvJSONLine(3 * time.Second)
	if err != nil {
		return err
	}
	if reply["type"] != "lifecycle_support" {
		return fmt.Errorf("expected lifecycle_support, got %v", reply)
	}
	return nil
}

// randomID returns a short random ID for use in checkpoint and interrupt
// envelopes. It does not need cryptographic strength — uniqueness within
// a single test run is enough.
func randomID() string {
	return strings.ReplaceAll(fmt.Sprintf("%016x", time.Now().UnixNano()), " ", "")
}
