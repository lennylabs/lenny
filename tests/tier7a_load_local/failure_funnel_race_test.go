// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the session server's failure
// funnel.
//
// A failure report that moves an active session off its pod counts a failed
// slot toward the pod's whole-pod replacement trigger and then releases the
// binding with the failed disposition. Two things act on that funnel at once
// in production: the failure reports of sibling slots on one concurrent pod,
// which can arrive together when the pod's runtime hangs, and a same-replica
// POST /resume that releases the session's earlier binding while the report
// is still accounting it. These cases drive those actors together under the
// race detector and assert the invariants a race would break: one drain and
// one replacement per pod, a drain stamped before the slot it was counted for
// is released, one release per binding, and no goroutine that outlives the
// race.
//
// spec: §5.2 (Pool Configuration and Execution Modes), §7.3 (Retry and
// Resume).
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// funnelRaceNS is the agent namespace the funnel race fixture resolves in.
const funnelRaceNS = "lenny-agents"

// funnelEvents records drain stamps and slot releases in the order they land.
type funnelEvents struct {
	mu     sync.Mutex
	events []string
}

func (e *funnelEvents) add(ev string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
}

func (e *funnelEvents) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...)
}

func (e *funnelEvents) count(prefix string) int {
	n := 0
	for _, ev := range e.list() {
		if strings.HasPrefix(ev, prefix) {
			n++
		}
	}
	return n
}

// unbindExecutor unbinds the session from the registry on Release, as
// PodExecutor.Release does, and records a "release:<session>" event only for
// a binding it removed: that is the ReleaseSlot the pod executor would issue.
type unbindExecutor struct {
	registry *podsession.Registry
	events   *funnelEvents
}

func (u *unbindExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (u *unbindExecutor) Close(ctx context.Context, sessionID string) error {
	return u.Release(ctx, sessionID, "")
}

func (u *unbindExecutor) Release(_ context.Context, sessionID string, _ executor.Disposition) error {
	if _, ok := u.registry.Remove(sessionID); ok {
		u.events.add("release:" + sessionID)
	}
	return nil
}

// funnelRaceFixture is a session server whose cluster serves pool-c with
// maxConcurrentSessions n, whose binder records every drain stamp, and whose
// replacement counter is observed.
type funnelRaceFixture struct {
	srv      *sessionserver.Server
	store    *memstore.Store
	registry *podsession.Registry
	events   *funnelEvents

	mu           sync.Mutex
	replacements int
}

func newFunnelRaceFixture(t *testing.T, n int) *funnelRaceFixture {
	t.Helper()
	f := &funnelRaceFixture{
		store:    memstore.New(),
		registry: podsession.NewRegistry(),
		events:   &funnelEvents{},
	}
	scheme := k8sruntime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&lennyv1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{Name: "pool-c", Namespace: funnelRaceNS},
			Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "tmpl-c", MinWarm: 1, MaxWarm: 2},
		},
		&lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "tmpl-c", Namespace: funnelRaceNS},
			Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "echo"},
		},
	).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
			if pod, ok := obj.(*corev1.Pod); ok {
				f.events.add("drain:" + pod.Name)
			}
			return c.Patch(ctx, obj, p, opts...)
		},
	}).Build()
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name: "pool-c", RuntimeRef: "echo", ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{MaxConcurrentSessions: n, AcknowledgeProcessLevelIsolation: true},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	f.srv = sessionserver.New(f.store, sessionserver.Options{
		Executor:        &unbindExecutor{registry: f.registry, events: f.events},
		PodRegistry:     f.registry,
		PodBinder:       &podsession.Binder{Client: cl, Namespace: funnelRaceNS},
		Pools:           pools,
		AgentNamespace:  funnelRaceNS,
		RetryPolicyCaps: session.RetryPolicyCaps{MaxRetries: 2},
		SlotReplacement: func(string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.replacements++
		},
	})
	return f
}

// seedSlot inserts a running session bound to a slot of pod.
func (f *funnelRaceFixture) seedSlot(t *testing.T, id, pod string, retries int64) {
	t.Helper()
	if err := f.store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning, RetryCount: retries,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	f.registry.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", SandboxName: pod, SlotID: id})
}

func (f *funnelRaceFixture) replacementCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.replacements
}

