//go:build chaos

// SPDX-License-Identifier: MIT

// Tier-8 chaos coverage for the §8.3 delegate_task reservation under a real
// Postgres outage. The reservation reads the root's sessions from the
// SessionStore when the tree-wide sentinel is absent, and reads the tree's
// cumulative token grant from the delegation_tree_budget row when the parent
// carries a finite token cap. A fault proxy in front of a real Postgres cuts
// every connection at the moment each read runs. Either way the delegation
// fails closed with DELEGATION_BUDGET_UNAVAILABLE, no Redis budget counter
// changes, no child row is created, and the same delegation succeeds once
// Postgres returns.
package tier8_chaos_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol/denialpg"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
)

const (
	dlgOutageTenant = "acme"
	dlgOutageRoot   = "00000000-0000-4000-8000-0000000083c1"
	dlgOutageChild  = "00000000-0000-4000-8000-0000000083c2"
)

// cutOnExpiry cuts the proxy after the first Reserve meets an absent
// sentinel, so the SessionStore read that follows runs against an
// unreachable Postgres.
type cutOnExpiry struct {
	inner *treebudget.Reserver
	proxy *tcpfault.Proxy
	armed bool
}

func (c *cutOnExpiry) Reserve(ctx context.Context, r treebudget.Reservation) (treebudget.Totals, error) {
	tot, err := c.inner.Reserve(ctx, r)
	if c.armed && errors.Is(err, treebudget.ErrBudgetKeysExpired) {
		c.armed = false
		c.proxy.Cut()
	}
	return tot, err
}

func (c *cutOnExpiry) Return(ctx context.Context, r treebudget.Reservation) error {
	return c.inner.Return(ctx, r)
}

// cutOnGrantRead cuts the proxy and then reads the tree's token grant from
// the delegation_tree_budget row over the proxied pool.
type cutOnGrantRead struct {
	store *denialpg.Store
	proxy *tcpfault.Proxy
	armed bool
}

func (c *cutOnGrantRead) TreeTokenGrant(ctx context.Context, tenantID, root string) (int64, error) {
	if c.armed {
		c.armed = false
		c.proxy.Cut()
	}
	g, err := c.store.Grants(ctx, tenantID, root)
	return g.Tokens, err
}

// dlgOutageEnv is a real Postgres seeded with a running root, a fault proxy
// in front of it, and a real Redis.
type dlgOutageEnv struct {
	pg    *containers.Postgres
	rd    *containers.Redis
	proxy *tcpfault.Proxy
	pool  *pgxpool.Pool
}

func newDlgOutageEnv(t *testing.T, lease *sessionstore.DelegationLease) *dlgOutageEnv {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	ctx := context.Background()
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, dlgOutageTenant, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pgstore.New(pg.Pool).Create(ctx, sessionstore.Session{
		ID: dlgOutageRoot, TenantID: dlgOutageTenant, UserID: "alice@acme.com", RuntimeRef: "echo",
		PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		State: session.StateRunning, DelegationLease: lease,
	}); err != nil {
		t.Fatalf("seed root row: %v", err)
	}
	proxy, dsn := tcpfault.StartURL(t, pg.DSN)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse proxied dsn: %v", err)
	}
	cfg.ConnConfig.ConnectTimeout = 2 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("proxied pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return &dlgOutageEnv{pg: pg, rd: containers.StartRedis(t, containers.RedisOptions{}), proxy: proxy, pool: pool}
}

func (e *dlgOutageEnv) delegate(svc *delegation.Service) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := svc.Delegate(ctx, dlgOutageTenant, delegation.Request{
		ParentSessionID: dlgOutageRoot, RuntimeRef: "worker", PoolRef: "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	return err
}

func (e *dlgOutageEnv) redisSnapshot(t *testing.T) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := e.rd.Client.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatalf("redis keys: %v", err)
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		v, err := e.rd.Client.Get(ctx, k).Result()
		if err != nil {
			t.Fatalf("redis get %s: %v", k, err)
		}
		out[k] = v
	}
	return out
}

func (e *dlgOutageEnv) assertNoChild(t *testing.T) {
	t.Helper()
	if _, err := pgstore.New(e.pg.Pool).Get(context.Background(), dlgOutageTenant, dlgOutageChild); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatalf("child row read = %v, want ErrNotFound", err)
	}
}

// spec: 8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), 8.6 (Extension
// state), 12.4
//
// diagnosis: a Postgres outage during a delegate_task reservation did not
// fail closed. The reservation's SessionStore read on the absent-sentinel
// path, or its delegation_tree_budget grant read, failed and the delegation
// was admitted, returned an error other than DELEGATION_BUDGET_UNAVAILABLE,
// changed a Redis budget counter, or created a child row; or the delegation
// did not succeed once Postgres returned.
func TestDelegateTaskPostgresOutageFailsClosed_spec_8_3(t *testing.T) {
	cases := []struct {
		name  string
		lease *sessionstore.DelegationLease
		seed  map[string]string
		build func(e *dlgOutageEnv) delegation.Options
	}{
		{
			name:  "session store read on the expiry path",
			lease: &sessionstore.DelegationLease{MaxTreeSize: 10},
			build: func(e *dlgOutageEnv) delegation.Options {
				return delegation.Options{
					TreeBudgetReserver: &cutOnExpiry{inner: treebudget.New(e.rd.Client, time.Hour), proxy: e.proxy, armed: true},
				}
			},
		},
		{
			name:  "delegation_tree_budget grant read",
			lease: &sessionstore.DelegationLease{MaxTokenBudget: 100_000},
			seed: map[string]string{
				"{" + dlgOutageRoot + "}:dlg:tokens":    "2500",
				"{" + dlgOutageRoot + "}:dlg:tree_size": "1",
			},
			build: func(e *dlgOutageEnv) delegation.Options {
				return delegation.Options{
					TreeBudgetReserver: treebudget.New(e.rd.Client, time.Hour),
					TreeGrants:         &cutOnGrantRead{store: denialpg.New(e.pool), proxy: e.proxy, armed: true},
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newDlgOutageEnv(t, c.lease)
			for k, v := range c.seed {
				if err := e.rd.Client.Set(context.Background(), k, v, time.Hour).Err(); err != nil {
					t.Fatalf("seed %s: %v", k, err)
				}
			}
			opts := c.build(e)
			opts.IDFunc = func() string { return dlgOutageChild }
			svc := delegation.NewService(pgstore.New(e.pool), opts)
			wantRedis := e.redisSnapshot(t)

			if err := e.delegate(svc); !errors.Is(err, delegation.ErrBudgetUnavailable) {
				t.Fatalf("delegate during the outage err = %v, want ErrBudgetUnavailable", err)
			}
			if got := e.redisSnapshot(t); !reflect.DeepEqual(got, wantRedis) {
				t.Fatalf("redis budget keys changed during the outage: %v, want %v", got, wantRedis)
			}
			e.assertNoChild(t)

			e.proxy.Restore(t)
			if err := e.delegate(svc); err != nil {
				t.Fatalf("delegate after Postgres returned: %v", err)
			}
			if _, err := pgstore.New(e.pg.Pool).Get(context.Background(), dlgOutageTenant, dlgOutageChild); err != nil {
				t.Fatalf("child row after recovery: %v", err)
			}
		})
	}
}
