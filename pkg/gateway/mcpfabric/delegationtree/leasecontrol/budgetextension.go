// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// Outcome is the tri-state result of a §8.6 budget-exhaustion extension
// attempt the gateway LLM Proxy drives through ExtendForBudget. It
// bounds the proxy's in-path decision: continue the session, terminate
// it, or leave it denying-per-request while the elicitation resolves
// out-of-band.
// spec: §8.6 line 629 (proxy trigger); proposal 0023.
type Outcome int

const (
	// OutcomeGranted means the extension episode resolved GRANTED or
	// PARTIALLY_GRANTED within the caller's in-path wait and this
	// session's budget was raised. The transparent path applies: the
	// non-streaming caller delivers its already-computed response.
	OutcomeGranted Outcome = iota
	// OutcomePending means the caller's in-path wait deadline elapsed
	// while an elicitation-mode episode was still unresolved. The proxy
	// does not terminate the session and does not cancel the episode,
	// which continues out-of-band; the session denies per request until
	// the episode's fan-out later raises its budget or terminates it.
	OutcomePending
	// OutcomeTerminal means the episode resolved CEILING_REACHED or
	// REJECTED, the underlying ExtendLease errored, or the caller's own
	// request context was cancelled (a client disconnect). The proxy
	// terminates the session and returns BUDGET_EXHAUSTED (fail closed).
	OutcomeTerminal
)

func (o Outcome) String() string {
	switch o {
	case OutcomeGranted:
		return "GRANTED"
	case OutcomePending:
		return "PENDING"
	case OutcomeTerminal:
		return "TERMINAL"
	default:
		return "UNKNOWN"
	}
}

// SessionReclaimer applies an extension episode's per-session resolution
// out-of-band, after the caller's in-path wait has already returned
// Pending. The gateway wires the §11.2 sessionbudget.Enforcer to it
// (S4): RaiseBudget raises the session's budget and clears its
// deny-next-request state on a grant, and TerminateSession terminates it
// (fail closed) on a terminal outcome. A nil reclaimer leaves the
// episode fan-out with nothing to reclaim, which is the pre-wiring
// posture; tests inject a fake to assert the fan-out.
// spec: §8.6 line 629, line 719; proposal 0023 S4.
type SessionReclaimer interface {
	// RaiseBudget raises sessionID's budget by delta and clears its
	// deny-next-request state so the pre-flight budget gate admits its
	// next request. delta is the granted token amount for that session.
	RaiseBudget(sessionID string, delta int64)
	// TerminateSession terminates sessionID for budget exhaustion, the
	// fail-closed path when that session's extension outcome is terminal.
	TerminateSession(sessionID string)
}

