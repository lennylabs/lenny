// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	podterminateprop "github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podterminate/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
)

// newLocalRevocationDenyList builds the §11.4 step-1 deny list the
// userLeaseRevoker requires: a *credrenewalprop.Propagator wrapping a
// fresh single-replica DenyList with a nil bus. The propagator is a
// local-only pass-through in this configuration (no pub/sub) but still
// satisfies the cross-replica marker so the §11.4 wiring contract is
// honored. The returned denylist.DenyList is the underlying state for
// assertions; the propagator is what the userLeaseRevoker drives.
// spec: §11.4 step 1.
func newLocalRevocationDenyList() (*credrenewalprop.Propagator, *denylist.DenyList) {
	dl := denylist.New()
	return credrenewalprop.New(dl, nil, nil), dl
}

// spec: §11.4 full_revoke — the gateway-side fan-out adapters.

// poolLease returns a valid pool-backed proxy lease bound to session.
func poolLease(leaseID, sessionID string) credential.Lease {
	return credential.Lease{
		LeaseID:      leaseID,
		SessionID:    sessionID,
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-" + leaseID,
		DeliveryMode: credential.DeliveryProxy,
		IssuedAt:     time.Now(),
		ExpiresAt:    time.Now().Add(time.Hour),
		Proxy: &credential.ProxyConfig{
			ProxyURL:     "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: "anthropic",
			LeaseToken:   "lt-" + leaseID,
		},
	}
}

// orderedLeaseStep records, in one sequence, every Release the lease step
// makes and every deny-list write it makes, so a test can assert that each
// Release precedes the first deny-list write. Release removes the lease
// record, as both assigners do, and fails for the leases named in failIDs.
type orderedLeaseStep struct {
	leases  *credleasestore.Store
	failIDs map[string]bool
	events  []string
}

func (o *orderedLeaseStep) Release(leaseID string) error {
	o.events = append(o.events, "release:"+leaseID)
	o.leases.Remove(leaseID)
	if o.failIDs[leaseID] {
		return errors.New("token service: revoke credentials: unavailable")
	}
	return nil
}

func (o *orderedLeaseStep) Revoke(key credential.CredentialKey) {
	o.events = append(o.events, "deny:"+key.CredentialID)
}

func (o *orderedLeaseStep) IsCrossReplicaPropagator() {}

// spec: §11.4 step 1 (User Invalidation), §4.9 (Credential Leasing Service)
func TestUserLeaseRevokerRevokesAndDenies(t *testing.T) {
	leases := credleasestore.New()
	prop, deny := newLocalRevocationDenyList()
	_ = leases.Put(poolLease("l1", "run_a"))
	_ = leases.Put(poolLease("l2", "run_a"))
	_ = leases.Put(poolLease("l4", "run_c"))
	_ = leases.Put(poolLease("l3", "run_b")) // a session not being revoked
	step := &orderedLeaseStep{leases: leases, failIDs: map[string]bool{"l2": true}}

	revoker := &userLeaseRevoker{leases: leases, assigner: step, denyList: prop}
	n := revoker.RevokeUserLeases("acme", "alice@acme.com", []string{"run_a", "run_c"})
	if n != 3 {
		t.Fatalf("RevokeUserLeases counted %d leases, want 3 (a failed Release still counts)", n)
	}
	// Every enumerated lease was returned through the assigner, which
	// removed its record; the failed return's record is gone too.
	for _, id := range []string{"l1", "l2", "l4"} {
		if _, ok := leases.GetByID(id); ok {
			t.Errorf("returned lease %s still in the store", id)
		}
		if !deny.Revoked(poolLease(id, "").CredentialKey()) {
			t.Errorf("lease %s's credential is not on the deny list", id)
		}
	}
	// The untouched session's lease survives.
	if _, ok := leases.GetByID("l3"); !ok {
		t.Error("lease l3 for an unrevoked session was dropped")
	}
	if deny.Revoked(poolLease("l3", "run_b").CredentialKey()) {
		t.Error("lease l3's credential was wrongly added to the deny list")
	}
	if deny.Len() != n {
		t.Errorf("deny list holds %d entries, want the counted %d", deny.Len(), n)
	}
}

