// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
	opsLogging "github.com/lennylabs/lenny/pkg/observability/logging"
)

const devSecret = "ops-dev-secret"

// fakeRevocationSource is a mutable rule-1 row source. duringRead, when
// set, runs inside ListRevoked, standing in for an event that arrives
// while a read is in flight.
type fakeRevocationSource struct {
	mu         sync.Mutex
	rows       map[string][]string
	fail       bool
	duringRead func()
}

func (f *fakeRevocationSource) set(tenant string, jtis ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[tenant] = jtis
}

func (f *fakeRevocationSource) setFail(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
}

func (f *fakeRevocationSource) setDuringRead(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.duringRead = fn
}

func (f *fakeRevocationSource) ListTenants(context.Context) ([]string, error) {
	return []string{auth.DefaultTenantID, "globex"}, nil
}

func (f *fakeRevocationSource) ListRevoked(_ context.Context, tenantID string) ([]issuedtokenstore.IssuedToken, error) {
	f.mu.Lock()
	fail, hook, jtis := f.fail, f.duringRead, f.rows[tenantID]
	f.mu.Unlock()
	if hook != nil && tenantID == auth.DefaultTenantID {
		hook()
	}
	if fail {
		return nil, errors.New("postgres unreachable")
	}
	out := make([]issuedtokenstore.IssuedToken, 0, len(jtis))
	for _, jti := range jtis {
		out = append(out, issuedtokenstore.IssuedToken{TenantID: tenantID, JTI: jti})
	}
	return out, nil
}

// fakeClock is a settable clock for the revocation cache.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// revHarness is lenny-ops's authentication chain over a revocation set
// driven by a fake source, a fake ticker, a fake clock, and a fake bus.
type revHarness struct {
	t       *testing.T
	ctx     context.Context
	rev     *opsRevocation
	handler http.Handler
	opts    authmw.Options
	keySet  *jwt.KeySet
	signer  jwt.Signer
	src     *fakeRevocationSource
	clock   *fakeClock
	ticks   chan time.Time
	reads   chan error
	redis   *redis.Client
}

