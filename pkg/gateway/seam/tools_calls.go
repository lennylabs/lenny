// SPDX-License-Identifier: MIT

package seam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/session/state"
)

// The forward kinds of the platform tools seam's tool calls. The session
// core reads each to pick the session states the forward applies to.
const (
	// ForwardTerminate is the forward kind of a call that ends a session,
	// such as lenny/cancel_session.
	ForwardTerminate ForwardKind = "terminate"
	// ForwardMessage is the forward kind of a message that answers an
	// input request, which fails closed when its forward is not delivered.
	ForwardMessage ForwardKind = "message"
	// ForwardMessageNoReplyTo is the forward kind of a message that answers
	// no input request. Such a message is held by its sender when the forward
	// to a running session does not arrive.
	ForwardMessageNoReplyTo ForwardKind = "message-no-reply-to"
)

// TargetNotReady returns the refusal of a tool call whose session's holder
// replica could not be reached: the TARGET_NOT_READY code with the
// session's current state. The session core returns it from
// GWSEAM-ForwardToolCall, and the platform tools seam returns it when its
// held delivery finds that another replica now coordinates the session, so
// both report the same refusal.
func TargetNotReady(current state.State) *Refusal {
	return &Refusal{
		Code:    "TARGET_NOT_READY",
		Message: "the session's coordinating gateway replica could not be reached; retry the tool call",
		Details: map[string]any{"currentState": string(current)},
	}
}

// SessionAccess is served by the session core to the platform tools seam:
// the ownership rule applied to a session record the seam read itself.
type SessionAccess interface {
	// SessionAccessible is GWSEAM-SessionAccessible. It reports false for a
	// context that carries no caller subject.
	SessionAccessible(ctx context.Context, s Session, action SessionAction) bool
}

// TreeBudgetExpiry is served by the session core to the platform tools
// seam's delegation service.
type TreeBudgetExpiry interface {
	// HandleBudgetKeysExpired is GWSEAM-HandleBudgetKeysExpired. It returns
	// at once when another call for the same tree is running.
	HandleBudgetKeysExpired(ctx context.Context, tenantID, rootSessionID string)
}

// lennyErrorContentType is the content block type that carries a lenny
// error envelope inside an isError tool result.
const lennyErrorContentType = "lenny/error"

// lennyError is the lenny error envelope of a tool error.
type lennyError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// DecodeToolCallResponse decodes the body of a tools/call JSON-RPC
// response into the tool result it carries, or into the refusal its lenny
// error envelope carries: the error data of a JSON-RPC error, or the
// lenny/error content block of an isError result. It returns an error for
// a body that is not a JSON-RPC 2.0 response, that carries neither a
// result nor an error, or whose error carries no lenny error envelope. The
// session core decodes a forwarded tool call's response with it, and the
// platform tools seam's MCP transport decodes its own responses with it.
func DecodeToolCallResponse(body []byte) (ToolResult, *Refusal, error) {
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Data json.RawMessage `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ToolResult{}, nil, fmt.Errorf("decode tools/call response: %w", err)
	}
	if resp.JSONRPC != "2.0" {
		return ToolResult{}, nil, fmt.Errorf("decode tools/call response: jsonrpc is %q, want \"2.0\"", resp.JSONRPC)
	}
	if resp.Error != nil {
		r, err := decodeLennyError(resp.Error.Data)
		if err != nil {
			return ToolResult{}, nil, fmt.Errorf("decode tools/call error data: %w", err)
		}
		return ToolResult{}, r, nil
	}
	if len(resp.Result) == 0 {
		return ToolResult{}, nil, errors.New("decode tools/call response: no result and no error")
	}
	var result struct {
		Content json.RawMessage `json:"content"`
		IsError bool            `json:"isError,omitempty"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return ToolResult{}, nil, fmt.Errorf("decode tools/call result: %w", err)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	if len(result.Content) > 0 && string(result.Content) != "null" {
		if err := json.Unmarshal(result.Content, &blocks); err != nil {
			return ToolResult{}, nil, fmt.Errorf("decode tools/call result: %w", err)
		}
	}
	if !result.IsError {
		return ToolResult{Content: result.Content}, nil, nil
	}
	for _, block := range blocks {
		if block.Type == lennyErrorContentType {
			r, err := decodeLennyError(json.RawMessage(block.Text))
			if err != nil {
				return ToolResult{}, nil, fmt.Errorf("decode tools/call lenny/error block: %w", err)
			}
			return ToolResult{}, r, nil
		}
	}
	return ToolResult{}, nil, errors.New("decode tools/call result: isError result carries no lenny/error block")
}

// decodeLennyError decodes a lenny error envelope into a refusal. An
// envelope with no code is not one.
func decodeLennyError(raw json.RawMessage) (*Refusal, error) {
	var env lennyError
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if env.Code == "" {
		return nil, errors.New("lenny error envelope has no code")
	}
	return &Refusal{Code: env.Code, Message: env.Message, Details: env.Details}, nil
}
