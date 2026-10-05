// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// sessionLifetimeReadWait bounds each read the session lifetime check
// makes for a response or a heartbeat_ack. It matches the Basic heartbeat
// bound, so a runtime the adapter would escalate as unresponsive fails.
const sessionLifetimeReadWait = heartbeatAckDeadline

// sessionLifetimeSessions are the sessions the session lifetime check
// opens. A and B run one after the other; C and D run at once.
var sessionLifetimeSessions = struct{ a, b, c, d string }{
	a: "sess_01J9X0ZW1ZF7K8Q1V2T3M4N5LA",
	b: "sess_01J9X0ZW1ZF7K8Q1V2T3M4N5LB",
	c: "sess_01J9X0ZW1ZF7K8Q1V2T3M4N5LC",
	d: "sess_01J9X0ZW1ZF7K8Q1V2T3M4N5LD",
}

// lifetimeSessionStart builds the session_start that opens sessionID with
// startID, omitting credentialsPath because the harness provisions no
// credential file for it.
func lifetimeSessionStart(sessionID, startID string) string {
	return `{"type":"session_start","sessionId":"` + sessionID + `","startId":"` + startID +
		`","experimentContext":null,"tracingContext":null,"llm":null}`
}

// lifetimeMessage builds a message for sessionID carrying text.
func lifetimeMessage(sessionID, id, text string) string {
	return `{"type":"message","id":"` + id + `","from":{"kind":"client","id":"client_alice"},"sessionId":"` +
		sessionID + `","input":[{"type":"text","inline":"` + text + `"}]}`
}

// checkSessionLifetime is the Basic session lifetime category. On one
// process it opens session A, exchanges a message, and ends it with
// session_end; does the same for session B; then opens C and D and writes
// alternating messages for them before reading any response. The runtime
// answers every message with a response for the message's session,
// answers a heartbeat written after each session_end, and neither exits
// nor closes stdout before the harness closes stdin. A runtime that ends
// its process at a session boundary fails, because the platform keeps one
// runtime process across sessions.
//
// spec: §15.4.6 (Conformance Test Suite, Basic session lifetime), §4.7.10
// (Runtime process lifetime), §28.5.3 (CH-MSGSOCK, Inbound: session_end).
func checkSessionLifetime(binary string, timeout time.Duration, _ bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 50*1024*1024)
	frames := newFrameReader(scanner)
	defer frames.close()
	defer reap(cmd, stdin, 2*time.Second)

	s := sessionLifetimeSessions
	for i, id := range []string{s.a, s.b} {
		start := fmt.Sprintf("lifetime-%d", i+1)
		if err := writeLines(stdin, lifetimeSessionStart(id, start), lifetimeMessage(id, fmt.Sprintf("msg_lifetime_seq_%d", i), "ping")); err != nil {
			return "", err
		}
		if err := expectResponses(frames, map[string]int{id: 1}); err != nil {
			return "", fmt.Errorf("sequential session %s: %w", id, err)
		}
		if err := writeLines(stdin, `{"type":"session_end","sessionId":"`+id+`"}`); err != nil {
			return "", err
		}
		if err := confirmServing(stdin, frames); err != nil {
			return "", fmt.Errorf("after session_end for %s: %w", id, err)
		}
	}
	if err := writeLines(
		stdin,
		lifetimeSessionStart(s.c, "lifetime-3"), lifetimeSessionStart(s.d, "lifetime-4"),
		lifetimeMessage(s.c, "msg_lifetime_c1", "c1"), lifetimeMessage(s.d, "msg_lifetime_d1", "d1"),
		lifetimeMessage(s.c, "msg_lifetime_c2", "c2"), lifetimeMessage(s.d, "msg_lifetime_d2", "d2"),
	); err != nil {
		return "", err
	}
	if err := expectResponses(frames, map[string]int{s.c: 2, s.d: 2}); err != nil {
		return "", fmt.Errorf("concurrent sessions: %w", err)
	}
	// The concurrent responses alone do not show the process outlives
	// them: a runtime that exits on its last response has written every
	// frame the check waited for. Sessions C and D are still open here, so
	// the process must still answer a heartbeat before stdin closes.
	if err := confirmServing(stdin, frames); err != nil {
		return "", fmt.Errorf("after the concurrent sessions: %w", err)
	}
	return "two sequential and two concurrent sessions served on one process", nil
}

// expectResponses reads frames until it has read want[sessionID] responses
// for every session in want, skipping session_started frames. A response
// for a session outside want, an extra response, or a response carrying
// error fails it, as do the end of stdout and a read that outlasts
// sessionLifetimeReadWait.
func expectResponses(frames *frameReader, want map[string]int) error {
	remaining := 0
	for _, n := range want {
		remaining += n
	}
	for remaining > 0 {
		line, err := frames.next(sessionLifetimeReadWait)
		if err != nil {
			return lifetimeReadError("a response", err)
		}
		f, ok := decodeSessionFrame(line)
		if !ok || f.Type == "session_started" {
			continue
		}
		if f.Type != "response" {
			return fmt.Errorf("expected a response, got %s", line)
		}
		if want[f.SessionID] == 0 {
			return fmt.Errorf("response for session %q, want one for %v: %s", f.SessionID, want, line)
		}
		if f.Error != nil {
			return fmt.Errorf("response for session %s carries error %s: the runtime did not serve the session", f.SessionID, f.Error.Code)
		}
		want[f.SessionID]--
		remaining--
	}
	return nil
}

// confirmServing writes a heartbeat and requires its heartbeat_ack, so a
// runtime that exited or closed stdout fails with lifetimeReadError's
// closed-stdout error rather than passing on the frames it already wrote.
// A write error is reported only after the read: a runtime that exited
// breaks the stdin pipe too, and the closed-stdout error names the cause.
//
// spec: §15.4.6 (Conformance Test Suite, Basic session lifetime), §4.7.10
// (Runtime process lifetime).
func confirmServing(stdin io.Writer, frames *frameReader) error {
	writeErr := writeLines(stdin, `{"type":"heartbeat","ts":1}`)
	if err := expectHeartbeatAck(frames); err != nil {
		return err
	}
	return writeErr
}

// expectHeartbeatAck reads frames until a heartbeat_ack, skipping
// session_started frames. Any other frame fails it.
func expectHeartbeatAck(frames *frameReader) error {
	for {
		line, err := frames.next(sessionLifetimeReadWait)
		if err != nil {
			return lifetimeReadError("heartbeat_ack", err)
		}
		f, ok := decodeSessionFrame(line)
		if !ok || f.Type == "session_started" {
			continue
		}
		if f.Type != "heartbeat_ack" {
			return fmt.Errorf("expected heartbeat_ack, got %s", line)
		}
		return nil
	}
}

// lifetimeReadError names what the session lifetime check was waiting for
// when a read failed, and says the runtime exited when stdout closed.
func lifetimeReadError(waitingFor string, err error) error {
	if errors.Is(err, errStdoutClosed) {
		return fmt.Errorf("runtime closed stdout before the harness closed stdin, waiting for %s: %w", waitingFor, err)
	}
	return fmt.Errorf("waiting for %s: %w", waitingFor, err)
}
