// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/messagerouting"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// runPreMessageDelivery runs the §4.8 PreMessageDelivery phase over a
// lenny/send_message body before the message is classified: the target's
// interceptor weakening cooldown, its contentPolicy.maxInputSize bound, and
// the interceptor chain. A refusal returns the tool error and nothing is held,
// enqueued, or sent. A MODIFY returns the rewritten body as the content every
// destination carries; modified reports it. The §15.4 union content is
// projected to its text form for the scan and the bound.
//
// spec: §4.8 (Gateway Policy Engine), §13.5, §7.2 (Message delivery routing —
// seven paths, path 6), §15.4 (MessageEnvelope.input)
func runPreMessageDelivery(ctx context.Context, deps Deps, tenant, sessionID string, msg sessionrecord.MessageContent) (sessionrecord.MessageContent, bool, error) {
	if deps.Interceptors == nil {
		return msg, false, nil
	}
	text := msg.Text()
	req := interceptor.Request{
		Phase:     interceptor.PhasePreMessageDelivery,
		SessionID: sessionID,
		TenantID:  tenant,
		Content:   []byte(text),
	}
	var res interceptor.Result
	if deps.ContentPolicies != nil {
		ref := ""
		if maxSize, r, ok := deps.ContentPolicies.ResolveContentPolicy(ctx, tenant, sessionID); ok {
			ref = r
			// spec: §4.8 / §8.3 (SEC-013, F-4.8.17) — refuse while the
			// target's interceptor is inside the fail-closed → fail-open
			// weakening cooldown.
			if deps.CooldownChecker != nil && ref != "" {
				if cdErr := deps.CooldownChecker.InterceptorFailPolicyCooldown(ctx, ref); cdErr != nil {
					var wc *delegation.InterceptorWeakeningCooldownError
					if errors.As(cdErr, &wc) {
						return msg, false, cooldownToolError(wc)
					}
				}
			}
			if maxSize > 0 && len(text) > maxSize {
				return msg, false, mcp.NewToolError(policy.CodeInputTooLarge,
					fmt.Sprintf("message body is %d bytes, exceeding the target session's contentPolicy.maxInputSize limit of %d bytes", len(text), maxSize),
					map[string]any{"phase": string(interceptor.PhasePreMessageDelivery)})
			}
		}
		res = deps.Interceptors.RunPolicyScoped(ctx, req, ref)
	} else {
		res = deps.Interceptors.Run(ctx, req)
	}
	switch res.Action {
	case interceptor.ActionReject:
		recordChainRejection(ctx, deps, tenant, sessionID, interceptor.PhasePreMessageDelivery, res)
		// spec: §15.2.1 rule 3 — a deliberate REJECT is a policy decision;
		// a built-in code on the Result is surfaced verbatim.
		code := "INTERCEPTOR_REJECTED"
		if res.Code != "" {
			code = res.Code
		}
		return msg, false, mcp.NewToolError(code,
			fmt.Sprintf("message delivery rejected by policy: %s", res.Reason), nil)
	case interceptor.ActionModify:
		return sessionrecord.MessageContentFromText(string(res.ModifiedContent)), true, nil
	default:
		return msg, false, nil
	}
}

// mcpSend carries one lenny/send_message delivery through the §7.2 paths
// after the precondition checks and the PreMessageDelivery phase.
type mcpSend struct {
	deps      Deps
	tenant    string
	messageID string
	senderID  string
	content   sessionrecord.MessageContent
	modified  bool
	clock     func() time.Time
	// unlock releases the session's delivery lock; it is idempotent.
	unlock func()
	// rerouted records that a sealed refusal already routed the message
	// once from a primary re-read.
	rerouted bool
}

