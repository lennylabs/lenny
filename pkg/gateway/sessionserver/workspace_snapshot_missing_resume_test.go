// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// absentSnapshotSource is an adapter.CheckpointSource that models a
// workspace snapshot object that is absent at the restored-from-replication
// ArtifactStore: LoadCheckpoint reports the object is not found. This is the
// state the spec's restore procedure describes after a primary-site loss —
// the GC path has swept the dangling pointer, and the object the session's
// WorkspaceSnapshot.Ref names no longer exists at the promoted target.
type absentSnapshotSource struct{}

func (absentSnapshotSource) LoadCheckpoint(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("object not found at restored artifact store")
}

// spec: §25.11 (Backup and Restore API — ArtifactStore restore procedure)
// diagnosis: a failure means the ArtifactStore restore procedure's
// client-visible failure mode is not honored. The spec's restore procedure
// states: "Sessions whose workspace snapshot is missing transition to
// `failed` on next resume attempt with error `WORKSPACE_SNAPSHOT_MISSING`;
// the gateway surfaces this to clients via the existing session-state API."
// When a session resumes against a restored ArtifactStore whose workspace
// snapshot object is absent, the resume must not land in a generic `failed`
// row with no client-visible cause: the session-state API must carry the
// `WORKSPACE_SNAPSHOT_MISSING` error so an operator or client can attribute
// the terminal transition to the missing snapshot rather than a transient
// resume fault.
//
// The test seeds a resumable session whose WorkspaceSnapshot.Ref points at a
// snapshot object that is absent at the restored ArtifactStore (the adapter's
// checkpoint source reports not-found), drives POST /v1/sessions/{id}/resume,
// and asserts the session transitions to `failed` with
// `WORKSPACE_SNAPSHOT_MISSING` surfaced on GET /v1/sessions/{id}.
func TestResumeWithMissingWorkspaceSnapshotFailsWithSnapshotMissing_spec_25_11(t *testing.T) {
	t.Skip("WORKSPACE_SNAPSHOT_MISSING resume failure mode is unimplemented; the string appears only in the spec, not in Go source. The detection mechanism (adapter Resume not-found signal vs. gateway pre-resume snapshot probe) and the session-state API field that carries the error are undecided — see the open TEST-GAPS finding for the ArtifactStore restore-procedure failure mode.")

	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	adapterSrv.Restorer = absentSnapshotSource{}

	cluster := podBindClient(
		t,
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	registry := podsession.NewRegistry()
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))

	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-snap-missing" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
	})

	seedAwaitingSession(t, store, sessionstore.Session{
		ID: "sess-snap-missing",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:    "ckpt-absent-at-target",
			Source: sessionstore.WorkspaceSnapshotCheckpoint,
		},
	})

	rr := postSessionStep(t, srv.Handler(), "/v1/sessions/sess-snap-missing/resume", nil)
	if rr.Code == http.StatusOK {
		t.Fatalf("resume succeeded (status 200) despite an absent workspace snapshot; the spec requires the session to transition to failed with WORKSPACE_SNAPSHOT_MISSING")
	}

	// The row transitioned to the terminal `failed` state on this resume
	// attempt — not to a retryable holding state — because a missing snapshot
	// is not recoverable by re-issuing resume.
	row, err := store.Get(context.Background(), "acme", "sess-snap-missing")
	if err != nil {
		t.Fatalf("get resumed session: %v", err)
	}
	if row.State != session.StateFailed {
		t.Errorf("state = %q, want failed; a missing workspace snapshot is a terminal resume failure per the ArtifactStore restore procedure", row.State)
	}

	// The session-state API surfaces WORKSPACE_SNAPSHOT_MISSING so the client
	// can attribute the terminal transition to the absent snapshot.
	got := sessionRequest(t, srv.Handler(), http.MethodGet, "/v1/sessions/sess-snap-missing")
	if got.Code != http.StatusOK {
		t.Fatalf("GET session: status %d, want 200; body=%s", got.Code, got.Body.String())
	}
	if !strings.Contains(got.Body.String(), "WORKSPACE_SNAPSHOT_MISSING") {
		t.Errorf("session-state API body did not surface WORKSPACE_SNAPSHOT_MISSING; got %s", got.Body.String())
	}
}
