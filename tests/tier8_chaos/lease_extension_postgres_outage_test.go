//go:build chaos

// SPDX-License-Identifier: MIT

// Tier-8 chaos coverage for the §8.6 store-backed extension state under a
// real Postgres outage. The extension request reads the session rows and
// the delegation_tree_budget row from Postgres on each request, and the
// grant commits in the transaction that re-checks the denial. A fault proxy
// in front of a real Postgres container cuts every connection while a
// request is between its TreeBudget read and its grant, and once before the
// request starts. Either way the extension fails closed: the outcome is
// terminal, no grant is applied, the delegation_tree_budget row reads back
// unchanged after Postgres returns, and no Redis budget key changes.
package tier8_chaos_test

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol/denialpg"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
)

const (
	extOutageTenant = "acme"
	extOutageRoot   = "00000000-0000-4000-8000-0000000086a1"
)

// extOutageEnv is a real Postgres with a fault proxy in front of it, the
// extension surface over the proxied pool, and a real Redis the tree's
// budget keys live in.
type extOutageEnv struct {
	pg      *containers.Postgres
	proxy   *tcpfault.Proxy
	direct  *denialpg.Store
	rd      *containers.Redis
	reclaim *outageReclaimer
}

// outageReclaimer counts the episode's RaiseBudget calls.
type outageReclaimer struct {
	mu     sync.Mutex
	raises int
}

func (r *outageReclaimer) RaiseBudget(string, int64) {
	r.mu.Lock()
	r.raises++
	r.mu.Unlock()
}

func (r *outageReclaimer) TerminateSession(string) {}

func (r *outageReclaimer) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.raises
}

// cutDuringConsent approves the elicitation after cutting the proxy, so the
// request's grant runs against an unreachable Postgres after its TreeBudget
// read succeeded.
type cutDuringConsent struct{ proxy *tcpfault.Proxy }

func (c cutDuringConsent) Elicit(context.Context, string, string) (bool, error) {
	c.proxy.Cut()
	return true, nil
}

// newExtOutageEnv seeds the root row with a 100k token lease and a
// delegation_tree_budget row holding a 10k cumulative grant, and two of the
// tree's Redis budget keys.
func newExtOutageEnv(t *testing.T) *extOutageEnv {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	ctx := context.Background()
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, extOutageTenant, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pgstore.New(pg.Pool).Create(ctx, sessionstore.Session{
		ID: extOutageRoot, TenantID: extOutageTenant, UserID: "alice@acme.com", RuntimeRef: "echo",
		State:           session.StateRunning,
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: 100_000},
	}); err != nil {
		t.Fatalf("seed root row: %v", err)
	}
	direct := denialpg.New(pg.Pool)
	if _, err := direct.Grant(ctx, extOutageTenant, extOutageRoot, leasecontrol.Dimensions{Tokens: 10_000}, leasecontrol.Dimensions{Tokens: 1 << 40}); err != nil {
		t.Fatalf("seed delegation_tree_budget row: %v", err)
	}
	rd := containers.StartRedis(t, containers.RedisOptions{})
	for k, v := range map[string]string{
		"{" + extOutageRoot + "}:dlg:tokens":    "2500",
		"{" + extOutageRoot + "}:dlg:tree_size": "2",
	} {
		if err := rd.Client.Set(ctx, k, v, time.Hour).Err(); err != nil {
			t.Fatalf("seed redis key %s: %v", k, err)
		}
	}
	return &extOutageEnv{pg: pg, direct: direct, rd: rd, reclaim: &outageReclaimer{}}
}