// deliver takes the target's delivery lock, honoring ctx, classifies the
// message on the primary row, and runs the path the classification selects.
// A missing row answers the session lookup error; a primary error classifies
// the entry row and logs at WARN.
// spec: §7.2 (Message delivery routing — seven paths, path 6), §12.3
// (Postgres HA Requirements)
func (m *mcpSend) deliver(ctx context.Context, entry sessionstore.Session) (mcp.ToolResult, error) {
	m.unlock = func() {}
	if m.deps.Delivery != nil {
		unlock, err := m.deps.Delivery.LockDelivery(ctx, entry.ID)
		if err != nil {
			return mcp.ToolResult{}, fmt.Errorf("lock delivery of session %s: %w", entry.ID, err)
		}
		m.unlock = unlock
	}
	defer func() { m.unlock() }()
	row, err := sessionstore.ReadPrimary(ctx, m.deps.Store, m.deps.PrimaryReadTimeout, m.tenant, entry.ID)
	switch {
	case errors.Is(err, sessionstore.ErrNotFound):
		return mcp.ToolResult{}, errSessionLookup(err)
	case err != nil:
		slog.Warn("mcptools: classify a message on the entry row; the primary read failed",
			"session_id", entry.ID, "error", err)
		row = entry
	}
	return m.route(ctx, row)
}

// route classifies row and runs the path the classification selects.
func (m *mcpSend) route(ctx context.Context, row sessionstore.Session) (mcp.ToolResult, error) {
	inputRequired := row.State == session.StateInputRequired ||
		(m.deps.InputWaits != nil && len(m.deps.InputWaits.PendingForSession(row.ID)) > 0)
	decision := messagerouting.Classify(row.State, inputRequired, false, messagerouting.SourceInterSession)
	switch decision.Action {
	case messagerouting.ActionRejectTerminal:
		return mcp.ToolResult{}, mcp.NewToolError("TARGET_TERMINAL",
			fmt.Sprintf("session %s is terminal (%s)", row.ID, row.State), nil)
	case messagerouting.ActionBufferInbox:
		return m.enqueueInbox(ctx, row)
	case messagerouting.ActionBufferDLQ:
		res, _, err := m.hold(ctx, row)
		return res, err
	case messagerouting.ActionResumeAndDeliver:
		return m.holdAndResume(ctx, row)
	default:
		return m.sendNow(ctx, row)
	}
}

// heldMessage builds the inbox or DLQ entry for the message. The entry holds
// the session server's message payload, so the delivery of held messages
// decodes an MCP-held message as it decodes a REST one; the §15.4 union
// content keeps its wire form, or carries the MODIFY result.
// spec: §15.4 (MessageEnvelope.input), §7.2 (Message delivery routing —
// seven paths, path 6)
func (m *mcpSend) heldMessage() (sessioninbox.Message, error) {
	payload, err := json.Marshal(sessionserver.MessagePayload{
		ID:      m.messageID,
		Role:    "user",
		Content: m.content,
	})
	if err != nil {
		return sessioninbox.Message{}, fmt.Errorf("marshal held message content: %w", err)
	}
	return sessioninbox.Message{
		MessageID:       m.messageID,
		SenderSessionID: m.senderID,
		Payload:         payload,
		EnqueuedAt:      m.clock(),
	}, nil
}

// receipt answers the message with a delivery receipt of status and reason.
func (m *mcpSend) receipt(status session.DeliveryStatus, reason session.DeliveryReason) mcp.ToolResult {
	return textResult(buildSendMessageReceiptStatusReason(m.messageID, status, reason, m.clock()))
}

// enqueueInbox is §7.2 path 3: an input_required target buffers the message
// in its inbox.
func (m *mcpSend) enqueueInbox(ctx context.Context, row sessionstore.Session) (mcp.ToolResult, error) {
	if m.deps.Messaging == nil {
		return m.receipt(session.DeliveryStatusError, session.DeliveryReasonInboxUnavailable), nil
	}
	msg, err := m.heldMessage()
	if err != nil {
		return mcp.ToolResult{}, err
	}
	evicted, err := m.deps.Messaging.EnqueueInbox(ctx, m.tenant, row.ID, msg)
	switch {
	case err != nil:
		return m.receipt(session.DeliveryStatusError, session.DeliveryReasonInboxUnavailable), nil
	case evicted != nil:
		return m.receipt(session.DeliveryStatusDropped, session.DeliveryReasonInboxOverflow), nil
	default:
		return m.receipt(session.DeliveryStatusQueued, ""), nil
	}
}

