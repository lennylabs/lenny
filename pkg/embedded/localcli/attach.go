// SPDX-License-Identifier: MIT

package localcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	lenny "github.com/lennylabs/lenny/sdks/client/go/lenny"
)

// terminalSessionStates is the §15.1 terminal session-state set. When a
// streamed status_change reports one of these, the session has ended and
// the attach render loop stops. spec: §15.1 — session lifecycle terminal
// states (completed, failed, cancelled, expired).
var terminalSessionStates = map[string]bool{
	string(lenny.StateCompleted): true,
	string(lenny.StateFailed):    true,
	string(lenny.StateCancelled): true,
	string(lenny.StateExpired):   true,
}

// attachExitCode maps a terminal session state onto the CLI exit code.
// A completed session exits 0; a failed, cancelled, or expired session
// exits 1 so a script driving `lenny session new --attach` can branch on
// the disposition. spec: §24.17 line 213.
func attachExitCode(state string) int {
	if state == string(lenny.StateCompleted) {
		return 0
	}
	return 1
}

// stdoutIsTTY reports whether w is an interactive terminal. It backs the
// §24.17 line 213 rule that `--attach` defaults on in interactive TTYs:
// a piped or redirected stdout (the common scripting and test path) is
// not a TTY, so attach stays off unless the flag is passed explicitly.
func stdoutIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// attachWanted resolves the §24.17 line 213 attach decision: attach when
// `--attach` is passed explicitly, or when stdout is an interactive TTY
// (the documented default). A non-interactive stdout without the flag
// keeps the non-attached behavior so piped and scripted callers still get
// just the session id.
func attachWanted(f sessionFlags, stdout io.Writer) bool {
	if f.attach {
		return true
	}
	return stdoutIsTTY(stdout)
}

// streamSession implements the §24.17 line 213 attach render loop. It
// opens the §15.1 Server-Sent Events stream for the session and renders
// the session's output, elicitation prompts, and lifecycle transitions
// inline until the session reaches a terminal state, the stream fails, or
// the caller interrupts (Ctrl-C). It returns the CLI exit code.
//
// Agent output (`response` events) is written to stdout so it can be
// piped; lifecycle transitions, delivered messages, and elicitation
// prompts are written to stderr so they annotate the run without
// polluting the captured output. A SIGINT/SIGTERM cancels the stream
// cleanly and returns 0 — the session keeps running gateway-side, and
// the caller can re-`lenny session attach <id>` later.
func streamSession(parent context.Context, client *lenny.Client, sessionID string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Fast path: a session that is already terminal (for example an
	// `attach` to a session that completed before the SSE backlog was
	// retained) is reported without opening a stream that would block.
	if sess, err := client.GetSession(ctx, sessionID); err == nil {
		if terminalSessionStates[string(sess.State)] {
			fmt.Fprintf(stderr, "session %s\n", sess.State)
			return attachExitCode(string(sess.State))
		}
	}

	stream := client.StreamEvents(ctx, sessionID, lenny.StreamOptions{})
	terminalState := ""
	for ev := range stream.Events() {
		switch ev.Type {
		case "response":
			renderResponse(ev.Data, stdout)
		case "message_delivered":
			renderDelivered(ev.Data, stderr)
		case "elicitation_request":
			renderElicitation(ev.Data, stderr)
		case "status_change":
			st := renderStatusChange(ev.Data, stderr)
			if terminalSessionStates[st] {
				terminalState = st
			}
		}
		if terminalState != "" {
			stop() // end the stream's reconnect loop before draining.
			break
		}
	}

	if terminalState != "" {
		return attachExitCode(terminalState)
	}
	// The stream ended without a terminal transition: a transport
	// failure, a non-retryable status, or a caller interrupt. Err is nil
	// on a clean context cancellation (the Ctrl-C path) and on a clean
	// gateway close.
	if err := stream.Err(); err != nil {
		fmt.Fprintf(stderr, "lenny session attach: stream ended: %v\n", err)
		return 1
	}
	return 0
}

// renderResponse writes the agent output text carried by a §15.1
// `response` event to stdout. The gateway controls formatting, so the
// text is emitted verbatim.
func renderResponse(data json.RawMessage, stdout io.Writer) {
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &p); err == nil && p.Text != "" {
		fmt.Fprint(stdout, p.Text)
	}
}

// renderDelivered annotates stderr with a user message the gateway echoed
// back as a `message_delivered` event so the transcript shows the turn
// that prompted the agent output.
func renderDelivered(data json.RawMessage, stderr io.Writer) {
	var p struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(data, &p); err == nil && p.Content != "" {
		fmt.Fprintf(stderr, "> %s\n", p.Content)
	}
}

// renderStatusChange renders a §7.2 line 137 `status_change(state)` event
// as a lifecycle annotation on stderr and returns the new state so the
// caller can detect a terminal transition.
func renderStatusChange(data json.RawMessage, stderr io.Writer) string {
	var p struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(data, &p); err != nil || p.State == "" {
		return ""
	}
	fmt.Fprintf(stderr, "session %s\n", p.State)
	return p.State
}

// renderElicitation renders an `elicitation_request` event (the shared
// §8.5 `lenny/request_input` / §9.2 `lenny/request_elicitation` payload)
// as a prompt on stderr. The payload carries the §8.5 `parts` array; text
// parts are surfaced inline so the operator sees the question. A
// `metadata.maxInputRounds: 1` annotation (a §8.8 one_shot runtime) is
// noted. Responding to the prompt is out of band today; the operator uses
// `lenny session send <id> <message>`.
func renderElicitation(data json.RawMessage, stderr io.Writer) {
	var p struct {
		Parts    []json.RawMessage `json:"parts"`
		Metadata map[string]any    `json:"metadata"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		fmt.Fprintln(stderr, "input requested")
		return
	}
	fmt.Fprint(stderr, "input requested")
	if v, ok := p.Metadata["maxInputRounds"]; ok {
		fmt.Fprintf(stderr, " (maxInputRounds=%v)", v)
	}
	fmt.Fprintln(stderr)
	for _, part := range p.Parts {
		var tp struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(part, &tp); err == nil && tp.Text != "" {
			fmt.Fprintf(stderr, "  %s\n", tp.Text)
		}
	}
	fmt.Fprintln(stderr, "respond with: lenny session send <sessionId> <message>")
}
