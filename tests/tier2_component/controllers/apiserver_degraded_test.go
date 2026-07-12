// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/podregistry"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// unreachableConfig returns a *rest.Config that is a copy of base but
// points its Host at a loopback port that is bound and immediately
// released, so every request dials a closed port and fails with a
// connection error. This reproduces the observable effect of the §4.6
// etcd/API-server degraded mode on the controller and gateway: their
// Kubernetes API operations return an error rather than data. A closed
// loopback port yields connection-refused immediately, which the
// reconciler and the CRDPodRegistry treat identically to any other
// API-server storage error — they propagate it — so this faithfully
// exercises the degraded-mode reaction path without a flaky process
// stop/restart. The short client Timeout bounds the dial so the test
// fails fast if the port is somehow reused.
func unreachableConfig(t *testing.T, base *rest.Config) *rest.Config {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close loopback listener: %v", err)
	}
	cfg := rest.CopyConfig(base)
	cfg.Host = "https://" + addr
	cfg.Timeout = 3 * time.Second
	return cfg
}

// TestEtcdUnavailableDegradedMode exercises the §4.6 "etcd unavailability
// (degraded mode)" contract at the CRD layer against a real kube-apiserver.
// The spec states that when the API server cannot process CRD writes:
// existing sessions continue unaffected, new session creation is rejected
// with a retryable error when ClaimPod fails, pool replenishment freezes,
// and once the API server recovers the controller's reconciliation replays
// and the pool self-heals back to minWarm.
//
// The API-server outage is reproduced by pointing a second client at a
// closed loopback port (unreachableConfig); the real apiserver stays up so
// the pods created before the outage genuinely survive it, and reads made
// through the live client after recovery confirm both the frozen deficit
// and the self-heal. The "existing sessions continue unaffected" clause is
// architectural (running pods talk to the gateway over gRPC and persist to
// Postgres, neither of which depends on etcd); at the CRD layer the closest
// faithful assertion is that the Sandbox objects representing existing warm
// capacity persist across the outage, which this test checks. The full
// gateway↔pod live-serving leg of that clause is covered end to end only by
// the Kind chaos variant.
//
// diagnosis: a failure means the degraded-mode reaction is broken. Either
// the WarmPoolController swallows an API-server failure instead of returning
// an error (so a failed reconcile would not requeue and the pool would not
// self-heal on recovery), or CRDPodRegistry.ClaimPod maps an API-server
// transport failure to ErrPoolExhausted (which the gateway would surface as
// the wrong, pool-exhaustion envelope) instead of propagating the transport
// error to the retryable SESSION_CREATION_FAILED fallback, or the recovered
// reconcile does not restore minWarm.
//
// spec: §4.6 (etcd unavailability degraded mode: existing sessions unaffected,
// new session creation rejected with a retryable error, pool replenishment
// frozen, pool self-heals to minWarm on recovery), §4.6.1 (per-pod ClaimPod).
func TestEtcdUnavailableDegradedMode_spec_4_6(t *testing.T) {
	env := envtest.Start(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))

	live, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("live client.New: %v", err)
	}
	broken, err := client.New(unreachableConfig(t, env.RESTConfig()), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("broken client.New: %v", err)
	}
	ctx := context.Background()

	const ns = "lenny-agents"
	const poolName = "claude-worker-small"

	mustCreate(t, ctx, live, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	mustCreate(t, ctx, live, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: ns},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			IsolationProfile: "sandboxed",
		},
	})
	mustCreate(t, ctx, live, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: poolName, Namespace: ns},
		Spec: lennyv1.SandboxWarmPoolSpec{
			TemplateRef: poolName,
			MinWarm:     3,
			MaxWarm:     10,
		},
	})

	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: ns, Name: poolName}}
	liveReconciler := &warmpool.Reconciler{Client: live, Scheme: scheme}
	brokenReconciler := &warmpool.Reconciler{Client: broken, Scheme: scheme}

	countWarm := func() int {
		t.Helper()
		var l lennyv1.SandboxList
		if err := live.List(ctx, &l, client.InNamespace(ns),
			client.MatchingLabels{warmpool.LabelPool: poolName}); err != nil {
			t.Fatalf("list sandboxes: %v", err)
		}
		return len(l.Items)
	}

	// Baseline: with the API server reachable the pool warms to minWarm.
	// These three Sandboxes are the existing warm capacity that must
	// survive the outage below.
	if _, err := liveReconciler.Reconcile(ctx, req); err != nil {
		t.Fatalf("baseline reconcile: %v", err)
	}
	if got := countWarm(); got != 3 {
		t.Fatalf("baseline warm count = %d, want 3", got)
	}

	// A new replenishment need arises: raise minWarm from 3 to 5. This
	// write lands while the API server is still reachable, so the pool now
	// carries an unmet deficit of two pods that a reconcile must fill.
	var pool lennyv1.SandboxWarmPool
	if err := live.Get(ctx, req.NamespacedName, &pool); err != nil {
		t.Fatalf("get pool: %v", err)
	}
	pool.Spec.MinWarm = 5
	if err := live.Update(ctx, &pool); err != nil {
		t.Fatalf("raise minWarm: %v", err)
	}

	// --- API-server outage begins (broken client) ---

	// Pool replenishment is frozen: a reconcile against the unreachable API
	// server returns an error (so controller-runtime requeues it) rather
	// than silently reporting success.
	if _, err := brokenReconciler.Reconcile(ctx, req); err == nil {
		t.Fatal("reconcile against an unreachable API server returned nil; " +
			"replenishment must fail (and requeue) rather than report success")
	}

	// New session creation is rejected: ClaimPod against the unreachable API
	// server returns a non-nil error, and crucially not ErrPoolExhausted —
	// a transport failure must propagate to the gateway's retryable
	// SESSION_CREATION_FAILED fallback, distinct from the pool-exhaustion
	// envelope. spec: §4.6 ("gateway detects API server unavailability when
	// ClaimPod fails and returns a retryable error to the client").
	brokenRegistry, err := podregistry.New(broken, ns)
	if err != nil {
		t.Fatalf("broken registry: %v", err)
	}
	_, claimErr := brokenRegistry.ClaimPod(ctx, podregistry.ClaimOpts{
		PoolID:    poolName,
		TenantID:  "acme",
		SessionID: "sess-degraded-1",
	})
	if claimErr == nil {
		t.Fatal("ClaimPod against an unreachable API server returned nil error; " +
			"new session creation must fail so the gateway returns a retryable error")
	}
	if errors.Is(claimErr, podregistry.ErrPoolExhausted) {
		t.Errorf("ClaimPod transport failure classified as ErrPoolExhausted (%v); "+
			"an API-server unavailability must propagate as a transport error, not pool exhaustion", claimErr)
	}

	// Existing warm capacity is unaffected by the outage and the frozen
	// deficit did not fill: the three Sandboxes created before the outage
	// still exist and no new pods were created toward the raised target.
	if got := countWarm(); got != 3 {
		t.Fatalf("warm count during outage = %d, want 3 (existing pods survive, deficit stays frozen)", got)
	}

	// --- API server recovers (live client) ---

	// The reconciliation replays against the now-reachable API server and
	// the pool self-heals to the raised minWarm of five.
	if _, err := liveReconciler.Reconcile(ctx, req); err != nil {
		t.Fatalf("recovery reconcile: %v", err)
	}
	if got := countWarm(); got != 5 {
		t.Fatalf("warm count after recovery = %d, want 5 (pool self-heals to minWarm)", got)
	}
}
