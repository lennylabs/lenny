// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"sync"
	"time"
)

// Elicitor presents the §8.6 line 718 generic budget elicitation to the
// requesting session's client and blocks until the user resolves it.
//
// The §8.6 elicitation is generic — "The agent needs more budget to
// continue. Approve?" — and shows no token amounts (line 718). The
// gateway wires a production Elicitor over the §9.2 interaction store and
// the client event stream; tests pass a fake.
//
// Return contract:
//   - approved=true, err=nil  — the user approved.
//   - approved=false, err=nil — the user explicitly rejected. The
//     coordinator marks the requesting subtree extension-denied and
//     starts the §8.6 line 734 rejection cool-off.
//   - approved=false, err!=nil — no decision was reached (timeout,
//     dismiss, transport failure). The request is not granted and the
//     subtree is NOT marked denied, because only an explicit user
//     rejection persists a denial per §8.6 line 729.
//
// spec: §8.6 line 714, line 718, line 727
type Elicitor interface {
	Elicit(ctx context.Context, tenantID, requestingSessionID string) (approved bool, err error)
}

// consent is the resolved §8.6 approval decision the coordinator hands
// back to ExtendLease for an elicitation-mode (or auto-mode rate-limit
// fallback) request.
type consent struct {
	// approved reports whether the request may proceed to the grant math.
	approved bool
	// approver is the §8.6 line 743 approver attribution: "client" when a
	// user approved or rejected an elicitation, "gateway-auto" when the
	// grant rode the post-approval cool-off window without a fresh
	// elicitation.
	approver string
}

// successCoolOffProvider is the optional BudgetSource extension that
// reports the tree's resolved §8.6 line 675 post-approval cool-off
// duration. MemoryBudgetSource implements it; a source that does not
// falls back to DefaultSuccessCoolOff. The pattern mirrors
// approvalModeProvider so the core BudgetSource interface stays minimal.
// spec: §8.6 line 675
type successCoolOffProvider interface {
	SuccessCoolOff(ctx context.Context, tenantID, rootSessionID string) time.Duration
}

// elicitCoordinator drives the §8.6 line 714 elicitation-mode approval
// flow. Requests are serialized per task tree: a first request opens one
// generic elicitation (line 718), concurrent requests batch onto it
// silently without a duplicate prompt (line 719), and an approval opens a
// success cool-off window during which further requests are auto-granted
// without re-eliciting (lines 722-723). A rejection marks the requesting
// subtree extension-denied and starts the rejection cool-off (line 729).
//
// The per-tree serialization and success cool-off live in memory on the
// owning gateway replica. §8.6 line 730 requires only the extension-denied
// flag and the rejection cool-off expiry to be durable across a
// coordinator handoff (that durability is the Postgres BudgetSource's
// concern); a handoff mid-elicitation simply starts a fresh elicitation
// on the new replica, which is the §8.6 line 718 first-request behaviour.
// spec: §8.6 lines 714-734
type elicitCoordinator struct {
	elicitor Elicitor
	budgets  BudgetSource
	clock    func() time.Time

	mu    sync.Mutex
	trees map[string]*treeConsent
}

// treeConsent is one delegation tree's mutable elicitation-mode state.
type treeConsent struct {
	mu sync.Mutex
	// coolOffUntil is the §8.6 line 722 success cool-off window end. A
	// request arriving before it is auto-granted without a fresh
	// elicitation.
	coolOffUntil time.Time
	// pending is the in-flight elicitation batch, or nil when no
	// elicitation is open. A concurrent request joins a non-nil batch
	// rather than opening a second elicitation (§8.6 line 719).
	pending *consentBatch
}

// consentBatch is one elicitation episode. The driving request invokes
// the Elicitor and records the outcome; concurrent requests that joined
// the batch read the same outcome once done is closed. spec: §8.6 line 719.
type consentBatch struct {
	done     chan struct{}
	approved bool
	err      error
}

