// SPDX-License-Identifier: MIT

//go:build chaos

package chaos_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/credential/revocation"
	revocationprop "github.com/lennylabs/lenny/pkg/credential/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/storage/pubsub"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
)

// revChaosTenant is the tenant every token in this file belongs to.
const revChaosTenant = "acme"

// revChaosRegistry admits revChaosTenant, so the middleware reaches its
// revocation checks.
type revChaosRegistry struct{}

func (revChaosRegistry) IsRegistered(tenantID string) (bool, error) {
	return tenantID == revChaosTenant, nil
}

// revChaosTenants lists revChaosTenant for the rehydration.
type revChaosTenants struct{}

func (revChaosTenants) ListTenants(context.Context) ([]string, error) {
	return []string{revChaosTenant}, nil
}

// revChaosReplica is one stand-in gateway replica: a revocation cache fed
// by the token-revocation subscriber and the rehydration loop, as
// startBillingAndSecurityWorkers wires them, behind the production auth
// middleware. Its periodic rehydration is an hour away, so a revocation it
// applies within the test arrives over pub/sub or through the rehydration
// a subscription confirmation triggers, spaced by the gateway's default
// reconnect spacing. offset shifts the clock its staleness gate and the
// spacing read; reads counts its reads of issued_tokens.
type revChaosReplica struct {
	prop      *revocationprop.Propagator
	srv       *httptest.Server
	offset    atomic.Int64
	rehydErrs atomic.Int32
	reads     atomic.Int32
}

// countedSource counts the reads of issued_tokens that one replica makes.
type countedSource struct {
	src   revocation.Source
	reads *atomic.Int32
}

func (c countedSource) ListRevoked(ctx context.Context, tenantID string) ([]issuedtokenstore.IssuedToken, error) {
	c.reads.Add(1)
	return c.src.ListRevoked(ctx, tenantID)
}