// ExtendForBudget is the §8.6 budget-exhaustion extension entry the
// gateway LLM Proxy calls at the exhaustion boundary of a proxy-mode
// session. It requests a token-budget extension for sessionID and
// returns a tri-state Outcome bounded by ctx, which the caller derives
// as context.WithTimeout(r.Context(), proxyExtensionWaitTimeout).
//
// The extension is dispatched as a single per-tree episode whose joined
// sessions batch onto one elicitation prompt: a first exhausting session
// in a tree starts the episode on a single tracked goroutine keyed per
// tree, and concurrent (and mid-episode) exhausting sessions join that
// one in-flight episode (§8.6 line 719 batching) rather than starting a
// second one. The episode goroutine dispatches every member of a batch
// concurrently so their ExtendLease -> requestConsent calls overlap and
// batch onto the one live tc.pending prompt; a session that joins after a
// batch has resolved is picked up as the next batch on the same episode
// goroutine. The episode dispatches its elicitation on the injected
// session-scoped EpisodeContext, not on ctx, so cancelling the caller's
// in-path wait does not cancel a still pending elicitation. When no
// unresolved member remains the episode goroutine runs the single
// per-session fan-out exactly once and exits, so no goroutine outlives
// the episode.
//
// The caller blocks on the episode's resolution for this session up to
// ctx. When ctx fires first, ExtendForBudget returns OutcomePending if
// the caller's own request context is still live (the in-path deadline
// elapsed) or OutcomeTerminal if the caller's request context was
// cancelled (a client disconnect, distinguished via r.Context().Err()
// passed as reqCtx). When the episode resolves within ctx it returns
// OutcomeGranted or OutcomeTerminal. On a deferred resolution after a
// Pending return, the episode's per-session fan-out raises or terminates
// the session through the SessionReclaimer.
// spec: §8.6 line 629; proposal 0023 S3.
func (s *Service) ExtendForBudget(ctx context.Context, sessionID string) (Outcome, error) {
	if sessionID == "" {
		return OutcomeTerminal, errors.New("leasecontrol: ExtendForBudget requires a session id")
	}
	if s.episodes == nil {
		return OutcomeTerminal, errors.New("leasecontrol: ExtendForBudget requires an initialized episode manager")
	}

	tenantID, err := s.tenants.TenantOf(ctx, sessionID)
	if err != nil {
		// Fail closed: an unresolvable session cannot be extended.
		return OutcomeTerminal, fmt.Errorf("leasecontrol: ExtendForBudget resolve tenant for %s: %w", sessionID, err)
	}
	budget, err := s.budgets.TreeBudget(ctx, tenantID, sessionID)
	if err != nil {
		return OutcomeTerminal, fmt.Errorf("leasecontrol: ExtendForBudget load tree budget for %s: %w", sessionID, err)
	}

	// The proxy does not carry a requested amount: budget exhaustion asks
	// for as much token headroom as the §8.6 ceiling allows. Requesting
	// the full effective ceiling makes leaseextension.Grant cap the grant
	// to the remaining headroom (min(requested, ceiling-current)), so a
	// session extends up to but never past its ceiling. spec: §8.6 line
	// 643, line 676.
	requested := Dimensions{Tokens: budget.EffectiveMax.Tokens}

	mem := s.episodes.join(budget.RootSessionID, sessionID, requested)

	// Block on this session's resolution up to the caller's in-path wait.
	select {
	case res := <-mem.result:
		// The episode resolved this session within the in-path window.
		if res.err != nil {
			return OutcomeTerminal, res.err
		}
		if res.granted {
			return OutcomeGranted, nil
		}
		return OutcomeTerminal, nil
	case <-ctx.Done():
		// The in-path wait elapsed or the caller's request context was
		// cancelled. detach hands ownership of this session's resolution to
		// the episode's fan-out — unless the fan-out already resolved it in
		// the same instant, in which case detach returns that resolution and
		// the caller uses it. Leaving the episode running lets its fan-out
		// reclaim this session later. Distinguish a client disconnect
		// (Terminal, fail closed) from the in-path deadline (Pending) by
		// inspecting the caller's own request-context error.
		res, resolved := mem.detach()
		if resolved {
			if res.err != nil {
				return OutcomeTerminal, res.err
			}
			if res.granted {
				return OutcomeGranted, nil
			}
			return OutcomeTerminal, nil
		}
		if reqCtxCancelled(ctx) {
			return OutcomeTerminal, nil
		}
		return OutcomePending, nil
	}
}

// SetReclaimer wires the SessionReclaimer the episode fan-out calls for
// a session that detached at the in-path deadline and is later resolved
// out-of-band. The gateway sets it after constructing the enforcer (S4);
// tests inject a fake. A nil reclaimer leaves the deferred fan-out with
// nothing to apply.
func (s *Service) SetReclaimer(r SessionReclaimer) {
	if s.episodes != nil {
		s.episodes.setReclaimer(r)
	}
}