// hold holds the message in the session's DLQ and answers queued, dropped
// with dlq_overflow, TARGET_TERMINAL, or error with inbox_unavailable. held
// reports a queued or dropped answer. With no delivery seam the answer is
// error with inbox_unavailable.
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func (m *mcpSend) hold(ctx context.Context, row sessionstore.Session) (res mcp.ToolResult, held bool, err error) {
	if m.deps.Delivery == nil {
		return m.receipt(session.DeliveryStatusError, session.DeliveryReasonInboxUnavailable), false, nil
	}
	msg, err := m.heldMessage()
	if err != nil {
		return mcp.ToolResult{}, false, err
	}
	outcome, state, err := m.deps.Delivery.HoldMessage(ctx, m.tenant, row.ID, msg)
	switch {
	case err != nil:
		return m.receipt(session.DeliveryStatusError, session.DeliveryReasonInboxUnavailable), false, nil
	case outcome == sessionserver.HoldTerminal:
		return mcp.ToolResult{}, false, mcp.NewToolError("TARGET_TERMINAL",
			fmt.Sprintf("session %s is terminal (%s)", row.ID, state), nil)
	case outcome == sessionserver.HoldDropped:
		return m.receipt(session.DeliveryStatusDropped, session.DeliveryReasonDLQOverflow), true, nil
	default:
		return m.receipt(session.DeliveryStatusQueued, ""), true, nil
	}
}

// holdAndResume is §7.2 path 6 on MCP: the message to a `suspended` target
// is held in the DLQ and, unless the hold found the session terminal or
// failed, the delivery lock is released and the session resumed with the
// classified row. A resume error, a cap refusal at the resume write
// included, leaves the answer queued. The path never enqueues to the inbox
// or sends to the runtime for a suspended target.
//
// Before the hold the resume admission check runs on the classified row: a
// cap refusal answers the QUOTA_EXCEEDED tool error with the refusal's
// details, any other check error is the tool's Go error, and the message is
// not held.
// spec: §7.2 (Message delivery routing — seven paths, path 6), §11.1
// (Resume admission)
func (m *mcpSend) holdAndResume(ctx context.Context, row sessionstore.Session) (mcp.ToolResult, error) {
	if m.deps.Delivery != nil {
		if err := m.deps.Delivery.AdmitResume(ctx, row); err != nil {
			var refusal *sessionserver.CapRefusal
			if errors.As(err, &refusal) {
				return mcp.ToolResult{}, mcp.NewToolError("QUOTA_EXCEEDED", refusal.Message(), refusal.Details())
			}
			return mcp.ToolResult{}, fmt.Errorf("resume admission check for session %s: %w", row.ID, err)
		}
	}
	res, held, err := m.hold(ctx, row)
	if err != nil || !held {
		return res, err
	}
	m.unlock()
	row.State = session.StateSuspended
	if rerr := m.deps.Delivery.ResumeSuspended(ctx, row); rerr != nil {
		slog.Info("mcptools: resume a suspended session a message is held for", "session_id", row.ID, "error", rerr)
	}
	return res, nil
}

