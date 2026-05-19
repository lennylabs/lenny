// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// AdapterTools is the §15.4.1 adapter-local tool surface available at
// every integration level. It emits a stdout tool_call frame and blocks
// until the matching tool_result frame arrives on stdin, correlating by
// id. Unlike the platform MCP tools (which require Standard level),
// adapter-local tools (read_file, write_file, list_dir, delete_file) are
// resolved inside the adapter process with no MCP server.
//
// A handler reaches AdapterTools through AdapterToolsFrom on the
// context the SDK passes to OnMessage.
type AdapterTools struct {
	w       *frameWriter
	timeout time.Duration
	slotID  string
}

// ToolResult is the decoded result of an adapter-local tool call. A
// failed call sets IsError; Content carries the result parts (for a
// failed call, Content[0].Inline is the error string).
type ToolResult struct {
	Content []OutputPart
	IsError bool
}

// ToolCall emits a §15.4.1 tool_call frame for the named adapter-local
// tool and blocks until the correlated tool_result arrives. ctx bounds
// the wait. The id is generated and unique within the process.
func (a *AdapterTools) ToolCall(ctx context.Context, name string, arguments map[string]any) (ToolResult, error) {
	if a == nil || a.w == nil {
		return ToolResult{}, errors.New("adapter tools unavailable: runtime not started")
	}
	id := newCallID()
	ch := a.w.registerToolCall(id)
	if err := a.w.write(outboundToolCall{
		Type:      "tool_call",
		ID:        id,
		Name:      name,
		Arguments: arguments,
		SlotID:    a.slotID,
	}); err != nil {
		a.w.cancelToolCall(id)
		return ToolResult{}, fmt.Errorf("write tool_call %q: %w", name, err)
	}

	timeout := a.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case tr := <-ch:
		return ToolResult{Content: tr.Content, IsError: tr.IsError}, nil
	case <-ctx.Done():
		a.w.cancelToolCall(id)
		return ToolResult{}, ctx.Err()
	case <-timer.C:
		a.w.cancelToolCall(id)
		return ToolResult{}, fmt.Errorf("tool_call %q timed out after %s", name, timeout)
	}
}

// ReadFile invokes the §15.4.1 read_file adapter-local tool. The path is
// confined to the pod workspace by the adapter; a path resolving
// outside /workspace returns an error result.
func (a *AdapterTools) ReadFile(ctx context.Context, path string) (string, error) {
	tr, err := a.ToolCall(ctx, "read_file", map[string]any{"path": path})
	if err != nil {
		return "", err
	}
	return firstInline(tr)
}

// WriteFile invokes the §15.4.1 write_file adapter-local tool, creating
// or overwriting a workspace file with UTF-8 content.
func (a *AdapterTools) WriteFile(ctx context.Context, path, content string) error {
	tr, err := a.ToolCall(ctx, "write_file", map[string]any{"path": path, "content": content})
	if err != nil {
		return err
	}
	return toolError(tr, "write_file")
}

// ListDir invokes the §15.4.1 list_dir adapter-local tool and returns
// the directory entries the adapter reports.
func (a *AdapterTools) ListDir(ctx context.Context, path string) ([]OutputPart, error) {
	tr, err := a.ToolCall(ctx, "list_dir", map[string]any{"path": path})
	if err != nil {
		return nil, err
	}
	if err := toolError(tr, "list_dir"); err != nil {
		return nil, err
	}
	return tr.Content, nil
}

// DeleteFile invokes the §15.4.1 delete_file adapter-local tool.
func (a *AdapterTools) DeleteFile(ctx context.Context, path string) error {
	tr, err := a.ToolCall(ctx, "delete_file", map[string]any{"path": path})
	if err != nil {
		return err
	}
	return toolError(tr, "delete_file")
}

// firstInline returns the inline content of the first result part, or
// an error when the tool reported a failure.
func firstInline(tr ToolResult) (string, error) {
	if err := toolError(tr, "tool"); err != nil {
		return "", err
	}
	if len(tr.Content) == 0 {
		return "", nil
	}
	return tr.Content[0].Inline, nil
}

// toolError converts an error-flagged tool result into a Go error. The
// adapter sets Content[0].Inline to the failure string (for example
// path_outside_workspace).
func toolError(tr ToolResult, tool string) error {
	if !tr.IsError {
		return nil
	}
	msg := "tool reported an error"
	if len(tr.Content) > 0 && tr.Content[0].Inline != "" {
		msg = tr.Content[0].Inline
	}
	return fmt.Errorf("%s: %s", tool, msg)
}

// newCallID generates a unique §15.4.1 tool_call id with the
// recommended tc_ prefix.
func newCallID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "tc_" + hex.EncodeToString(b[:])
}

// AdapterToolsFrom returns the §15.4.1 adapter-local tool surface
// carried on ctx. It is non-nil inside OnMessage at every integration
// level. Handlers use it to call read_file, write_file, list_dir, and
// delete_file, or to emit an arbitrary tool_call.
func AdapterToolsFrom(ctx context.Context) *AdapterTools {
	a, _ := ctx.Value(ctxKeyAdapterTools).(*AdapterTools)
	return a
}