// reqCtxCancelled reports whether the caller's derived in-path wait
// context was cancelled by a genuine parent (request) cancellation
// rather than by its own timeout. The caller derives the wait as
// context.WithTimeout(r.Context(), proxyExtensionWaitTimeout): a
// DeadlineExceeded is the in-path timeout (Pending), while a Canceled is
// the parent request cancellation propagating (Terminal). A wait context
// with no deadline that reports Canceled is likewise a parent
// cancellation.
func reqCtxCancelled(ctx context.Context) bool {
	return errors.Is(ctx.Err(), context.Canceled)
}

// sessionResult is the resolution the episode fan-out delivers to one
// joined session: whether its own §8.6 grant math landed a raise, the
// granted token delta, plus any dispatch error. granted true means
// GRANTED/PARTIALLY_GRANTED with a positive token grant applied; granted
// false with a nil err means a terminal CEILING_REACHED/REJECTED.
type sessionResult struct {
	granted bool
	// grantedTokens is the token amount ExtendLease granted this session,
	// the delta the reclaimer raises the enforcer budget by. Meaningful
	// only when granted is true.
	grantedTokens int64
	err           error
}

// episode is one per-tree §8.6 extension episode. A first exhausting
// session opens it and starts the one tracked episode goroutine
// (runEpisode); concurrent exhausting sessions in the same tree join it
// (§8.6 line 719 batching) rather than opening a second elicitation. The
// episode goroutine dispatches each joined member's ExtendLease
// concurrently the moment it joins, so an in-flight elicitation and a
// mid-episode joiner overlap inside requestConsent and batch onto the one
// live tc.pending prompt. Those per-member dispatches are ephemeral
// helpers the episode goroutine owns and awaits; the episode goroutine
// itself is the single tracked entity that persists across the whole
// episode and, once every joined member has resolved and none remains
// pending, runs each joined session's own Grant math and fans the
// resolution out to every member exactly once before exiting. The fan-out
// is per session because a tree holds multiple proxy-mode sessions that
// resolve independently (one may be GRANTED while another is
// CEILING_REACHED). spec: §8.6 line 719, line 737-741.
type episode struct {
	rootSessionID string

	mu sync.Mutex
	// members maps a joined session to its participation record. The
	// in-path caller reads its own channel; the fan-out writes each
	// member's channel exactly once. A member that detached at the
	// in-path deadline stays in the map so the fan-out still reclaims it
	// through the reclaimer.
	members map[string]*member
	// pending holds members that joined but whose dispatch the episode
	// goroutine has not yet started. The goroutine drains it and spawns
	// each member's concurrent dispatch; a joiner arriving while an
	// elicitation is in flight is dispatched immediately so it overlaps
	// and batches onto the one live prompt (§8.6 line 719). Guarded by mu.
	pending []*member
	// inFlight counts member dispatches the episode goroutine has started
	// but that have not yet recorded a resolution. The episode closes and
	// fans out only when pending is empty and inFlight is zero, so a member
	// that joins while any dispatch is still running is served by this
	// episode rather than a fresh one. Guarded by mu.
	inFlight int
	// notify wakes the episode goroutine when a new member joins ep.pending
	// while it is parked waiting for the current dispatches. It is buffered
	// (size 1) and coalescing: a joiner does a non-blocking send, so several
	// joins collapse to one wake and the goroutine re-drains pending.
	notify chan struct{}
	// closed is set under ep.mu when the episode goroutine finds no pending
	// member and no dispatch in flight, so a joiner arriving after that
	// mints a fresh episode rather than adding a member this episode never
	// dispatches. Once closed, the goroutine runs the fan-out and exits.
	closed bool
	// resolved holds each dispatched member's resolution, keyed by session
	// id, for the single fan-out the episode goroutine runs. Guarded by mu.
	resolved map[string]sessionResult
	// dispatchCtx is the session-scoped context the episode dispatches its
	// ExtendLease elicitations on, bounded by the §8.6 elicitation
	// lifecycle rather than any caller's in-path wait. Resolved once when
	// the episode opens.
	dispatchCtx context.Context
}

