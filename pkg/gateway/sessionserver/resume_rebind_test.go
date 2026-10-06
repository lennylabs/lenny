// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// unbindingExecutor unbinds a session from the pod registry on Release, as
// PodExecutor.Release does, and records the sandbox each release removed and
// its disposition. A release of an unbound session records nothing.
type unbindingExecutor struct {
	registry *podsession.Registry

	mu       sync.Mutex
	released []string // "<sandbox>:<disposition>"
}

func (e *unbindingExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *unbindingExecutor) Close(ctx context.Context, sessionID string) error {
	return e.Release(ctx, sessionID, "")
}

func (e *unbindingExecutor) Release(_ context.Context, sessionID string, d executor.Disposition) error {
	b, ok := e.registry.Remove(sessionID)
	if !ok {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.released = append(e.released, b.SandboxName+":"+string(d))
	return nil
}

func (e *unbindingExecutor) releases() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.released...)
}

// rebindFixture is a pod-backed server for the resume rebind cases. Its
// cluster serves the echo runtime, with one idle Sandbox, sbx-1, unless
// noIdlePod is set. The server is replica rep-1 and records executor
// releases.
type rebindFixture struct {
	srv      *sessionserver.Server
	store    *memstore.Store
	registry *podsession.Registry
	cluster  client.Client
	exec     *unbindingExecutor
	leases   *componentLeaseStore
}