// newRevHarness builds the chain buildAuthConfig returns for the given
// posture: production, or non-production dev mode with the dev trust hook.
func newRevHarness(t *testing.T, production bool) *revHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	keySet, signer := testKeySet(t, 0x42)
	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	rev := newOpsRevocation(revocation.WithClock(clock.Now))
	in := authInputsFor(keySet, "", "", !production, production)
	if !production {
		in.hmacKeyFile = writeHMACKeyFile(t, devSecret)
	}
	in.revocations = rev
	cfg, err := buildAuthConfig(in)
	if err != nil || cfg == nil {
		t.Fatalf("buildAuthConfig: cfg=%v err=%v", cfg, err)
	}
	return &revHarness{
		t: t, ctx: ctx, rev: rev, keySet: keySet, signer: signer, clock: clock,
		handler: authmw.Wrap(okHandler(), cfg.Options),
		opts:    cfg.Options,
		src:     &fakeRevocationSource{rows: map[string][]string{}},
		ticks:   make(chan time.Time),
		reads:   make(chan error, 16),
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

// startReads starts the rule-1 loop and waits for its startup read.
func (h *revHarness) startReads() error {
	h.t.Helper()
	go h.rev.runReads(h.ctx, h.src, h.src, h.ticks, func(err error) { h.reads <- err })
	return h.awaitRead()
}

// tick fires the 30 s ticker and waits for the read it starts.
func (h *revHarness) tick() error {
	h.t.Helper()
	h.ticks <- h.clock.Now()
	return h.awaitRead()
}

func (h *revHarness) awaitRead() error {
	h.t.Helper()
	select {
	case err := <-h.reads:
		return err
	case <-time.After(5 * time.Second):
		h.t.Fatal("rule-1 read did not complete")
		return nil
	}
}

// startBus starts the rule-2 subscription over an in-process Redis.
func (h *revHarness) startBus() {
	h.t.Helper()
	mr := miniredis.RunT(h.t)
	h.redis = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	h.t.Cleanup(func() { _ = h.redis.Close() })
	go h.rev.runEvents(h.ctx, pubsub.New(h.redis))
}

// publishRevoked publishes a token.revoked event for jti until the set
// holds it. Publishing again is harmless, and it covers the window before
// the subscription attaches.
func (h *revHarness) publishRevoked(jti string) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !h.rev.IsRevoked(jti) {
		if time.Now().After(deadline) {
			h.t.Fatalf("event for %s never reached the set", jti)
		}
		if err := h.redis.Publish(h.ctx, propagator.Channel, jti).Err(); err != nil {
			h.t.Fatalf("publish: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *revHarness) keySetToken(jti, origin string) string {
	return mintKeySetToken(h.t, h.signer, opsClaims(jti, "", origin))
}

func (h *revHarness) devToken(jti string) string {
	return mintDevToken(h.t, devSecret, opsClaims(jti, "", ""))
}

// serve sends tok through the lenny-ops chain.
func (h *revHarness) serve(tok string) *httptest.ResponseRecorder {
	return serveThrough(h.handler, tok)
}

func serveThrough(handler http.Handler, tok string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// gatewayResponse is what a gateway replica's authmw, verifying through
// the same key set, writes for tok under opts.
func (h *revHarness) gatewayResponse(tok string, opts authmw.Options) *httptest.ResponseRecorder {
	opts.Verifier = h.keySet
	return serveThrough(authmw.Wrap(okHandler(), opts), tok)
}

type staticRevoked map[string]bool

func (s staticRevoked) IsRevoked(jti string) bool { return s[jti] }

type staleSet struct{}

func (staleSet) Stale() bool { return true }

type revokedPlayground struct{}

func (revokedPlayground) IsBearerRevoked(context.Context, string, string) (bool, error) {
	return true, nil
}

// assertSameResponse fails unless got matches want in status and body.
func assertSameResponse(t *testing.T, what string, got, want *httptest.ResponseRecorder) {
	t.Helper()
	if got.Code != want.Code || got.Body.String() != want.Body.String() {
		t.Errorf("%s: lenny-ops answered %d %s, the gateway answers %d %s",
			what, got.Code, got.Body.String(), want.Code, want.Body.String())
	}
}

func assertAdmitted(t *testing.T, what string, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusNoContent {
		t.Errorf("%s: status %d %s, want admitted", what, rr.Code, rr.Body.String())
	}
}

func assertRevoked(t *testing.T, what string, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("%s: status %d %s, want 401 for a revoked token", what, rr.Code, rr.Body.String())
	}
}

// TestRevocationSetRule1PeriodicReadReplacesSet pins rule 1 with the bus
// down: a row added to the source is refused only after the next 30 s
// read, a row removed from it is admitted after the next read, and a jti
// a rule-2 event added while that read ran stays refused after it.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 13.3 (Credential Flow)
func TestRevocationSetRule1PeriodicReadReplacesSet(t *testing.T) {
	h := newRevHarness(t, true)
	if err := h.startReads(); err != nil {
		t.Fatalf("startup read: %v", err)
	}
	added := h.keySetToken("jti-added", "")
	h.src.set(auth.DefaultTenantID, "jti-added")
	assertAdmitted(t, "row added, before the tick", h.serve(added))
	if err := h.tick(); err != nil {
		t.Fatalf("tick read: %v", err)
	}
	assertRevoked(t, "row added, after the tick", h.serve(added))

	during := h.keySetToken("jti-during", "")
	h.src.set(auth.DefaultTenantID)
	h.src.setDuringRead(func() { h.rev.cache.Revoke("jti-during") })
	if err := h.tick(); err != nil {
		t.Fatalf("tick read: %v", err)
	}
	h.src.setDuringRead(nil)
	assertAdmitted(t, "row removed, after the tick", h.serve(added))
	assertRevoked(t, "jti added by an event during the read", h.serve(during))
}

// TestRevocationSetRule2EventsApplyBeforeTheNextRead pins rule 2: a
// token.revoked event is enforced before the next read, including while
// the source fails, and an event that lands during a read survives it.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 13.3 (Credential Flow), 12.4 (Redis HA and Failure Modes)
func TestRevocationSetRule2EventsApplyBeforeTheNextRead(t *testing.T) {
	h := newRevHarness(t, true)
	h.startBus()
	if err := h.startReads(); err != nil {
		t.Fatalf("startup read: %v", err)
	}
	tok := h.keySetToken("jti-event", "")
	assertAdmitted(t, "before the event", h.serve(tok))
	h.publishRevoked("jti-event")
	assertRevoked(t, "after the event, before any tick", h.serve(tok))

	h.src.setFail(true)
	failingTok := h.keySetToken("jti-event-while-failing", "")
	h.publishRevoked("jti-event-while-failing")
	assertRevoked(t, "event while the source fails", h.serve(failingTok))

	h.src.setFail(false)
	inRead := h.keySetToken("jti-event-in-read", "")
	h.src.setDuringRead(func() { h.publishRevoked("jti-event-in-read") })
	if err := h.tick(); err != nil {
		t.Fatalf("tick read: %v", err)
	}
	assertRevoked(t, "event delivered during a read", h.serve(inRead))
}

// TestRevocationSetRule3MatchesGatewayRevokedResponse pins rule 3: a
// token whose jti is in the set gets the response a gateway replica's
// authmw writes for a revoked jti.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 13.3 (Credential Flow)
func TestRevocationSetRule3MatchesGatewayRevokedResponse(t *testing.T) {
	h := newRevHarness(t, true)
	h.src.set("globex", "jti-revoked")
	if err := h.startReads(); err != nil {
		t.Fatalf("startup read: %v", err)
	}
	tok := h.keySetToken("jti-revoked", "")
	want := h.gatewayResponse(tok, authmw.Options{Revocations: staticRevoked{"jti-revoked": true}})
	if want.Code != http.StatusUnauthorized {
		t.Fatalf("gateway reference response %d, want 401", want.Code)
	}
	assertSameResponse(t, "revoked jti", h.serve(tok), want)
}

// TestRevocationSetRule4RefusesBeforeFirstRead pins rule 4 and its
// precedence: before the first successful read a key-set token, a
// playground-origin key-set token, and a key-set token an event revoked
// each get the response authmw writes for a stale revocation set. A
// failed startup read keeps the gate closed, and the first successful
// read opens it.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 13.3 (Credential Flow)
func TestRevocationSetRule4RefusesBeforeFirstRead(t *testing.T) {
	h := newRevHarness(t, true)
	h.startBus()
	h.publishRevoked("jti-evented")
	h.src.setFail(true)
	if err := h.startReads(); err == nil {
		t.Fatal("startup read over a failing source returned nil")
	}
	plain := h.keySetToken("jti-plain", "")
	want := h.gatewayResponse(plain, authmw.Options{RevocationFreshness: staleSet{}})
	if want.Code != http.StatusServiceUnavailable {
		t.Fatalf("gateway reference response %d, want 503", want.Code)
	}
	for name, tok := range map[string]string{
		"key-set token":         plain,
		"playground key-set":    h.keySetToken("jti-pg", "playground"),
		"event-revoked key-set": h.keySetToken("jti-evented", ""),
	} {
		assertSameResponse(t, name, h.serve(tok), want)
	}
	h.src.setFail(false)
	if err := h.tick(); err != nil {
		t.Fatalf("tick read: %v", err)
	}
	assertAdmitted(t, "after the first successful read", h.serve(plain))
}

// TestRevocationSetRule5KeepsSetThroughPostgresOutage pins rule 5: after
// a read, reads failing for longer than 90 s leave the set in force, so a
// non-revoked token is admitted and a revoked one refused, even though
// the cache itself reports stale. A gate keyed on Cache.Stale would
// refuse both.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 25.15 (Failure Mode Analysis)
func TestRevocationSetRule5KeepsSetThroughPostgresOutage(t *testing.T) {
	h := newRevHarness(t, true)
	h.src.set(auth.DefaultTenantID, "jti-revoked")
	if err := h.startReads(); err != nil {
		t.Fatalf("startup read: %v", err)
	}
	h.src.setFail(true)
	for i := 0; i < 4; i++ {
		h.clock.advance(revocationReadInterval)
		if err := h.tick(); err == nil {
			t.Fatal("read over a failing source returned nil")
		}
	}
	if !h.rev.cache.Stale() {
		t.Fatal("precondition: the cache should report stale after 120 s of failed reads")
	}
	assertAdmitted(t, "non-revoked token during the outage", h.serve(h.keySetToken("jti-live", "")))
	assertRevoked(t, "revoked token during the outage", h.serve(h.keySetToken("jti-revoked", "")))
}

// TestRevocationSetRule6ExcludesDevTrustTokens pins rule 6: before the
// first read, a dev trust-hook token is admitted, and so is one whose jti
// is in the set, while a key-set token is refused.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 17.4
func TestRevocationSetRule6ExcludesDevTrustTokens(t *testing.T) {
	h := newRevHarness(t, false)
	h.rev.cache.Revoke("jti-dev-revoked")
	assertAdmitted(t, "dev trust-hook token", h.serve(h.devToken("jti-dev")))
	assertAdmitted(t, "dev trust-hook token whose jti is in the set", h.serve(h.devToken("jti-dev-revoked")))
	if rr := h.serve(h.keySetToken("jti-ks", "")); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("key-set token before the first read: status %d, want 503", rr.Code)
	}
}

// verifiedBeforeExpiry stands in for lenny-ops's verifier composition at
// a moment before a key-set token's exp: it accepts the token with the
// claims it carries, as the composition did, although the key set's own
// clock has since moved past exp.
type verifiedBeforeExpiry struct{ claims jwt.Claims }

func (v verifiedBeforeExpiry) Verify(string) (jwt.Claims, error) { return v.claims, nil }

// TestRevocationSetRule6ScopeHoldsWhenExpPassesAfterVerification pins
// that rule 6 follows which member signed a token rather than a second
// time-dependent verification: a key-set token whose exp passes between
// authmw's verification and the scope decision is still refused before
// the first read and still refused when its jti is in the set. A scope
// that re-verified the token through the key set would treat it as a
// dev trust-hook token and admit it in both cases.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 13.3 (Credential Flow)
func TestRevocationSetRule6ScopeHoldsWhenExpPassesAfterVerification(t *testing.T) {
	h := newRevHarness(t, false)
	claims := opsClaims("jti-expiring", "", "")
	claims.IssuedAt = time.Now().Add(-2 * time.Hour).Unix()
	claims.Expiry = time.Now().Add(-time.Hour).Unix()
	tok := mintKeySetToken(t, h.signer, claims)
	if _, err := h.keySet.Verify(tok); err == nil {
		t.Fatal("key set accepted a token past its exp; the case needs one it refuses")
	}
	opts := h.opts
	opts.Verifier = verifiedBeforeExpiry{claims: claims}
	handler := authmw.Wrap(okHandler(), opts)

	if rr := serveThrough(handler, tok); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("before the first read: status %d %s, want 503", rr.Code, rr.Body.String())
	}
	h.src.set(auth.DefaultTenantID, "jti-expiring")
	if err := h.startReads(); err != nil {
		t.Fatalf("startup read: %v", err)
	}
	if rr := serveThrough(handler, tok); rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), "TOKEN_REVOKED") {
		t.Errorf("revoked jti: status %d %s, want 401 TOKEN_REVOKED", rr.Code, rr.Body.String())
	}
}