// member is one session's participation in an episode. Its mutex
// serializes the ownership handoff between the in-path caller and the
// fan-out: exactly one of them applies the resolution, so a raise or a
// terminate never both fires and never races.
type member struct {
	// sessionID identifies the joined session so the episode goroutine can
	// dispatch and resolve it. It is immutable after the member is created.
	sessionID string
	requested Dimensions
	// result delivers the session's resolution to its in-path caller. It
	// is buffered (size 1) so the fan-out never blocks on a detached
	// caller that is no longer reading.
	result chan sessionResult

	mu sync.Mutex
	// resolved reports that the fan-out has computed this session's
	// resolution. res holds it.
	resolved bool
	res      sessionResult
	// detached reports that the in-path caller stopped waiting before the
	// fan-out resolved, so the fan-out reclaims this session through the
	// SessionReclaimer rather than the caller applying it.
	detached bool
}

// detach is called by the in-path caller when its wait context fires. It
// hands ownership of the resolution to the fan-out. When the fan-out has
// already resolved this member (a same-instant race), it returns that
// resolution so the caller applies it directly; otherwise it marks the
// member detached and the fan-out reclaims it out-of-band.
func (mem *member) detach() (res sessionResult, resolved bool) {
	mem.mu.Lock()
	defer mem.mu.Unlock()
	if mem.resolved {
		return mem.res, true
	}
	mem.detached = true
	return sessionResult{}, false
}

// resolve records the fan-out's resolution for this member and reports
// whether the in-path caller already detached (so the fan-out must
// reclaim the session itself). It is called once per member by the
// fan-out.
func (mem *member) resolve(res sessionResult) (detached bool) {
	mem.mu.Lock()
	mem.resolved = true
	mem.res = res
	detached = mem.detached
	mem.mu.Unlock()
	// Signal a still-waiting in-path caller. Buffered, so this never
	// blocks even when the caller already detached.
	select {
	case mem.result <- res:
	default:
	}
	return detached
}

// episodeManager owns the per-tree §8.6 extension episodes for one
// Service. It reuses the coordinator's per-tree serialization intent:
// one episode per tree at a time, joined by concurrent exhausting
// sessions.
type episodeManager struct {
	svc *Service
	// episodeCtx supplies the session-scoped context each episode
	// dispatches its ExtendLease elicitation on, decoupled from any
	// caller's in-path wait. spec: §8.6 line 629.
	episodeCtx func() context.Context

	mu        sync.Mutex
	trees     map[string]*episode
	reclaimer SessionReclaimer
}

// newEpisodeManager returns an episode manager for svc. episodeCtx is
// the session-scoped context factory (nil selects context.Background).
func newEpisodeManager(svc *Service, episodeCtx func() context.Context) *episodeManager {
	if episodeCtx == nil {
		episodeCtx = context.Background
	}
	return &episodeManager{
		svc:        svc,
		episodeCtx: episodeCtx,
		trees:      map[string]*episode{},
	}
}

// setReclaimer records the reclaimer the fan-out calls for detached
// sessions.
func (m *episodeManager) setReclaimer(r SessionReclaimer) {
	m.mu.Lock()
	m.reclaimer = r
	m.mu.Unlock()
}

