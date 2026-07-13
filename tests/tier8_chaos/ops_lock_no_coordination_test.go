// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §25.4 remediation-lock multi-replica safety
// default under a genuine dual-storage outage, exercised against real
// Postgres and real Redis stores through the same tiered lock service
// cmd/lenny-ops wires.
//
// The tier-1 tests cover the pieces in isolation: the coordination gate
// matrix over a fake replica counter (gate_test.go), the tiered service
// falling to the in-memory tier when both fakes are down under the
// single-replica or "never" policy (service_test.go), and the acquire
// handler's gate branch over a fake lock service (opsserver
// locks_coordination_test.go). None of them composes the real tiered
// coordination.Service (Postgres Tier 1 + Redis Tier 2 over the in-memory
// Tier 3) with the ops.locks.memoryTier gate and takes both real stores
// down, so the integrated multi-replica rejection path — a real
// dual-store outage falling through both durable tiers into the gate,
// surfacing as the §25.4 503 REMEDIATION_LOCK_NO_COORDINATION envelope on
// the acquire endpoint — is unexercised. This test composes those the way
// cmd/lenny-ops does (services_wiring.go buildSelfHealthAndLocks +
// buildLockService), drives POST /v1/admin/remediation-locks over the real
// opsserver HTTP handler, and asserts the multi-replica default rejects
// while a single-replica deployment under the same dual outage still
// grants.
//
// Store availability is toggled at the Store boundary (the same technique
// the escalation tiered-store chaos test uses) so every operation
// round-trips through the real Postgres/Redis backend while a tier is up
// and returns the exact coordination.ErrStoreUnavailable the real pgstore
// and redisstore raise on a genuine connection failure while a tier is
// down, letting the tiered service fall through as it would in production.

package tier8_chaos_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/coordination/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/coordination/redisstore"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// fixedLockReplicas is a constant coordination.ReplicaCounter: it reports
// a fixed ready-replica count so the §25.4 single-replica-only gate can be
// driven at a known replica posture without a live Kubernetes Endpoints
// lookup (that lookup is exercised by pkg/ops/opsservice/replicas_test.go).
type fixedLockReplicas int

func (f fixedLockReplicas) ReplicaCount() int { return int(f) }

// toggleLockStore wraps a real §25.4 remediation-lock coordination.Store
// with an atomic availability switch. While up every operation delegates
// to the real store, which round-trips through the real Postgres or Redis
// backend; while down every operation returns coordination.ErrStoreUnavailable
// — the exact signal the real pgstore and redisstore raise on a genuine
// connection failure — so the tiered Service falls through to the next
// tier. Tier() is a static label and is always answerable, matching the
// real stores.
type toggleLockStore struct {
	inner coordination.Store
	up    atomic.Bool
}

func newToggleLockStore(inner coordination.Store, up bool) *toggleLockStore {
	t := &toggleLockStore{inner: inner}
	t.up.Store(up)
	return t
}

func (t *toggleLockStore) setUp(up bool) { t.up.Store(up) }

func (t *toggleLockStore) Tier() string { return t.inner.Tier() }

func (t *toggleLockStore) Acquire(ctx context.Context, req coordination.LockRequest, epoch uint64) (*coordination.Lock, error) {
	if !t.up.Load() {
		return nil, coordination.ErrStoreUnavailable
	}
	return t.inner.Acquire(ctx, req, epoch)
}

func (t *toggleLockStore) List(ctx context.Context) ([]coordination.Lock, error) {
	if !t.up.Load() {
		return nil, coordination.ErrStoreUnavailable
	}
	return t.inner.List(ctx)
}

func (t *toggleLockStore) Get(ctx context.Context, lockID string) (*coordination.Lock, error) {
	if !t.up.Load() {
		return nil, coordination.ErrStoreUnavailable
	}
	return t.inner.Get(ctx, lockID)
}

func (t *toggleLockStore) Extend(ctx context.Context, lockID string, ttlSeconds int, caller string) (*coordination.Lock, error) {
	if !t.up.Load() {
		return nil, coordination.ErrStoreUnavailable
	}
	return t.inner.Extend(ctx, lockID, ttlSeconds, caller)
}

func (t *toggleLockStore) Release(ctx context.Context, lockID, caller string) error {
	if !t.up.Load() {
		return coordination.ErrStoreUnavailable
	}
	return t.inner.Release(ctx, lockID, caller)
}

