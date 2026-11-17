//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	poolpg "github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// retiredPolicyJSON is a sessionPolicy document that still names the
// deleted maxSessionRetries key next to a field that survives.
const retiredPolicyJSON = `{"maxSessionRetries":2,"cleanupTimeoutSeconds":30}`

// decodeRetiredPolicy decodes retiredPolicyJSON the way the admin handler
// decodes a submitted policy.
func decodeRetiredPolicy(t *testing.T) *runtimestore.SessionPolicy {
	t.Helper()
	var sp runtimestore.SessionPolicy
	if err := json.Unmarshal([]byte(retiredPolicyJSON), &sp); err != nil {
		t.Fatalf("decode policy carrying maxSessionRetries: %v", err)
	}
	return &sp
}

// rawSessionPolicy reads the session_policy jsonb column of one row as text.
func rawSessionPolicy(t *testing.T, pg *containers.Postgres, table, name string) string {
	t.Helper()
	var raw string
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT session_policy::text FROM `+table+` WHERE name = $1`, name).Scan(&raw); err != nil {
		t.Fatalf("read %s.session_policy for %s: %v", table, name, err)
	}
	return raw
}

// plantLegacyPolicy writes retiredPolicyJSON straight into the column, as a
// row written before the key was deleted holds it.
func plantLegacyPolicy(t *testing.T, pg *containers.Postgres, table, name string) {
	t.Helper()
	if _, err := pg.Pool.Exec(context.Background(),
		`UPDATE `+table+` SET session_policy = $2::jsonb WHERE name = $1`, name, retiredPolicyJSON); err != nil {
		t.Fatalf("plant legacy session_policy on %s %s: %v", table, name, err)
	}
}

// assertPolicyWithoutRetries fails when the column still names the retired
// key or lost the field that survives it.
func assertPolicyWithoutRetries(t *testing.T, what, raw string) {
	t.Helper()
	if strings.Contains(raw, "maxSessionRetries") {
		t.Errorf("%s: session_policy still carries maxSessionRetries: %s", what, raw)
	}
	if !strings.Contains(raw, `"cleanupTimeoutSeconds": 30`) && !strings.Contains(raw, `"cleanupTimeoutSeconds":30`) {
		t.Errorf("%s: session_policy lost cleanupTimeoutSeconds: %s", what, raw)
	}
}

// spec: 7.3 (Retry and Resume), 5.2 (Pool Configuration and Execution Modes)
// diagnosis: the Postgres runtime or pool store persisted the deleted
//
//	sessionPolicy.maxSessionRetries key, or failed to read a row that a
//	pre-deletion write left carrying it. retryPolicy.maxRetries is the one
//	retry budget, so SessionPolicy must carry no such field: a create from
//	a payload that names it writes a column without it, a legacy row
//	decodes, and the next update rewrites the column without it.
func TestSessionPolicyStoresDropRetiredMaxSessionRetries(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	ctx := context.Background()

	t.Run("runtime store", func(t *testing.T) {
		store := runtimepg.New(pg.Pool)
		r := sampleRuntime(runtimeName(t))
		r.SessionPolicy = decodeRetiredPolicy(t)
		if err := store.Create(ctx, r); err != nil {
			t.Fatalf("Create: %v", err)
		}
		assertPolicyWithoutRetries(t, "after create", rawSessionPolicy(t, pg, "runtime_definitions", r.Name))

		plantLegacyPolicy(t, pg, "runtime_definitions", r.Name)
		got, err := store.Get(ctx, r.Name)
		if err != nil {
			t.Fatalf("Get a legacy row: %v", err)
		}
		if got.SessionPolicy == nil || got.SessionPolicy.CleanupTimeoutSeconds != 30 {
			t.Fatalf("legacy row decoded to %+v", got.SessionPolicy)
		}
		if _, err := store.Update(ctx, r.Name, func(rt *runtimestore.Runtime) error {
			rt.Description = "rewritten"
			return nil
		}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertPolicyWithoutRetries(t, "after update", rawSessionPolicy(t, pg, "runtime_definitions", r.Name))
	})

	t.Run("pool store", func(t *testing.T) {
		store := poolpg.New(pg.Pool)
		p := poolstore.Pool{
			Name:             poolName(t),
			RuntimeRef:       "claude",
			IsolationProfile: isolation.ProfileSandboxed,
			ExecutionMode:    runtimestore.ExecutionModeSession,
			SessionPolicy:    decodeRetiredPolicy(t),
		}
		if err := store.Create(ctx, p); err != nil {
			t.Fatalf("Create: %v", err)
		}
		assertPolicyWithoutRetries(t, "after create", rawSessionPolicy(t, pg, "sandbox_warm_pools", p.Name))

		plantLegacyPolicy(t, pg, "sandbox_warm_pools", p.Name)
		got, err := store.Get(ctx, p.Name)
		if err != nil {
			t.Fatalf("Get a legacy row: %v", err)
		}
		if got.SessionPolicy == nil || got.SessionPolicy.CleanupTimeoutSeconds != 30 {
			t.Fatalf("legacy row decoded to %+v", got.SessionPolicy)
		}
		if _, err := store.Update(ctx, p.Name, func(pp *poolstore.Pool) error {
			pp.WarmCount = 2
			return nil
		}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertPolicyWithoutRetries(t, "after update", rawSessionPolicy(t, pg, "sandbox_warm_pools", p.Name))
	})
}