// join enrolls sessionID (with its requested dimensions) in the tree's
// current episode, starting one (and its single tracked episode
// goroutine) if none is open, and returns the session's member so the
// caller can block on its resolution. A session that joins twice before
// resolution reuses its existing member. join retries against a fresh
// episode if it lost a race to an episode that closed between the
// manager-lock and the episode-lock. The episode goroutine dispatches
// this member with the rest of its batch concurrently, so a mid-episode
// joiner still overlaps with the in-flight elicitation and batches onto
// the one tc.pending prompt (§8.6 line 719).
func (m *episodeManager) join(rootSessionID, sessionID string, requested Dimensions) *member {
	for {
		m.mu.Lock()
		ep := m.trees[rootSessionID]
		fresh := ep == nil
		if fresh {
			ep = &episode{
				rootSessionID: rootSessionID,
				members:       map[string]*member{},
				resolved:      map[string]sessionResult{},
				notify:        make(chan struct{}, 1),
				// The dispatch context is bounded by the §8.6 elicitation
				// lifecycle, decoupled from any caller's in-path wait, so a
				// caller that detached at its deadline does not cancel the
				// still-pending elicitation. spec: §8.6 line 629.
				dispatchCtx: m.episodeCtx(),
			}
			m.trees[rootSessionID] = ep
		}
		m.mu.Unlock()

		ep.mu.Lock()
		if ep.closed {
			// This episode stopped accepting joiners once its goroutine found
			// no pending members and no dispatch in flight; a fresh episode is
			// minted on the next loop once the goroutine clears the per-tree
			// slot.
			ep.mu.Unlock()
			m.mu.Lock()
			if m.trees[rootSessionID] == ep {
				delete(m.trees, rootSessionID)
			}
			m.mu.Unlock()
			continue
		}
		mem := ep.members[sessionID]
		if mem == nil {
			mem = &member{sessionID: sessionID, requested: requested, result: make(chan sessionResult, 1)}
			ep.members[sessionID] = mem
			// Queue the member for the episode goroutine to dispatch, and wake
			// the goroutine if it is parked. Dispatching immediately (rather
			// than after the in-flight elicitation resolves) is what makes a
			// mid-episode joiner overlap inside requestConsent and batch onto
			// the one live tc.pending prompt (§8.6 line 719).
			ep.pending = append(ep.pending, mem)
			select {
			case ep.notify <- struct{}{}:
			default:
			}
		}
		ep.mu.Unlock()

		if fresh {
			// Start the one tracked episode goroutine for this tree. Later
			// joiners spawn no episode goroutine; they enqueue onto ep.pending,
			// wake this goroutine, and it dispatches them.
			go m.runEpisode(ep)
		}
		return mem
	}
}

// runEpisode is the single tracked per-tree episode goroutine. It owns the
// whole episode lifecycle: it starts each newly-joined member's ExtendLease
// dispatch on an ephemeral helper goroutine it awaits, so concurrent and
// mid-episode joiners overlap inside requestConsent and batch onto the one
// tc.pending prompt (§8.6 line 719). When no member is pending and no
// dispatch is in flight it closes the episode to new joiners, runs the
// single per-session fan-out exactly once, and exits, so no goroutine
// outlives the episode. Dispatch uses the episode's session-scoped
// context, not any caller's in-path wait, so the elicitation survives a
// caller that detached at its deadline. spec: §8.6 line 629, line 719.
func (m *episodeManager) runEpisode(ep *episode) {
	// done receives one signal per completed member dispatch, so the episode
	// goroutine can wake to check whether the episode has fully resolved.
	done := make(chan struct{}, 1)
	for {
		ep.mu.Lock()
		batch := ep.pending
		ep.pending = nil
		ctx := ep.dispatchCtx
		if len(batch) == 0 && ep.inFlight == 0 {
			// No member queued and none in flight. Close to new joiners under
			// this lock hold so a joiner arriving now mints a fresh episode
			// rather than adding a member this goroutine has stopped draining.
			ep.closed = true
			ep.mu.Unlock()
			break
		}
		ep.inFlight += len(batch)
		ep.mu.Unlock()

		// Start each newly-joined member's dispatch concurrently so it
		// overlaps any in-flight elicitation and batches onto the one prompt.
		for _, mem := range batch {
			go func(mem *member) {
				res := m.dispatchOne(ctx, mem.sessionID, mem.requested)
				ep.mu.Lock()
				ep.resolved[mem.sessionID] = res
				ep.inFlight--
				ep.mu.Unlock()
				// Wake the episode goroutine to re-evaluate; coalescing send so
				// several completions collapse to one wake.
				select {
				case done <- struct{}{}:
				default:
				}
			}(mem)
		}

		// Park until a new member joins (notify) or a dispatch completes
		// (done), then re-drain pending and re-check the resolution gate.
		select {
		case <-ep.notify:
		case <-done:
		}
	}
	m.finish(ep)
}

