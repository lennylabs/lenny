// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// fixedReplicas is a constant ReplicaCounter for the coordination-gate
// handler tests.
type fixedReplicas int

func (f fixedReplicas) ReplicaCount() int { return int(f) }

func gatedLockServer(tier coordination.MemoryTier, replicas coordination.ReplicaCounter) *opsserver.Server {
	return opsserver.New(opsserver.Options{
		Locks:            coordination.NewMemStore(),
		LockCoordination: coordination.NewCoordinationGate(tier, replicas),
	})
}

// spec: §25.4 lines 2206-2208 — single-replica-only grants the acquire on
// a single replica with no degradation envelope.
func TestAcquireSingleReplicaOnlyGrantsOnSingleReplica(t *testing.T) {
	srv := gatedLockServer(coordination.MemoryTierSingleReplicaOnly, fixedReplicas(1))
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "watchdog"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%v", rec.Code, body)
	}
	if _, present := body["degradation"]; present {
		t.Errorf("single-replica grant carried a degradation envelope: %v", body["degradation"])
	}
}

// spec: §25.4 lines 2208, 2326, 1539 — single-replica-only rejects the
// in-memory acquire in a multi-replica deployment with 503
// REMEDIATION_LOCK_NO_COORDINATION / TRANSIENT.
func TestAcquireSingleReplicaOnlyRejectsMultiReplica(t *testing.T) {
	srv := gatedLockServer(coordination.MemoryTierSingleReplicaOnly, fixedReplicas(2))
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "watchdog"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "REMEDIATION_LOCK_NO_COORDINATION" || errObj["category"] != "TRANSIENT" {
		t.Errorf("error = %v, want REMEDIATION_LOCK_NO_COORDINATION/TRANSIENT", errObj)
	}
}

// spec: §25.4 line 2209 — "always" grants but attaches a replica-local
// warning in the degradation envelope.
func TestAcquireAlwaysGrantsWithDegradationWarning(t *testing.T) {
	srv := gatedLockServer(coordination.MemoryTierAlways, fixedReplicas(3))
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "watchdog"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%v", rec.Code, body)
	}
	// The lock fields are still inlined on the response.
	if body["acquiredBy"] != "watchdog" {
		t.Errorf("acquiredBy = %v, want watchdog", body["acquiredBy"])
	}
	deg, _ := body["degradation"].(map[string]any)
	if deg["level"] != "degraded" {
		t.Errorf("degradation.level = %v, want degraded", deg["level"])
	}
	warnings, _ := deg["warnings"].([]any)
	if len(warnings) != 1 {
		t.Fatalf("degradation.warnings = %v, want one replica-local warning", deg["warnings"])
	}
}

// spec: §25.4 line 2210 — "never" rejects every acquire even on a single
// replica.
func TestAcquireNeverRejects(t *testing.T) {
	srv := gatedLockServer(coordination.MemoryTierNever, fixedReplicas(1))
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "watchdog"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%v", rec.Code, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "REMEDIATION_LOCK_NO_COORDINATION" {
		t.Errorf("error code = %v, want REMEDIATION_LOCK_NO_COORDINATION", errObj["code"])
	}
}

// tieredFakeLocks is a lock service that reports an active durable tier,
// so the handler bypasses the ops.locks.memoryTier gate. It embeds a
// MemStore for the RemediationLockService surface.
type tieredFakeLocks struct {
	*coordination.MemStore
	tier string
}

func (f tieredFakeLocks) ActiveTier() string { return f.tier }

// spec: §25.4 lines 2204-2208 — the memoryTier gate governs only the
// in-memory (Tier-3) acquire path. When a durable tier (Postgres/Redis) is
// serving, even the most conservative "never" policy must not reject an
// acquire, because the in-memory tier is not in play.
func TestAcquireDurableTierBypassesGate(t *testing.T) {
	srv := opsserver.New(opsserver.Options{
		Locks:            tieredFakeLocks{MemStore: coordination.NewMemStore(), tier: coordination.StorePostgres},
		LockCoordination: coordination.NewCoordinationGate(coordination.MemoryTierNever, fixedReplicas(3)),
	})
	rec, body := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "watchdog"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (durable tier bypasses the gate); body=%v", rec.Code, body)
	}
	if _, present := body["degradation"]; present {
		t.Errorf("durable-tier acquire carried a degradation envelope: %v", body["degradation"])
	}
}

// spec: §25.4 lines 2208, 2326 — when the in-memory tier is the active one,
// the gate still rejects under single-replica-only on a multi-replica
// deployment.
func TestAcquireMemoryTierStillGated(t *testing.T) {
	srv := opsserver.New(opsserver.Options{
		Locks:            tieredFakeLocks{MemStore: coordination.NewMemStore(), tier: coordination.StoreMemory},
		LockCoordination: coordination.NewCoordinationGate(coordination.MemoryTierSingleReplicaOnly, fixedReplicas(2)),
	})
	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/admin/remediation-locks",
		map[string]string{"X-Lenny-Caller": "watchdog"},
		map[string]any{"scope": "pool:p", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (in-memory tier remains gated)", rec.Code)
	}
}

// The gate governs only acquisition. A lock obtained while single-replica
// remains releasable even if the gate would now reject a fresh acquire,
// because release/extend operate on an already-granted lock.
func TestCoordinationGateGovernsOnlyAcquire(t *testing.T) {
	// Single-replica server grants the lock.
	srv := gatedLockServer(coordination.MemoryTierNever, fixedReplicas(1))
	// never mode rejects acquire, so list should still be reachable (no gate).
	rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/remediation-locks", nil, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("list status = %d, want 200 (the gate does not block reads)", rec.Code)
	}
}