func newRebindFixture(t *testing.T, id string, noIdlePod bool) *rebindFixture {
	t.Helper()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	objs := []client.Object{
		podBindWarmPool("echo-pool", "echo-tmpl"),
		podBindTemplate("echo-tmpl", "echo", string(isolation.ProfileSandboxed)),
	}
	if !noIdlePod {
		objs = append(objs, podBindIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"))
	}
	f := &rebindFixture{
		store:    memstore.New(),
		registry: podsession.NewRegistry(),
		cluster:  podBindClient(t, objs...),
		leases:   newComponentLeaseStore(),
	}
	f.exec = &unbindingExecutor{registry: f.registry}
	f.srv = sessionserver.New(f.store, sessionserver.Options{
		IDFunc:                  func() string { return id },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               podBindBinder(f.cluster, podBindAdapterDialer(t, adapterSrv)),
		PodRegistry:             f.registry,
		AgentNamespace:          podTestNS,
		Executor:                f.exec,
		CoordinationLeaseStore:  f.leases,
		ReplicaID:               "rep-1",
	})
	return f
}

func (f *rebindFixture) holder(id string) string {
	f.leases.mu.Lock()
	defer f.leases.mu.Unlock()
	return f.leases.holders[clk("acme", id)]
}

func (f *rebindFixture) setHolder(id, replica string) {
	f.leases.mu.Lock()
	defer f.leases.mu.Unlock()
	f.leases.holders[clk("acme", id)] = replica
}

func (f *rebindFixture) row(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	row, err := f.store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row
}

func checkpointed() *sessionstore.WorkspaceSnapshot {
	return &sessionstore.WorkspaceSnapshot{Ref: "ckpt-1", Source: sessionstore.WorkspaceSnapshotCheckpoint}
}

// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination), §7.3
// (Retry and Resume), §29.6 (Restore and resume)
// diagnosis: a POST /resume releases any binding this replica still holds for
// the session, with the failed disposition, before it publishes the new one,
// and a resume that fails transiently leaves the coordination lease with this
// replica. A failure means a leftover binding, such as one whose fence
// relinquished after its publish, is overwritten and its pod or slot leaks, or
// a failed resume gives away the lease the coordinating replica must keep.
func TestResumeReleasesEarlierBindingBeforeBind_spec_10_1(t *testing.T) {
	t.Run("leftover binding is released before the new bind", func(t *testing.T) {
		f := newRebindFixture(t, "sess-rebind", false)
		seedAwaitingSession(t, f.store, sessionstore.Session{ID: "sess-rebind", WorkspaceSnapshot: checkpointed()})
		f.registry.Put(&podsession.BindResult{SessionID: "sess-rebind", TenantID: "acme", SandboxName: "sbx-old"})
		f.setHolder("sess-rebind", "rep-1")

		rr := postSessionStep(t, f.srv.Handler(), "/v1/sessions/sess-rebind/resume", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("resume: status %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if got := f.exec.releases(); len(got) != 1 || got[0] != "sbx-old:failed" {
			t.Errorf("releases = %v, want [sbx-old:failed] before the new bind", got)
		}
		if b, ok := f.registry.Get("sess-rebind"); !ok || b.SandboxName != "sbx-1" {
			t.Errorf("binding = %+v ok=%v, want the new sbx-1 binding published", b, ok)
		}
		if h := f.holder("sess-rebind"); h != "rep-1" {
			t.Errorf("lease holder = %q, want rep-1", h)
		}
		if st := f.row(t, "sess-rebind").State; st != session.StateRunning {
			t.Errorf("state = %q, want running", st)
		}
	})

	t.Run("transient bind failure keeps this replica's lease", func(t *testing.T) {
		f := newRebindFixture(t, "sess-keep", true)
		seedAwaitingSession(t, f.store, sessionstore.Session{ID: "sess-keep", WorkspaceSnapshot: checkpointed()})
		f.setHolder("sess-keep", "rep-1")

		rr := postSessionStep(t, f.srv.Handler(), "/v1/sessions/sess-keep/resume", nil)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("resume: status %d, want 503; body=%s", rr.Code, rr.Body.String())
		}
		if st := f.row(t, "sess-keep").State; st != session.StateAwaitingClientAction {
			t.Errorf("state = %q, want awaiting_client_action", st)
		}
		if h := f.holder("sess-keep"); h != "rep-1" {
			t.Errorf("lease holder = %q, want rep-1: the resume runs no lease release", h)
		}
		if got := f.exec.releases(); len(got) != 0 {
			t.Errorf("releases = %v, want none: nothing was bound", got)
		}
	})
}

// spec: §15.1 (REST API), §29.3 (Interactive message send), §29.6 (Restore
// and resume), §7.3 (Retry and Resume)
// diagnosis: a POST /resume whose bind meets another replica's live
// coordination lease answers the retryable 503 RESUME_FAILED with
// Retry-After and no replica named in the body, rolls the claimed pod back,
// publishes no binding, leaves the lease with its holder, and holds the row
// in awaiting_client_action, on the checkpoint branch and the snapshotless
// branch alike. A failure on the row state means the row is demoted to
// terminal `failed` under a retryable answer, so the client's retry is
// rejected against a terminal row.
func TestResumeAgainstForeignLeaseHolderHoldsRow_spec_15_1(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  sessionstore.Session
	}{
		{"checkpoint", sessionstore.Session{WorkspaceSnapshot: checkpointed()}},
		{"snapshotless", sessionstore.Session{WorkspacePlan: json.RawMessage(`{
			"schemaVersion": 1,
			"sources": [{"type":"inlineFile","path":"CLAUDE.md","content":"# resumed","mode":"0644"}]
		}`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "sess-foreign-" + tc.name
			f := newRebindFixture(t, id, false)
			row := tc.row
			row.ID = id
			seedAwaitingSession(t, f.store, row)
			f.setHolder(id, "rep-2")

			rr := postSessionStep(t, f.srv.Handler(), "/v1/sessions/"+id+"/resume", nil)
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("resume: status %d, want 503; body=%s", rr.Code, rr.Body.String())
			}
			if ra := rr.Header().Get("Retry-After"); ra != "5" {
				t.Errorf("Retry-After = %q, want 5", ra)
			}
			var env struct {
				Error struct {
					Code      string `json:"code"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if env.Error.Code != "RESUME_FAILED" || !env.Error.Retryable {
				t.Errorf("error = %+v, want retryable RESUME_FAILED", env.Error)
			}
			if strings.Contains(rr.Body.String(), "rep-2") {
				t.Errorf("body names the holding replica: %s", rr.Body.String())
			}
			if st := f.row(t, id).State; st != session.StateAwaitingClientAction {
				t.Errorf("state = %q, want awaiting_client_action", st)
			}
			if _, ok := f.registry.Get(id); ok {
				t.Error("a binding was published against a foreign lease")
			}
			if h := f.holder(id); h != "rep-2" {
				t.Errorf("lease holder = %q, want rep-2", h)
			}
			var claim lennyv1.SandboxClaim
			err := f.cluster.Get(context.Background(), client.ObjectKey{Namespace: podTestNS, Name: "claim-sbx-1"}, &claim)
			if err == nil && claim.Status.Phase == string(claimstate.Bound) {
				t.Errorf("claim-sbx-1 still bound, want the claimed pod rolled back")
			} else if err != nil && !apierrors.IsNotFound(err) {
				t.Fatalf("get claim: %v", err)
			}
		})
	}
}
