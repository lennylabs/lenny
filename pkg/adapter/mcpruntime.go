// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// MCPRuntime drives a §5.1 type: mcp runtime: a runtime whose agent
// process IS an MCP server. The adapter starts that process and reaches
// it as an MCP client (§9.1 "Direct MCP server access"), rather than
// driving an agent binary over the §15.4.1 JSONL stdin/stdout protocol
// used for a type: agent runtime.
//
// MCPRuntime implements the RuntimeProcess interface so the §4.7
// session RPCs (StartSession, SendMessage, Interrupt, Shutdown) drive a
// type: mcp runtime through the same Server code path as a type: agent
// runtime; only the RuntimeProcess implementation differs, selected by
// the adapter's RuntimeKind. The §4.7 session operations map onto MCP
// as follows:
//
//   - Start: spawn the agent's MCP-server process and perform the MCP
//     `initialize` handshake over its stdio.
//   - message: an MCP `tools/call`. The §15.4.1 message envelope names
//     the tool (`tool` field) and carries its arguments (`arguments`
//     field); the adapter issues the call and surfaces the MCP result
//     as a §15.4.1 `response` frame on the Output channel.
//   - Interrupt: close the MCP connection and signal the process
//     (SIGTERM for a clean interrupt, SIGKILL for a hard one). A type:
//     mcp runtime has no §15.4.3 lifecycle channel, so there is no
//     in-band interrupt.
//   - Close: close the MCP connection and terminate the process.
//
// The agent's MCP server is "oblivious to Lenny" (§5.1): it is a plain
// MCP server with no Lenny-specific code, so the adapter→agent MCP
// connection carries no §15.4.3 intra-pod nonce. The nonce authenticates
// a type: agent runtime connecting to the adapter's own platform MCP
// server; it has no role on this connection.
type MCPRuntime struct {
	// Command is the agent's MCP-server executable.
	Command string
	// Args are the arguments passed to Command.
	Args []string
	// Dir is the process working directory — the materialized workspace
	// (/workspace/current). Empty inherits the adapter's directory.
	Dir string
	// Env is the process environment. Nil inherits the adapter's
	// environment.
	Env []string
	// Stderr receives the agent process's stderr for diagnostics. Nil
	// discards it.
	Stderr *os.File
	// InitializeTimeout bounds the MCP `initialize` handshake at Start.
	// Zero applies defaultMCPInitializeTimeout.
	InitializeTimeout time.Duration
	// ShutdownGrace is how long Close waits for the process to exit after
	// the MCP connection is closed before sending SIGKILL. Zero applies
	// defaultMCPShutdownGrace.
	ShutdownGrace time.Duration

	mu      sync.Mutex
	cmd     *exec.Cmd
	client  *mcp.Client
	stdin   interface{ Close() error }
	out     chan []byte
	started bool
	closed  bool
	// initResult records the agent server's negotiated protocol version
	// and identity from the initialize handshake.
	initResult mcp.InitializeResult
}

const (
	defaultMCPInitializeTimeout = 10 * time.Second
	defaultMCPShutdownGrace     = 5 * time.Second
)

// ErrMCPRuntimeNotConfigured reports a Start with no Command set.
var ErrMCPRuntimeNotConfigured = errors.New("adapter: MCP runtime has no command configured")

// Start spawns the agent's MCP-server process and performs the MCP
// `initialize` handshake over its stdio pipes (§9.1). It is the type:
// mcp analogue of starting an agent binary for a type: agent runtime.
func (r *MCPRuntime) Start(ctx context.Context, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return errors.New("adapter: MCP runtime already started")
	}
	if r.Command == "" {
		return ErrMCPRuntimeNotConfigured
	}

	cmd := exec.Command(r.Command, r.Args...)
	cmd.Dir = r.Dir
	cmd.Env = r.Env
	if r.Stderr != nil {
		cmd.Stderr = r.Stderr
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("adapter: open MCP runtime stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("adapter: open MCP runtime stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("adapter: start MCP runtime process: %w", err)
	}

	client := mcp.NewClient(stdout, stdin)

	// Bound the handshake. A type: mcp agent that never completes
	// `initialize` would otherwise hang Start indefinitely.
	timeout := r.InitializeTimeout
	if timeout <= 0 {
		timeout = defaultMCPInitializeTimeout
	}
	initCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resultCh := make(chan struct {
		res mcp.InitializeResult
		err error
	}, 1)
	go func() {
		res, err := client.Initialize()
		resultCh <- struct {
			res mcp.InitializeResult
			err error
		}{res, err}
	}()
	select {
	case <-initCtx.Done():
		client.Close()
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("adapter: MCP runtime initialize handshake: %w", initCtx.Err())
	case got := <-resultCh:
		if got.err != nil {
			client.Close()
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("adapter: MCP runtime initialize handshake: %w", got.err)
		}
		r.initResult = got.res
	}

	r.cmd = cmd
	r.client = client
	r.stdin = stdin
	r.out = make(chan []byte, 16)
	r.started = true
	return nil
}