// finish clears the resolved episode's per-tree slot and runs the single
// per-session fan-out. It is called exactly once, by the episode
// goroutine after it has drained every batch. spec: §8.6 line 719.
func (m *episodeManager) finish(ep *episode) {
	m.mu.Lock()
	if m.trees[ep.rootSessionID] == ep {
		delete(m.trees, ep.rootSessionID)
	}
	reclaimer := m.reclaimer
	m.mu.Unlock()

	ep.mu.Lock()
	members := make(map[string]*member, len(ep.members))
	for id, mem := range ep.members {
		members[id] = mem
	}
	resolved := make(map[string]sessionResult, len(ep.resolved))
	for id, r := range ep.resolved {
		resolved[id] = r
	}
	ep.mu.Unlock()

	m.fanOut(members, resolved, reclaimer)
}

// dispatchOne runs one joined session's §8.6 ExtendLease on the episode
// context and maps the response to a sessionResult. The shared per-tree
// elicitation consent resolves once inside ExtendLease's coordinator (a
// concurrent second session batches onto the same prompt, §8.6 line
// 719), while the grant math and terminal outcome are computed per
// session against that session's own budget.Current and requested
// amounts.
func (m *episodeManager) dispatchOne(ctx context.Context, sessionID string, requested Dimensions) sessionResult {
	resp, err := m.svc.ExtendLease(ctx, ExtendRequest{SessionID: sessionID, Requested: requested})
	if err != nil {
		return sessionResult{err: err}
	}
	switch resp.Status {
	case StatusGranted, StatusPartiallyGranted:
		if resp.Granted.Tokens > 0 {
			return sessionResult{granted: true, grantedTokens: resp.Granted.Tokens}
		}
		// A GRANTED response with a zero token grant (a request that fit
		// but asked for nothing extendable) is not a budget raise; treat
		// it as terminal so the session is not left believing it recovered
		// budget it never received. Fail closed.
		return sessionResult{granted: false}
	default:
		// CEILING_REACHED, REJECTED, or UNSPECIFIED — terminal, fail closed.
		return sessionResult{granted: false}
	}
}

// fanOut delivers each member's resolution: it signals the member's
// channel (for a still-waiting in-path caller) and, for a member that
// detached at the in-path deadline, applies the resolution out-of-band
// through the SessionReclaimer — RaiseBudget on a grant (clearing that
// session's deny state) or TerminateSession on a terminal outcome (fail
// closed). Every joined member is reclaimed, so a session that batched
// on and detached is never left denied with nothing to clear it. spec:
// §8.6 line 719, line 737-741; proposal 0023 S3.
func (m *episodeManager) fanOut(members map[string]*member, resolved map[string]sessionResult, reclaimer SessionReclaimer) {
	for id, mem := range members {
		res := resolved[id]
		// resolve records the outcome and signals a still-waiting in-path
		// caller. It returns true only when the caller already detached, so
		// exactly one of the caller and the fan-out applies the resolution.
		if !mem.resolve(res) {
			continue
		}
		// The in-path caller detached (it returned Pending); reclaim the
		// session out-of-band.
		if reclaimer == nil {
			slog.Warn("leasecontrol: extension episode has no reclaimer for a detached session",
				"session_id", id)
			continue
		}
		switch {
		case res.err != nil:
			reclaimer.TerminateSession(id)
		case res.granted:
			// Raise by this session's own granted token delta. The episode
			// applied the grant to the leasecontrol view; reflect the same
			// raise on the enforcer's per-session budget so the pre-flight
			// gate admits the session's next request.
			reclaimer.RaiseBudget(id, res.grantedTokens)
		default:
			reclaimer.TerminateSession(id)
		}
	}
}