// startRevChaosReplica starts a replica over the Redis client and the
// Postgres pool given, after a startup rehydration that must succeed.
func startRevChaosReplica(t *testing.T, ctx context.Context, rd redis.UniversalClient, pool *pgxpool.Pool, signer jwt.Verifier) *revChaosReplica {
	t.Helper()
	r := &revChaosReplica{}
	cache := revocation.NewCache(revocation.WithClock(func() time.Time {
		return time.Now().Add(time.Duration(r.offset.Load()))
	}))
	store := countedSource{src: issuedtokenstore.New(pool), reads: &r.reads}
	if err := cache.Rehydrate(ctx, revChaosTenants{}, store); err != nil {
		t.Fatalf("startup rehydration: %v", err)
	}
	r.prop = revocationprop.New(cache, pubsub.New(rd))
	trigger := revocation.NewTrigger()
	go r.prop.Run(ctx, trigger.Fire)
	go cache.RunRehydration(ctx, time.Hour, revocation.DefaultReconnectSpacing, trigger, revChaosTenants{}, store, func(err error) {
		r.rehydErrs.Add(1)
		t.Logf("replica rehydration failed: %v", err)
	})
	r.srv = httptest.NewServer(authmw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), authmw.Options{
		Verifier:            signer,
		MultiTenant:         true,
		Registry:            revChaosRegistry{},
		Revocations:         cache,
		RevocationFreshness: cache,
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// call presents bearer to the replica and returns the status and the
// error code of the body.
func (r *revChaosReplica) call(t *testing.T, bearer string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, r.srv.URL+"/v1/sessions", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("call replica: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, body.Error.Code
}

// awaitStatus polls the replica with bearer until it answers want or
// bound passes, and returns how long it took.
func (r *revChaosReplica) awaitStatus(t *testing.T, name, bearer string, want int, bound time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	deadline := start.Add(bound)
	for {
		code, errCode := r.call(t, bearer)
		if code == want {
			return time.Since(start)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s answered %d %s for %s, want %d", name, code, errCode, bound, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// revChaosEnv is one Postgres and one Redis with a recorded token, and the
// signer that mints its bearer.
type revChaosEnv struct {
	pg     *containers.Postgres
	rd     *containers.Redis
	store  *issuedtokenstore.Store
	signer *jwt.HMACSigner
}

func startRevChaosEnv(t *testing.T) *revChaosEnv {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	return &revChaosEnv{
		pg: pg, rd: containers.StartRedis(t, containers.RedisOptions{}),
		store:  issuedtokenstore.New(pg.Pool),
		signer: jwt.NewHMACSigner("revocation-chaos", []byte("revocation-chaos-key")),
	}
}

// mint records an issued_tokens row for jti and returns a bearer carrying it.
func (e *revChaosEnv) mint(t *testing.T, jti string) string {
	t.Helper()
	now := time.Now().UTC()
	if err := e.store.Record(context.Background(), issuedtokenstore.IssuedToken{
		JTI: jti, TenantID: revChaosTenant, Subject: "alice@acme.com", TokenHash: []byte(jti),
		Audience: "lenny-gateway", IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("record %s: %v", jti, err)
	}
	tok, err := e.signer.Sign(jwt.Claims{
		Subject: "alice@acme.com", TenantID: revChaosTenant, Expiry: now.Add(time.Hour).Unix(),
		Typ: pkgauth.TokenUserBearer, JWTID: jti,
	})
	if err != nil {
		t.Fatalf("sign %s: %v", jti, err)
	}
	return tok
}

// revoke durably revokes jti and then publishes it through replica, as the
// gateway's revoke paths commit before they push the cache.
func (e *revChaosEnv) revoke(t *testing.T, through *revChaosReplica, jti string) {
	t.Helper()
	if err := e.store.Revoke(context.Background(), revChaosTenant, jti, "explicit_revoke", time.Time{}); err != nil {
		t.Fatalf("revoke %s: %v", jti, err)
	}
	through.prop.Revoke(jti)
}

// proxiedPool returns a pool that reaches Postgres through a tcpfault
// proxy, and the proxy.
func proxiedPool(t *testing.T, dsn string) (*pgxpool.Pool, *tcpfault.Proxy) {
	t.Helper()
	proxy, proxied := tcpfault.StartURL(t, dsn)
	cfg, err := pgxpool.ParseConfig(proxied)
	if err != nil {
		t.Fatalf("parse proxied dsn: %v", err)
	}
	cfg.ConnConfig.ConnectTimeout = 2 * time.Second
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("proxied pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, proxy
}

// proxiedRedis returns a client that reaches Redis through a tcpfault
// proxy, and the proxy.
func proxiedRedis(t *testing.T, addr string) (*redis.Client, *tcpfault.Proxy) {
	t.Helper()
	proxy := tcpfault.Start(t, addr)
	client := redis.NewClient(&redis.Options{Addr: proxy.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client, proxy
}

// spec: §13.3 (Credential Flow) "Authoritative durability for revocation", §13.3 (Credential Flow) "Token rotation and revocation — no grace period"
// diagnosis: replica B, whose Redis link was cut while replica A revoked a
// token, kept admitting the token after its Redis came back, although its
// periodic rehydration is an hour away. B's revocation subscriber did not
// report the re-established subscription, or the rehydration loop did not
// reread issued_tokens when it was told to. A 200 from A after the revoke
// means the revoking replica does not apply its own revocation; a 401 from
// B while its Redis is down means the event reached B some other way and
// the case did not exercise the reconnect.
func TestRevocationMissedDuringARedisLinkLossIsRefusedAfterTheReplicaReconnects(t *testing.T) {
	e := startRevChaosEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	redisB, linkB := proxiedRedis(t, e.rd.Addr)
	a := startRevChaosReplica(t, ctx, e.rd.Client, e.pg.Pool, e.signer)
	b := startRevChaosReplica(t, ctx, redisB, e.pg.Pool, e.signer)

	probe := e.mint(t, "jti-probe")
	e.revoke(t, a, "jti-probe")
	b.awaitStatus(t, "replica B with the probe over a live subscription", probe, http.StatusUnauthorized, 10*time.Second)

	bearer := e.mint(t, "jti-revoked-during-link-loss")
	for name, r := range map[string]*revChaosReplica{"A": a, "B": b} {
		if code, errCode := r.call(t, bearer); code != http.StatusOK {
			t.Fatalf("replica %s answered %d %s before the revocation, want 200", name, code, errCode)
		}
	}

	linkB.Cut()
	e.revoke(t, a, "jti-revoked-during-link-loss")
	if code, errCode := a.call(t, bearer); code != http.StatusUnauthorized || errCode != "TOKEN_REVOKED" {
		t.Errorf("replica A answered %d %s after it revoked the token, want 401 TOKEN_REVOKED", code, errCode)
	}
	time.Sleep(time.Second)
	if code, _ := b.call(t, bearer); code != http.StatusOK {
		t.Fatalf("replica B answered %d while its Redis was down, want 200: the event reached it without a reconnect", code)
	}

	linkB.Restore(t)
	took := b.awaitStatus(t, "replica B after its Redis came back", bearer, http.StatusUnauthorized, 15*time.Second)
	t.Logf("replica B refused the token %s after its Redis came back", took.Round(time.Millisecond))
	if code, errCode := b.call(t, bearer); code != http.StatusUnauthorized || errCode != "TOKEN_REVOKED" {
		t.Errorf("replica B answered %d %s, want 401 TOKEN_REVOKED", code, errCode)
	}
}

// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
// diagnosis: a replica whose rehydrations fail because Postgres is
// unreachable did not answer 503 token_validation_unavailable once its
// last successful rehydration was more than 90 seconds old, or answered it
// before then, or did not return to admitting the token after Postgres came
// back and a rehydration succeeded. A failure before the outage means the
// replica never admitted the token. The case shifts the clock the
// staleness gate reads rather than waiting 90 seconds.
func TestReplicaWhoseRehydrationKeepsFailingRefusesValidationAfterTheStalenessWindow(t *testing.T) {
	e := startRevChaosEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	redisB, linkB := proxiedRedis(t, e.rd.Addr)
	pgB, pgLinkB := proxiedPool(t, e.pg.DSN)
	b := startRevChaosReplica(t, ctx, redisB, pgB, e.signer)
	bearer := e.mint(t, "jti-live")
	b.awaitStatus(t, "replica B before the outage", bearer, http.StatusOK, 10*time.Second)

	// reconnectUntil cuts and restores B's Redis link, so its subscriber
	// triggers a rehydration, until done holds.
	reconnectUntil := func(name string, done func() bool) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", name)
			}
			linkB.Cut()
			time.Sleep(200 * time.Millisecond)
			linkB.Restore(t)
			time.Sleep(time.Second)
		}
	}

	pgLinkB.Cut()
	reconnectUntil("a failed rehydration", func() bool { return b.rehydErrs.Load() > 0 })

	b.offset.Store(int64(60 * time.Second))
	if code, errCode := b.call(t, bearer); code != http.StatusOK {
		t.Errorf("replica B answered %d %s 60 s after its last successful rehydration, want 200 inside the 90 s window", code, errCode)
	}
	b.offset.Store(int64(91 * time.Second))
	if code, errCode := b.call(t, bearer); code != http.StatusServiceUnavailable || errCode != "token_validation_unavailable" {
		t.Errorf("replica B answered %d %s 91 s after its last successful rehydration, want 503 token_validation_unavailable", code, errCode)
	}

	pgLinkB.Restore(t)
	reconnectUntil("replica B to admit the token after Postgres came back", func() bool {
		code, _ := b.call(t, bearer)
		return code == http.StatusOK
	})
}

// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
// diagnosis: replica B started while its Redis link was cut, so its
// subscription was not confirmed when replica A revoked a token, and B
// kept admitting the token after its Redis came up, although its periodic
// rehydration is an hour away. B's revocation subscriber did not report
// the first subscription confirmation, or the rehydration loop held the
// first requested read. A 401 from B while its Redis is down means the
// event reached it some other way and the case did not exercise the
// first confirmation.
func TestRevocationMissedBeforeTheFirstSubscriptionIsRefusedOnceItIsConfirmed(t *testing.T) {
	e := startRevChaosEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	redisB, linkB := proxiedRedis(t, e.rd.Addr)
	linkB.Cut()
	a := startRevChaosReplica(t, ctx, e.rd.Client, e.pg.Pool, e.signer)
	b := startRevChaosReplica(t, ctx, redisB, e.pg.Pool, e.signer)

	bearer := e.mint(t, "jti-revoked-before-first-subscription")
	b.awaitStatus(t, "replica B before the revocation", bearer, http.StatusOK, 10*time.Second)
	e.revoke(t, a, "jti-revoked-before-first-subscription")
	time.Sleep(time.Second)
	if code, _ := b.call(t, bearer); code != http.StatusOK {
		t.Fatalf("replica B answered %d while its Redis was unreachable, want 200: the event reached it without a subscription", code)
	}

	linkB.Restore(t)
	took := b.awaitStatus(t, "replica B once its Redis came up", bearer, http.StatusUnauthorized, 15*time.Second)
	t.Logf("replica B refused the token %s after its Redis came up", took.Round(time.Millisecond))
	if code, errCode := b.call(t, bearer); code != http.StatusUnauthorized || errCode != "TOKEN_REVOKED" {
		t.Errorf("replica B answered %d %s, want 401 TOKEN_REVOKED", code, errCode)
	}
}

// spec: §13.3 (Credential Flow) "Authoritative durability for revocation"
// diagnosis: while replica B's Redis link flapped every 300 ms, B read
// issued_tokens more often than once per reconnect spacing, or did not
// refuse a token revoked during the flapping within the spacing and a
// margin after the link settled. The rehydration loop did not hold a
// subscription confirmation that came inside the spacing, or dropped the
// held request instead of running it once the spacing had passed.
func TestAFlappingRedisLinkCostsAtMostOneReadPerReconnectSpacing(t *testing.T) {
	e := startRevChaosEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	redisB, linkB := proxiedRedis(t, e.rd.Addr)
	a := startRevChaosReplica(t, ctx, e.rd.Client, e.pg.Pool, e.signer)
	b := startRevChaosReplica(t, ctx, redisB, e.pg.Pool, e.signer)

	probe := e.mint(t, "jti-flap-probe")
	e.revoke(t, a, "jti-flap-probe")
	b.awaitStatus(t, "replica B with the probe over a live subscription", probe, http.StatusUnauthorized, 10*time.Second)

	bearer := e.mint(t, "jti-revoked-while-flapping")
	before := b.reads.Load()
	start := time.Now()
	flapFor := 3 * revocation.DefaultReconnectSpacing
	revoked := false
	for time.Since(start) < flapFor {
		linkB.Cut()
		if !revoked && time.Since(start) > flapFor/2 {
			e.revoke(t, a, "jti-revoked-while-flapping")
			revoked = true
		}
		time.Sleep(150 * time.Millisecond)
		linkB.Restore(t)
		time.Sleep(150 * time.Millisecond)
	}
	elapsed := time.Since(start)
	reads := b.reads.Load() - before
	// One requested read may start at the beginning of the window, then at
	// most one per spacing, and one held request may run just after it.
	limit := int32(elapsed/revocation.DefaultReconnectSpacing) + 2
	t.Logf("replica B read issued_tokens %d times in %s of flapping", reads, elapsed.Round(time.Millisecond))
	if reads > limit {
		t.Errorf("replica B read issued_tokens %d times in %s of flapping, want at most %d", reads, elapsed.Round(time.Millisecond), limit)
	}
	took := b.awaitStatus(t, "replica B after the flapping", bearer, http.StatusUnauthorized, revocation.DefaultReconnectSpacing+10*time.Second)
	t.Logf("replica B refused the token %s after the flapping ended", took.Round(time.Millisecond))
}
