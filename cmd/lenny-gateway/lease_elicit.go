// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// leaseElicitPublisher publishes a §7.2 client event to a session's event
// stream. *sessionevents.Bus.Publish satisfies it; tests pass a capture.
type leaseElicitPublisher func(sessionID, eventType, data string, now time.Time)

// leaseSessionLookup is the narrow slice of sessionstore.Store the
// elicitor needs: resolving the requesting session's owning user for the
// §9.2 interaction triple. The full session store satisfies it.
type leaseSessionLookup interface {
	Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error)
}

const (
	// leaseElicitMessage is the §8.6 line 718 generic budget prompt. It
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

// leaseElicitor is the production leasecontrol.Elicitor. It presents the
// §8.6 line 718 generic budget elicitation to the requesting session's
// client over the §9.2 interaction store and the client event stream,
// then blocks until the user resolves it.
//
// The elicitation is a §9.2 KindElicitation interaction keyed to the
// requesting session and its owning user. The canonical resolution is the
// §7.2 approve/deny surface: PhaseApproved approves, PhaseDenied rejects.
// A PhaseResponded answer is interpreted by its action — an explicit
// decline rejects, any other engagement approves. A dismissal or a
// timeout is a non-decision, so the request is neither granted nor
// persisted as a denial, per the §8.6 Elicitor contract (only an explicit
// rejection marks the subtree denied, line 729).
//
// spec: §8.6 line 714, line 718, line 727
type leaseElicitor struct {
	sessions     leaseSessionLookup
	interactions interactionstore.Store
	publish      leaseElicitPublisher
	clock        func() time.Time
	idgen        func() string
	timeout      time.Duration
	poll         time.Duration
}

// Elicit implements leasecontrol.Elicitor.
func (e *leaseElicitor) Elicit(ctx context.Context, tenantID, requestingSessionID string) (bool, error) {
	sess, err := e.sessions.Get(ctx, tenantID, requestingSessionID)
	if err != nil {
		return false, fmt.Errorf("lease elicitation: resolve session %s: %w", requestingSessionID, err)
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

	// spec: §7.2 line 136 — surface the prompt on the session's stream as
	// the canonical `elicitation_request` event.
	if e.publish != nil {
		payload, _ := json.Marshal(struct {
			ElicitationID string `json:"elicitationId"`
			Message       string `json:"message"`
			Reason        string `json:"reason"`
		}{ElicitationID: id, Message: leaseElicitMessage, Reason: "lease_extension"})
		e.publish(requestingSessionID, "elicitation_request", string(payload), now)
	}

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
		cur, err := e.interactions.Get(ctx, tenantID, requestingSessionID, sess.UserID, id)
		if err != nil {
			return false, fmt.Errorf("lease elicitation: poll interaction %s: %w", id, err)
		}
		switch cur.Phase {
		case interactionstore.PhaseApproved:
			return true, nil
		case interactionstore.PhaseDenied:
			return false, nil
		case interactionstore.PhaseResponded:
			return !elicitationDeclined(cur.Response), nil
		case interactionstore.PhaseDismissed:
			return false, fmt.Errorf("lease elicitation %s dismissed: %s", id, cur.Reason)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline:
			_, _ = e.interactions.Resolve(ctx, tenantID, requestingSessionID, sess.UserID, id,
				func(i *interactionstore.Interaction) error {
					if i.Phase == interactionstore.PhasePending {
						i.Phase = interactionstore.PhaseDismissed
						i.Reason = "LEASE_EXTENSION_ELICITATION_TIMEOUT"
					}
					return nil
				})
			return false, fmt.Errorf("lease elicitation %s timed out after %s", id, timeout)
		case <-ticker.C:
		}
	}
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