// sendNow is §7.2 path 2: the session's held messages are delivered first,
// and then the message is sent. An error of either is an error of the send,
// except the adapter's sealed refusal, which routes the message again.
// spec: §7.2 (Message delivery routing — seven paths, path 6), §6.2 (Pod
// State Machine)
func (m *mcpSend) sendNow(ctx context.Context, row sessionstore.Session) (mcp.ToolResult, error) {
	if m.deps.Executor == nil {
		return mcp.ToolResult{}, errors.New("no executor configured")
	}
	if m.deps.Delivery != nil {
		if err := m.deps.Delivery.DeliverHeld(ctx, row); err != nil {
			if errors.Is(err, executor.ErrSessionSealed) {
				return m.rerouteSealed(ctx, row)
			}
			return mcp.ToolResult{}, err
		}
	}
	// spec: §15.4 / §13.5 mitigation 6 — the gateway stamps the delivered
	// envelope's `from` from the authenticated sending session.
	resp, err := m.deps.Executor.Send(ctx, row.ID, []executor.Message{
		{Role: "user", Content: m.content.Text(), From: executor.SenderFrom(m.senderID)},
	})
	if errors.Is(err, executor.ErrSessionSealed) {
		// A refusal after the runtime received the message delivered it;
		// only an undelivered message routes again.
		var sealed *executor.SealedError
		if errors.As(err, &sealed) && sealed.Delivered > 0 {
			return m.deliveredResult(ctx, row, sealed.Response)
		}
		return m.rerouteSealed(ctx, row)
	}
	if err != nil {
		return mcp.ToolResult{}, err
	}
	return m.deliveredResult(ctx, row, resp)
}

// rerouteSealed routes a message the adapter refused as sealed once more,
// from the classification of a primary re-read; a re-read failure or a
// second refusal takes the held branch, which holds the message and attempts
// the guarded resume_pending write.
// spec: §7.2 (Message delivery routing — seven paths, path 6), §6.2 (Pod
// State Machine)
func (m *mcpSend) rerouteSealed(ctx context.Context, row sessionstore.Session) (mcp.ToolResult, error) {
	if !m.rerouted {
		m.rerouted = true
		fresh, err := sessionstore.ReadPrimary(ctx, m.deps.Store, m.deps.PrimaryReadTimeout, m.tenant, row.ID)
		if err == nil {
			return m.route(ctx, fresh)
		}
	}
	return m.holdAndResume(ctx, row)
}

// deliveredResult runs the §4.8 PostAgentOutput chain over the response and
// answers the delivered receipt followed by the runtime's text output.
func (m *mcpSend) deliveredResult(ctx context.Context, row sessionstore.Session, resp executor.Response) (mcp.ToolResult, error) {
	out, rej := applyPostAgentOutput(ctx, m.deps, m.tenant, row.ID, resp.Parts)
	if rej != nil {
		code := rej.Code
		if code == "" {
			code = "INTERCEPTOR_REJECTED"
		}
		return mcp.ToolResult{}, mcp.NewToolError(code, rej.Reason, nil)
	}
	// spec: §15.4 — the delivery_receipt is the first text block; the
	// runtime's text output follows.
	content := make([]mcp.ToolContent, 0, 1+len(out))
	content = append(content, mcp.ToolContent{
		Type: "text",
		Text: buildSendMessageReceipt(m.messageID, "", out, m.clock()),
	})
	for _, p := range out {
		if p.Type == "text" {
			content = append(content, mcp.ToolContent{Type: "text", Text: p.Text})
		}
	}
	return mcp.ToolResult{Content: content}, nil
}

// deliverChildHeld delivers a delegated child's held messages after its task
// input: under the child's delivery lock it reads the child from the primary
// and delivers only when the child is `running`. A lock, read, or delivery
// error is logged and leaves the tool result unchanged. It does nothing when
// no materializer or no delivery seam is wired.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6); §8.2
// (Delegation Mechanism)
func deliverChildHeld(ctx context.Context, deps Deps, tenant, childID string) {
	if deps.ChildMaterializer == nil || deps.Delivery == nil {
		return
	}
	unlock, err := deps.Delivery.LockDelivery(ctx, childID)
	if err != nil {
		slog.Warn("mcptools: lock delivery of a delegated child", "session_id", childID, "error", err)
		return
	}
	defer unlock()
	child, err := sessionstore.ReadPrimary(ctx, deps.Store, deps.PrimaryReadTimeout, tenant, childID)
	if err != nil {
		slog.Warn("mcptools: read a delegated child before delivering its held messages", "session_id", childID, "error", err)
		return
	}
	if child.State != session.StateRunning {
		return
	}
	if err := deps.Delivery.DeliverHeld(ctx, child); err != nil {
		slog.Warn("mcptools: deliver a delegated child's held messages", "session_id", childID, "error", err)
	}
}
