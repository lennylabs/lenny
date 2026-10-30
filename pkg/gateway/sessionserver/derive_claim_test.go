// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// errInsertRejected is the insert failure insertRejectingStore injects.
var errInsertRejected = errors.New("test: derived-row insert failure injected")

// insertRejectingStore fails both inserts derive and replay use, so the
// handler reaches its insert-failure branch after a successful claim.
type insertRejectingStore struct {
	*memstore.Store
}

func (s *insertRejectingStore) CreateReserved(context.Context, sessionstore.Session) error {
	return errInsertRejected
}

func (s *insertRejectingStore) CreateWithTranscript(context.Context, sessionstore.Session, string) error {
	return errInsertRejected
}

// newSessionRoute is one handler that creates a top-level session from a
// source session's snapshot: derive or one of the two replay modes.
type newSessionRoute struct {
	name     string
	sourceID string
	newID    string
	seed     func(t *testing.T, store sessionstore.Store)
	call     func(t *testing.T, h http.Handler) *httptest.ResponseRecorder
}

// newSessionRoutes returns derive and both replay modes, each seeding an
// `echo`-runtime source the exclusive `echo-pool` serves.
func newSessionRoutes() []newSessionRoute {
	echoSource := func(s *sessionstore.Session) {
		s.RuntimeRef = "echo"
		s.PoolRef = "echo-pool"
		s.IsolationProfile = isolation.ProfileSandboxed
	}
	replay := func(mode sessionserver.ReplayMode) func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
		return func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
			return replayReq(t, h, sessionserver.ReplayRequest{ReplayMode: mode, TargetRuntime: "echo"})
		}
	}
	return []newSessionRoute{
		{
			name: "derive", sourceID: "sess_source", newID: "sess_new",
			seed: func(t *testing.T, store sessionstore.Store) { newSourceSession(t, store, echoSource) },
			call: func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
				return deriveRequest(t, h, sessionserver.DeriveRequest{})
			},
		},
		{
			name: "replay workspace_derive", sourceID: "sess_src", newID: "sess_new",
			seed: func(t *testing.T, store sessionstore.Store) { seedTerminalSource(t, store, echoSource) },
			call: replay(sessionserver.ReplayWorkspaceDerive),
		},
		{
			name: "replay prompt_history", sourceID: "sess_src", newID: "sess_new",
			seed: func(t *testing.T, store sessionstore.Store) { seedTerminalSource(t, store, echoSource) },
			call: replay(sessionserver.ReplayPromptHistory),
		},
	}
}

// claimingServer builds a server over store with the route's source
// snapshot seeded and a pod binder over a cluster holding objs.
func claimingServer(t *testing.T, store sessionstore.Store, rt newSessionRoute, objs ...client.Object) (http.Handler, client.Client, chunkedSnapshot) {
	t.Helper()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	cluster := podBindClient(t, objs...)
	snap := snapshotFor(t, rt.sourceID)
	srv := sessionserver.New(store, snap.wire(sessionserver.Options{
		DevTenantHeader:         true,
		IDFunc:                  func() string { return rt.newID },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv)),
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
	}))
	return srv.Handler(), cluster, snap
}

// TestDeriveAndReplayClaimAWarmPodAtCreate_spec_7_1 pins that derive and
// both replay modes claim a warm pod before inserting the new row, so the
// row holds the binding a later /finalize prepares and /start launches on.
//
// spec: 7.1 (Normal Flow) derive rule 2 and Atomicity of session creation
// (steps 2–8), 15.1 (REST API) the `created` state row and Session Replay
// Semantics.
func TestDeriveAndReplayClaimAWarmPodAtCreate_spec_7_1(t *testing.T) {
	t.Parallel()
	for _, rt := range newSessionRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()
			store := memstore.New()
			rt.seed(t, store)
			h, cluster, _ := claimingServer(
				t, store, rt,
				podBindWarmPool("echo-pool", "echo-tmpl"),
				podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
				podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
			)
			rr := rt.call(t, h)
			if rr.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
			}
			row, err := store.Get(context.Background(), "acme", rt.newID)
			if err != nil {
				t.Fatalf("get new row: %v", err)
			}
			if row.State != session.StateCreated {
				t.Errorf("state = %q, want created", row.State)
			}
			if row.PodAssignment != "sbx-1" || row.PoolRef != "echo-pool" {
				t.Errorf("binding = (%q, %q), want (sbx-1, echo-pool): the row must carry the create-time claim",
					row.PodAssignment, row.PoolRef)
			}
			var claim lennyv1.SandboxClaim
			if err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim); err != nil {
				t.Fatalf("the create-time claim object is missing: %v", err)
			}
		})
	}
}

// TestDeriveAndReplayClaimFailureLeavesNoRowAndNoCopy_spec_7_1 pins the
// §7.1 atomicity rollback on derive and replay: a pool with no idle pod
// answers the create path's 503 SESSION_CREATION_FAILED with Retry-After,
// persists no row, and deletes the snapshot copy.
//
// spec: 7.1 (Normal Flow) Atomicity of session creation (steps 2–8) and
// derive rule 2, 15.1 (REST API) SESSION_CREATION_FAILED.
func TestDeriveAndReplayClaimFailureLeavesNoRowAndNoCopy_spec_7_1(t *testing.T) {
	t.Parallel()
	for _, rt := range newSessionRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()
			store := memstore.New()
			rt.seed(t, store)
			h, _, snap := claimingServer(
				t, store, rt,
				podBindWarmPool("echo-pool", "echo-tmpl"),
				podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
			)
			rr := rt.call(t, h)
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
			}
			code, _, details := decodeError(t, rr)
			if code != "SESSION_CREATION_FAILED" || details["reason"] != "no_idle_pods" {
				t.Errorf("error = %s %v, want SESSION_CREATION_FAILED reason no_idle_pods", code, details)
			}
			if rr.Header().Get("Retry-After") == "" {
				t.Error("Retry-After missing on the retryable claim failure")
			}
			if _, err := store.Get(context.Background(), "acme", rt.newID); err == nil {
				t.Error("a row was persisted although the claim failed")
			}
			if _, _, err := snap.blobs.Get(derivedChunk(rt.newID, rt.newID)); err == nil {
				t.Error("the snapshot copy survived the failed claim")
			}
		})
	}
}

// TestDeriveAndReplayInsertFailureReleasesTheClaim_spec_7_1 pins that an
// insert failure after a successful claim releases the claimed pod, so no
// pod is held by a response that carries no session_id.
//
// spec: 7.1 (Normal Flow) Atomicity of session creation (steps 2–8).
func TestDeriveAndReplayInsertFailureReleasesTheClaim_spec_7_1(t *testing.T) {
	t.Parallel()
	for _, rt := range newSessionRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()
			mem := memstore.New()
			rt.seed(t, mem)
			h, cluster, _ := claimingServer(
				t, &insertRejectingStore{Store: mem}, rt,
				podBindWarmPool("echo-pool", "echo-tmpl"),
				podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
				podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
			)
			rr := rt.call(t, h)
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%s", rr.Code, rr.Body.String())
			}
			var claim lennyv1.SandboxClaim
			err := cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim)
			if !apierrors.IsNotFound(err) {
				t.Errorf("claim-sbx-1 after the failed insert: err=%v phase=%q, want NotFound (released)", err, claim.Status.Phase)
			}
		})
	}
}
