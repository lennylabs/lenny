// SPDX-License-Identifier: MIT

// R-03 contract test. §12.3 line 144 mandates that every billing event
// insert and every audit log insert is routed through the StoreRouter
// interface rather than reaching a Postgres pool directly, and that an
// integration test named TestBillingAuditRoutedThroughStoreRouter
// verifies the call sites use StoreRouter methods. This file is that
// test. It is a tier-1 unit test with no container dependency: a
// recording router returns a sentinel from BillingShard / AuditShard so
// each write short-circuits before any SQL, and the assertion is that
// the store obtained its pool through the router (recording the tenant)
// rather than holding a raw *pgxpool.Pool. F-12.6.2 / F-12.3.4 /
// F-12.6.1 / F-12.2.13 / F-12.7.1.
package stores_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	billingpg "github.com/lennylabs/lenny/pkg/gateway/billingstore/pgstore"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// recordingRouter satisfies both billingpg.Router and auditstore.Router.
// Every shard accessor records the tenant it was asked to route and
// returns errSentinel so the store under test never touches a pool.
type recordingRouter struct {
	billingTenants []storerouter.TenantID
	auditTenants   []storerouter.TenantID
}

var errSentinel = errors.New("recording router: no pool")

func (r *recordingRouter) BillingShard(_ context.Context, t storerouter.TenantID) (*pgxpool.Pool, error) {
	r.billingTenants = append(r.billingTenants, t)
	return nil, errSentinel
}

func (r *recordingRouter) AuditShard(_ context.Context, t storerouter.TenantID) (*pgxpool.Pool, error) {
	r.auditTenants = append(r.auditTenants, t)
	return nil, errSentinel
}

func (r *recordingRouter) AuditReadShard(_ context.Context, t storerouter.TenantID) (*pgxpool.Pool, error) {
	r.auditTenants = append(r.auditTenants, t)
	return nil, errSentinel
}

func (r *recordingRouter) AllAuditShards(context.Context) ([]storerouter.ShardHandle, error) {
	return nil, errSentinel
}

// spec: §12.3 R-03 line 144.
// diagnosis: a failure means billing and audit writes bypass the
// StoreRouter, breaking the §12.3 R-03 routing that targets the correct
// per-tenant shard.
func TestBillingAuditRoutedThroughStoreRouter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("billing Append resolves its pool through StoreRouter.BillingShard", func(t *testing.T) {
		rr := &recordingRouter{}
		store := billingpg.New(rr)
		_, err := store.Append(ctx, billingstore.Event{
			TenantID:  "acme",
			UserID:    "alice@acme.com",
			SessionID: "sess-1",
			EventType: billingstore.EventSessionCreated,
		})
		if !errors.Is(err, errSentinel) {
			t.Fatalf("Append err = %v, want errSentinel (proves the write went through the router, not a raw pool)", err)
		}
		if len(rr.billingTenants) != 1 || rr.billingTenants[0] != "acme" {
			t.Fatalf("BillingShard calls = %v, want exactly [acme]", rr.billingTenants)
		}
		if len(rr.auditTenants) != 0 {
			t.Fatalf("a billing write must not route through AuditShard, got %v", rr.auditTenants)
		}
	})

	t.Run("audit Append resolves its pool through StoreRouter.AuditShard", func(t *testing.T) {
		rr := &recordingRouter{}
		store := auditstore.New(rr)
		_, err := store.Append(ctx, "acme", "session.created", json.RawMessage(`{}`), time.Time{})
		if !errors.Is(err, errSentinel) {
			t.Fatalf("Append err = %v, want errSentinel", err)
		}
		if len(rr.auditTenants) != 1 || rr.auditTenants[0] != "acme" {
			t.Fatalf("AuditShard calls = %v, want exactly [acme]", rr.auditTenants)
		}
		if len(rr.billingTenants) != 0 {
			t.Fatalf("an audit write must not route through BillingShard, got %v", rr.billingTenants)
		}
	})

	t.Run("the recording router satisfies both store Router interfaces", func(t *testing.T) {
		var _ billingpg.Router = (*recordingRouter)(nil)
		var _ auditstore.Router = (*recordingRouter)(nil)
		// The production single-shard router satisfies them too, so the
		// gateway wiring compiles against the same contract.
		var _ billingpg.Router = (*storerouter.SingleShardRouter)(nil)
		var _ auditstore.Router = (*storerouter.SingleShardRouter)(nil)
	})
}
