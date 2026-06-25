// SPDX-License-Identifier: MIT

// Command delegate is a Standard-level Lenny agent runtime built on the
// Go runtime-author SDK (sdks/runtime/go/runtime). It shows the §8.5
// delegation flow expressed through the SDK's typed platform MCP tool
// helpers, and is the SDK counterpart of the no-SDK reference runtime
// at cmd/runtimes/delegation-echo.
//
// The runtime is started with WithStandardLevel, so the SDK reads the
// adapter manifest, dials the platform MCP server and every connector
// MCP server with the §15.4.3 manifest-nonce handshake, and exposes the
// platform tool surface through runtime.ToolsFrom. On each inbound
// message the handler delegates a sub-task, awaits the child, confirms
// the result via request_input, emits the child output via
// lenny/output, and returns the child parts as the turn response.
//
// When no adapter manifest is present the SDK cannot reach Standard
// level; ToolsFrom returns nil and the handler degrades to a plain
// echo so the binary still runs in Basic-only test paths.
//
// Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol
// error.
package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

const (
	exitOK            = 0
	exitRuntimeError  = 1
	exitProtocolError = 2
)

// delegateHandler is a Standard-level runtime.Handler.
type delegateHandler struct {
	seq atomic.Uint64
}

// OnCreate has no task-scoped setup. The SDK has already dialed the
// platform MCP server and connector MCP servers by the time OnCreate
// runs.
func (h *delegateHandler) OnCreate(context.Context, runtime.CreateRequest) error {
	return nil
}

// OnMessage runs the §8.5 delegation flow through the SDK platform
// tool helpers. Without a platform MCP server it echoes the input.
func (h *delegateHandler) OnMessage(ctx context.Context, msg runtime.Message) (runtime.Reply, error) {
	n := h.seq.Add(1)
	tools := runtime.ToolsFrom(ctx)
	if tools == nil {
		// Basic-level fallback: no platform MCP server in the manifest.
		return runtime.Reply{Parts: echoParts(msg.Envelope.Input, n), Final: true}, nil
	}

	// 1. lenny/delegate_task — spawn a child whose input is this
	//    message's input parts.
	handle, err := tools.DelegateTask("delegate-child", echoParts(msg.Envelope.Input, n), nil)
	if err != nil {
		return delegationError(err), nil
	}

	// 2. lenny/await_children — wait for the child to settle.
	results, err := tools.AwaitChildren([]string{handle.TaskID}, "all")
	if err != nil {
		return delegationError(err), nil
	}
	if len(results) == 0 || results[0].State != "completed" {
		return delegationError(fmt.Errorf("child did not complete")), nil
	}
	childParts := results[0].Output.Parts

	// 3. lenny/request_input — confirm the echoed result.
	if _, err := tools.RequestInput([]runtime.MessagePart{
		runtime.Text(fmt.Sprintf("confirm echo of child %s?", handle.TaskID)),
	}); err != nil {
		return delegationError(err), nil
	}

	// 4. lenny/output — emit the child output to the parent or client.
	//    The response below still signals turn completion (§15.4.1).
	if err := tools.Output(childParts); err != nil {
		return delegationError(err), nil
	}
	return runtime.Reply{Parts: childParts, Final: true}, nil
}

// OnTerminate has no teardown.
func (h *delegateHandler) OnTerminate(context.Context, runtime.TerminationReason) error {
	return nil
}

// echoParts prefixes text parts with the per-session sequence number.
func echoParts(in []runtime.MessagePart, seq uint64) []runtime.MessagePart {
	out := make([]runtime.MessagePart, 0, len(in))
	for _, p := range in {
		if p.Type == "text" && p.Inline != "" {
			out = append(out, runtime.Text(fmt.Sprintf("[delegate seq=%d] %s", seq, p.Inline)))
			continue
		}
		out = append(out, p)
	}
	return out
}

// delegationError builds a Final Reply carrying a structured error so
// the adapter records the failure without losing context (§15.4.1).
func delegationError(err error) runtime.Reply {
	return runtime.Reply{
		Error: &runtime.ResponseError{Code: "DELEGATION_FAILED", Message: err.Error()},
		Final: true,
	}
}

func main() {
	err := runtime.Run(&delegateHandler{}, runtime.WithStandardLevel())
	if err == nil {
		os.Exit(exitOK)
	}
	fmt.Fprintln(os.Stderr, err)
	if runtime.ErrIsProtocol(err) {
		os.Exit(exitProtocolError)
	}
	os.Exit(exitRuntimeError)
}
