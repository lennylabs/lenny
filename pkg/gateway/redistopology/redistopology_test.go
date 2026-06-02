// SPDX-License-Identifier: MIT

package redistopology_test

import (
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/redistopology"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

func baseClient(t *testing.T) redis.UniversalClient {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// insecureTemplate carries AllowInsecure so a per-concern redis:// URL
// is taken at face value (the §12.4 AUTH/TLS invariant is exercised
// separately).
var insecureTemplate = redisconn.Config{AllowInsecure: true}

// spec: §12.4 lines 237-245 — with no per-concern URLs every concern
// resolves to the base client (the single Tier 1/2 topology). F-12.4.16.
func TestBuildNoSplit_spec_12_4_245(t *testing.T) {
	base := baseClient(t)
	clients, err := redistopology.Build(base, map[storerouter.RedisConcern]string{}, insecureTemplate)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() { _ = clients.Close() })
	if clients.Split() {
		t.Error("Split() = true, want false with no per-concern URLs")
	}
	if clients.ByConcern() != nil {
		t.Error("ByConcern() non-nil with no split")
	}
	for _, c := range redistopology.Concerns {
		if clients.For(c) != base {
			t.Errorf("For(%s): got non-base client, want base", c)
		}
	}
}

// A per-concern URL routes that concern to a dedicated client while the
// rest stay on the base client. F-12.4.16.
func TestBuildSplitOneConcern_spec_12_4_237(t *testing.T) {
	base := baseClient(t)
	clients, err := redistopology.Build(base, map[storerouter.RedisConcern]string{
		storerouter.RedisConcernQuota: "redis://10.0.0.2:6379/0",
	}, insecureTemplate)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() { _ = clients.Close() })
	if !clients.Split() {
		t.Error("Split() = false, want true")
	}
	if clients.For(storerouter.RedisConcernQuota) == base {
		t.Error("For(quota) returned base, want a dedicated client")
	}
	if clients.For(storerouter.RedisConcernCoordination) != base {
		t.Error("For(coordination) not base; only quota was split")
	}
	bc := clients.ByConcern()
	if bc == nil || bc[storerouter.RedisConcernQuota] != clients.For(storerouter.RedisConcernQuota) {
		t.Error("ByConcern() omits the split quota client")
	}
	if _, ok := bc[storerouter.RedisConcernCoordination]; ok {
		t.Error("ByConcern() should only carry split concerns, not the base fallback")
	}
}

// Two concerns naming the same URL share one client (one pool, one
// Guard install).
func TestBuildSharedURLDedup_spec_12_4_237(t *testing.T) {
	base := baseClient(t)
	clients, err := redistopology.Build(base, map[storerouter.RedisConcern]string{
		storerouter.RedisConcernQuota:      "redis://10.0.0.5:6379/0",
		storerouter.RedisConcernDelegation: "redis://10.0.0.5:6379/0",
	}, insecureTemplate)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() { _ = clients.Close() })
	if clients.For(storerouter.RedisConcernQuota) != clients.For(storerouter.RedisConcernDelegation) {
		t.Error("concerns sharing a URL did not share a client")
	}
	if clients.For(storerouter.RedisConcernQuota) == base {
		t.Error("shared concern resolved to base, want the dedicated client")
	}
}

// A nil base (Postgres-only / in-memory deployment) yields an empty
// Clients: For returns nil, ByConcern nil, regardless of per-concern
// URLs.
func TestBuildNilBase(t *testing.T) {
	clients, err := redistopology.Build(nil, map[storerouter.RedisConcern]string{
		storerouter.RedisConcernQuota: "redis://10.0.0.2:6379/0",
	}, insecureTemplate)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if clients.Split() {
		t.Error("Split() = true with nil base")
	}
	if clients.For(storerouter.RedisConcernQuota) != nil {
		t.Error("For() non-nil with nil base")
	}
}

// A per-concern URL that violates the §12.4 AUTH/TLS invariant fails the
// build (the template here enforces, so a plaintext passwordless URL is
// rejected). F-12.4.16 / §12.4 line 197.
func TestBuildPerConcernHonorsAuthInvariant_spec_12_4_197(t *testing.T) {
	base := baseClient(t)
	_, err := redistopology.Build(base, map[storerouter.RedisConcern]string{
		storerouter.RedisConcernQuota: "redis://10.0.0.2:6379/0",
	}, redisconn.Config{}) // enforcement active (AllowInsecure false)
	if err == nil {
		t.Fatal("expected an error for a passwordless plaintext per-concern URL")
	}
}

// nil *Clients is safe: the methods no-op rather than panic.
func TestNilClientsSafe(t *testing.T) {
	var clients *redistopology.Clients
	if clients.For(storerouter.RedisConcernQuota) != nil {
		t.Error("nil.For() non-nil")
	}
	if clients.ByConcern() != nil {
		t.Error("nil.ByConcern() non-nil")
	}
	if clients.Split() {
		t.Error("nil.Split() true")
	}
	if err := clients.Close(); err != nil {
		t.Errorf("nil.Close(): %v", err)
	}
}