// waitGoroutines waits for the goroutine count to fall back to baseline plus
// slack, failing when it does not.
func waitGoroutines(t *testing.T, baseline, slack int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > baseline+slack {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, want at most %d: a funnel or resume goroutine outlived the race",
				runtime.NumGoroutine(), baseline+slack)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// spec: §5.2 (Pool Configuration and Execution Modes), §7.3 (Retry and Resume)
// diagnosis: every slot of an n-slot pod fails at once, as when the pod's
// runtime hangs and each slot's stream ends. The pod's whole-pod replacement
// trigger fires once: one drain stamp and one replacement count, and each slot
// is released once. A failure means a pod is drained and counted for
// replacement more than once, or a concurrent report loses a release.
func TestSlotTripOncePerPodUnderConcurrentFailures_spec_5_2(t *testing.T) {
	const n = 8
	for round := 0; round < 20; round++ {
		f := newFunnelRaceFixture(t, n)
		pod := fmt.Sprintf("pod-%d", round)
		ids := make([]string, n)
		for i := range ids {
			ids[i] = fmt.Sprintf("sess-%d-%d", round, i)
			f.seedSlot(t, ids[i], pod, 0)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				<-start
				if _, err := f.srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
					TenantID: "acme", SessionID: id, Reason: "runtime_crash",
				}); err != nil {
					t.Errorf("ReportSessionFailure %s: %v", id, err)
				}
			}(id)
		}
		close(start)
		wg.Wait()

		if got := f.events.count("drain:" + pod); got != 1 {
			t.Fatalf("round %d: drains of %s = %d, want 1; events=%v", round, pod, got, f.events.list())
		}
		if got := f.replacementCount(); got != 1 {
			t.Fatalf("round %d: replacements = %d, want 1", round, got)
		}
		if got := f.events.count("release:"); got != n {
			t.Fatalf("round %d: releases = %d, want %d", round, got, n)
		}
	}
}

// spec: §7.3 (Retry and Resume), §5.2 (Pool Configuration and Execution
// Modes), §10.1.1 (Stateless Replicas and Per-Session Coordination)
// diagnosis: a failure report that commits awaiting_client_action races a
// POST /resume on the same server, which releases the session's earlier
// binding. The slot is released once, a drain the report's accounting
// requests is stamped before that release, and no goroutine outlives the
// race. A failure means the resume's release reaches the slot before the
// drain is stamped, so a recycling pool can hand the pod on, or the two paths
// release the slot twice or deadlock.
func TestFailureFunnelRacesSameReplicaResume_spec_7_3(t *testing.T) {
	baseline := runtime.NumGoroutine()
	for round := 0; round < 50; round++ {
		f := newFunnelRaceFixture(t, 4)
		id := fmt.Sprintf("sess-race-%d", round)
		pod := fmt.Sprintf("pod-race-%d", round)
		// A sibling slot's earlier failure puts the pod one short of its
		// threshold of 2, so this report's accounting requests the drain.
		f.seedSlot(t, "sibling-"+id, pod, 2)
		if _, err := f.srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
			TenantID: "acme", SessionID: "sibling-" + id, Reason: "runtime_crash",
		}); err != nil {
			t.Fatalf("sibling report: %v", err)
		}
		f.seedSlot(t, id, pod, 2)
		h := f.srv.Handler()

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			if _, err := f.srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
				TenantID: "acme", SessionID: id, Reason: "runtime_crash",
			}); err != nil {
				t.Errorf("ReportSessionFailure: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			// Resume until the report's commit makes the row resumable; a
			// resume admitted earlier answers 409 and changes nothing.
			for i := 0; i < 1000; i++ {
				req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/resume", nil)
				req.Header.Set("X-Lenny-Tenant-ID", "acme")
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, req)
				if rr.Code != http.StatusConflict {
					return
				}
				runtime.Gosched()
			}
		}()
		close(start)
		wg.Wait()

		events := f.events.list()
		drain, release := -1, -1
		for i, ev := range events {
			if ev == "drain:"+pod && drain < 0 {
				drain = i
			}
			if ev == "release:"+id && release < 0 {
				release = i
			}
		}
		if got := f.events.count("release:" + id); got != 1 {
			t.Fatalf("round %d: releases of %s = %d, want 1; events=%v", round, id, got, events)
		}
		if drain < 0 || drain > release {
			t.Fatalf("round %d: events = %v, want drain:%s before release:%s", round, events, pod, id)
		}
	}
	waitGoroutines(t, baseline, 5)
}
