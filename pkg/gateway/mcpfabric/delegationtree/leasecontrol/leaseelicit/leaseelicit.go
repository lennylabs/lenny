// SPDX-License-Identifier: MIT

// Package leaseelicit is the production §8.6 lease-extension Elicitor. It
// presents the generic budget elicitation to the requesting session's
// client over the §9.2 interaction store and the client event stream,
// and dismisses it when the requesting session ends.
package leaseelicit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// Publisher publishes a §7.2 client event to a session's event
// stream. *sessionevents.Bus.Publish satisfies it; tests pass a capture.
type Publisher func(sessionID, eventType, data string, now time.Time)

// SessionLookup is the narrow slice of sessionstore.Store the
// elicitor needs: resolving the requesting session's owning user for the
// §9.2 interaction triple and re-reading its state while the elicitation
// is pending. The full session store satisfies it.
type SessionLookup interface {
	Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error)
}

const (
	// leaseElicitMessage is the §8.6 generic budget prompt. It
	// names no token amounts, per the spec.
	leaseElicitMessage = "The agent needs more budget to continue. Approve?"
	// defaultLeaseElicitTimeout bounds how long ExtendLease blocks waiting
	// for the user to resolve a budget elicitation before treating it as a
	// non-decision (the request is not granted, the subtree is not denied).
	defaultLeaseElicitTimeout = 5 * time.Minute
	// defaultLeaseElicitPoll is the interaction-store poll cadence while
	// awaiting the user's decision, matching the §9.2 elicitation handler's
	// polling approach.
	defaultLeaseElicitPoll = 250 * time.Millisecond
)

// Dismissal reasons the elicitor records on the interaction.
const (
	// ReasonRequesterTerminal is the §8.6 dismissal reason recorded when
	// the requesting session reaches a terminal state before the user
	// decides.
	ReasonRequesterTerminal = "LEASE_EXTENSION_REQUESTER_TERMINAL"
	// ReasonTimeout is the dismissal reason recorded when the user does
	// not decide within the elicitation timeout.
	ReasonTimeout = "LEASE_EXTENSION_ELICITATION_TIMEOUT"
)

// Elicitor is the production leasecontrol.Elicitor. It presents the
// §8.6 generic budget elicitation to the requesting session's
// client over the §9.2 interaction store and the client event stream,
// then blocks until the user resolves it.
//
// The elicitation is a §9.2 KindElicitation interaction keyed to the
// requesting session and its owning user. The canonical resolution is the
// §7.2 approve/deny surface: PhaseApproved approves, PhaseDenied rejects.
// A PhaseResponded answer is interpreted by its action — an explicit
// decline rejects, any other engagement approves. A dismissal or a
// timeout is a non-decision, so the request is neither granted nor
// persisted as a denial, per the §8.6 Elicitor contract.
//
// The requesting session is read before the interaction is written and
// again on every poll tick. A terminal or missing requester before the
// write returns leasecontrol.ErrRequesterEnded with no interaction; one
// observed on a tick dismisses the interaction with
// LEASE_EXTENSION_REQUESTER_TERMINAL and returns ErrRequesterEnded, so
// the coordinator redrives the requests batched onto it. A lease
// elicitation carries no originPod, so the §9.2 expiry on leaving the pod
// never ends it and only this check notices the requester ending.
//
// spec: §8.6 (Elicitation mode flow), §9.2
type Elicitor struct {
	sessions     SessionLookup
	interactions interactionstore.Store
	publish      Publisher
	clock        func() time.Time
	idgen        func() string
	timeout      time.Duration
	poll         time.Duration
	// log records a session-store read error on a poll tick. Nil uses
	// slog.Default.
	log *slog.Logger
}

// Config carries an Elicitor's dependencies. Sessions, Interactions,
// Clock, and IDGen are required. A zero Timeout or Poll applies the
// package default, and a nil Log uses slog.Default.
type Config struct {
	Sessions     SessionLookup
	Interactions interactionstore.Store
	Publish      Publisher
	Clock        func() time.Time
	IDGen        func() string
	Timeout      time.Duration
	Poll         time.Duration
	Log          *slog.Logger
}