// newElicitCoordinator returns a coordinator over the given elicitor and
// budget source. It is created only when an Elicitor is wired; a Service
// with no elicitor has a nil coordinator and fails elicitation-mode
// requests closed rather than auto-granting them.
func newElicitCoordinator(elicitor Elicitor, budgets BudgetSource, clock func() time.Time) *elicitCoordinator {
	return &elicitCoordinator{
		elicitor: elicitor,
		budgets:  budgets,
		clock:    clock,
		trees:    map[string]*treeConsent{},
	}
}

// requestConsent resolves the §8.6 elicitation-mode approval for one
// extension request from requestingSessionID in the tree rooted at
// rootSessionID. It returns the consent decision (approved + approver
// attribution), or a non-nil error when the elicitation did not reach a
// user decision (timeout, dismiss, transport failure).
func (c *elicitCoordinator) requestConsent(ctx context.Context, tenantID, rootSessionID, requestingSessionID string) (consent, error) {
	tc := c.treeFor(rootSessionID)

	tc.mu.Lock()
	// §8.6 line 723 — during the post-approval cool-off, auto-grant
	// without a fresh elicitation. The grant is gateway-issued with no
	// client input, so the approver is gateway-auto.
	if c.clock().Before(tc.coolOffUntil) {
		tc.mu.Unlock()
		return consent{approved: true, approver: "gateway-auto"}, nil
	}
	// §8.6 line 719 — a concurrent request arriving while an elicitation
	// is pending joins it silently; no duplicate elicitation is sent.
	if tc.pending != nil {
		batch := tc.pending
		tc.mu.Unlock()
		return c.await(ctx, batch)
	}
	// §8.6 line 718 — first request opens a new elicitation.
	batch := &consentBatch{done: make(chan struct{})}
	tc.pending = batch
	tc.mu.Unlock()

	approved, err := c.elicitor.Elicit(ctx, tenantID, requestingSessionID)

	tc.mu.Lock()
	tc.pending = nil
	batch.approved = approved
	batch.err = err
	if err == nil && approved {
		// §8.6 line 722 — approval opens the success cool-off window.
		tc.coolOffUntil = c.clock().Add(c.resolveSuccessCoolOff(ctx, tenantID, rootSessionID))
	}
	close(batch.done)
	tc.mu.Unlock()

	if err != nil {
		return consent{}, err
	}
	if !approved {
		// §8.6 line 729 — an explicit rejection marks the requesting
		// subtree extension-denied and starts the rejection cool-off. Only
		// the driving request persists the denial; batched waiters echo
		// the rejection without re-denying (their own subtrees may differ).
		_ = c.budgets.Deny(ctx, tenantID, rootSessionID, requestingSessionID)
		return consent{approved: false, approver: "client"}, nil
	}
	return consent{approved: true, approver: "client"}, nil
}

// await blocks a batched (non-driving) request until the driving request
// resolves the elicitation, then returns the shared outcome. spec: §8.6
// line 719.
func (c *elicitCoordinator) await(ctx context.Context, batch *consentBatch) (consent, error) {
	select {
	case <-ctx.Done():
		return consent{}, ctx.Err()
	case <-batch.done:
	}
	// The close of done happens-after the driver's writes to batch, so
	// these reads observe them without holding tc.mu.
	if batch.err != nil {
		return consent{}, batch.err
	}
	if !batch.approved {
		return consent{approved: false, approver: "client"}, nil
	}
	return consent{approved: true, approver: "client"}, nil
}

// treeFor returns the per-tree consent state for rootSessionID, creating
// it on first use.
func (c *elicitCoordinator) treeFor(rootSessionID string) *treeConsent {
	c.mu.Lock()
	defer c.mu.Unlock()
	tc, ok := c.trees[rootSessionID]
	if !ok {
		tc = &treeConsent{}
		c.trees[rootSessionID] = tc
	}
	return tc
}

// resolveSuccessCoolOff returns the §8.6 line 675 post-approval cool-off
// for the tree, falling back to DefaultSuccessCoolOff when the source
// supplies no override.
func (c *elicitCoordinator) resolveSuccessCoolOff(ctx context.Context, tenantID, rootSessionID string) time.Duration {
	if p, ok := c.budgets.(successCoolOffProvider); ok {
		if d := p.SuccessCoolOff(ctx, tenantID, rootSessionID); d > 0 {
			return d
		}
	}
	return DefaultSuccessCoolOff
}
