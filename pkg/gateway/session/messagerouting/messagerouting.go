// SPDX-License-Identifier: MIT

// Package messagerouting implements the §7.2 message-delivery routing precedence. It is the single
// path-selection function shared by the REST `POST /v1/sessions/{id}/messages`
// handler and the MCP `lenny/send_message` tool so the two surfaces
// route a message to the same destination for the same target state.
//
// The function is pure: it maps a target session state plus the
// observable `input_required` condition, the `delivery: "immediate"`
// flag, and the message source onto the destination action and the
// §15.4 delivery-receipt status. The caller performs the action
// (resolve / deliver / buffer / reject) against its own machinery.
//
// spec: §7.2.
package messagerouting

import "github.com/lennylabs/lenny/pkg/api/v1/session"

// Source distinguishes an external client `POST /v1/sessions/{id}/messages`
// call from an inter-session `lenny/send_message`. The §7.2 pre-running row branches on it: an external client is rejected with
// TARGET_NOT_READY while a parent→child inter-session message is
// buffered in the DLQ until the child reaches `running`.
type Source int

const (
	// SourceExternal is the REST `POST /v1/sessions/{id}/messages` path.
	SourceExternal Source = iota
	// SourceInterSession is the `lenny/send_message` parent→child /
	// sibling path.
	SourceInterSession
)

// Action names the destination the gateway routes a message to once the
// §7.2 path has been selected. The caller maps each action onto the
// machinery it has wired (the inputwait registry, the executor, the
// session inbox, or the DLQ).
type Action int

const (
	// ActionDeliver routes the message to the runtime now (§7.2 path 2:
	// the runtime is reading from stdin / `ready_for_input`). The
	// receipt is `delivered` once the runtime consumes it.
	ActionDeliver Action = iota

	// ActionBufferInbox buffers the message in the session inbox for
	// FIFO redelivery when the runtime next reaches `ready_for_input`
	// (§7.2 path 3 input_required and path 5 runtime-busy). The receipt
	// is `queued`.
	ActionBufferInbox

	// ActionBufferDLQ holds the message in the dead-letter queue (§7.2
	// path 7 recovering target, plus the pre-running inter-session row of
	// the dead-letter table). The receipt is `queued`; the message is
	// delivered when the target reaches `running` before the DLQ TTL
	// elapses.
	ActionBufferDLQ

	// ActionRejectTerminal rejects the message because the target is in
	// a terminal state (§7.2 dead-letter table terminal row): the
	// gateway returns TARGET_TERMINAL and does not enqueue.
	ActionRejectTerminal

	// ActionRejectNotReady rejects an external-client message against a
	// pre-running target (§7.2 pre-running row, external
	// client): the gateway returns TARGET_NOT_READY so the client
	// retries after the session starts.
	ActionRejectNotReady

	// ActionResumeAndDeliver is §7.2 path 6 for every `suspended` target,
	// whatever `delivery: "immediate"` is. On REST, the coordinating
	// replica of a session that holds its pod resumes it on that pod,
	// delivers the held messages, and sends the message (`delivered`);
	// otherwise the message is held in the DLQ and the session moves to
	// resume_pending (`queued`). On MCP, the message is held and the
	// session resumed (`queued`).
	// spec: §7.2 (Message delivery routing — seven paths, path 6).
	ActionResumeAndDeliver
)

// Decision is the routing outcome for one non-reply message. Path is
// the §7.2 path number (1-7) for logging and test fidelity; Action is
// the destination; Status is the §15.4 delivery-receipt status the
// caller stamps on the synchronous receipt (empty for the reject
// actions, which surface a typed error instead).
type Decision struct {
	Path   int
	Action Action
	Status session.DeliveryStatus
}

// isRecovering reports whether s is one of the §7.2 recovering states the
// dead-letter table routes to the DLQ. `resuming` is one: a session the
// resume driver is restoring has no runtime reading its input.
func isRecovering(s session.State) bool {
	switch s {
	case session.StateResumePending, session.StateAwaitingClientAction, session.StateResuming:
		return true
	}
	return false
}

// isPreRunning reports whether s is one of the four §7.2 pre-running states.
func isPreRunning(s session.State) bool {
	switch s {
	case session.StateCreated, session.StateFinalizing, session.StateReady, session.StateStarting:
		return true
	}
	return false
}

// Classify selects the §7.2 message-delivery path for a message that
// did not match path 1 (an `inReplyTo` resolving an outstanding
// `lenny/request_input`; the caller resolves that before classifying,
// because path 1 wins over every other path).
//
// The precedence below is the spec's "first matching path wins" order,
// restricted to the conditions the caller can observe from the session
// row, the inputwait registry, and the message envelope:
//
//   - Terminal target → reject TARGET_TERMINAL (path 7).
//   - Recovering target (resume_pending, awaiting_client_action, or
//     resuming) → DLQ, queued (path 7).
//   - Pre-running target → external client TARGET_NOT_READY, or
//     inter-session DLQ, queued (dead-letter table pre-running row).
//   - Suspended (path 6) → ActionResumeAndDeliver, whatever immediate
//     is and whatever the inputwait registry holds: a suspended row with
//     a pending lenny/request_input wait still resumes.
//   - input_required (path 3) → inbox, queued. Path 3 wins over the
//     `delivery: "immediate"` escalation (path 4).
//   - Running, not input_required (path 2) → deliver now. The path-5
//     runtime-busy buffering and the path-4 in-flight-tool interrupt
//     require the adapter `ready_for_input` signal; a synchronous
//     in-process executor is ready by construction, so this collapses
//     to direct delivery.
//
// immediate is the `delivery: "immediate"` flag. For a running target it
// selects path 4 over path 2 when the adapter exposes an in-flight-tool
// signal, and otherwise does not change the destination.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func Classify(state session.State, inputRequired, immediate bool, src Source) Decision {
	switch {
	case session.IsTerminal(state):
		return Decision{Path: 7, Action: ActionRejectTerminal}
	case isRecovering(state):
		return Decision{Path: 7, Action: ActionBufferDLQ, Status: session.DeliveryStatusQueued}
	case isPreRunning(state):
		if src == SourceExternal {
			return Decision{Path: 7, Action: ActionRejectNotReady}
		}
		// Inter-session parent→child: the parent knows the child will
		// start, so buffer in the DLQ until it reaches running.
		return Decision{Path: 7, Action: ActionBufferDLQ, Status: session.DeliveryStatusQueued}
	case state == session.StateSuspended:
		// §7.2 path 6: a message to a suspended session resumes it,
		// whatever the input-wait registry holds. An interrupt of a
		// runtime blocked in lenny/request_input leaves the wait
		// registered on a suspended row; routing that row to the inbox
		// would never resume it. The caller answers delivered or queued
		// from the branch it takes.
		return Decision{Path: 6, Action: ActionResumeAndDeliver, Status: session.DeliveryStatusQueued}
	case state == session.StateInputRequired || inputRequired:
		return Decision{Path: 3, Action: ActionBufferInbox, Status: session.DeliveryStatusQueued}
	default:
		// Running (or any other admitted non-blocked state): deliver now.
		return Decision{Path: 2, Action: ActionDeliver, Status: session.DeliveryStatusDelivered}
	}
}
