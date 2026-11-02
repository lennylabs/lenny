// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §15.1 finalize precondition under
// concurrent callers.
//
// POST /v1/sessions/{id}/finalize reads the row before it takes any lock, so
// two calls that both read `created` both pass that early check. The
// precondition is authoritative only when the handler re-checks it inside the
// store Update that commits `finalizing`, and the closing `ready` write admits
// only a row that is still `finalizing`. These cases race real HTTP calls
// through the sessionserver handler on a memstore with no pod binder, and read
// every committed State change from a store wrapper, so a second admission or
// a write over a terminal state shows up as a recorded transition rather than
// only as a final value. The admission cases wire no upload-token verifier. The
// consumption case wires one verifier on each of two servers over one store,
// and races calls carrying one token, so §7.1 consumption is derived from the
// shared row.
//
// Every case carries a stress budget:
//
//	lenny-test stress --test TestConcurrentFinalizeAdmitsExactlyOne_spec_15_1 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
//	lenny-test stress --test TestConcurrentFinalizeAndDeleteKeepTerminal_spec_15_1 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
//	lenny-test stress --test TestOneTokenRacedAcrossTwoServersFinalizesOnce_spec_7_1 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
//
// spec: §15.1 (finalize precondition), §7.2 (terminal states), §7.1 (Single-use invalidation)
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

const (
	finalizeRaceTenant  = "acme"
	finalizeRaceSession = "sess-fin-admission"
	// finalizeEntryGateWait bounds how long an entry Update waits for its
	// peer at the gate. The peer arrives within microseconds on a correct
	// handler; the bound only keeps a regression from hanging the suite.
	finalizeEntryGateWait = 5 * time.Second
)

// stateTransition is one committed change of a session row's State, read
// under the store's lock.
type stateTransition struct {
	from, to session.State
}

// transitionStore wraps a session store and records the State before and
// after every Update that commits. The before value is read inside the
// mutation, under the store's row lock, so it is the state the write
// actually replaced.
//
// When entryGate is set, an Update whose mutation would move the row from
// `created` to `finalizing` waits at the gate until entryPeers such Updates
// have arrived. That forces every finalize call's pre-lock read of `created`
// to happen before any of them commits, which is the schedule where only the
// in-mutation re-check can refuse the loser.
type transitionStore struct {
	sessionstore.Store

	// mu guards transitions.
	mu          sync.Mutex
	transitions []stateTransition

	entryGate *peerGate
}

// GetPrimary forwards the primary read, so sessionstore.ReadPrimary reaches the
// inner store rather than falling back to transitionStore.Update.
func (s *transitionStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	return s.Store.(sessionstore.PrimaryReader).GetPrimary(ctx, tenantID, id)
}

func (s *transitionStore) Update(
	ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error,
) (sessionstore.Session, error) {
	if s.entryGate != nil && s.isFinalizeEntry(ctx, tenantID, id, mutate) {
		s.entryGate.wait()
	}
	var tr stateTransition
	out, err := s.Store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
		tr.from = row.State
		if err := mutate(row); err != nil {
			return err
		}
		tr.to = row.State
		return nil
	})
	if err == nil && tr.from != tr.to {
		s.mu.Lock()
		s.transitions = append(s.transitions, tr)
		s.mu.Unlock()
	}
	return out, err
}

// isFinalizeEntry probes mutate on a copy of the current row and reports
// whether it moves `created` to `finalizing`. The finalize mutations only
// assign row fields, so the probe has no side effect.
func (s *transitionStore) isFinalizeEntry(
	ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error,
) bool {
	cur, err := s.Get(ctx, tenantID, id)
	if err != nil || cur.State != session.StateCreated {
		return false
	}
	probe := cur
	return mutate(&probe) == nil && probe.State == session.StateFinalizing
}

func (s *transitionStore) snapshot() []stateTransition {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stateTransition(nil), s.transitions...)
}

// count returns how many recorded transitions went from `from` to `to`.
func (s *transitionStore) count(from, to session.State) int {
	n := 0
	for _, tr := range s.snapshot() {
		if tr.from == from && tr.to == to {
			n++
		}
	}
	return n
}

// peerGate holds each arriving caller until peers callers have arrived, or
// until finalizeEntryGateWait passes.
type peerGate struct {
	peers int

	mu      sync.Mutex
	arrived int
	open    chan struct{}
}