// TestRevocationSetRule7RefusesPlaygroundBearer pins rule 7: after a
// read, a playground-origin key-set token whose jti is in no store gets
// the response authmw writes for a revoked playground bearer, while the
// same claims without the origin are admitted.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 27.3.1 (OIDC Cookie-to-MCP Bearer Exchange)
func TestRevocationSetRule7RefusesPlaygroundBearer(t *testing.T) {
	h := newRevHarness(t, true)
	if err := h.startReads(); err != nil {
		t.Fatalf("startup read: %v", err)
	}
	pg := h.keySetToken("jti-pg", "playground")
	want := h.gatewayResponse(pg, authmw.Options{PlaygroundRevocations: revokedPlayground{}})
	if want.Code != http.StatusUnauthorized {
		t.Fatalf("gateway reference response %d, want 401", want.Code)
	}
	assertSameResponse(t, "playground bearer", h.serve(pg), want)
	assertAdmitted(t, "same claims without origin", h.serve(h.keySetToken("jti-pg", "")))
}

// TestRevocationSetWithoutDSNRefusesKeySetTokens pins that without
// --postgres-dsn no read runs, so a key-set token gets the rule-4
// response with --production, and with the dev trust hook without it.
//
// spec: 25.4 (The lenny-ops Service: Authentication)
func TestRevocationSetWithoutDSNRefusesKeySetTokens(t *testing.T) {
	for name, production := range map[string]bool{"production": true, "dev trust hook": false} {
		t.Run(name, func(t *testing.T) {
			h := newRevHarness(t, production)
			tok := h.keySetToken("jti-no-dsn", "")
			want := h.gatewayResponse(tok, authmw.Options{RevocationFreshness: staleSet{}})
			assertSameResponse(t, "no DSN", h.serve(tok), want)
		})
	}
}

