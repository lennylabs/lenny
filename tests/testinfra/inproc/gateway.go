// SPDX-License-Identifier: MIT

package inproc

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	rlredis "github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/idempotency"
)

// gateway is the in-process Lenny gateway the tier-7a multi-component
// scenarios drive. It is assembled from the same packages
// cmd/lenny-gateway assembles its HTTP surface from, so a scenario
// exercises Lenny code rather than a harness-local reimplementation:
//
//   - pkg/gateway/sessionserver serves the §15.1 REST session
//     lifecycle, including the precondition table and the §15.1 error
//     envelope.
//   - pkg/gateway/middleware/idempotency enforces the §11.5
//     Idempotency-Key contract (claim, replay, in-flight rejection)
//     around it, mounted on the same §11.5 critical-operation path set
//     cmd/lenny-gateway mounts it on.
//   - pkg/gateway/policy/ratelimit/redisstore backs the §11.1
//     per-runtime admission counter against the harness's embedded
//     miniredis, so session creation genuinely transacts with Redis.
//   - pkg/gateway/session/sessionstore/pgstore is the §4.2 SessionStore,
//     running against the harness's embedded PostgreSQL. Every session
//     create, read, and state transition is a real SQL transaction
//     under the §12.3 tenant guard, which is the storage layer
//     TESTING.md §12.7.a names for this harness.
type gateway struct {
	store    sessionstore.Store
	srv      *sessionserver.Server
	handler  http.Handler
	idem     *countingIdempotencyStore
	creates  atomic.Int64
	redis    redis.UniversalClient
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
}

// defaultAdmissionPerRuntimePerMinute is the §11.1 per-runtime
// requests-per-minute admission limit the harness configures when a
// scenario does not pick one. The load profiles drive far more than a
// production limit would allow, so the default is set high enough that
// the counter runs (and writes to the embedded Redis) on every create
// without rejecting scenario traffic. A scenario that wants to exercise
// the §11.1 rejection path sets Config.AdmissionPerRuntimePerMinute.
const defaultAdmissionPerRuntimePerMinute = 1_000_000

// newGateway assembles the in-process gateway against the supplied
// Redis client (the harness's embedded miniredis) and the supplied
// session-store pool (a private database on the harness's embedded
// PostgreSQL).
func newGateway(rdb redis.UniversalClient, pool *pgxpool.Pool, cfg Config) *gateway {
	perRuntime := cfg.AdmissionPerRuntimePerMinute
	if perRuntime <= 0 {
		perRuntime = defaultAdmissionPerRuntimePerMinute
	}
	// spec: TESTING.md §12.7.a — the harness boots the embedded
	// Postgres adapter, so a scenario's concurrency, ordering, and
	// atomicity results describe the storage layer the shipped gateway
	// uses.
	var store sessionstore.Store = newTenantAnchoringStore(pgstore.New(pool), pool)
	srv := sessionserver.New(store, sessionserver.Options{
		// spec: §11.1 line 7 — the per-runtime admission counter is the
		// Redis-backed one, so the harness's miniredis is on the session
		// creation path rather than decorative.
		AdmissionRateLimitCounter: rlredis.New(rdb),
		PerRuntimePerMinute:       perRuntime,
	})
	g := &gateway{store: store, srv: srv, redis: rdb}
	g.idem = &countingIdempotencyStore{inner: idemmw.NewMemoryStore()}

	// Count session creations at the sessionserver boundary, inside the
	// §11.5 middleware: a replayed response never reaches the inner
	// handler, so this counts genuine creates rather than cache hits.
	counted := &createCounter{inner: srv.Handler(), n: &g.creates}

	g.handler = idemmw.Wrap(counted, g.idem, idemmw.Options{
		// The harness stands in for the auth middleware that normally
		// resolves the tenant ahead of §11.5. It mirrors
		// sessionserver.resolveTenant: the dev X-Lenny-Tenant-ID header,
		// falling back to the §10.2 single-tenant "default" so both
		// tenant-scoped and unscoped scenarios reach the handler.
		TenantFromRequest: func(r *http.Request) string {
			if v := r.Header.Get("X-Lenny-Tenant-ID"); v != "" {
				return v
			}
			return "default"
		},
		// spec: §11.5 line 268 — the critical-operation path set, the
		// same list cmd/lenny-gateway mounts.
		AllowedPaths: []string{
			"/v1/sessions",
			"/v1/sessions/start",
			"/v1/sessions/{id}/finalize",
			"/v1/sessions/{id}/start",
			"/v1/sessions/{id}/resume",
			"/v1/sessions/{id}/derive",
		},
	})
	return g
}

// start binds the gateway to a loopback port and returns the resolved
// URL. Idempotent across repeated calls.
func (g *gateway) start() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.server != nil {
		return "http://" + g.listener.Addr().String(), nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("/", g.handler)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	g.listener = ln
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	g.server = srv
	// Capture srv locally: stop() clears g.server, and a closure reading
	// the field would race with (and then nil-deref on) that clear.
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String(), nil
}

func (g *gateway) stop(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.server == nil {
		return nil
	}
	srv := g.server
	g.server = nil
	return srv.Shutdown(ctx)
}

// sessionCount returns how many sessions the gateway created over its
// lifetime. Terminal sessions stay counted: the §15.1 lifecycle moves a
// deleted session to `cancelled` rather than removing the row.
func (g *gateway) sessionCount() int { return int(g.creates.Load()) }

// idempotencyHits returns how many requests the §11.5 middleware served
// from a completed cache entry instead of re-executing.
func (g *gateway) idempotencyHits() int64 { return g.idem.replays.Load() }

// createCounter increments n for every 201 the inner handler writes on
// POST /v1/sessions. It sits inside the §11.5 middleware so replays,
// which short-circuit before the inner handler, are not counted.
type createCounter struct {
	inner http.Handler
	n     *atomic.Int64
}

func (c *createCounter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/sessions" {
		c.inner.ServeHTTP(w, r)
		return
	}
	rec := &statusRecorder{ResponseWriter: w}
	c.inner.ServeHTTP(rec, r)
	if rec.status == http.StatusCreated {
		c.n.Add(1)
	}
}

// statusRecorder remembers the status code written through it and
// forwards everything else to the wrapped ResponseWriter, including
// Flush so the §11.5 middleware's streamed-response detection still
// sees the flush it uses to decide replayability.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(p)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// countingIdempotencyStore wraps a real §11.5 Store and counts the
// claims that observed a completed record for the same body — the
// condition under which the middleware replays the cached response
// instead of re-executing. Scenarios read the count through
// Env.IdempotencyHits.
type countingIdempotencyStore struct {
	inner   idemmw.Store
	replays atomic.Int64
}

func (c *countingIdempotencyStore) Get(ctx context.Context, tenantID, key string) (idempotency.Record, bool, error) {
	return c.inner.Get(ctx, tenantID, key)
}

func (c *countingIdempotencyStore) Put(ctx context.Context, rec idempotency.Record) error {
	return c.inner.Put(ctx, rec)
}

func (c *countingIdempotencyStore) Claim(ctx context.Context, tenantID, key, bodyHash string, now time.Time) (idempotency.Record, bool, error) {
	rec, claimed, err := c.inner.Claim(ctx, tenantID, key, bodyHash, now)
	if err == nil && !claimed && rec.Response.StatusCode != 0 && rec.BodyHash == bodyHash {
		c.replays.Add(1)
	}
	return rec, claimed, err
}

func (c *countingIdempotencyStore) Release(ctx context.Context, tenantID, key string) error {
	return c.inner.Release(ctx, tenantID, key)
}