// TestUserLeaseRevokerReturnsEveryLeaseBeforeTheFirstDenial_spec_11_4 asserts
// the lease step's order: every Release precedes the first deny-list write,
// and a Release error still leaves the lease deny-listed and counted.
//
// spec: §11.4 step 1 (User Invalidation)
func TestUserLeaseRevokerReturnsEveryLeaseBeforeTheFirstDenial_spec_11_4(t *testing.T) {
	leases := credleasestore.New()
	_ = leases.Put(poolLease("l1", "run_a"))
	_ = leases.Put(poolLease("l2", "run_b"))
	_ = leases.Put(poolLease("l3", "run_b"))
	step := &orderedLeaseStep{leases: leases, failIDs: map[string]bool{"l1": true}}
	revoker := &userLeaseRevoker{leases: leases, assigner: step, denyList: step}
	if n := revoker.RevokeUserLeases("acme", "alice@acme.com", []string{"run_a", "run_b"}); n != 3 {
		t.Fatalf("RevokeUserLeases counted %d, want 3", n)
	}
	firstDeny, lastRelease, denies := -1, -1, 0
	for i, e := range step.events {
		switch {
		case strings.HasPrefix(e, "deny:"):
			denies++
			if firstDeny < 0 {
				firstDeny = i
			}
		case strings.HasPrefix(e, "release:"):
			lastRelease = i
		}
	}
	if denies != 3 || lastRelease < 0 || firstDeny < lastRelease {
		t.Errorf("events = %v, want three releases before three deny-list writes", step.events)
	}
}

func TestUserLeaseRevokerNoLeases(t *testing.T) {
	leases := credleasestore.New()
	prop, deny := newLocalRevocationDenyList()
	revoker := &userLeaseRevoker{leases: leases, assigner: &orderedLeaseStep{leases: leases}, denyList: prop}
	if n := revoker.RevokeUserLeases("acme", "bob@acme.com", []string{"run_x"}); n != 0 {
		t.Errorf("RevokeUserLeases for a session with no leases revoked %d, want 0", n)
	}
	if deny.Len() != 0 {
		t.Errorf("the deny list gained %d entries for a no-lease user, want 0", deny.Len())
	}
}

func TestUserLeaseRevokerEmptySessionSet(t *testing.T) {
	leases := credleasestore.New()
	prop, _ := newLocalRevocationDenyList()
	_ = leases.Put(poolLease("l1", "run_a"))
	revoker := &userLeaseRevoker{leases: leases, assigner: &orderedLeaseStep{leases: leases}, denyList: prop}
	if n := revoker.RevokeUserLeases("acme", "carol@acme.com", nil); n != 0 {
		t.Errorf("RevokeUserLeases with no sessions revoked %d, want 0", n)
	}
	if _, ok := leases.GetByID("l1"); !ok {
		t.Error("an unrelated lease was dropped for an empty session set")
	}
}

// TestUserLeaseRevokerRequiresCrossReplicaPropagator asserts the §11.4
// step-1 wiring contract: only *credrenewalprop.Propagator satisfies
// credentialDenyList. A bare *denylist.DenyList carries the revocation
// only on the calling replica and is no longer accepted by the
// interface, so an accidental future wiring downgrade fails at compile
// time. spec: §11.4 step 1.
func TestUserLeaseRevokerRequiresCrossReplicaPropagator_spec_11_4_step_1(t *testing.T) {
	// Compile-time assertion that the propagator satisfies the
	// interface; the file would not compile if it did not.
	var _ credentialDenyList = (*credrenewalprop.Propagator)(nil)
	// Sibling assertion in a runtime form so the test names the bare
	// DenyList path explicitly. The line is dead code — the if check
	// is unreachable — but `denylist.DenyList` not satisfying the
	// interface is the entire point.
	var d any = denylist.New()
	if _, ok := d.(credentialDenyList); ok {
		t.Fatalf("bare *denylist.DenyList satisfies credentialDenyList; the cross-replica marker is missing — §11.4 step 1 would be replica-local")
	}
}

// fakeRevokedReleaser is a revokedBindingReleaser that returns a configured
// result per session and records the context each call received.
type fakeRevokedReleaser struct {
	mu      sync.Mutex
	results map[string]releaseResult
	calls   []string
	ctxs    []context.Context
	// errAtCall is each call context's Err() read during the call.
	errAtCall []error
}

type releaseResult struct {
	released bool
	err      error
}

func (f *fakeRevokedReleaser) ReleaseRevokedBinding(ctx context.Context, tenantID, sessionID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, tenantID+"/"+sessionID)
	f.ctxs = append(f.ctxs, ctx)
	f.errAtCall = append(f.errAtCall, ctx.Err())
	r := f.results[sessionID]
	return r.released, r.err
}