// TestRevocationTenantsListsDeletedAndDefault pins the rule-1 tenant
// lister: every tenant row, tombstoned ones included, and the default
// tenant once.
//
// spec: 13.3 (Credential Flow: Authoritative durability for revocation)
func TestRevocationTenantsListsDeletedAndDefault(t *testing.T) {
	ids, err := revocationTenants{store: listOnlyTenants{"acme", "globex"}}.ListTenants(context.Background())
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	if len(ids) != 3 || ids[2] != auth.DefaultTenantID {
		t.Errorf("ids = %v, want acme, globex, default", ids)
	}
	ids, err = revocationTenants{store: listOnlyTenants{auth.DefaultTenantID}}.ListTenants(context.Background())
	if err != nil || len(ids) != 1 {
		t.Errorf("ids = %v err = %v, want default once", ids, err)
	}
	if _, err := (revocationTenants{store: listOnlyTenants(nil)}).ListTenants(context.Background()); err == nil {
		t.Error("a failing tenant list should return an error")
	}
}

// listOnlyTenants serves tenant rows for the given IDs; a nil value fails.
type listOnlyTenants []string

func (l listOnlyTenants) List(context.Context, tenantstore.ListFilter) ([]tenantstore.Tenant, error) {
	if l == nil {
		return nil, errors.New("postgres unreachable")
	}
	out := make([]tenantstore.Tenant, 0, len(l))
	for _, id := range l {
		out = append(out, tenantstore.Tenant{ID: id})
	}
	return out, nil
}

