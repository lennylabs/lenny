// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.2 recovery_generation increment on a
// real pod recovery, verified through the durable stack rather than the
// in-memory session store. It wires a fully in-process gateway Server
// against a real kube-apiserver (envtest), a real adapter over an
// in-memory gRPC connection, and the Postgres-backed session store
// (pkg/gateway/session/sessionstore/pgstore) running against a Postgres
// container with the production migrations applied.
//
// The flow seeds a session in `awaiting_client_action` (the state a
// session reaches after its pod fails and automatic recovery is
// abandoned) carrying a §7.1 workspace checkpoint, drives
// POST /v1/sessions/{id}/resume, which restores the session onto a
// fresh warm pod, and asserts that recovery_generation advanced by
// exactly one and that the advance is observable to clients through the
// session API (GET /v1/sessions/{id}) and durable in Postgres. The
// existing coverage of this increment exercises only the in-memory
// store; this test pins the "visible to clients via the session API"
// and durability halves of the §4.2 claim end to end.
//
// This crosses the gateway, the datastore (the per-pod SandboxClaim on
// the apiserver plus the Postgres session row), and the pod adapter,
// which is the multi-service surface the integration tier owns.

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// resumeArchiveSource is an adapter.CheckpointSource that serves a fixed
// gzip-tar workspace archive to the Resume RPC, standing in for the §4.4
// Artifact Store the production restore path reads from.
type resumeArchiveSource struct{ archive []byte }

func (r resumeArchiveSource) LoadCheckpoint(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(r.archive)), nil
}

// emptyResumeArchive builds the gzip-tar archive of an empty workspace,
// the minimal payload the adapter Resume RPC accepts.
func emptyResumeArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := workspace.Archive(t.TempDir(), &buf); err != nil {
		t.Fatalf("archive empty workspace: %v", err)
	}
	return buf.Bytes()
}

// spec: §4.2 line 156 — "recovery_generation is incremented on each pod
// recovery (visible to clients via the session API and session.resumed
// events); it tracks how many times this logical session has been
// recovered onto a new pod."
// diagnosis: a failure means the §4.2 recovery_generation increment is
// not durable or not client-visible through the real stack. If the
// counter stays 0 after a resume, the gateway did not bump it on the
// pod-recovery path when backed by Postgres (the in-memory store test
// passes but the durable path regressed). If GET /v1/sessions/{id}
// reports a different value than the Postgres row, the session API view
// diverged from the persisted counter. If the direct Postgres read
// disagrees, the increment was not committed to the durable store.
func TestResumeBumpsRecoveryGenerationDurablyPostgres(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx := context.Background()

	// The tenant row must exist before a session can reference it (FK +
	// RLS). Mirror the tier-2 SessionStore contract's minimal seed.
	const tenant = "acme"
	if _, err := pg.Pool.Exec(
		ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, tenant, []byte{0x01},
	); err != nil {
		t.Fatalf("seed tenant %q: %v", tenant, err)
	}

	// The adapter serves the Resume RPC: the Restorer feeds the workspace
	// checkpoint the seeded session references, so resumeOnPod takes the
	// §7.1 snapshot-restore branch (no credential resolution).
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.StagingDir = t.TempDir()
	adapterSrv.Runtime = &eagerRuntime{}
	adapterSrv.Restorer = resumeArchiveSource{archive: emptyResumeArchive(t)}

	// eagerCluster seeds a warm pool (echo-pool / echo-tmpl) with one idle
	// Sandbox (sbx-1) in eagerNS that the resume path claims a fresh pod
	// from. Reused from eager_claim_lifecycle_test.go (same package).
	cluster := eagerCluster(t)
	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        eagerNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      eagerAdapterDialer(t, adapterSrv),
	}

	store := sessionpg.New(pg.Pool)
	srv := sessionserver.New(store, sessionserver.Options{
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          eagerNS,
	})
	h := srv.Handler()

	// Seed a session in awaiting_client_action carrying a workspace
	// checkpoint. This is the only state POST /resume accepts, and the
	// checkpoint ref routes resumeOnPod down the snapshot-restore branch.
	sid := newSessionUUID(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Create(ctx, sessionstore.Session{
		ID:               sid,
		TenantID:         tenant,
		UserID:           "alice@acme.com",
		State:            session.StateAwaitingClientAction,
		RuntimeRef:       "echo",
		IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt:        now,
		UpdatedAt:        now,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:       "lenny-blob://acme/checkpoint/ckpt-1",
			Source:    sessionstore.WorkspaceSnapshotCheckpoint,
			Timestamp: now.Add(-time.Minute),
		},
	}); err != nil {
		t.Fatalf("seed awaiting session: %v", err)
	}

	get := func() sessionserver.SessionResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sid, nil)
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET session: status %d, body=%s", rr.Code, rr.Body.String())
		}
		var out sessionserver.SessionResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode session response: %v", err)
		}
		return out
	}

	// dbRecoveryGeneration reads the recovery_generation column straight
	// from Postgres, bypassing the store, to prove the increment is
	// committed to the durable row rather than held in a process cache.
	// The container connects as the table owner, which bypasses RLS.
	dbRecoveryGeneration := func() int64 {
		t.Helper()
		var n int64
		if err := pg.Pool.QueryRow(
			ctx,
			`SELECT recovery_generation FROM sessions WHERE id = $1`, sid,
		).Scan(&n); err != nil {
			t.Fatalf("read recovery_generation from postgres: %v", err)
		}
		return n
	}

	// Baseline: a freshly seeded session has recovery_generation 0 in
	// both the API view and the durable row.
	if got := get().RecoveryGeneration; got != 0 {
		t.Fatalf("baseline recoveryGeneration (session API) = %d, want 0", got)
	}
	if got := dbRecoveryGeneration(); got != 0 {
		t.Fatalf("baseline recovery_generation (postgres) = %d, want 0", got)
	}

	// Recover the session onto a fresh warm pod.
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sid+"/resume", nil)
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, body=%s", rr.Code, rr.Body.String())
	}

	// After one pod recovery the counter advanced by exactly one, and the
	// advance is visible through the session API — the §4.2
	// client-visibility claim.
	after := get()
	if after.RecoveryGeneration != 1 {
		t.Errorf("after resume, recoveryGeneration (session API) = %d, want 1 (§4.2 line 156)",
			after.RecoveryGeneration)
	}
	if after.State != string(session.StateRunning) {
		t.Errorf("after resume, state = %q, want running", after.State)
	}

	// The advance is durable: the committed Postgres row carries the
	// incremented value, matching the API view.
	if got := dbRecoveryGeneration(); got != 1 {
		t.Errorf("after resume, recovery_generation (postgres) = %d, want 1 (durable increment)", got)
	}
}