// TestPodTerminateFanOutReportsTheReleaseOutcomes_spec_11_4 asserts that
// terminateLocal releases each matching registry entry through the
// revoked-binding releaser rather than sending its own Shutdown, and maps
// the outcomes into the response: a release counts one pod, an error names
// the session as a failure, and a release that released nothing counts
// nothing. A session with no registry entry here is not released.
//
// spec: §11.4 step 2 (User Invalidation), §10.1.5 (Stale Replica Behavior)
func TestPodTerminateFanOutReportsTheReleaseOutcomes_spec_11_4(t *testing.T) {
	reg := podsession.NewRegistry()
	for _, id := range []string{"run_released", "run_noop", "run_err", "run_other"} {
		reg.Put(&podsession.BindResult{SessionID: id})
	}
	rel := &fakeRevokedReleaser{results: map[string]releaseResult{
		"run_released": {released: true},
		"run_noop":     {},
		"run_err":      {err: errors.New("executor release failed")},
	}}
	fan := &podTerminateFanOut{registry: reg, releaser: rel}
	res := fan.TerminateUserSessions(context.Background(), "acme", "alice@acme.com",
		[]string{"run_released", "run_noop", "run_err", "run_unbound"})
	if res.PodsTerminated != 1 {
		t.Errorf("PodsTerminated = %d, want 1", res.PodsTerminated)
	}
	if len(res.FailedSessions) != 1 || res.FailedSessions[0] != "run_err" {
		t.Errorf("FailedSessions = %v, want [run_err]", res.FailedSessions)
	}
	sort.Strings(rel.calls)
	want := []string{"acme/run_err", "acme/run_noop", "acme/run_released"}
	if strings.Join(rel.calls, ",") != strings.Join(want, ",") {
		t.Errorf("release calls = %v, want %v", rel.calls, want)
	}
}

// TestPodTerminateFanOutDetachesTheReleaseContext_spec_11_4 asserts that a
// cancelled admin request does not cut a full_revoke release: the context
// each release receives is not done and carries the
// userTerminateRPCTimeout deadline.
//
// spec: §11.4 step 2 (User Invalidation)
func TestPodTerminateFanOutDetachesTheReleaseContext_spec_11_4(t *testing.T) {
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "run_a"})
	rel := &fakeRevokedReleaser{}
	fan := &podTerminateFanOut{registry: reg, releaser: rel}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	fan.TerminateUserSessions(ctx, "acme", "alice@acme.com", []string{"run_a"})
	if len(rel.ctxs) != 1 {
		t.Fatalf("release calls = %d, want 1", len(rel.ctxs))
	}
	if err := rel.errAtCall[0]; err != nil {
		t.Fatalf("release context was done during the call: %v; a cancelled request cut the release", err)
	}
	dl, ok := rel.ctxs[0].Deadline()
	if !ok {
		t.Fatal("release context carries no deadline")
	}
	if d := dl.Sub(start); d < userTerminateRPCTimeout-time.Second || d > userTerminateRPCTimeout+time.Second {
		t.Errorf("release deadline is %v after the call, want about %v", d, userTerminateRPCTimeout)
	}
}

// TestPodTerminateFanOutPublishesCrossReplica is the §11.4 step-2 fix:
// the handling replica's full_revoke fans the release request out over
// Redis pub/sub so peer replicas release the bindings they hold. F-11.4.3.
func TestPodTerminateFanOutPublishesCrossReplica_spec_11_4_step_2(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	// Replica A is the handling replica and holds none of the revoked
	// sessions locally. Replica B holds run_b's binding; its local
	// releaser must observe the request A publishes.
	peerB := &recordingPeer{}
	propB := podterminateprop.New(peerB, pubsub.New(clientB), "replica-B")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go propB.Run(ctx)

	fanA := &podTerminateFanOut{registry: podsession.NewRegistry(), releaser: &fakeRevokedReleaser{}}
	fanA.prop = podterminateprop.New(fanA, pubsub.New(clientA), "replica-A")

	// miniredis drops a publish that lands before B's SUBSCRIBE registers,
	// so re-issue the full_revoke until B observes the request.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fanA.TerminateUserSessions(ctx, "acme", "alice@acme.com", []string{"run_b"})
		if peerB.count() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := peerB.last()
	if peerB.count() == 0 {
		t.Fatal("replica B never received the §11.4 cross-replica release request")
	}
	if got.UserID != "alice@acme.com" || got.Origin != "replica-A" {
		t.Errorf("replica B received %+v, want acme/alice from replica-A", got)
	}
	if len(got.SessionIDs) != 1 || got.SessionIDs[0] != "run_b" {
		t.Errorf("replica B received sessions %v, want [run_b]", got.SessionIDs)
	}
}

