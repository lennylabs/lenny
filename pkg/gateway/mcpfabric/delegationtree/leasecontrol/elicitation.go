// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrRequesterEnded reports that the session whose request drove an
// elicitation is terminal or no longer exists. An Elicitor returns it
// when it writes no interaction because the requester has already ended,
// and when it dismisses a pending interaction with the reason
// LEASE_EXTENSION_REQUESTER_TERMINAL. The coordinator applies no outcome
// of such an elicitation to the requests batched onto it and sends each
// back to the start of the flow.
// spec: §8.6 (Elicitation mode flow) step 2, §9.2.
var ErrRequesterEnded = errors.New("leasecontrol: elicitation requester ended")

// Elicitor presents the §8.6 generic budget elicitation to the
// requesting session's client and blocks until the user resolves it.
//
// The §8.6 elicitation is generic — "The agent needs more budget to
// continue. Approve?" — and shows no token amounts. The
// gateway wires a production Elicitor over the §9.2 interaction store and
// the client event stream; tests pass a fake.
//
// Return contract:
//   - approved=true, err=nil  — the user approved.
//   - approved=false, err=nil — the user explicitly rejected. The
//     coordinator marks the requesting subtree extension-denied and
//     starts the §8.6 rejection cool-off.
//   - approved=false, err!=nil — no decision was reached (timeout,
//     dismiss, transport failure). The request is not granted and the
//     subtree is NOT marked denied, because only an explicit user
//     rejection persists a denial per §8.6.
//   - approved=false, err wrapping ErrRequesterEnded — the requesting
//     session ended before the user decided. The driving request fails
//     and every batched request re-enters the flow.
//
// spec: §8.6
type Elicitor interface {
	Elicit(ctx context.Context, tenantID, requestingSessionID string) (approved bool, err error)
}

// consent is the resolved §8.6 approval decision the coordinator hands
// back to ExtendLease for an elicitation-mode (or auto-mode rate-limit
// fallback) request.
type consent struct {
	// approved reports whether the request may proceed to the grant math.
	approved bool
	// approver is the §8.6 approver attribution: "client" when a
	// user approved or rejected an elicitation, "gateway-auto" when the
	// grant rode the post-approval cool-off window without a fresh
	// elicitation.
	approver string
}

// successCoolOffProvider is the optional BudgetSource extension that
// reports the tree's resolved §8.6 post-approval cool-off
// duration. StoreBudgetSource implements it; a source that does not
// falls back to DefaultSuccessCoolOff. The pattern mirrors
// approvalModeProvider so the core BudgetSource interface stays minimal.
// spec: §8.6
type successCoolOffProvider interface {
	SuccessCoolOff(ctx context.Context, tenantID, rootSessionID string) time.Duration
}

// elicitCoordinator drives the §8.6 elicitation-mode approval flow.
// Requests are serialized per task tree: a first request opens one
// generic elicitation, concurrent requests batch onto it silently
// without a duplicate prompt, and an approval opens a success cool-off
// window during which further requests are auto-granted without
// re-eliciting. A rejection marks the requesting subtree
// extension-denied and starts the rejection cool-off.
//
// The per-tree serialization and success cool-off live in this replica's
// memory. The denial and its cool-off are durable on the
// delegation_tree_budget row and bind every replica, so losing this state
// re-elicits but never grants without approval. A tree's entry is
// discarded once no elicitation is pending and the success cool-off has
// elapsed.
// spec: §8.6 (Elicitation mode flow), §8.6 (Extension state), §19
type elicitCoordinator struct {
	elicitor Elicitor
	budgets  BudgetSource
	clock    func() time.Time
	// afterFunc schedules the reclamation of a tree whose batch opened a
	// success cool-off. Production uses time.AfterFunc; tests inject a
	// controllable scheduler.
	afterFunc func(d time.Duration, f func())

	// mu guards trees and every field of each treeConsent.
	mu    sync.Mutex
	trees map[string]*treeConsent
}

// treeConsent is one delegation tree's mutable elicitation-mode state.
// The coordinator's mu guards every field.
type treeConsent struct {
	// coolOffUntil is the §8.6 success cool-off window end. A
	// request arriving before it is auto-granted without a fresh
	// elicitation.
	coolOffUntil time.Time
	// pending is the in-flight elicitation batch, or nil when no
	// elicitation is open. A concurrent request joins a non-nil batch
	// rather than opening a second elicitation (§8.6).
	pending *consentBatch
}

// consentBatch is one elicitation episode. The driving request invokes
// the Elicitor and records the outcome; concurrent requests that joined
// the batch read the same outcome once done is closed. spec: §8.6.
type consentBatch struct {
	done     chan struct{}
	approved bool
	err      error
	// redrive reports that the driving session ended before the user
	// decided. Batched requests take no outcome from the batch and
	// re-enter requestConsent. spec: §8.6 (Elicitation mode flow) step 2.
	redrive bool
}

// newElicitCoordinator returns a coordinator over the given elicitor and
// budget source. It is created only when an Elicitor is wired; a Service
// with no elicitor has a nil coordinator and fails elicitation-mode
// requests closed rather than auto-granting them.
func newElicitCoordinator(elicitor Elicitor, budgets BudgetSource, clock func() time.Time) *elicitCoordinator {
	return &elicitCoordinator{
		elicitor:  elicitor,
		budgets:   budgets,
		clock:     clock,
		afterFunc: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		trees:     map[string]*treeConsent{},
	}
}