func newPeerGate(peers int) *peerGate {
	return &peerGate{peers: peers, open: make(chan struct{})}
}

func (g *peerGate) wait() {
	g.mu.Lock()
	g.arrived++
	if g.arrived == g.peers {
		close(g.open)
	}
	g.mu.Unlock()
	select {
	case <-g.open:
	case <-time.After(finalizeEntryGateWait):
	}
}

// finalizeRaceFixture is one sessionserver on a transition-recording
// memstore holding a single session in `created`.
type finalizeRaceFixture struct {
	h     http.Handler
	store *transitionStore
}

// newFinalizeRaceFixture builds the server with no pod binder and no
// upload-token verifier, so the prepare phase materializes nothing and the
// finalize call runs only its state writes. entryPeers, when positive, arms
// the entry gate for that many finalize calls.
func newFinalizeRaceFixture(t *testing.T, entryPeers int) *finalizeRaceFixture {
	t.Helper()
	inner := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := inner.Create(context.Background(), sessionstore.Session{
		ID: finalizeRaceSession, TenantID: finalizeRaceTenant, State: session.StateCreated,
		UserID: "alice@acme.com", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	store := &transitionStore{Store: inner}
	if entryPeers > 0 {
		store.entryGate = newPeerGate(entryPeers)
	}
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	return &finalizeRaceFixture{h: ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice), store: store}
}

// call serves one request against the session and returns its status and
// body.
func (f *finalizeRaceFixture) call(method, suffix string) (int, []byte) {
	req := httptest.NewRequest(method, "/v1/sessions/"+finalizeRaceSession+suffix, nil)
	req.Header.Set("X-Lenny-Tenant-ID", finalizeRaceTenant)
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}

func (f *finalizeRaceFixture) finalState(t *testing.T) session.State {
	t.Helper()
	return storedRaceState(t, f.store)
}

// storedRaceState reads the raced session's State from store.
func storedRaceState(t *testing.T, store *transitionStore) session.State {
	t.Helper()
	row, err := store.Get(context.Background(), finalizeRaceTenant, finalizeRaceSession)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	return row.State
}

// raceResult is one racing call's response.
type raceResult struct {
	code int
	body []byte
}

// runRacing releases every call in calls from one rendezvous and returns
// their responses in the same order.
func runRacing(t *testing.T, calls ...func() (int, []byte)) []raceResult {
	t.Helper()
	start := newRaceStart(len(calls))
	out := make([]raceResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.arrive()
			code, body := call()
			out[i] = raceResult{code: code, body: body}
		}()
	}
	start.release(t)
	wg.Wait()
	return out
}

// finalizeConflict is the §15.1 INVALID_STATE_TRANSITION envelope.
type finalizeConflict struct {
	Error struct {
		Code    string `json:"code"`
		Details struct {
			CurrentState  string   `json:"currentState"`
			AllowedStates []string `json:"allowedStates"`
		} `json:"details"`
	} `json:"error"`
}

// assertFinalizeConflict checks that res is the 409 a refused or overtaken
// finalize call answers, with a currentState drawn from want and the finalize
// row's allowedStates.
func assertFinalizeConflict(t *testing.T, attempt int, res raceResult, want ...session.State) {
	t.Helper()
	var env finalizeConflict
	if err := json.Unmarshal(res.body, &env); err != nil {
		t.Errorf("attempt %d: decode 409 envelope: %v; body %s", attempt, err, res.body)
		return
	}
	if env.Error.Code != "INVALID_STATE_TRANSITION" {
		t.Errorf("attempt %d: code = %q, want INVALID_STATE_TRANSITION", attempt, env.Error.Code)
	}
	matched := false
	for _, st := range want {
		if env.Error.Details.CurrentState == string(st) {
			matched = true
		}
	}
	if !matched {
		t.Errorf("attempt %d: currentState = %q, want one of %v", attempt, env.Error.Details.CurrentState, want)
	}
	if !reflect.DeepEqual(env.Error.Details.AllowedStates, []string{string(session.StateCreated)}) {
		t.Errorf("attempt %d: allowedStates = %v, want [created]", attempt, env.Error.Details.AllowedStates)
	}
}

// consumptionRaceCallers is how many finalize calls race one token, split
// across the two servers.
const consumptionRaceCallers = 6