// TestRevocationSetRule1FailedReadLogsAtErrorLevel pins the operator
// signal for a failed rule-1 read: under the lenny-ops structured logger,
// a failed read, including the startup read, emits a record whose level
// field is ERROR, and the read loop keeps running so the next read opens
// the gate. A record routed through the stdlib-log bridge carries level
// INFO, which a query filtering on level=ERROR does not see.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 25.15 (Failure Mode Analysis)
func TestRevocationSetRule1FailedReadLogsAtErrorLevel(t *testing.T) {
	var buf syncBuffer
	prevLogger := slog.Default()
	prevFlags, prevOut := log.Flags(), log.Writer()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		log.SetFlags(prevFlags)
		log.SetOutput(prevOut)
	})
	opsLogging.Setup(&buf, "lenny-ops")

	h := newRevHarness(t, true)
	h.src.setFail(true)
	go h.rev.runReads(h.ctx, h.src, h.src, h.ticks, func(err error) {
		logRevocationRead(err)
		h.reads <- err
	})
	if err := h.awaitRead(); err == nil {
		t.Fatal("startup read over a failing source returned nil")
	}

	records := buf.records(t)
	if len(records) != 1 {
		t.Fatalf("got %d log records after the failed startup read, want 1: %v", len(records), records)
	}
	rec := records[0]
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if msg, _ := rec["msg"].(string); strings.HasPrefix(msg, "ERROR") || !strings.Contains(msg, "revocation set read failed") {
		t.Errorf("msg = %q, want the read-failure message without a textual level prefix", msg)
	}
	if e, _ := rec["err"].(string); e == "" {
		t.Errorf("record carries no err field: %v", rec)
	}
	if rec["component"] != "lenny-ops" {
		t.Errorf("component = %v, want lenny-ops", rec["component"])
	}

	h.src.setFail(false)
	if err := h.tick(); err != nil {
		t.Fatalf("read after the failed startup read: %v", err)
	}
	if h.rev.Stale() {
		t.Fatal("the loop stopped after the failed startup read; the next read did not open the gate")
	}
	if n := len(buf.records(t)); n != 1 {
		t.Errorf("a successful read logged %d more records, want none", n-1)
	}
}

