// SPDX-License-Identifier: MIT

// Package heartbeatsilence is a test-only runtime fixture for a runtime
// that hangs between turns. It runs an unmodified reference runtime as a
// child process and sits between the adapter and that child on the
// §28.5.3 CH-MSGSOCK stream. Every frame passes through unchanged until a
// `message` whose first text part is Directive reaches the child; from then
// on the fixture drops every `heartbeat` frame, so the child never sees one
// and the adapter's ack deadline elapses as for a hung runtime. The adapter
// then ends the session's CH-ATTACH stream with DEADLINE_EXCEEDED, which the
// gateway reports as runtime_crash.
//
// The fixture also logs each `session_end` frame, with the session it
// names, to its stderr, which is the runtime container's log, so a tier-5
// test can observe that the adapter wrote session_end to the runtime.
//
// The behavior lives here, in front of the reference runtime, so that the
// shipped echo runtimes keep acking every heartbeat as §28.5.3 and §15.4.3
// describe. The Kind overlay builds the fixture image on top of a reference
// runtime image and installs it only for the pools the stream-failure tests
// claim (see Dockerfile in this directory and tests/testinfra/kind).
//
// spec: §28.5.3 (CH-MSGSOCK Timing.); §28.5.1 (CH-ATTACH Degradation.).
package heartbeatsilence

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/runtimekit"
	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
)

// Directive is the message text that makes the fixture stop forwarding
// heartbeats. The message itself reaches the child runtime, which answers
// it like any other message.
const Directive = "lenny-e2e:stop-heartbeat-ack"

// SessionEndLogPrefix starts the stderr line the fixture writes for each
// session_end frame; the session identifier follows it.
const SessionEndLogPrefix = "heartbeat-silence: session_end for session "

// childStopGrace bounds how long Supervise waits for the child to exit
// after its context is cancelled before the child is killed.
const childStopGrace = 10 * time.Second

// frame is the part of an inbound CH-MSGSOCK frame the fixture reads.
type frame struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Input     []struct {
		Type   string `json:"type"`
		Inline string `json:"inline"`
	} `json:"input"`
}

// isDirective reports whether the frame is a message whose first text part
// is Directive.
func (f frame) isDirective() bool {
	return f.Type == "message" && len(f.Input) > 0 &&
		f.Input[0].Type == "text" && f.Input[0].Inline == Directive
}

// Filter copies the adapter-to-runtime frames from in to out, one JSONL
// line at a time, and drops every heartbeat after the first Directive
// message has been forwarded. A line that does not parse is forwarded
// unchanged, so the child runtime applies its own protocol-error rule to
// it. Each session_end is logged to log before it is forwarded. Filter
// returns nil at EOF on in.
func Filter(in io.Reader, out io.Writer, log io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), echocore.MaxFrameBytes)
	silent := false
	for scanner.Scan() {
		line := scanner.Bytes()
		var f frame
		_ = json.Unmarshal(line, &f) // an unparsable line is forwarded as is
		if f.Type == "heartbeat" && silent {
			continue
		}
		if f.Type == "session_end" {
			fmt.Fprintf(log, "%s%s\n", SessionEndLogPrefix, f.SessionID)
		}
		if err := writeLine(out, line); err != nil {
			return fmt.Errorf("forward %q frame: %w", f.Type, err)
		}
		if !silent && f.isDirective() {
			silent = true
			fmt.Fprintln(log, "heartbeat-silence: directive received; heartbeats are no longer forwarded")
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read adapter frames: %w", err)
	}
	return nil
}

// writeLine writes line and its terminating newline.
func writeLine(out io.Writer, line []byte) error {
	if _, err := out.Write(line); err != nil {
		return err
	}
	_, err := out.Write([]byte{'\n'})
	return err
}

// Child names the reference runtime binary the fixture supervises and the
// environment it inherits.
type Child struct {
	// Path is the runtime binary.
	Path string
	// Env is the fixture's environment. ChildEnv removes the adapter
	// socket variable from it, so the child uses stdin and stdout.
	Env []string
}

// ChildEnv returns env without the §4.7 adapter socket variable. The
// fixture holds the authenticated socket connection itself, so the child
// must select the stdin/stdout transport.
func ChildEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, runtimekit.SocketEnvVar+"=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Supervise runs the child runtime with in, passed through Filter, as its
// stdin and out as its stdout, and returns the child's exit error, which is
// an *exec.ExitError when the child exits non-zero. The child's stderr goes
// to stderr, which the filter also writes, so stderr must accept
// concurrent writes, as an *os.File does. Cancelling ctx sends the child SIGTERM and kills it after
// childStopGrace. EOF on in closes the child's stdin, which the reference
// runtimes treat as a clean exit. A Filter goroutine still blocked reading
// in when the child exits is left to the caller, which closes in.
func Supervise(ctx context.Context, child Child, in io.Reader, out, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, child.Path)
	cmd.Env = ChildEnv(child.Env)
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = childStopGrace
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("child stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start child runtime %s: %w", child.Path, err)
	}
	go func() {
		if ferr := Filter(in, stdin, stderr); ferr != nil && !childGone(ferr) {
			fmt.Fprintf(stderr, "heartbeat-silence: %v\n", ferr)
		}
		_ = stdin.Close()
	}()
	return cmd.Wait()
}

// childGone reports whether err is a write to the stdin of a child that has
// already exited, which is the normal end of a forward after a shutdown.
func childGone(err error) bool {
	return errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe)
}
