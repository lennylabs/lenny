// SPDX-License-Identifier: MIT

// Command coding-agent-stub is a test-infrastructure double for the
// §26.3 claude-code adapter bootstrap contract. The real adapter binary
// (github.com/lennylabs/runtime-claude-code) lives outside this
// repository, so this stub reproduces the two observable behaviors §26.3
// documents that no in-repo binary otherwise exercises:
//
//   - "On first `message`: adapter invokes `claude` ... and pipes the
//     message to stdin. All subsequent messages for the session reuse
//     the same `claude` process (multi-turn)."
//   - "On session termination: adapter sends SIGTERM to `claude`; waits
//     up to 5s for graceful shutdown; force-kills on timeout."
//
// The stub speaks the same §15.4.1 JSONL `message`/`response` envelope
// shape as cmd/runtimes/echo over stdin/stdout so it can be driven by
// pkg/gateway/session/executor.SubprocessExecutor exactly like any other
// Basic-level runtime binary. Internally, on the first inbound `message`
// it re-execs itself (CODING_AGENT_STUB_CHILD=1) to stand in for the
// `claude` CLI child process, pipes the message's inline text to that
// child's stdin, and relays the child's reply back inside the `response`
// envelope's `annotations.childPid` field so a test can assert the same
// child process handled every message in the session (spawn-once,
// reuse-for-subsequent-messages).
//
// On stdin EOF (the session-termination signal the executor's Close
// gives every §15.4 runtime binary), the stub sends SIGTERM to its
// child, waits up to the §26.3 5s budget for it to exit, and
// force-kills it on timeout, then reports the outcome as a final
// `shutdown_ack` JSONL line before it exits.
//
// This binary is a test double, not a §26 reference runtime: it is not
// registered in the runtime catalog and ships no Dockerfile.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// childEnv, when set on the parent's own environment, marks a re-exec of
// this binary as the child ("claude") role rather than the adapter role.
const childEnv = "CODING_AGENT_STUB_CHILD"

// ignoreSigtermEnv, when set alongside childEnv, makes the child ignore
// SIGTERM so the parent's shutdown sequence must fall through to the
// force-kill path. It models an unresponsive `claude` process.
const ignoreSigtermEnv = "CODING_AGENT_STUB_CHILD_IGNORE_SIGTERM"

// childKillDeadline is the §26.3 line 222 budget: "waits up to 5s for
// graceful shutdown; force-kills on timeout."
const childKillDeadline = 5 * time.Second

func main() {
	if os.Getenv(childEnv) != "" {
		runChild()
		return
	}
	runParent()
}

// ---- child ("claude") role ----

// runChild is the inner process the parent spawns to stand in for the
// `claude` CLI. It echoes every inbound line back prefixed, and either
// exits promptly on SIGTERM (the default disposition, modeling a
// responsive `claude` process) or ignores SIGTERM (modeling an
// unresponsive one) when ignoreSigtermEnv is set.
func runChild() {
	if os.Getenv(ignoreSigtermEnv) != "" {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM)
		// Never drain sigCh: the signal is delivered but has no
		// effect, so only SIGKILL (the parent's force-kill) ends
		// this process.
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		fmt.Fprintf(os.Stdout, "child-ack:%s\n", scanner.Text())
	}
}

// ---- parent (adapter) role ----

// childProc holds the state of the spawned "claude" child process
// across messages, so the second and later messages in a session reuse
// it instead of spawning a new one.
type childProc struct {
	cmd    *exec.Cmd
	stdin  *bufio.Writer
	stdout *bufio.Scanner
	pid    int
}

func runParent() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	out := json.NewEncoder(os.Stdout)
	out.SetEscapeHTML(false)

	var child *childProc
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env struct {
			Type  string `json:"type"`
			Input []struct {
				Type   string `json:"type"`
				Inline string `json:"inline"`
			} `json:"input"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		if env.Type != "message" {
			continue
		}
		if child == nil {
			var err error
			child, err = spawnChild()
			if err != nil {
				fmt.Fprintf(os.Stderr, "coding-agent-stub: spawn child: %v\n", err)
				os.Exit(1)
			}
		}
		text := ""
		if len(env.Input) > 0 {
			text = env.Input[0].Inline
		}
		reply, err := relay(child, text)
		if err != nil {
			fmt.Fprintf(os.Stderr, "coding-agent-stub: relay to child: %v\n", err)
			os.Exit(1)
		}
		_ = out.Encode(responseEnvelope{
			Type: "response",
			Output: []wireMessagePart{{
				Type:          "text",
				Inline:        reply,
				SchemaVersion: 1,
				Annotations:   map[string]any{"childPid": child.pid},
			}},
		})
	}

	// spec: §26.3 line 222 — stdin EOF is the session-termination
	// signal every §15.4 runtime binary honors; for the coding-agent
	// bootstrap this is where the adapter tears down the `claude`
	// child it has been reusing.
	if child != nil {
		forced := shutdownChild(child)
		_ = out.Encode(shutdownAck{Type: "shutdown_ack", ChildForceKilled: forced})
	}
}

// spawnChild re-execs this same binary in the child role, wiring its
// stdin/stdout as pipes so the parent can relay messages to it and read
// its replies. It propagates ignoreSigtermEnv from the parent's own
// environment so a test controls child responsiveness by setting that
// variable before starting the parent.
func spawnChild() (*childProc, error) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 4096), 1<<20)
	return &childProc{cmd: cmd, stdin: bufio.NewWriter(stdin), stdout: sc, pid: cmd.Process.Pid}, nil
}

// relay writes text to the child's stdin and returns its one-line reply.
// spec: §26.3 line 220 — "pipes the message to stdin."
func relay(c *childProc, text string) (string, error) {
	if _, err := c.stdin.WriteString(text + "\n"); err != nil {
		return "", err
	}
	if err := c.stdin.Flush(); err != nil {
		return "", err
	}
	if !c.stdout.Scan() {
		if err := c.stdout.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("child closed stdout before replying")
	}
	return c.stdout.Text(), nil
}

// shutdownChild sends SIGTERM to the child, waits up to
// childKillDeadline for it to exit, and force-kills it on timeout. It
// returns whether the force-kill path was taken.
//
// spec: §26.3 line 222 — "adapter sends SIGTERM to `claude`; waits up to
// 5s for graceful shutdown; force-kills on timeout."
func shutdownChild(c *childProc) bool {
	if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Already gone; nothing to wait for.
		return false
	}
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case <-done:
		return false
	case <-time.After(childKillDeadline):
		_ = c.cmd.Process.Kill()
		<-done
		return true
	}
}

// ---- wire types (mirrors pkg/gateway/session/executor's §15.4.1 shapes) ----

type wireMessagePart struct {
	Type          string         `json:"type"`
	Inline        string         `json:"inline,omitempty"`
	SchemaVersion int            `json:"schemaVersion,omitempty"`
	Annotations   map[string]any `json:"annotations,omitempty"`
}

type responseEnvelope struct {
	Type   string            `json:"type"`
	Output []wireMessagePart `json:"output"`
}

type shutdownAck struct {
	Type             string `json:"type"`
	ChildForceKilled bool   `json:"childForceKilled"`
}
