//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component coverage for the §25.9 Redis-backed cross-tenant
// scatter-gather result cache. The spec binds the platform-admin
// cross-tenant scatter-gather result to a shared Redis entry (5-minute
// TTL, keyed by a hash of the query parameters), so a second gateway
// replica serves the same cached page without re-running the scatter
// fan-out. This test drives two independent admin.Router instances, each
// with its own RedisScatterGatherCache over the same Redis container, and
// asserts a query executed on the first is served from the shared cache
// by the second. The in-process MemScatterGatherCache cannot satisfy this
// contract because its entries are per-replica.
package auditstore_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// countingScatterReader is a §25.9 cross-tenant scatter-gather reader that
// returns canned rows and counts invocations so a test can prove a query
// was served from the shared cache rather than by re-reading the shards.
type countingScatterReader struct {
	rows  []audit.Row
	calls int
}

func (r *countingScatterReader) ScatterGatherRows(_ context.Context) ([]audit.Row, []string, error) {
	r.calls++
	return r.rows, nil, nil
}

// scatterRedisRows builds a valid pair of per-tenant §11.7 chains ordered
// by (tenant_id, sequence_number), the merged result a scatter-gather
// read returns.
func scatterRedisRows() []audit.Row {
	cs := audit.NewChainSet()
	// Anchor the rows inside the §25.9 default 24-hour look-back window,
	// which is measured from adminQueryClock, so they are not filtered out.
	ts := adminQueryClock.Add(-time.Hour)
	return []audit.Row{
		cs.Append("acme", "session.created", json.RawMessage(`{"actor_id":"alice"}`), ts),
		cs.Append("acme", "session.completed", json.RawMessage(`{"actor_id":"alice"}`), ts),
		cs.Append("globex", "session.created", json.RawMessage(`{"actor_id":"bob"}`), ts),
	}
}

// redisCacheRouter builds a production admin.Router wired to an in-memory
// audit chain (so the /v1/admin/audit-events route registers), the given
// scatter reader, and a Redis-backed scatter-gather cache over client.
// Each router models one gateway replica.
func redisCacheRouter(reader *countingScatterReader, client redis.UniversalClient) *admin.Router {
	cache := admin.NewRedisScatterGatherCache(client, nil)
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return adminQueryClock },
	}).
		WithAuditChains(audit.NewChainSet()).
		WithAuditScatter(reader).
		WithScatterGatherCache(cache, true)
}

func getCrossTenantRedis(t *testing.T, router *admin.Router, query string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, asPlatformAdmin(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events"+query, nil)))
	return rr
}

// spec: §25.9 (Query Limits and Scatter-Gather) — "platform-admin
// cross-tenant queries that use AllAuditShards() cache their results in
// Redis for 5 minutes keyed by a hash of the query parameters. Repeated
// queries within the window ... return cached results."
//
// diagnosis: a failure means the cross-tenant scatter-gather result cache
// is not coherent across gateway replicas — a second replica re-runs the
// scatter fan-out instead of serving the shared Redis entry the first
// replica wrote, contradicting the §25.9 Redis-backed cache. An
// in-process (per-replica) cache reproduces this failure.
func TestScatterGatherRedisCacheSharedAcrossReplicas_spec_25_9(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})

	// Two independent Redis clients to the same server model two gateway
	// replicas, each holding its own cache object over the shared store.
	clientB := redis.NewClient(&redis.Options{Addr: rd.Addr})
	t.Cleanup(func() { _ = clientB.Close() })

	readerA := &countingScatterReader{rows: scatterRedisRows()}
	// Replica B's reader returns no rows: if replica B were to miss the
	// shared cache and re-read the shards, its response would differ from
	// replica A's, so a byte-identical body proves the shared cache served
	// it.
	readerB := &countingScatterReader{rows: nil}

	routerA := redisCacheRouter(readerA, rd.Client)
	routerB := redisCacheRouter(readerB, clientB)

	// Replica A runs the query cold: it reads the shards once and writes
	// the result to the shared Redis cache.
	first := getCrossTenantRedis(t, routerA, "")
	if first.Code != http.StatusOK {
		t.Fatalf("replica A status = %d, body=%s", first.Code, first.Body.String())
	}
	if readerA.calls != 1 {
		t.Fatalf("replica A scatter reader calls = %d, want 1", readerA.calls)
	}
	var envA admin.AuditEventEnvelope
	if err := json.Unmarshal(first.Body.Bytes(), &envA); err != nil {
		t.Fatalf("decode replica A body: %v", err)
	}
	if len(envA.Items) != 3 {
		t.Fatalf("replica A items = %d, want 3", len(envA.Items))
	}

	// Replica B runs the identical query: it must serve the entry replica
	// A wrote to shared Redis, never consulting its own (empty) reader.
	second := getCrossTenantRedis(t, routerB, "")
	if second.Code != http.StatusOK {
		t.Fatalf("replica B status = %d, body=%s", second.Code, second.Body.String())
	}
	if readerB.calls != 0 {
		t.Fatalf("replica B scatter reader calls = %d, want 0 (served from shared Redis cache)", readerB.calls)
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("replica B body differs from replica A body; shared cache did not serve it\nA=%s\nB=%s",
			first.Body.String(), second.Body.String())
	}
}

// spec: §25.9 (Query Limits and Scatter-Gather) — "Set `?fresh=true` to
// bypass the cache."
//
// diagnosis: a failure means ?fresh=true did not bypass the shared Redis
// cache: replica B returned replica A's cached page instead of re-reading
// the shards, so an operator cannot force a fresh cross-tenant read.
func TestScatterGatherRedisCacheFreshBypass_spec_25_9(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})

	clientB := redis.NewClient(&redis.Options{Addr: rd.Addr})
	t.Cleanup(func() { _ = clientB.Close() })

	readerA := &countingScatterReader{rows: scatterRedisRows()}
	readerB := &countingScatterReader{rows: scatterRedisRows()}
	routerA := redisCacheRouter(readerA, rd.Client)
	routerB := redisCacheRouter(readerB, clientB)

	if rr := getCrossTenantRedis(t, routerA, ""); rr.Code != http.StatusOK {
		t.Fatalf("warm status = %d", rr.Code)
	}

	rr := getCrossTenantRedis(t, routerB, "?fresh=true")
	if rr.Code != http.StatusOK {
		t.Fatalf("fresh status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if readerB.calls != 1 {
		t.Fatalf("replica B scatter reader calls = %d, want 1 (?fresh=true bypasses shared cache)", readerB.calls)
	}
}
