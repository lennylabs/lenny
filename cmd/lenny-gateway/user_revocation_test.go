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
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/bindrelease"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
)

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

// TestBindReleaseFanOutReportsTheReleaseOutcomes_spec_11_4 asserts that
// terminateLocal releases each matching registry entry through the
// revoked-binding releaser rather than sending its own Shutdown, and maps
// the outcomes into the response: a release counts one pod, an error names
// the session as a failure, and a release that released nothing counts
// nothing. A session with no registry entry here is not released.
//
// spec: §11.4 step 2 (User Invalidation), §10.1.5 (Stale Replica Behavior)
func TestBindReleaseFanOutReportsTheReleaseOutcomes_spec_11_4(t *testing.T) {
	reg := podsession.NewRegistry()
	for _, id := range []string{"run_released", "run_noop", "run_err", "run_other"} {
		reg.Put(&podsession.BindResult{SessionID: id})
	}
	rel := &fakeRevokedReleaser{results: map[string]releaseResult{
		"run_released": {released: true},
		"run_noop":     {},
		"run_err":      {err: errors.New("executor release failed")},
	}}
	fan := &bindReleaseFanOut{registry: reg, releaser: rel}
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

// TestBindReleaseFanOutDetachesTheReleaseContext_spec_11_4 asserts that a
// cancelled admin request does not cut a full_revoke release: the context
// each release receives is not done and carries the
// userTerminateRPCTimeout deadline.
//
// spec: §11.4 step 2 (User Invalidation)
func TestBindReleaseFanOutDetachesTheReleaseContext_spec_11_4(t *testing.T) {
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "run_a"})
	rel := &fakeRevokedReleaser{}
	fan := &bindReleaseFanOut{registry: reg, releaser: rel}
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

// TestBindReleaseFanOutPublishesCrossReplica is the §11.4 step-2 fix:
// the handling replica's full_revoke fans the release request out over
// Redis pub/sub so peer replicas release the bindings they hold. F-11.4.3.
func TestBindReleaseFanOutPublishesCrossReplica_spec_11_4_step_2(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	// Replica A is the handling replica and holds none of the revoked
	// sessions locally. Replica B holds run_b's binding; its local
	// releaser must observe the request A publishes.
	peerB := &recordingPeer{}
	propB := bindrelease.New(peerB, pubsub.New(clientB), "replica-B")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go propB.Run(ctx)

	fanA := &bindReleaseFanOut{registry: podsession.NewRegistry(), releaser: &fakeRevokedReleaser{}}
	fanA.prop = bindrelease.New(fanA, pubsub.New(clientA), "replica-A")

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

// recordingPeer is a bindrelease.LocalTerminator that records the
// requests a peer replica's subscriber applies, standing in for the
// binding-release fan-out on the receiving replica.
type recordingPeer struct {
	mu       sync.Mutex
	requests []bindrelease.Request
}

func (r *recordingPeer) TerminateLocal(_ context.Context, req bindrelease.Request) bindrelease.Result {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	return bindrelease.Result{}
}

func (r *recordingPeer) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *recordingPeer) last() bindrelease.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return bindrelease.Request{}
	}
	return r.requests[len(r.requests)-1]
}

func TestBindReleaseFanOutEmptySessionSet(t *testing.T) {
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "run_a"})
	rel := &fakeRevokedReleaser{}
	fan := &bindReleaseFanOut{registry: reg, releaser: rel}
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

func (f *fakeAdminIssuedTokenStore) RecordWithAudit(context.Context, issuedtokenstore.IssuedToken, []issuedtokenstore.SourceToken, string, json.RawMessage, time.Time) (audit.Row, error) {
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
	_ admin.UserPodTerminator = (*bindReleaseFanOut)(nil)
	_ admin.UserTokenRevoker  = (*userTokenRevoker)(nil)
	// *issuedtokenstore.Store satisfies the consumer-side store interface
	// the adapter now depends on, so the production wiring is type-checked.
	_ adminIssuedTokenStore         = (*issuedtokenstore.Store)(nil)
	_ userTokenStore                = (*issuedtokenstore.Store)(nil)
	_ crossTenantReadEmitter        = (*auditstore.Store)(nil)
	_ revocationPropagationObserver = (*recordingRevocationObserver)(nil)
)
