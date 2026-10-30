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
		DevTenantHeader:         true,
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

// resumeToPending posts /resume and asserts the store-only answer: 200 with
// the row in resume_pending.
func resumeToPending(t *testing.T, f *rebindFixture, id string) sessionstore.Session {
	t.Helper()
	rr := postSessionStep(t, f.srv.Handler(), "/v1/sessions/"+id+"/resume", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	row := f.row(t, id)
	if row.State != session.StateResumePending {
		t.Fatalf("state after POST /resume = %q, want resume_pending", row.State)
	}
	return row
}

// spec: §10.1.1 (Stateless Replicas and Per-Session Coordination), §7.3
// (Resume driver; Retry and Resume), §29.6 (Restore and resume)
// diagnosis: the resume driver releases any binding this replica still holds
// for the session, with the failed disposition, before it publishes the new
// one, and an attempt that fails before its claim writes nothing and leaves
// the coordination lease with this replica. A failure means a leftover
// binding, such as one whose fence relinquished after its publish, is
// overwritten and its pod or slot leaks, or a failed attempt gives away the
// lease the coordinating replica must keep.
func TestResumeReleasesEarlierBindingBeforeBind_spec_10_1(t *testing.T) {
	t.Parallel()
	t.Run("leftover binding is released before the new bind", func(t *testing.T) {
		t.Parallel()
		f := newRebindFixture(t, "sess-rebind", false)
		seedAwaitingSession(t, f.store, sessionstore.Session{ID: "sess-rebind", WorkspaceSnapshot: checkpointed()})
		f.registry.Put(&podsession.BindResult{SessionID: "sess-rebind", TenantID: "acme", SandboxName: "sbx-old"})
		f.setHolder("sess-rebind", "rep-1")

		row := resumeToPending(t, f, "sess-rebind")
		f.srv.DriveResumeAndWait(context.Background(), row)
		if got := f.exec.releases(); len(got) != 1 || got[0] != "sbx-old:failed" {
			t.Errorf("releases = %v, want [sbx-old:failed] before the new bind", got)
		}
		if b, ok := f.registry.Get("sess-rebind"); !ok || b.SandboxName != "sbx-1" {
			t.Errorf("binding = %+v ok=%v, want the new sbx-1 binding published", b, ok)
		}
		if h := f.holder("sess-rebind"); h != "rep-1" {
			t.Errorf("lease holder = %q, want rep-1", h)
		}
		got := f.row(t, "sess-rebind")
		if got.State != session.StateRunning {
			t.Errorf("state = %q, want running", got.State)
		}
		if got.CoordinationGeneration != row.CoordinationGeneration+1 {
			t.Errorf("coordination_generation = %d, want %d: the entry write advances it once and the commit does not",
				got.CoordinationGeneration, row.CoordinationGeneration+1)
		}
	})

	t.Run("pre-claim failure writes nothing and keeps this replica's lease", func(t *testing.T) {
		t.Parallel()
		f := newRebindFixture(t, "sess-keep", true)
		seedAwaitingSession(t, f.store, sessionstore.Session{ID: "sess-keep", WorkspaceSnapshot: checkpointed()})
		f.setHolder("sess-keep", "rep-1")

		row := resumeToPending(t, f, "sess-keep")
		f.srv.DriveResumeAndWait(context.Background(), row)
		got := f.row(t, "sess-keep")
		if got.State != session.StateResumePending || got.CoordinationGeneration != row.CoordinationGeneration ||
			got.RetryCount != row.RetryCount {
			t.Errorf("row = %q gen %d retry %d, want resume_pending gen %d retry %d unchanged",
				got.State, got.CoordinationGeneration, got.RetryCount, row.CoordinationGeneration, row.RetryCount)
		}
		if h := f.holder("sess-keep"); h != "rep-1" {
			t.Errorf("lease holder = %q, want rep-1: the attempt runs no lease release", h)
		}
		if got := f.exec.releases(); len(got) != 0 {
			t.Errorf("releases = %v, want none: nothing was bound", got)
		}
	})
}

// spec: §15.1 (REST API), §29.6 (Restore and resume), §7.3 (Resume driver;
// Retry and Resume)
// diagnosis: a POST /resume is a store write any replica serves: it answers
// 200 with the row in resume_pending even while another replica holds the
// session's coordination lease, and names no replica. The resume driver on a
// replica that does not hold the lease writes nothing, claims no pod, and
// publishes no binding, on the checkpoint branch and the snapshotless branch
// alike. A failure means the non-coordinating replica restored a session the
// lease holder owns, or the resume was refused on a replica that could have
// recorded it.
func TestResumeAgainstForeignLeaseHolderHoldsRow_spec_15_1(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			id := "sess-foreign-" + tc.name
			f := newRebindFixture(t, id, false)
			row := tc.row
			row.ID = id
			seedAwaitingSession(t, f.store, row)
			f.setHolder(id, "rep-2")

			rr := postSessionStep(t, f.srv.Handler(), "/v1/sessions/"+id+"/resume", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("resume: status %d, want 200; body=%s", rr.Code, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), "rep-2") {
				t.Errorf("body names the holding replica: %s", rr.Body.String())
			}
			pending := f.row(t, id)
			f.srv.DriveResumeAndWait(context.Background(), pending)
			got := f.row(t, id)
			if got.State != session.StateResumePending || got.CoordinationGeneration != pending.CoordinationGeneration {
				t.Errorf("row = %q gen %d, want resume_pending gen %d unchanged", got.State, got.CoordinationGeneration, pending.CoordinationGeneration)
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
				t.Errorf("claim-sbx-1 bound, want no pod claimed by a replica without the lease")
			} else if err != nil && !apierrors.IsNotFound(err) {
				t.Fatalf("get claim: %v", err)
			}
		})
	}
}