// requestConsent resolves the §8.6 elicitation-mode approval for one
// extension request from requestingSessionID in the tree rooted at
// rootSessionID. It returns the consent decision (approved + approver
// attribution), or a non-nil error when the elicitation did not reach a
// user decision (timeout, dismiss, transport failure, a failed denial
// write, or the driving request's own session ending).
// spec: §8.6 (Elicitation mode flow)
func (c *elicitCoordinator) requestConsent(ctx context.Context, tenantID, rootSessionID, requestingSessionID string) (consent, error) {
	c.mu.Lock()
	tc := c.treeLocked(rootSessionID)
	// §8.6 — during the post-approval cool-off, auto-grant
	// without a fresh elicitation. The grant is gateway-issued with no
	// client input, so the approver is gateway-auto.
	if c.clock().Before(tc.coolOffUntil) {
		c.mu.Unlock()
		return consent{approved: true, approver: "gateway-auto"}, nil
	}
	// §8.6 — a concurrent request arriving while an elicitation
	// is pending joins it silently; no duplicate elicitation is sent.
	if tc.pending != nil {
		batch := tc.pending
		c.mu.Unlock()
		return c.await(ctx, tenantID, rootSessionID, requestingSessionID, batch)
	}
	// §8.6 — first request opens a new elicitation.
	batch := &consentBatch{done: make(chan struct{})}
	tc.pending = batch
	c.mu.Unlock()

	return c.drive(ctx, tenantID, rootSessionID, requestingSessionID, tc, batch)
}

// drive runs the elicitation for the batch this request opened, persists
// a rejection, records the outcome, and closes the batch.
func (c *elicitCoordinator) drive(ctx context.Context, tenantID, rootSessionID, requestingSessionID string, tc *treeConsent, batch *consentBatch) (consent, error) {
	approved, err := c.elicitor.Elicit(ctx, tenantID, requestingSessionID)
	var coolOff time.Duration
	switch {
	case err == nil && approved:
		// Resolved before the lock: the provider may read the store.
		coolOff = c.resolveSuccessCoolOff(ctx, tenantID, rootSessionID)
	case err == nil:
		// §8.6 — the denial is persisted before REJECTED is
		// returned, and a failed write fails the driver and every batched
		// waiter with no grant. Only the driving request persists the
		// denial; batched waiters echo the rejection. The batch stays
		// pending until the write completes, so a waiter does not return
		// while it is in flight.
		if derr := c.budgets.Deny(ctx, tenantID, rootSessionID, requestingSessionID); derr != nil {
			err = fmt.Errorf("leasecontrol: persist extension denial for %s: %w", requestingSessionID, derr)
		}
	}
	c.closeBatch(rootSessionID, tc, batch, approved, err, coolOff)

	if err != nil {
		return consent{}, err
	}
	return consent{approved: approved, approver: "client"}, nil
}

// closeBatch records the batch outcome, clears the pending batch, and
// reclaims the tree's entry: at once when no success cool-off opened,
// and through a timer at the cool-off end otherwise.
// spec: §8.6 (Extension state)
func (c *elicitCoordinator) closeBatch(rootSessionID string, tc *treeConsent, batch *consentBatch, approved bool, err error, coolOff time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tc.pending = nil
	batch.approved = approved
	if errors.Is(err, ErrRequesterEnded) {
		batch.redrive = true
	} else {
		batch.err = err
	}
	if err == nil && approved {
		// §8.6 — approval opens the success cool-off window.
		tc.coolOffUntil = c.clock().Add(coolOff)
		c.afterFunc(coolOff, func() { c.reclaimAfterCoolOff(rootSessionID, tc) })
	} else if c.trees[rootSessionID] == tc {
		delete(c.trees, rootSessionID)
	}
	close(batch.done)
}

// reclaimAfterCoolOff discards the tree's entry once nothing is pending
// and the success cool-off has elapsed. An entry another batch has since
// replaced, re-opened, or extended is left to that batch.
// spec: §8.6 (Extension state)
func (c *elicitCoordinator) reclaimAfterCoolOff(rootSessionID string, tc *treeConsent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.trees[rootSessionID] != tc || tc.pending != nil || c.clock().Before(tc.coolOffUntil) {
		return
	}
	delete(c.trees, rootSessionID)
}

// await blocks a batched (non-driving) request until the driving request
// resolves the elicitation, then returns the shared outcome. A batch whose
// driving session ended carries no outcome, so the request re-enters
// requestConsent and either opens the next elicitation or joins it.
// spec: §8.6 (Elicitation mode flow) step 2.
func (c *elicitCoordinator) await(ctx context.Context, tenantID, rootSessionID, requestingSessionID string, batch *consentBatch) (consent, error) {
	select {
	case <-ctx.Done():
		return consent{}, ctx.Err()
	case <-batch.done:
	}
	// The close of done happens-after the driver's writes to batch, so
	// these reads observe them without holding the coordinator lock.
	if batch.redrive {
		return c.requestConsent(ctx, tenantID, rootSessionID, requestingSessionID)
	}
	if batch.err != nil {
		return consent{}, batch.err
	}
	return consent{approved: batch.approved, approver: "client"}, nil
}

// treeLocked returns the per-tree consent state for rootSessionID,
// creating it on first use. The caller holds c.mu.
func (c *elicitCoordinator) treeLocked(rootSessionID string) *treeConsent {
	tc, ok := c.trees[rootSessionID]
	if !ok {
		tc = &treeConsent{}
		c.trees[rootSessionID] = tc
	}
	return tc
}

// resolveSuccessCoolOff returns the §8.6 post-approval cool-off
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