// service builds a Service over a pool that dials Postgres through a fresh
// fault proxy, in elicitation mode with el.
func (e *extOutageEnv) service(t *testing.T, el func(*tcpfault.Proxy) leasecontrol.Elicitor) *leasecontrol.Service {
	t.Helper()
	proxy, dsn := tcpfault.StartURL(t, e.pg.DSN)
	e.proxy = proxy
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse proxied dsn: %v", err)
	}
	cfg.ConnConfig.ConnectTimeout = 2 * time.Second
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("proxied pool: %v", err)
	}
	t.Cleanup(pool.Close)
	src, err := leasecontrol.NewStoreBudgetSource(pgstore.New(pool), denialpg.New(pool), leasecontrol.ExtensionConfig{
		DeploymentBudget:    500_000,
		DeploymentMaxBudget: 2_000_000,
		ApprovalMode:        leasecontrol.ApprovalModeElicitation,
	})
	if err != nil {
		t.Fatalf("NewStoreBudgetSource: %v", err)
	}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:        src,
		Elicitor:       el(proxy),
		EpisodeContext: context.Background,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetReclaimer(e.reclaim)
	return svc
}

// redisSnapshot returns every key and its serialized value.
func (e *extOutageEnv) redisSnapshot(t *testing.T) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := e.rd.Client.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatalf("redis keys: %v", err)
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		v, err := e.rd.Client.Dump(ctx, k).Result()
		if err != nil {
			t.Fatalf("redis dump %s: %v", k, err)
		}
		out[k] = v
	}
	return out
}

// budgetRow reads the tree's cumulative grant and updated_at directly,
// bypassing the proxy.
func (e *extOutageEnv) budgetRow(t *testing.T) (leasecontrol.Dimensions, time.Time) {
	t.Helper()
	ctx := context.Background()
	grants, err := e.direct.Grants(ctx, extOutageTenant, extOutageRoot)
	if err != nil {
		t.Fatalf("read grants: %v", err)
	}
	tx, err := e.pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+extOutageTenant+"'"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	var updated time.Time
	if err := tx.QueryRow(ctx,
		`SELECT updated_at FROM delegation_tree_budget WHERE tenant_id = $1 AND root_session_id = $2`,
		extOutageTenant, extOutageRoot).Scan(&updated); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	return grants, updated
}

// spec: 8.6 (Extension state), 8.6 (In-flight requests), 12.3
//
// diagnosis: a Postgres outage during a §8.6 extension request did not fail
// closed. Either the request resolved Granted or raised the session's
// budget while Postgres was unreachable, a grant reached the
// delegation_tree_budget row, or a Redis budget key changed. The grant must
// commit only in the Postgres transaction that re-checks the denial, so an
// outage leaves no trace of a grant.
func TestLeaseExtensionPostgresOutageFailsClosed_spec_8_6(t *testing.T) {
	cases := []struct {
		name      string
		cutBefore bool
	}{
		{name: "outage between the read and the grant"},
		{name: "outage before the request", cutBefore: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newExtOutageEnv(t)
			svc := e.service(t, func(p *tcpfault.Proxy) leasecontrol.Elicitor {
				if c.cutBefore {
					return approveOnly{}
				}
				return cutDuringConsent{proxy: p}
			})
			wantGrants, wantUpdated := e.budgetRow(t)
			wantRedis := e.redisSnapshot(t)
			if c.cutBefore {
				e.proxy.Cut()
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			outcome, err := svc.ExtendForBudget(ctx, ctx, extOutageTenant, extOutageRoot)
			if outcome != leasecontrol.OutcomeTerminal || err == nil {
				t.Fatalf("outcome = %v, err = %v, want Terminal with the store error", outcome, err)
			}
			if n := e.reclaim.count(); n != 0 {
				t.Fatalf("the enforcer was raised %d times during the outage", n)
			}

			e.proxy.Restore(t)
			gotGrants, gotUpdated := e.budgetRow(t)
			if gotGrants != wantGrants || !gotUpdated.Equal(wantUpdated) {
				t.Fatalf("delegation_tree_budget row changed: grants %+v at %v, want %+v at %v", gotGrants, gotUpdated, wantGrants, wantUpdated)
			}
			if got := e.redisSnapshot(t); !reflect.DeepEqual(got, wantRedis) {
				t.Fatalf("redis budget keys changed during the outage: %v, want %v", got, wantRedis)
			}
		})
	}
}

// approveOnly approves every elicitation without touching the proxy.
type approveOnly struct{}

func (approveOnly) Elicit(context.Context, string, string) (bool, error) { return true, nil }
