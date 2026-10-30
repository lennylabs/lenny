// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// reasonBudgetKeysExpired is the FailureReason the §8.3 tree cleanup stamps
// on the root it fails.
const reasonBudgetKeysExpired = "BUDGET_KEYS_EXPIRED"

// eventDelegationBudgetKeysExpired is the critical structured event the §8.3
// tree cleanup emits once per root, from the writer whose root write applied.
const eventDelegationBudgetKeysExpired events.EventType = "delegation.budget_keys_expired"

// HandleBudgetKeysExpired runs the §8.3 BUDGET_KEYS_EXPIRED tree cleanup for
// the tree rooted at rootID. Only a reservation detection calls it; a budget
// return that finds the sentinel absent is not a detection.
//
// Every call increments lenny_delegation_budget_keys_expired_total under the
// root row's pool. The handler then fails the root through
// failBudgetKeysExpired. Only the call whose root write applied emits
// delegation.budget_keys_expired, so repeated detections fail the root once
// and emit the event once. Every call, whether its root write applied or was
// refused, cancels each non-terminal node of the tree through
// cancelNodeGuarded, whatever the node's cascadeOnFailure policy. The guarded
// writes make the sweep idempotent, and a node another writer already ended
// is skipped. The cleanup adds no state of its own.
//
// The gateway decides these writes, so the handler runs detached from the
// caller's cancellation: a delegate_task request that ends mid-cleanup does
// not leave the tree half swept.
//
// spec: §8.3, §7.1, §8.10
func (s *Server) HandleBudgetKeysExpired(ctx context.Context, tenantID, rootID string) {
	ctx = context.WithoutCancel(ctx)
	s.countBudgetKeysExpired(ctx, tenantID, rootID)
	if s.failBudgetKeysExpired(ctx, tenantID, rootID) {
		s.emitBudgetKeysExpired(ctx, tenantID, rootID)
	}
	s.cancelTreeNodes(ctx, tenantID, rootID)
}

// countBudgetKeysExpired increments the §16.1 detection counter with the root
// row's pool. A root row that cannot be read is counted under an empty pool,
// because the detection happened either way.
// spec: §16.1, §8.3
func (s *Server) countBudgetKeysExpired(ctx context.Context, tenantID, rootID string) {
	if s.incDelegationBudgetKeysExpired == nil {
		return
	}
	pool := ""
	if root, err := s.store.Get(ctx, tenantID, rootID); err == nil {
		pool = root.PoolRef
	}
	s.incDelegationBudgetKeysExpired(pool, tenantID)
}

// failBudgetKeysExpired is the hook-path writer that fails the root of a tree
// whose budget keys expired. It opens the root's terminal claim before the
// Update and writes `failed` through budgetKeysExpiredGuard, which admits only
// the states §7.2 gives a BUDGET_KEYS_EXPIRED transition to `failed`. On a
// write that applied it hands the row to OnSessionTerminal with the locked
// row's pre-terminal state; OnSessionTerminal runs the post-terminal
// pipeline detached under a claim of its own, and this writer's claim ends
// when OnSessionTerminal returns. It reports whether the write applied; a
// refused write or a store error runs no hook.
//
// The write does not go through transitionToFailed: that funnel's guard
// refuses a suspended root that holds no pod, and its reason mapping would
// record a runtime failure class.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §7.2
// (Interactive Session Model), §7.1 (Gateway-owned terminal writes)
func (s *Server) failBudgetKeysExpired(ctx context.Context, tenantID, rootID string) bool {
	end := s.BeginTerminal(rootID)
	defer end()
	from, updated, err := s.writeTerminal(ctx, tenantID, rootID, session.StateFailed, budgetKeysExpiredGuard)
	if err != nil {
		return false
	}
	s.OnSessionTerminal(ctx, from, updated)
	return true
}

// budgetKeysExpiredGuard is the root write's locked-row guard. It admits a
// row in running, input_required, or suspended, the states from which §7.2
// gives a BUDGET_KEYS_EXPIRED transition to `failed`, and stamps the
// budget_keys_expired failure class and reason. Every other state, terminal
// states and resume_pending, resuming, and awaiting_client_action included,
// is refused with sessionstore.ErrSuperseded.
// spec: §7.2 (Interactive Session Model), §8.3
func budgetKeysExpiredGuard(r *sessionstore.Session) error {
	switch r.State {
	case session.StateRunning, session.StateInputRequired, session.StateSuspended:
	default:
		return sessionstore.ErrSuperseded
	}
	r.FailureClass = session.FailureClassBudgetKeysExpired
	r.FailureReason = reasonBudgetKeysExpired
	return nil
}

// emitBudgetKeysExpired emits the critical delegation.budget_keys_expired
// event with root_session_id and tenant_id, as a structured log line and, when
// an operational-event emitter is wired, as an operational event.
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup, step 4)
func (s *Server) emitBudgetKeysExpired(ctx context.Context, tenantID, rootID string) {
	slog.Error("sessionserver: delegation budget keys expired during an active tree",
		"event", string(eventDelegationBudgetKeysExpired),
		"root_session_id", rootID, "tenant_id", tenantID)
	if s.opsEmitter == nil {
		return
	}
	data, _ := json.Marshal(map[string]string{
		"root_session_id": rootID,
		"tenant_id":       tenantID,
	})
	_ = s.opsEmitter.Emit(ctx, events.OperationalEvent{
		Source:          "/v1/sessions",
		Type:            eventDelegationBudgetKeysExpired.CloudEventsType(),
		Severity:        "critical",
		DataContentType: "application/json",
		Data:            data,
	})
}

// cancelTreeNodes cancels every non-terminal node of the root's tree other
// than the root, whatever its cascadeOnFailure policy, through
// cancelNodeGuarded. ListByRoot also returns the root's own row, which the
// root write owns. A listing error is logged and sweeps nothing; a later
// detection sweeps again.
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup, step 1), §8.10
func (s *Server) cancelTreeNodes(ctx context.Context, tenantID, rootID string) {
	nodes, err := s.store.ListByRoot(ctx, tenantID, rootID)
	if err != nil {
		slog.Warn("sessionserver: list the tree of a root whose budget keys expired",
			"root_session_id", rootID, "tenant_id", tenantID, "error", err)
		return
	}
	for _, node := range nodes {
		if node.ID == rootID {
			continue
		}
		s.cancelNodeGuarded(ctx, tenantID, node)
	}
}