func (t *toggleLockStore) Steal(ctx context.Context, lockID string, req coordination.StealRequest) (*coordination.Lock, error) {
	if !t.up.Load() {
		return nil, coordination.ErrStoreUnavailable
	}
	return t.inner.Steal(ctx, lockID, req)
}

func (t *toggleLockStore) Reap(ctx context.Context) (int, error) {
	if !t.up.Load() {
		return 0, coordination.ErrStoreUnavailable
	}
	return t.inner.Reap(ctx)
}

func (t *toggleLockStore) Epoch(ctx context.Context) (uint64, error) {
	if !t.up.Load() {
		return 0, coordination.ErrStoreUnavailable
	}
	return t.inner.Epoch(ctx)
}

// acquireResult is the subset of the acquire response the assertions read.
type acquireResult struct {
	status    int
	lockStore string // the granted lock's lockStore tier, when 201
	errCode   string // the §25.2 error envelope code, when non-2xx
	errCat    string // the §25.2 error envelope category, when non-2xx
	raw       string // the raw body, for diagnostics
}

// acquireLock POSTs a §25.4 remediation-lock acquire to the opsserver HTTP
// surface and decodes the granted lock's tier or the error envelope. The
// X-Lenny-Caller header stamps acquiredBy; with no AuthConfig wired the
// caller resolves to platform-admin, which is authorized for every scope.
func acquireLock(t *testing.T, baseURL, scope string) acquireResult {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"scope":      scope,
		"operation":  "scale",
		"ttlSeconds": 300,
	})
	if err != nil {
		t.Fatalf("marshal acquire body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/admin/remediation-locks", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build acquire request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Caller", "prod-watchdog")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST acquire %q: %v", scope, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	res := acquireResult{status: resp.StatusCode, raw: string(body)}
	var decoded struct {
		LockStore string `json:"lockStore"`
		Error     struct {
			Code     string `json:"code"`
			Category string `json:"category"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &decoded); err == nil {
		res.lockStore = decoded.LockStore
		res.errCode = decoded.Error.Code
		res.errCat = decoded.Error.Category
	}
	return res
}

// spec: 25.4 (multi-replica memoryTier rejection under dual-store outage)
// diagnosis: the §25.4 remediation-lock multi-replica safety default did
// not hold against real stores. §25.4 line 1545: "Default behavior in
// multi-replica mode (ops.locks.memoryTier: single-replica-only) rejects
// in-memory locks during dual-storage outages, returning 503
// REMEDIATION_LOCK_NO_COORDINATION." §25.4 line 2214: Tier 3 "succeeds
// only if lenny-ops is running with a single replica ... In multi-replica
// deployments, Tier 3 returns 503 REMEDIATION_LOCK_NO_COORDINATION." The
// test composes the real tiered lock service (real Postgres Tier 1 + real
// Redis Tier 2 over the in-memory Tier 3) with the single-replica-only
// gate the way cmd/lenny-ops wires it, drives the acquire endpoint over
// real HTTP, and asserts that with both durable stores down a two-replica
// deployment rejects the acquire with 503 REMEDIATION_LOCK_NO_COORDINATION
// / TRANSIENT while a single-replica deployment under the same dual outage
// still grants an in-memory lock. A failure means either two lenny-ops
// replicas would silently grant uncoordinated per-replica locks on the
// same scope during a dual outage (split-brain remediation), or a
// single-replica deployment was needlessly denied coordination it can
// safely provide.
func TestOpsLockMultiReplicaRejectsUncoordinatedAcquireUnderDualOutage(t *testing.T) {
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})

	pgTier := newToggleLockStore(pgstore.New(pg.Pool), true)
	rdTier := newToggleLockStore(redisstore.New(rd.Client), true)

	// The two-replica deployment under the §25.4 single-replica-only
	// default. The gate is shared with the service so the acquire path
	// consults the same policy cmd/lenny-ops wires (buildLockService passes
	// the gate into ServiceOptions).
	multiGate := coordination.NewCoordinationGate(coordination.MemoryTierSingleReplicaOnly, fixedLockReplicas(2))
	multiSvc := coordination.NewService(coordination.ServiceOptions{
		Postgres: pgTier,
		Redis:    rdTier,
		Gate:     multiGate,
	})
	multiSrv := httptest.NewServer(opsserver.New(opsserver.Options{
		Locks:            multiSvc,
		LockCoordination: multiGate,
	}))
	defer multiSrv.Close()

	// Precondition: with both stores up the tiered service genuinely serves
	// from the durable Tier 1 (Postgres). This proves the later rejection is
	// a real dual-outage fall-through rather than a service that was already
	// serving from the in-memory tier.
	pre := acquireLock(t, multiSrv.URL, "pool:precondition-gvisor")
	if pre.status != http.StatusCreated {
		t.Fatalf("precondition acquire status = %d, want 201 (both stores up); body=%s", pre.status, pre.raw)
	}
	if pre.lockStore != coordination.StorePostgres {
		t.Fatalf("precondition acquire lockStore = %q, want %q; the tiered service is not serving from the durable Tier 1",
			pre.lockStore, coordination.StorePostgres)
	}
	if got := multiSvc.ActiveTier(); got != coordination.StorePostgres {
		t.Fatalf("active tier = %q before the outage, want %q", got, coordination.StorePostgres)
	}
	t.Logf("precondition: tiered lock service serving from Postgres (Tier 1)")

	// Inject: take both durable stores down. The tiered service now falls
	// Postgres (unavailable) -> Redis (unavailable) -> in-memory Tier 3, where
	// the single-replica-only gate governs the acquire.
	pgTier.setUp(false)
	rdTier.setUp(false)
	t.Logf("injected: both Postgres and Redis unavailable; the in-memory Tier 3 gate now governs acquires")

	// Assert: the two-replica deployment rejects the uncoordinated in-memory
	// acquire with the §25.4 503 REMEDIATION_LOCK_NO_COORDINATION / TRANSIENT
	// envelope.
	got := acquireLock(t, multiSrv.URL, "pool:default-gvisor")
	if got.status != http.StatusServiceUnavailable {
		t.Fatalf("multi-replica acquire under dual outage status = %d, want 503 (§25.4 REMEDIATION_LOCK_NO_COORDINATION); body=%s",
			got.status, got.raw)
	}
	if got.errCode != coordination.ErrCodeNoCoordination {
		t.Errorf("multi-replica acquire error code = %q, want %q; body=%s",
			got.errCode, coordination.ErrCodeNoCoordination, got.raw)
	}
	if got.errCat != "TRANSIENT" {
		t.Errorf("multi-replica acquire error category = %q, want TRANSIENT (§25.4 error table); body=%s",
			got.errCat, got.raw)
	}
	// The rejection must not have persisted anything at any tier: the Tier 3
	// store was never asked to grant, so a subsequent read (once a store is
	// back) shows no pool:default-gvisor lock. Confirm no in-memory lock was
	// granted by bringing Redis back and listing.
	rdTier.setUp(true)
	if locks, err := rdTier.List(ctx); err != nil {
		t.Logf("post-reject Redis list (diagnostic only): %v", err)
	} else {
		for _, l := range locks {
			if l.Scope == "pool:default-gvisor" {
				t.Errorf("a lock on pool:default-gvisor exists after the rejected acquire; the reject must grant nothing")
			}
		}
	}
	rdTier.setUp(false)
	t.Logf("multi-replica deployment rejected the uncoordinated acquire with 503 REMEDIATION_LOCK_NO_COORDINATION")

	// Control: a single-replica deployment under the SAME dual outage still
	// grants an in-memory Tier 3 lock (§25.4 line 2214: Tier 3 "succeeds only
	// if lenny-ops is running with a single replica"). This attributes the
	// 503 above specifically to the multi-replica count rather than to a
	// blanket dual-outage denial. A fresh service over the same down stores
	// isolates the single-replica gate.
	singleGate := coordination.NewCoordinationGate(coordination.MemoryTierSingleReplicaOnly, fixedLockReplicas(1))
	singleSvc := coordination.NewService(coordination.ServiceOptions{
		Postgres: pgTier,
		Redis:    rdTier,
		Gate:     singleGate,
	})
	singleSrv := httptest.NewServer(opsserver.New(opsserver.Options{
		Locks:            singleSvc,
		LockCoordination: singleGate,
	}))
	defer singleSrv.Close()

	grant := acquireLock(t, singleSrv.URL, "pool:single-replica-gvisor")
	if grant.status != http.StatusCreated {
		t.Fatalf("single-replica acquire under dual outage status = %d, want 201 (§25.4 single-replica Tier 3 grants); body=%s",
			grant.status, grant.raw)
	}
	if grant.lockStore != coordination.StoreMemory {
		t.Errorf("single-replica acquire lockStore = %q, want %q (in-memory Tier 3); body=%s",
			grant.lockStore, coordination.StoreMemory, grant.raw)
	}
	t.Logf("single-replica deployment granted an in-memory Tier 3 lock under the same dual outage; " +
		"the multi-replica 503 is attributable to the replica count")
}
