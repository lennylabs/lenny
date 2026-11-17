//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §10.1.1 composite coordination holder value on the
// Postgres stores. The Postgres lease fallback (session_leases) and the
// barrier-target mirror (coordination_lease) store the holder value
// `<replica identity>.<process token>@<address>` byte for byte, so a Redis
// outage keeps the forward address readable and the §10.1.8 barrier query
// matches the value the Sweeper writes.
package leases_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	coordleasepg "github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	leasepg "github.com/lennylabs/lenny/pkg/gateway/storage/leasestore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// compositeHolders are holder values whose address is IPv4, bracketed IPv6,
// and a DNS name, one with an identity that itself contains `@`.
func compositeHolders(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, c := range []struct{ id, addr string }{
		{"gateway-7f9c", "10.0.0.7:50054"},
		{"gateway-7f9d", "fd00::7:50054"},
		{"ops@acme", "lenny-gateway-0.lenny-system:50054"},
	} {
		h, err := sessionserver.HolderValue(c.id, c.addr)
		if err != nil {
			t.Fatalf("HolderValue(%q, %q): %v", c.id, c.addr, err)
		}
		out = append(out, h)
	}
	return out
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address, Fallback
// diagnosis: the Postgres lease fallback or the coordination_lease mirror
// altered a composite holder value on the way through Postgres, or compares
// it so that the holder's own Renew or Release no longer matches. Under a
// Redis outage a peer replica then reads no forward address, and the holder
// can neither renew nor release its lease. Read the Acquire, Get, Renew, and
// Release statements in pkg/gateway/storage/leasestore/pgstore and the
// Upsert and ListHeldByReplica statements in
// pkg/gateway/coordination/coordlease/pgstore.
func TestCompositeHolderValueRoundTripsThroughPostgres_spec_10_1_1(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00') ON CONFLICT (id) DO NOTHING`, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	leases := leasepg.New(pg.Pool)
	mirror := coordleasepg.New(pg.Pool, nil)

	for _, holder := range compositeHolders(t) {
		t.Run(holder, func(t *testing.T) {
			sid := sessionID(t)
			if _, err := leases.Acquire(ctx, tenant, sid, holder, time.Minute); err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			got, err := leases.Get(ctx, tenant, sid)
			if err != nil || got.Holder != holder {
				t.Fatalf("Get = %+v, %v; want the holder %q byte for byte", got, err, holder)
			}
			want, _ := sessionserver.HolderAddress(holder)
			if addr, ok := sessionserver.HolderAddress(got.Holder); !ok || addr != want {
				t.Errorf("HolderAddress of the stored value = %q, %v; want %q", addr, ok, want)
			}
			if _, err := leases.Renew(ctx, tenant, sid, holder, time.Minute); err != nil {
				t.Errorf("Renew by the composite holder: %v", err)
			}
			if err := leases.Release(ctx, tenant, sid, holder); err != nil {
				t.Fatalf("Release by the composite holder: %v", err)
			}
			if _, err := leases.Get(ctx, tenant, sid); !errors.Is(err, leasestore.ErrNotFound) {
				t.Errorf("Get after Release = %v, want ErrNotFound: the release by the composite value did not match", err)
			}

			if err := mirror.Upsert(ctx, coordlease.Lease{TenantID: tenant, SessionID: sid, CoordinatorReplica: holder, CoordinationGeneration: 2}); err != nil {
				t.Fatalf("mirror Upsert: %v", err)
			}
			held, err := mirror.ListHeldByReplica(ctx, holder)
			if err != nil {
				t.Fatalf("ListHeldByReplica: %v", err)
			}
			found := false
			for _, l := range held {
				if l.SessionID == sid && l.CoordinatorReplica == holder {
					found = true
				}
			}
			if !found {
				t.Errorf("barrier-target query for %q returned %+v, want the mirrored session", holder, held)
			}
		})
	}
}