// New returns an Elicitor over cfg.
func New(cfg Config) *Elicitor {
	return &Elicitor{
		sessions:     cfg.Sessions,
		interactions: cfg.Interactions,
		publish:      cfg.Publish,
		clock:        cfg.Clock,
		idgen:        cfg.IDGen,
		timeout:      cfg.Timeout,
		poll:         cfg.Poll,
		log:          cfg.Log,
	}
}

// Elicit implements leasecontrol.Elicitor.
func (e *Elicitor) Elicit(ctx context.Context, tenantID, requestingSessionID string) (bool, error) {
	sess, err := e.liveRequester(ctx, tenantID, requestingSessionID)
	if err != nil {
		return false, err
	}
	id := e.idgen()
	now := e.clock()
	if err := e.interactions.Put(ctx, interactionstore.Interaction{
		ID:        id,
		Kind:      interactionstore.KindElicitation,
		SessionID: requestingSessionID,
		TenantID:  tenantID,
		UserID:    sess.UserID,
		Phase:     interactionstore.PhasePending,
		Detail:    map[string]any{"message": leaseElicitMessage, "reason": "lease_extension"},
		CreatedAt: now,
	}); err != nil {
		return false, fmt.Errorf("lease elicitation: record interaction: %w", err)
	}
	e.publishRequest(requestingSessionID, id, now)
	return e.awaitDecision(ctx, tenantID, requestingSessionID, sess.UserID, id)
}

// liveRequester reads the requesting session before the interaction is
// written. A terminal or missing row returns ErrRequesterEnded, and any
// other read error is returned wrapped.
// spec: §8.6 (Extension state) — no elicitation for a terminal session.
func (e *Elicitor) liveRequester(ctx context.Context, tenantID, sessionID string) (sessionstore.Session, error) {
	sess, err := e.sessions.Get(ctx, tenantID, sessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return sessionstore.Session{}, fmt.Errorf("lease elicitation: session %s not found: %w", sessionID, leasecontrol.ErrRequesterEnded)
	}
	if err != nil {
		return sessionstore.Session{}, fmt.Errorf("lease elicitation: resolve session %s: %w", sessionID, err)
	}
	if session.IsTerminal(sess.State) {
		return sessionstore.Session{}, fmt.Errorf("lease elicitation: session %s is %s: %w", sessionID, sess.State, leasecontrol.ErrRequesterEnded)
	}
	return sess, nil
}

// publishRequest surfaces the prompt on the session's stream as the
// canonical `elicitation_request` event. spec: §7.2.
func (e *Elicitor) publishRequest(sessionID, id string, now time.Time) {
	if e.publish == nil {
		return
	}
	payload, _ := json.Marshal(struct {
		ElicitationID string `json:"elicitationId"`
		Message       string `json:"message"`
		Reason        string `json:"reason"`
	}{ElicitationID: id, Message: leaseElicitMessage, Reason: "lease_extension"})
	e.publish(sessionID, "elicitation_request", string(payload), now)
}

