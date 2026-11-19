// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/seamrow"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// The helpers below call the session core through the seam interface with
// the rows the tools read from the session store, and turn the core's
// answers back into the tools' own forms.

// afterTerminalWrite runs the session core's post-terminal hook,
// GWSEAM-AfterTerminalWrite, on the row a cancel tool wrote, with the
// tool's signals on the row the hook passes them, and returns the row the
// hook returned.
func afterTerminalWrite(ctx context.Context, t seam.Terminals, from session.State, row sessionstore.Session,
	signals func(context.Context, sessionstore.Session),
) sessionstore.Session {
	var hook func(context.Context, seam.Session)
	if signals != nil {
		hook = func(ctx context.Context, s seam.Session) { signals(ctx, seamrow.Row(s)) }
	}
	return seamrow.Row(t.AfterTerminalWrite(ctx, state.State(from), seamrow.Record(row), hook))
}

// forwardToolCall forwards a tool call on row to the session's holder
// replica through GWSEAM-ForwardToolCall, and returns the holder's result,
// or its refusal as the tool error it carries.
func forwardToolCall(ctx context.Context, f seam.Forwarding, row sessionstore.Session, kind seam.ForwardKind, tool string, args json.RawMessage) (mcp.ToolResult, seam.ForwardOutcome, error) {
	res, outcome, err := f.ForwardToolCall(ctx, seamrow.Record(row), kind, tool, args)
	if r, ok := seam.IsRefusal(err); ok {
		return mcp.ToolResult{}, outcome, mcp.RefusalError(r)
	}
	if err != nil {
		return mcp.ToolResult{}, outcome, err
	}
	if outcome != seam.ForwardDelivered {
		return mcp.ToolResult{}, outcome, nil
	}
	out, derr := mcp.FromSeamResult(res)
	if derr != nil {
		return mcp.ToolResult{}, outcome, derr
	}
	return out, outcome, nil
}

// heldMessagePayload is the client API message envelope a held message
// carries, which the session core's delivery of held messages decodes as it
// decodes a message of the REST message path.
type heldMessagePayload struct {
	ID      string                       `json:"id,omitempty"`
	Role    string                       `json:"role,omitempty"`
	Content sessionrecord.MessageContent `json:"content"`
}

// adapterCapabilities is the AdapterCapabilities block of the external
// protocol adapters, in the JSON form every discovery response embeds. The
// session core's runtime discovery encodes the same block from its own
// type; the platform tools seam encodes the MCP adapter's block with this
// one, which carries the same fields and JSON names.
type adapterCapabilities struct {
	PathPrefix                string `json:"pathPrefix"`
	Protocol                  string `json:"protocol"`
	SupportsSessionContinuity bool   `json:"supportsSessionContinuity"`
	SupportsDelegation        bool   `json:"supportsDelegation"`
	SupportsElicitation       bool   `json:"supportsElicitation"`
	SupportsInterrupt         bool   `json:"supportsInterrupt"`
}

// senderMessage returns the user message an inter-session sender sent,
// attributed to the sending session as an agent. A send with no sender
// session carries no attribution, so the session core's executor stamps its
// default gateway-client identity. The attribution is the one the core
// gives a held message when it delivers it.
func senderMessage(text, senderID string) seam.Message {
	m := seam.Message{Role: "user", Content: text}
	if senderID != "" {
		m.FromKind, m.FromID = "agent", senderID
	}
	return m
}

// capRefusalError returns the QUOTA_EXCEEDED tool error of a resume that a
// tenant limit refused, or nil when err is no such refusal.
func capRefusalError(err error) *mcp.ToolError {
	var refusal *seam.CapRefusal
	if errors.As(err, &refusal) {
		return mcp.NewToolError("QUOTA_EXCEEDED", refusal.Message, refusal.Details)
	}
	return nil
}