// finalizeTwoServerFixture is two servers over one transition-recording store,
// each with its own verifier, holding a session whose row carries the digest
// and expiry of token. The row is the only token state the servers share.
type finalizeTwoServerFixture struct {
	handlers [2]http.Handler
	store    *transitionStore
	token    string
}

func newFinalizeTwoServerFixture(t *testing.T) *finalizeTwoServerFixture {
	t.Helper()
	token, issued, err := uploadtoken.NewIssuer(nil).IssueDetailed(finalizeRaceSession, uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	inner := memstore.New()
	now := time.Now().UTC()
	if err := inner.Create(context.Background(), sessionstore.Session{
		ID: finalizeRaceSession, TenantID: finalizeRaceTenant, State: session.StateCreated,
		UserID: "alice@acme.com", CreatedAt: now, UpdatedAt: now,
		UploadTokenDigest: issued.Digest, UploadTokenExpiry: issued.Expiry,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	f := &finalizeTwoServerFixture{store: &transitionStore{Store: inner}, token: token}
	for i := range f.handlers {
		srv := sessionserver.New(f.store, sessionserver.Options{
			DevTenantHeader:     true,
			UploadTokenVerifier: uploadtoken.NewVerifier(nil),
		})
		t.Cleanup(srv.Close)
		f.handlers[i] = ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice)
	}
	return f
}

// finalize serves one finalize carrying the fixture's token on server i.
func (f *finalizeTwoServerFixture) finalize(i int) (int, []byte) {
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+finalizeRaceSession+"/finalize", nil)
	req.Header.Set("X-Lenny-Tenant-ID", finalizeRaceTenant)
	req.Header.Set("X-Lenny-Upload-Token", f.token)
	rr := httptest.NewRecorder()
	f.handlers[i].ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}

// assertConsumptionLoser checks that res is one of the two refusals a call
// that lost the race answers: 409 while the winner holds the row in
// `finalizing`, or 410 once the winner committed `ready`.
func assertConsumptionLoser(t *testing.T, attempt int, res raceResult) {
	t.Helper()
	var env finalizeConflict
	if err := json.Unmarshal(res.body, &env); err != nil {
		t.Errorf("attempt %d: decode refusal: %v; body %s", attempt, err, res.body)
		return
	}
	switch {
	case res.code == http.StatusConflict && env.Error.Code == "INVALID_STATE_TRANSITION":
	case res.code == http.StatusGone && env.Error.Code == "UPLOAD_TOKEN_CONSUMED":
	default:
		t.Errorf("attempt %d: status %d code %q, want 409 INVALID_STATE_TRANSITION or 410 UPLOAD_TOKEN_CONSUMED; body %s",
			attempt, res.code, env.Error.Code, res.body)
	}
}

// assertNoWriteOverTerminal checks that no recorded transition left a
// terminal state.
func assertNoWriteOverTerminal(t *testing.T, attempt int, store *transitionStore) {
	t.Helper()
	for _, tr := range store.snapshot() {
		if session.IsTerminal(tr.from) {
			t.Errorf("attempt %d: recorded %s -> %s, a write away from a terminal state; transitions %v",
				attempt, tr.from, tr.to, store.snapshot())
		}
	}
}

// Two keyless finalize calls race on one `created` session, both past their
// pre-lock read before either commits. Exactly one is admitted and closes the
// session to `ready`; the other is refused under the row lock with 409.
//
// diagnosis: the finalize state writes are no longer checked against the
// locked session row, so two concurrent finalize calls both commit
// `finalizing` and both answer 200, or the refused call reports a state other
// than the one the winner committed.
// spec: 15.1 (finalize precondition), 7.2 (terminal states)
func TestConcurrentFinalizeAdmitsExactlyOne_spec_15_1(t *testing.T) {
	for attempt := range raceAttempts {
		f := newFinalizeRaceFixture(t, 2)
		finalize := func() (int, []byte) { return f.call(http.MethodPost, "/finalize") }
		results := runRacing(t, finalize, finalize)

		var ok, conflict []raceResult
		for _, res := range results {
			switch res.code {
			case http.StatusOK:
				ok = append(ok, res)
			case http.StatusConflict:
				conflict = append(conflict, res)
			default:
				t.Errorf("attempt %d: finalize status %d, body %s", attempt, res.code, res.body)
			}
		}
		if len(ok) != 1 || len(conflict) != 1 {
			t.Fatalf("attempt %d: got %d x 200 and %d x 409, want one of each; transitions %v",
				attempt, len(ok), len(conflict), f.store.snapshot())
		}
		assertFinalizeConflict(t, attempt, conflict[0], session.StateFinalizing, session.StateReady)
		if n := f.store.count(session.StateCreated, session.StateFinalizing); n != 1 {
			t.Errorf("attempt %d: created -> finalizing committed %d times, want 1; transitions %v",
				attempt, n, f.store.snapshot())
		}
		if st := f.finalState(t); st != session.StateReady {
			t.Errorf("attempt %d: final state = %s, want ready", attempt, st)
		}
	}
}

// Two keyless finalize calls and one DELETE race on one `created` session.
// At most one finalize is admitted, and a DELETE that succeeds leaves the row
// `cancelled`: no finalize write, at entry or at exit, replaces the terminal
// state it committed.
//
// diagnosis: the finalize state writes are no longer checked against the
// locked session row, so a finalize call admitted a second time or wrote
// `finalizing`, `ready`, or `failed` over the `cancelled` state DELETE
// committed.
// spec: 15.1 (finalize precondition), 7.2 (terminal states)
func TestConcurrentFinalizeAndDeleteKeepTerminal_spec_15_1(t *testing.T) {
	for attempt := range raceAttempts {
		f := newFinalizeRaceFixture(t, 0)
		finalize := func() (int, []byte) { return f.call(http.MethodPost, "/finalize") }
		del := func() (int, []byte) { return f.call(http.MethodDelete, "") }
		results := runRacing(t, finalize, finalize, del)

		admitted := 0
		for _, res := range results[:2] {
			switch res.code {
			case http.StatusOK:
				admitted++
			case http.StatusConflict:
				assertFinalizeConflict(t, attempt, res,
					session.StateFinalizing, session.StateReady, session.StateCancelled)
			default:
				t.Errorf("attempt %d: finalize status %d, body %s", attempt, res.code, res.body)
			}
		}
		if admitted > 1 {
			t.Errorf("attempt %d: %d finalize calls answered 200, want at most 1", attempt, admitted)
		}
		if n := f.store.count(session.StateCreated, session.StateFinalizing); n > 1 {
			t.Errorf("attempt %d: created -> finalizing committed %d times, want at most 1; transitions %v",
				attempt, n, f.store.snapshot())
		}
		if results[2].code == http.StatusOK {
			if st := f.finalState(t); st != session.StateCancelled {
				t.Errorf("attempt %d: DELETE answered 200 but final state = %s, want cancelled; transitions %v",
					attempt, st, f.store.snapshot())
			}
		}
		assertNoWriteOverTerminal(t, attempt, f.store)
	}
}

// Calls carrying one valid token race on two servers that share only the
// session row. Exactly one call finalizes the session, and every other call is
// refused, whichever server it reached.
//
// diagnosis: more than one 200 or more than one created -> finalizing commit
// means §7.1 consumption is not derived from the shared row, or the locked
// precondition no longer refuses the loser. Any other refusal, such as a 401,
// means one server holds token state its peer lacks.
// spec: 7.1 (Single-use invalidation), 15.1 (finalize precondition), 10.1.1 (Gateway replicas)
func TestOneTokenRacedAcrossTwoServersFinalizesOnce_spec_7_1(t *testing.T) {
	for attempt := range raceAttempts {
		f := newFinalizeTwoServerFixture(t)
		calls := make([]func() (int, []byte), consumptionRaceCallers)
		for i := range calls {
			calls[i] = func() (int, []byte) { return f.finalize(i % len(f.handlers)) }
		}
		admitted := 0
		for _, res := range runRacing(t, calls...) {
			if res.code == http.StatusOK {
				admitted++
				continue
			}
			assertConsumptionLoser(t, attempt, res)
		}
		if admitted != 1 {
			t.Errorf("attempt %d: %d finalize calls answered 200, want 1; transitions %v",
				attempt, admitted, f.store.snapshot())
		}
		if n := f.store.count(session.StateCreated, session.StateFinalizing); n != 1 {
			t.Errorf("attempt %d: created -> finalizing committed %d times, want 1; transitions %v",
				attempt, n, f.store.snapshot())
		}
		if st := storedRaceState(t, f.store); st != session.StateReady {
			t.Errorf("attempt %d: final state = %s, want ready", attempt, st)
		}
	}
}