// syncBuffer is a bytes.Buffer safe for the read loop's goroutine to
// write while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// records decodes every JSON log line written so far.
func (b *syncBuffer) records(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %v\nline: %s", err, line)
		}
		out = append(out, rec)
	}
	return out
}

// TestStartRevocationSetWiresTheEventSubscriptionAndKeepsTheGateClosed
// pins the startRevocationSet composition: with Redis wired, a
// token.revoked event published on the cache/pub-sub concern reaches the
// set (rule 2), and with Postgres wired but unreachable no rule-1 read
// succeeds, so the gate stays closed and every Lenny-issued token keeps
// the rule-4 refusal. Without Redis the subscription is never started.
//
// spec: 25.4 (The lenny-ops Service: Authentication), 12.4 (Redis HA and Failure Modes)
func TestStartRevocationSetWiresTheEventSubscriptionAndKeepsTheGateClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	pool, err := pgxpool.New(ctx, "postgres://lenny@127.0.0.1:1/lenny?connect_timeout=1")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	w := &opsWiring{
		f: &opsFlags{
			redisCachePubSubURL: strPtr(""),
			redisPassword:       strPtr(""),
			redisAllowInsecure:  boolPtr(true),
		},
		opsWiringFields: opsWiringFields{ctx: ctx, pgPool: pool, redisClient: client},
	}
	rev := newOpsRevocation()
	w.startRevocationSet(rev)

	deadline := time.Now().Add(5 * time.Second)
	for !rev.IsRevoked("jti-wired") {
		if time.Now().After(deadline) {
			t.Fatal("a token.revoked event published on the cache/pub-sub concern never reached the set")
		}
		if err := client.Publish(ctx, propagator.Channel, "jti-wired").Err(); err != nil {
			t.Fatalf("publish: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !rev.Stale() {
		t.Error("the gate opened although every rule-1 read against an unreachable Postgres fails")
	}

	bare := newOpsRevocation()
	(&opsWiring{opsWiringFields: opsWiringFields{ctx: ctx}}).startRevocationSet(bare)
	if !bare.Stale() || bare.IsRevoked("jti-wired") {
		t.Error("without Postgres or Redis the set must stay empty with its gate closed")
	}
}