// awaitDecision polls the interaction until the user resolves it, the
// requester ends, the timeout elapses, or ctx is cancelled.
func (e *Elicitor) awaitDecision(ctx context.Context, tenantID, sessionID, userID, id string) (bool, error) {
	timeout := e.timeout
	if timeout <= 0 {
		timeout = defaultLeaseElicitTimeout
	}
	poll := e.poll
	if poll <= 0 {
		poll = defaultLeaseElicitPoll
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	deadline := time.After(timeout)
	for {
		cur, err := e.interactions.Get(ctx, tenantID, sessionID, userID, id)
		if err != nil {
			return false, fmt.Errorf("lease elicitation: poll interaction %s: %w", id, err)
		}
		if approved, decided, err := leaseDecision(cur); decided {
			return approved, err
		}
		if e.requesterEnded(ctx, tenantID, sessionID) {
			return e.dismissForEndedRequester(ctx, tenantID, sessionID, userID, id)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline:
			e.dismissPending(ctx, tenantID, sessionID, userID, id, ReasonTimeout)
			return false, fmt.Errorf("lease elicitation %s timed out after %s", id, timeout)
		case <-ticker.C:
		}
	}
}

// leaseDecision classifies a polled interaction. decided is false while
// the interaction is pending.
func leaseDecision(cur interactionstore.Interaction) (approved, decided bool, err error) {
	switch cur.Phase {
	case interactionstore.PhaseApproved:
		return true, true, nil
	case interactionstore.PhaseDenied:
		return false, true, nil
	case interactionstore.PhaseResponded:
		return !elicitationDeclined(cur.Response), true, nil
	case interactionstore.PhaseDismissed:
		if cur.Reason == ReasonRequesterTerminal {
			return false, true, fmt.Errorf("lease elicitation %s dismissed: %s: %w", cur.ID, cur.Reason, leasecontrol.ErrRequesterEnded)
		}
		return false, true, fmt.Errorf("lease elicitation %s dismissed: %s", cur.ID, cur.Reason)
	}
	return false, false, nil
}

// requesterEnded re-reads the requesting session on a poll tick. A
// terminal or missing row reports true. A read error is logged and
// reports false, so polling continues.
// spec: §8.6 (Elicitation mode flow) step 2.
func (e *Elicitor) requesterEnded(ctx context.Context, tenantID, sessionID string) bool {
	sess, err := e.sessions.Get(ctx, tenantID, sessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return true
	}
	if err != nil {
		e.logger().WarnContext(ctx, "lease elicitation: requester read failed; polling continues",
			"tenant_id", tenantID, "session_id", sessionID, "error", err)
		return false
	}
	return session.IsTerminal(sess.State)
}

// dismissForEndedRequester dismisses the pending interaction with
// LEASE_EXTENSION_REQUESTER_TERMINAL and returns ErrRequesterEnded. A
// decision the user recorded before the dismissal wins and is returned.
// spec: §8.6 (Elicitation mode flow) step 2, §9.2.
func (e *Elicitor) dismissForEndedRequester(ctx context.Context, tenantID, sessionID, userID, id string) (bool, error) {
	cur, err := e.interactions.Resolve(ctx, tenantID, sessionID, userID, id, dismissIfPending(ReasonRequesterTerminal))
	if err != nil {
		return false, fmt.Errorf("lease elicitation %s: dismiss for ended requester: %v: %w", id, err, leasecontrol.ErrRequesterEnded)
	}
	if approved, decided, derr := leaseDecision(cur); decided {
		return approved, derr
	}
	return false, fmt.Errorf("lease elicitation %s left pending after dismissal: %w", id, leasecontrol.ErrRequesterEnded)
}

// dismissPending dismisses the interaction with reason when it is still
// pending. A store error leaves it for the §9.2 sweep.
func (e *Elicitor) dismissPending(ctx context.Context, tenantID, sessionID, userID, id, reason string) {
	_, _ = e.interactions.Resolve(ctx, tenantID, sessionID, userID, id, dismissIfPending(reason))
}

// dismissIfPending returns a Resolve mutator that moves a pending
// interaction to PhaseDismissed with reason and leaves a resolved one as
// it is.
func dismissIfPending(reason string) func(*interactionstore.Interaction) error {
	return func(i *interactionstore.Interaction) error {
		if i.Phase == interactionstore.PhasePending {
			i.Phase = interactionstore.PhaseDismissed
			i.Reason = reason
		}
		return nil
	}
}

func (e *Elicitor) logger() *slog.Logger {
	if e.log != nil {
		return e.log
	}
	return slog.Default()
}

// elicitationDeclined reports whether a §9.2 elicitation response carries
// an explicit decline. An MCP elicitation result names its outcome in an
// "action" field; "decline", "reject", and "cancel" are rejections. A
// bool false, or the strings "false"/"no"/"decline"/"reject", answer the
// yes/no prompt directly. Any other engaged response is treated as
// approval, since the user interacted with the prompt rather than
// dismissing it.
func elicitationDeclined(resp any) bool {
	switch v := resp.(type) {
	case bool:
		return !v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "false", "no", "decline", "reject":
			return true
		}
	case map[string]any:
		if a, ok := v["action"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(a)) {
			case "decline", "reject", "cancel":
				return true
			}
		}
		if approved, ok := v["approved"].(bool); ok {
			return !approved
		}
	}
	return false
}