// mcpInboundMessage is the subset of a §15.4.1 `message` envelope the
// type: mcp adapter path reads to build an MCP `tools/call`. The agent
// of a type: mcp runtime is an MCP server, so the unit of work is a
// tool call: the envelope names the tool and carries its arguments.
type mcpInboundMessage struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// WriteEnvelope maps a §15.4.1 inbound message onto an MCP `tools/call`
// against the agent's MCP server and pushes the call's result onto the
// Output channel as a §15.4.1 `response` frame. A non-`message`
// envelope (a lifecycle frame) is ignored: a type: mcp runtime has no
// JSONL lifecycle protocol.
func (r *MCPRuntime) WriteEnvelope(_ string, envelope []byte) error {
	r.mu.Lock()
	client := r.client
	out := r.out
	closed := r.closed
	r.mu.Unlock()
	if closed || client == nil {
		return errors.New("adapter: MCP runtime is not running")
	}

	var msg mcpInboundMessage
	if err := json.Unmarshal(envelope, &msg); err != nil {
		return fmt.Errorf("adapter: decode MCP runtime message envelope: %w", err)
	}
	// Only `message` envelopes carry tool calls. Lifecycle frames such as
	// `heartbeat` and `shutdown` have no MCP analogue for a type: mcp
	// runtime — the adapter manages the process lifecycle directly.
	if msg.Type != "" && msg.Type != "message" {
		return nil
	}
	if msg.Tool == "" {
		return fmt.Errorf("adapter: MCP runtime message %q names no tool", msg.ID)
	}

	result, callErr := client.CallTool(msg.Tool, msg.Arguments)
	frame, err := mcpResponseFrame(msg.ID, msg.Tool, result, callErr)
	if err != nil {
		return err
	}
	select {
	case out <- frame:
		return nil
	default:
		// The Output buffer is full. Block-free delivery would drop the
		// frame silently; surface it as an error the gateway can retry.
		return errors.New("adapter: MCP runtime output buffer is full")
	}
}

// Output streams the §15.4.1 `response` frames WriteEnvelope produces
// from MCP tool-call results. The channel is closed by Close.
func (r *MCPRuntime) Output(_ context.Context, _ string) (<-chan []byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.out == nil {
		return nil, errors.New("adapter: MCP runtime is not running")
	}
	return r.out, nil
}

// Interrupt signals the agent's MCP-server process. A type: mcp runtime
// has no §15.4.3 lifecycle channel, so a clean interrupt is a SIGTERM
// and a hard interrupt is a SIGKILL — the same signal path the §4.7
// Interrupt RPC falls back to for any runtime without a lifecycle
// channel.
func (r *MCPRuntime) Interrupt(_ context.Context, _ string, hard bool) error {
	r.mu.Lock()
	cmd := r.cmd
	r.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return errors.New("adapter: MCP runtime is not running")
	}
	sig := syscall.SIGTERM
	if hard {
		sig = syscall.SIGKILL
	}
	if err := cmd.Process.Signal(sig); err != nil {
		return fmt.Errorf("adapter: signal MCP runtime process: %w", err)
	}
	return nil
}

// Close tears the type: mcp runtime down: it closes the MCP connection,
// waits ShutdownGrace for the process to exit, then sends SIGKILL if it
// has not. The Output channel is closed exactly once.
func (r *MCPRuntime) Close(_ context.Context, _ string) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	cmd := r.cmd
	client := r.client
	stdin := r.stdin
	out := r.out
	grace := r.ShutdownGrace
	r.mu.Unlock()

	if out != nil {
		close(out)
	}
	if client != nil {
		client.Close()
	}
	// Closing stdin signals a stdio MCP server to exit (its stdin reader
	// observes EOF), the conventional clean-shutdown path.
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	if grace <= 0 {
		grace = defaultMCPShutdownGrace
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return exitErr(err)
	case <-time.After(grace):
		_ = cmd.Process.Kill()
		<-done
		return nil
	}
}

// InitializeResult reports the agent MCP server's negotiated protocol
// version and identity from the Start handshake. It is valid only after
// a successful Start.
func (r *MCPRuntime) InitializeResult() mcp.InitializeResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.initResult
}