// recordingPeer is a podterminateprop.LocalTerminator that records the
// requests a peer replica's subscriber applies, standing in for the
// binding-release fan-out on the receiving replica.
type recordingPeer struct {
	mu       sync.Mutex
	requests []podterminateprop.Request
}

func (r *recordingPeer) TerminateLocal(_ context.Context, req podterminateprop.Request) podterminateprop.Result {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	return podterminateprop.Result{}
}

func (r *recordingPeer) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *recordingPeer) last() podterminateprop.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return podterminateprop.Request{}
	}
	return r.requests[len(r.requests)-1]
}

func TestPodTerminateFanOutEmptySessionSet(t *testing.T) {
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "run_a"})
	rel := &fakeRevokedReleaser{}
	fan := &podTerminateFanOut{registry: reg, releaser: rel}
	res := fan.TerminateUserSessions(context.Background(), "acme", "bob@acme.com", nil)
	if res.PodsTerminated != 0 || len(res.FailedSessions) != 0 || len(rel.calls) != 0 {
		t.Errorf("an empty session set produced %+v with calls %v, want a zero result", res, rel.calls)
	}
}

// recordingRevocationObserver captures the ObserveRevocationPropagation
// calls the admin-rotation adapter makes so a test can assert the SEC-TS-1
// producer fires exactly on the durable-revoke success path, keyed by the
// §16.7 propagation_mode outcome. spec: §16.7 — SEC-TS-1.
type recordingRevocationObserver struct {
	outcomes []string
}

func (r *recordingRevocationObserver) ObserveRevocationPropagation(outcome string, _ time.Duration) {
	r.outcomes = append(r.outcomes, outcome)
}

// fakeAdminIssuedTokenStore is a Postgres-free stand-in for
// *issuedtokenstore.Store so the adapter's audit-vocabulary and SEC-TS-1
// metric wiring can be exercised at tier 1. revokeErr, when non-nil, is
// returned by RevokeWithAudit so the fail-closed durable-revoke branch is
// reachable without a real datastore.
type fakeAdminIssuedTokenStore struct {
	revokeErr    error
	revokeCalled int
}

func (f *fakeAdminIssuedTokenStore) Record(context.Context, issuedtokenstore.IssuedToken) error {
	return nil
}

func (f *fakeAdminIssuedTokenStore) RecordWithAudit(context.Context, issuedtokenstore.IssuedToken, string, json.RawMessage, time.Time) (audit.Row, error) {
	return audit.Row{}, nil
}

func (f *fakeAdminIssuedTokenStore) RevokeWithAudit(_ context.Context, _, _, _, _ string, _ json.RawMessage, _ time.Time) (audit.Row, error) {
	f.revokeCalled++
	return audit.Row{}, f.revokeErr
}

func (f *fakeAdminIssuedTokenStore) WithSubjectLock(ctx context.Context, _, _ string, fn func(context.Context) error) error {
	return fn(ctx)
}

// recordingRevocationCache records the jtis pushed onto the cross-replica
// cache so a test can assert the fail-closed ordering: the cache push
// happens only after the durable write commits.
type recordingRevocationCache struct {
	revoked []string
}

func (c *recordingRevocationCache) Revoke(jti string) { c.revoked = append(c.revoked, jti) }

// TestAdminDurableRevokeObservesPostgresOnly_SEC_TS_1 pins the SEC-TS-1
// producer wiring on the gateway admin-rotation path: a successful durable
// revoke observes lenny_token_revocation_propagation_seconds keyed by the
// §16.7 propagation_mode (`postgres_only`), which the C3/§7 design lists as
// a producer site alongside the Token Service. Before the fix the adapter
// recorded propagation_mode in the audit payload but never observed the
// histogram, so no producer fired on this path and the outcomes slice would
// stay empty. spec: §16.7 — SEC-TS-1.
func TestAdminDurableRevokeObservesPostgresOnly_SEC_TS_1(t *testing.T) {
	obs := &recordingRevocationObserver{}
	cache := &recordingRevocationCache{}
	a := adminIssuedTokens{
		store:   &fakeAdminIssuedTokenStore{},
		cache:   cache,
		metrics: obs,
		clock:   func() time.Time { return time.Unix(0, 0).UTC() },
	}
	if err := a.DurableRevoke(context.Background(), "AdminTenant", "jti-old", time.Unix(0, 0).UTC()); err != nil {
		t.Fatalf("DurableRevoke: %v", err)
	}
	if len(obs.outcomes) != 1 || obs.outcomes[0] != "postgres_only" {
		t.Fatalf("SEC-TS-1 producer did not fire: observed outcomes %v, want [postgres_only]", obs.outcomes)
	}
	if len(cache.revoked) != 1 || cache.revoked[0] != "jti-old" {
		t.Errorf("cross-replica cache push = %v, want [jti-old] after the durable commit", cache.revoked)
	}

	// A nil clock defaults to the wall clock, so the producer still fires
	// (the injected-clock default path). This covers the now() fallback the
	// production wiring never hits (it always injects clockinject.Now).
	obsNil := &recordingRevocationObserver{}
	aNil := adminIssuedTokens{store: &fakeAdminIssuedTokenStore{}, cache: &recordingRevocationCache{}, metrics: obsNil}
	if err := aNil.DurableRevoke(context.Background(), "AdminTenant", "jti-old", time.Now().UTC()); err != nil {
		t.Fatalf("DurableRevoke with a nil clock: %v", err)
	}
	if len(obsNil.outcomes) != 1 || obsNil.outcomes[0] != "postgres_only" {
		t.Fatalf("SEC-TS-1 producer did not fire with a nil clock: %v, want [postgres_only]", obsNil.outcomes)
	}
}

