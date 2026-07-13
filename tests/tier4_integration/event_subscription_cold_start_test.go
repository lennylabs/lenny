//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration tests for the §25.5 "Subscription Cache and
// Invalidation" cold-start contract: when lenny-ops comes up while
// Postgres is unreachable the subscription cache is empty, so no webhook
// delivery occurs and lenny-ops emits ops_health_status_changed carrying
// subscriptionsUnavailable: true; once Postgres becomes reachable the
// cache loads and delivery begins.
//
// The unit suite (pkg/ops/opsservice/subscriptioncache_extra_test.go)
// pins the availability transitions and the empty-cache-on-list-error
// behavior against a stub store, and cmd/lenny-ops has no test for the
// availability→emission mapping at all. Neither exercises the behavior
// under a genuine Postgres outage: the emitted ops_health_status_changed
// flag observed on the live event stream, and the empty-cache/no-delivery
// then delivery-resumes sequence composed against the real
// Postgres-backed store. These tests add that composed proof.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	eventsubpgstore "github.com/lennylabs/lenny/pkg/ops/eventsubscription/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// TestOpsColdStartWithPostgresDownEmitsSubscriptionsUnavailable boots the
// real cmd/lenny-ops binary with its --postgres-dsn pointed at an
// unreachable address and Redis reachable. The §25.5 subscription cache
// cannot load, so lenny-ops emits ops_health_status_changed carrying the
// subscriptionsUnavailable: true flag. The assertion reads it off the
// live platform event stream (the same Redis ops:events:stream the binary
// emits to), which is the only place the composition-root mapping from the
// cache's cold-start availability signal to the emitted CloudEvent is
// observable.
//
// spec: §25.5 (Subscription Cache and Invalidation) — "Cold-start: if
// lenny-ops starts while Postgres is down, the cache is empty — no webhook
// delivery occurs. A warning is logged loudly and ops_health_status_changed
// emits with a subscriptionsUnavailable: true flag."
//
// diagnosis: a failure means lenny-ops did not announce that webhook
// subscriptions are unavailable when it started without Postgres. Either
// the subscription cache's failed cold-start load did not drive the
// availability signal, or the composition root did not translate that
// signal into an ops_health_status_changed CloudEvent with
// subscriptionsUnavailable: true on the event stream — an operator would
// have no notification that no webhooks will fire until Postgres recovers.
func TestOpsColdStartWithPostgresDownEmitsSubscriptionsUnavailable(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)

	rd := containers.StartRedis(t, containers.RedisOptions{})

	// A well-formed DSN pointed at a closed loopback port: pgxpool.New
	// connects lazily, so the binary boots, but every List against the
	// cache's store fails with connection-refused — the genuine "Postgres
	// unreachable at cold start" condition the spec names.
	deadDSN := fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/lenny_test?sslmode=disable", closedLoopbackPort(t))

	opsprocess.StartWith(t,
		"--postgres-dsn="+deadDSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--redis-allow-insecure",
	)

	ctx := context.Background()
	healthType := events.EventOpsHealthStatusChanged.CloudEventsType()

	// Poll the platform stream from its head for the cold-start emission.
	// The event is appended during binary wiring (the synchronous initial
	// cache refresh), so it is on the stream by the time StartWith returns;
	// the window absorbs scheduling slack.
	deadline := time.Now().Add(30 * time.Second)
	var found bool
	for time.Now().Before(deadline) && !found {
		entries, err := rd.Client.XRange(ctx, eventbuffer.DefaultStreamKey, "-", "+").Result()
		if err != nil {
			t.Fatalf("XRange %s: %v", eventbuffer.DefaultStreamKey, err)
		}
		for _, e := range entries {
			raw, ok := e.Values["event"].(string)
			if !ok {
				continue
			}
			var ce struct {
				Type string `json:"type"`
				Data struct {
					SubscriptionsUnavailable bool `json:"subscriptionsUnavailable"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(raw), &ce); err != nil {
				continue
			}
			if ce.Type == healthType && ce.Data.SubscriptionsUnavailable {
				found = true
				break
			}
		}
		if !found {
			time.Sleep(300 * time.Millisecond)
		}
	}
	if !found {
		t.Fatalf("no %s with subscriptionsUnavailable:true was emitted to %s after cold start with Postgres unreachable", healthType, eventbuffer.DefaultStreamKey)
	}
}

// TestSubscriptionCacheColdStartNoDeliveryThenResumesOnPostgresRecovery
// composes the §25.5 cold-start delivery contract against the real
// Postgres-backed subscription store: with Postgres unreachable the cache
// loads empty and the webhook worker delivers nothing; once Postgres
// becomes reachable and the cache refreshes, the same worker delivers the
// event to the subscription's callback. A subscription persisted before
// the outage is the target so the recovery step has something to deliver.
//
// The outage is a genuine loss of Postgres reachability, injected with an
// in-process TCP gate in front of the real Postgres container: closed, the
// store's List fails with a broken connection; opened, it succeeds against
// the unchanged database, so the row seeded before the outage is intact.
// The subscription cache, the webhook worker, the HTTP transport, and the
// Postgres store are all the real §25.5 product code; only the event
// source (a controllable queue) and the callback receiver are test stubs,
// and the worker is driven directly (its per-delivery SSRF guard lives in
// the cmd/lenny-ops HTTP client, exercised elsewhere) so a loopback
// receiver is reachable.
//
// spec: §25.5 (Subscription Cache and Invalidation) — "Cold-start: if
// lenny-ops starts while Postgres is down, the cache is empty — no webhook
// delivery occurs. ... When Postgres recovers, the cache is populated and
// delivery begins."
//
// diagnosis: a failure means the §25.5 cold-start delivery behavior is
// broken against a real Postgres outage. Either the cache did not load
// empty while Postgres was unreachable (a stale or fabricated subscription
// leaked a delivery that should not have happened), or after Postgres
// recovered the cache never populated from the store and the webhook
// worker kept delivering nothing — an operator's subscriptions would stay
// dark after the database came back.
func TestSubscriptionCacheColdStartNoDeliveryThenResumesOnPostgresRecovery(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	ctx := context.Background()

	// ---- callback receiver ----
	received := make(chan receivedWebhook, 8)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		select {
		case received <- receivedWebhook{
			signature: r.Header.Get(webhookdelivery.HeaderSignature),
			eventType: r.Header.Get(webhookdelivery.HeaderEventType),
			body:      raw,
		}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	eventType := events.EventAlertFired.CloudEventsType()

	// ---- seed a subscription while Postgres is reachable ----
	// Persisted directly through the store so it survives the outage and
	// is present for the cache to load on recovery. The callback points at
	// the in-process receiver.
	now := time.Now().UTC()
	seedStore := eventsubpgstore.New(pg.Pool)
	if err := seedStore.Create(ctx, eventsubscription.Record{
		ID:                "sub-cold-start",
		CallbackURL:       receiver.URL,
		Types:             []string{eventType},
		SecretHash:        "",
		SecretFingerprint: "",
		CreatedBy:         "alice@acme.com",
		TenantFilter:      eventsubscription.TenantFilterAll,
		Generation:        1,
		CreatedAt:         now,
		UpdatedAt:         now,
		Active:            true,
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	// ---- TCP gate in front of Postgres, starting closed ----
	backend, err := url.Parse(pg.DSN)
	if err != nil {
		t.Fatalf("parse postgres dsn: %v", err)
	}
	gate := newPGGate(t, backend.Host)
	gated := *backend
	gated.Host = gate.addr()
	gatedDSN := gated.String()

	gatePool, err := pgxpool.New(ctx, gatedDSN)
	if err != nil {
		t.Fatalf("gate pool: %v", err)
	}
	defer gatePool.Close()
	cacheStore := eventsubpgstore.New(gatePool)

	// ---- cold start: build the cache while the gate is closed ----
	var mu sync.Mutex
	var transitions []bool
	cache := opsservice.NewSubscriptionCache(ctx, opsservice.SubscriptionCacheConfig{
		Store:           cacheStore,
		RefreshInterval: time.Hour, // periodic refresh out of the way; drive explicitly
		OnAvailabilityChange: func(available bool) {
			mu.Lock()
			transitions = append(transitions, available)
			mu.Unlock()
		},
	})
	defer cache.Stop()

	// The failed cold-start load leaves the cache empty and unavailable —
	// the signal the binary maps to subscriptionsUnavailable:true.
	if cache.Available() {
		t.Fatal("cache reports Available() true after a cold start with Postgres unreachable")
	}
	if subs := cache.Subscriptions(); len(subs) != 0 {
		t.Fatalf("cache holds %d subscriptions after a failed cold-start load, want 0", len(subs))
	}

	// ---- no delivery while the cache is empty ----
	source := &coldStartQueueSource{}
	worker := opsservice.NewWebhookWorker(opsservice.WebhookWorkerConfig{
		Events:        source,
		Subscriptions: cache,
	})
	source.push(opsservice.WebhookEvent{
		ID:   "evt-during-outage",
		Type: eventType,
		Body: []byte(`{"id":"evt-during-outage","type":"` + eventType + `","data":{}}`),
	})
	for i := 0; i < 3; i++ {
		if err := worker.Tick(ctx); err != nil {
			t.Fatalf("worker tick during outage: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	select {
	case got := <-received:
		t.Fatalf("webhook delivered while the cache was empty (Postgres down): %+v", got)
	default:
	}

	// ---- recover Postgres and refresh the cache ----
	gate.setOpen(true)
	// Invalidate forces the immediate refresh the subscription_cache_invalidate
	// RPC and CRUD hooks trigger in production; here it stands in for the
	// first successful load after recovery.
	refreshDeadline := time.Now().Add(20 * time.Second)
	for {
		err := cache.Invalidate(ctx)
		if err == nil {
			break
		}
		if time.Now().After(refreshDeadline) {
			t.Fatalf("cache never refreshed after Postgres recovered: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !cache.Available() {
		t.Fatal("cache reports Available() false after Postgres recovered")
	}
	if subs := cache.Subscriptions(); len(subs) != 1 || subs[0].ID != "sub-cold-start" {
		t.Fatalf("cache did not load the seeded subscription after recovery: %+v", subs)
	}

	mu.Lock()
	gotTransitions := append([]bool(nil), transitions...)
	mu.Unlock()
	if len(gotTransitions) < 2 || gotTransitions[0] != false || gotTransitions[len(gotTransitions)-1] != true {
		t.Fatalf("availability transitions = %v, want a false cold start followed by a true recovery", gotTransitions)
	}

	// ---- delivery resumes ----
	source.push(opsservice.WebhookEvent{
		ID:   "evt-after-recovery",
		Type: eventType,
		Body: []byte(`{"id":"evt-after-recovery","type":"` + eventType + `","data":{}}`),
	})
	deadline := time.Now().Add(20 * time.Second)
	var delivered *receivedWebhook
	for time.Now().Before(deadline) && delivered == nil {
		if err := worker.Tick(ctx); err != nil {
			t.Fatalf("worker tick after recovery: %v", err)
		}
		select {
		case r := <-received:
			delivered = &r
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}
	if delivered == nil {
		t.Fatal("webhook was not delivered after Postgres recovered and the cache populated")
	}
	if delivered.eventType != eventType {
		t.Errorf("delivered X-Lenny-Event-Type = %q, want %q", delivered.eventType, eventType)
	}
}

// coldStartQueueSource is a controllable opsservice.EventSource. Each Poll
// drains and returns the events pushed since the previous Poll, so a test
// can assert an event yields no delivery while the cache is empty and a
// later event delivers once the cache has loaded.
type coldStartQueueSource struct {
	mu  sync.Mutex
	evs []opsservice.WebhookEvent
}

func (q *coldStartQueueSource) push(ev opsservice.WebhookEvent) {
	q.mu.Lock()
	q.evs = append(q.evs, ev)
	q.mu.Unlock()
}

// Poll implements opsservice.EventSource.
func (q *coldStartQueueSource) Poll(context.Context) ([]opsservice.WebhookEvent, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.evs
	q.evs = nil
	return out, nil
}

// pgGate is an in-process TCP proxy used to inject a genuine Postgres
// reachability outage: opened, it forwards bytes between a client and the
// real Postgres backend; closed, it refuses to proxy and drops any live
// connections so the pool's queries fail. The backend database is
// untouched, so a row written before the outage survives it.
type pgGate struct {
	ln      net.Listener
	backend string

	mu    sync.Mutex
	open  bool
	conns map[net.Conn]struct{}
}

// newPGGate starts a gate proxying to backend (host:port), initially
// closed. The accept loop and any live connections are torn down on
// cleanup.
func newPGGate(t testing.TB, backend string) *pgGate {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pgGate listen: %v", err)
	}
	g := &pgGate{ln: ln, backend: backend, conns: map[net.Conn]struct{}{}}
	go g.serve()
	t.Cleanup(func() {
		_ = ln.Close()
		g.closeAll()
	})
	return g
}

func (g *pgGate) addr() string { return g.ln.Addr().String() }

func (g *pgGate) serve() {
	for {
		c, err := g.ln.Accept()
		if err != nil {
			return
		}
		go g.handle(c)
	}
}

func (g *pgGate) handle(client net.Conn) {
	g.mu.Lock()
	open := g.open
	g.mu.Unlock()
	if !open {
		_ = client.Close()
		return
	}
	backend, err := net.Dial("tcp", g.backend)
	if err != nil {
		_ = client.Close()
		return
	}
	g.track(client)
	g.track(backend)
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(backend, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, backend); done <- struct{}{} }()
	<-done
	_ = client.Close()
	_ = backend.Close()
	g.untrack(client)
	g.untrack(backend)
}

func (g *pgGate) track(c net.Conn) {
	g.mu.Lock()
	g.conns[c] = struct{}{}
	g.mu.Unlock()
}

func (g *pgGate) untrack(c net.Conn) {
	g.mu.Lock()
	delete(g.conns, c)
	g.mu.Unlock()
}

// setOpen flips the gate. Closing it drops every live connection so the
// pool's in-flight and idle connections break immediately rather than
// hanging until a timeout.
func (g *pgGate) setOpen(v bool) {
	g.mu.Lock()
	g.open = v
	var toClose []net.Conn
	if !v {
		for c := range g.conns {
			toClose = append(toClose, c)
		}
		g.conns = map[net.Conn]struct{}{}
	}
	g.mu.Unlock()
	for _, c := range toClose {
		_ = c.Close()
	}
}

func (g *pgGate) closeAll() {
	g.mu.Lock()
	var toClose []net.Conn
	for c := range g.conns {
		toClose = append(toClose, c)
	}
	g.conns = map[net.Conn]struct{}{}
	g.mu.Unlock()
	for _, c := range toClose {
		_ = c.Close()
	}
}

// closedLoopbackPort returns a loopback TCP port with no listener, so a
// dial to it is refused. The kernel-assigned port is released before
// return; the brief reuse race is acceptable for an integration test.
func closedLoopbackPort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("closedLoopbackPort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}