// mcpResponseFrame builds the §15.4.1 `response` frame for an MCP
// tools/call outcome. A failed call yields a response carrying an
// `error` object; a successful call yields a single structured
// OutputPart wrapping the MCP result, plus the MCP `content` blocks
// flattened to text parts when the result follows the MCP tool-result
// shape.
func mcpResponseFrame(msgID, tool string, result json.RawMessage, callErr error) ([]byte, error) {
	if callErr != nil {
		frame := map[string]any{
			"type":      "response",
			"inReplyTo": msgID,
			"output":    []any{},
			"error": map[string]any{
				"code":    "MCP_TOOL_CALL_FAILED",
				"message": callErr.Error(),
			},
		}
		return json.Marshal(frame)
	}

	parts := mcpResultParts(result)
	frame := map[string]any{
		"type":      "response",
		"inReplyTo": msgID,
		"output":    parts,
	}
	_ = tool
	return json.Marshal(frame)
}

// mcpResultParts converts an MCP tools/call result into a §15.4.1
// OutputPart array. An MCP tool result is `{content: [...], isError}`;
// each text content block becomes a `text` OutputPart. A result that
// does not follow that shape is wrapped verbatim in a single structured
// `application/json` part so no information is lost.
//
// Each content block's `schemaVersion` is preserved verbatim when the
// producer set one (Lenny runtimes speaking MCP may stamp the field on
// the block as a Lenny-internal extension); when the block carries no
// `schemaVersion` the adapter falls through to `1` so a durable
// consumer reading the persisted §8.8 TaskRecord still sees a value.
// This honours the §15.5 item 7 forward-read contract: the producer's
// declared revision crosses the MCP boundary without being silently
// downgraded mid-flight. spec: §15.5 item 7; §15.4.1 line 1524. F-15.5.13.
func mcpResultParts(result json.RawMessage) []map[string]any {
	if len(result) == 0 {
		return []map[string]any{}
	}
	var toolResult struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(result, &toolResult); err == nil && len(toolResult.Content) > 0 {
		parts := make([]map[string]any, 0, len(toolResult.Content))
		for _, rawBlock := range toolResult.Content {
			var fields map[string]any
			if err := json.Unmarshal(rawBlock, &fields); err != nil || fields == nil {
				// Block did not decode as an object; preserve the raw
				// bytes as a structured part so no information is lost.
				parts = append(parts, map[string]any{
					"schemaVersion": 1,
					"type":          "data",
					"mimeType":      "application/json",
					"inline":        string(rawBlock),
				})
				continue
			}
			schemaVersion := readProducerSchemaVersion(fields["schemaVersion"])
			btype, _ := fields["type"].(string)
			if btype == "text" {
				text, _ := fields["text"].(string)
				parts = append(parts, map[string]any{
					"schemaVersion": schemaVersion,
					"type":          "text",
					"inline":        text,
				})
				continue
			}
			// A non-text content block: preserve it as structured JSON.
			// Drop the producer-stamped schemaVersion from the inline
			// payload (it is now hoisted to the OutputPart envelope) so
			// the consumer never sees it duplicated.
			delete(fields, "schemaVersion")
			raw, _ := json.Marshal(fields)
			parts = append(parts, map[string]any{
				"schemaVersion": schemaVersion,
				"type":          "data",
				"mimeType":      "application/json",
				"inline":        string(raw),
			})
		}
		return parts
	}
	// Not an MCP tool-result object: wrap the whole result as one part.
	return []map[string]any{{
		"schemaVersion": 1,
		"type":          "data",
		"mimeType":      "application/json",
		"inline":        string(result),
	}}
}

// readProducerSchemaVersion projects a producer-stamped schemaVersion
// onto a positive integer. JSON numbers decode as float64 through the
// generic map[string]any path; an integer that round-trips cleanly is
// preserved verbatim, otherwise the adapter falls through to 1 so a
// durable consumer reading the persisted §8.8 TaskRecord still sees a
// value. spec: §15.5 item 7. F-15.5.13.
func readProducerSchemaVersion(v any) int {
	switch t := v.(type) {
	case float64:
		if t > 0 && t == float64(int(t)) {
			return int(t)
		}
	case int:
		if t > 0 {
			return t
		}
	case json.Number:
		if i, err := t.Int64(); err == nil && i > 0 {
			return int(i)
		}
	}
	return 1
}

// exitErr maps a cmd.Wait error to nil for a clean exit and a normal
// SIGTERM/SIGKILL teardown, and returns the error otherwise.
func exitErr(err error) error {
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// A process the adapter terminated (SIGTERM/SIGKILL on Close) exits
		// non-zero by signal; that is the expected teardown, not a fault.
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return nil
		}
	}
	return err
}