// TestAdminDurableRevokeNoObserveOnDurableFailure_SEC_TS_1 asserts the
// producer does NOT fire when the durable Postgres write fails: the
// observation is bracketed to run only after RevokeWithAudit commits, so a
// failed revoke surfaces the error, pushes nothing onto the cache (the
// §13.3 fail-closed ordering), and records no propagation latency. This
// distinguishes the corrected wiring from a naive unconditional observe.
// spec: §13.3, §16.7 — SEC-TS-1.
func TestAdminDurableRevokeNoObserveOnDurableFailure_SEC_TS_1(t *testing.T) {
	obs := &recordingRevocationObserver{}
	cache := &recordingRevocationCache{}
	a := adminIssuedTokens{
		store:   &fakeAdminIssuedTokenStore{revokeErr: errors.New("postgres down")},
		cache:   cache,
		metrics: obs,
		clock:   func() time.Time { return time.Unix(0, 0).UTC() },
	}
	err := a.DurableRevoke(context.Background(), "AdminTenant", "jti-old", time.Unix(0, 0).UTC())
	if err == nil {
		t.Fatal("DurableRevoke returned nil on a durable-write failure; the caller must retry")
	}
	if len(obs.outcomes) != 0 {
		t.Errorf("SEC-TS-1 producer fired on a failed durable revoke: %v, want none", obs.outcomes)
	}
	if len(cache.revoked) != 0 {
		t.Errorf("cache push on a failed durable revoke: %v, want none (fail-closed)", cache.revoked)
	}
}

// TestAdminDurableRevokeNoObserveOnAlreadyRevoked_SEC_TS_1 asserts an
// idempotent no-op (ErrNotFound: already revoked or absent) neither observes
// the histogram nor double-pushes the cache, so a retried rotation does not
// inflate the propagation metric. spec: §16.7 — SEC-TS-1.
func TestAdminDurableRevokeNoObserveOnAlreadyRevoked_SEC_TS_1(t *testing.T) {
	obs := &recordingRevocationObserver{}
	cache := &recordingRevocationCache{}
	a := adminIssuedTokens{
		store:   &fakeAdminIssuedTokenStore{revokeErr: issuedtokenstore.ErrNotFound},
		cache:   cache,
		metrics: obs,
		clock:   func() time.Time { return time.Unix(0, 0).UTC() },
	}
	if err := a.DurableRevoke(context.Background(), "AdminTenant", "jti-old", time.Unix(0, 0).UTC()); err != nil {
		t.Fatalf("DurableRevoke on an already-revoked jti should be a nil no-op, got %v", err)
	}
	if len(obs.outcomes) != 0 {
		t.Errorf("SEC-TS-1 producer fired on an idempotent no-op: %v, want none", obs.outcomes)
	}
	if len(cache.revoked) != 0 {
		t.Errorf("cache push on an idempotent no-op: %v, want none", cache.revoked)
	}
}

// The fan-out adapters satisfy the §11.4 admin-router interfaces they
// are wired against in WithUserRevocation.
var (
	_ admin.UserPodTerminator = (*podTerminateFanOut)(nil)
	_ admin.UserLeaseRevoker  = (*userLeaseRevoker)(nil)
	_ admin.UserTokenRevoker  = (*userTokenRevoker)(nil)
	// *issuedtokenstore.Store satisfies the consumer-side store interface
	// the adapter now depends on, so the production wiring is type-checked.
	_ adminIssuedTokenStore         = (*issuedtokenstore.Store)(nil)
	_ revocationPropagationObserver = (*recordingRevocationObserver)(nil)
)
