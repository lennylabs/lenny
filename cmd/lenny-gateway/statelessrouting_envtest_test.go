// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// TestPodEndpointListerAgainstAPIServer_spec_5_2_500 exercises the
// production §5.2 line 500 stateless EndpointLister against a real
// kube-apiserver: it must surface a pool's running, ready pods (with
// their IPs) and exclude foreign-pool, not-ready, IP-less, and
// terminating pods. The pod Ready condition is the readiness signal the
// statelessslot probe drives and an EndpointSlice mirrors.
func TestPodEndpointListerAgainstAPIServer_spec_5_2_500(t *testing.T) {
	env := envtest.Start(t)
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	cl, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx := context.Background()
	const ns = "default"
	const pool = "acme-stateless"

	// Helper to create a pod and stamp its status (envtest has no kubelet,
	// so PodIP and the Ready condition are written explicitly).
	mk := func(name, poolLabel, ip string, phase corev1.PodPhase, ready bool, terminating bool) {
		t.Helper()
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Labels:    map[string]string{labelPool: poolLabel},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "busybox"}}},
		}
		if err := cl.Create(ctx, pod); err != nil {
			t.Fatalf("create pod %s: %v", name, err)
		}
		pod.Status.Phase = phase
		pod.Status.PodIP = ip
		cond := corev1.ConditionFalse
		if ready {
			cond = corev1.ConditionTrue
		}
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: cond}}
		if err := cl.Status().Update(ctx, pod); err != nil {
			t.Fatalf("status update %s: %v", name, err)
		}
		if terminating {
			// A foreground delete with a finalizer leaves the pod present
			// with a DeletionTimestamp set, which the lister must skip.
			pod.Finalizers = []string{"lenny.dev/test-hold"}
			if err := cl.Update(ctx, pod); err != nil {
				t.Fatalf("add finalizer %s: %v", name, err)
			}
			if err := cl.Delete(ctx, pod); err != nil {
				t.Fatalf("delete %s: %v", name, err)
			}
		}
	}

	mk("ready-1", pool, "10.1.0.1", corev1.PodRunning, true, false)
	mk("ready-2", pool, "10.1.0.2", corev1.PodRunning, true, false)
	mk("notready", pool, "10.1.0.3", corev1.PodRunning, false, false) // at slot capacity
	mk("noip", pool, "", corev1.PodPending, false, false)             // not scheduled yet
	mk("foreign", "other-pool", "10.1.0.9", corev1.PodRunning, true, false)
	mk("terminating", pool, "10.1.0.4", corev1.PodRunning, true, true)

	lister := podEndpointLister{client: cl, namespace: ns, pool: pool}
	eps, err := lister.ListEndpoints(ctx)
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}

	byIP := map[string]bool{}
	for _, e := range eps {
		byIP[e.PodIP] = e.Ready
	}
	if r, ok := byIP["10.1.0.1"]; !ok || !r {
		t.Errorf("ready-1 missing/not-ready: %v", byIP)
	}
	if r, ok := byIP["10.1.0.2"]; !ok || !r {
		t.Errorf("ready-2 missing/not-ready: %v", byIP)
	}
	if r, ok := byIP["10.1.0.3"]; !ok || r {
		t.Errorf("notready should be present but Ready=false: %v", byIP)
	}
	if _, ok := byIP["10.1.0.9"]; ok {
		t.Errorf("foreign-pool pod leaked into the lister: %v", byIP)
	}
	if _, ok := byIP[""]; ok {
		t.Errorf("IP-less pod leaked into the lister: %v", byIP)
	}
	if _, ok := byIP["10.1.0.4"]; ok {
		t.Errorf("terminating pod leaked into the lister: %v", byIP)
	}
}

// TestPodTenantLabelerAgainstAPIServer_spec_5_2_500 exercises the
// production §5.2 line 500 tenant-pin labeler: stamping
// lenny.dev/tenant-id on the pod at a given IP, and idempotency on a
// re-pin.
func TestPodTenantLabelerAgainstAPIServer_spec_5_2_500(t *testing.T) {
	env := envtest.Start(t)
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	cl, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx := context.Background()
	const ns = "default"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pin-me",
			Namespace: ns,
			Labels:    map[string]string{labelPool: "acme-stateless"},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "busybox"}}},
	}
	if err := cl.Create(ctx, pod); err != nil {
		t.Fatalf("create: %v", err)
	}
	pod.Status.PodIP = "10.2.0.1"
	pod.Status.Phase = corev1.PodRunning
	if err := cl.Status().Update(ctx, pod); err != nil {
		t.Fatalf("status: %v", err)
	}

	labeler := podTenantLabeler{client: cl, namespace: ns}
	if err := labeler.LabelTenant(ctx, "10.2.0.1", "acme"); err != nil {
		t.Fatalf("LabelTenant: %v", err)
	}
	var got corev1.Pod
	if err := cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: "pin-me"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Labels[podclaim.LabelTenant] != "acme" {
		t.Fatalf("pin label = %q, want acme", got.Labels[podclaim.LabelTenant])
	}

	// Re-pin to the same tenant is a no-op (idempotent).
	if err := labeler.LabelTenant(ctx, "10.2.0.1", "acme"); err != nil {
		t.Fatalf("re-pin: %v", err)
	}

	// No pod at an unknown IP returns an error rather than silently
	// succeeding.
	if err := labeler.LabelTenant(ctx, "10.9.9.9", "acme"); err == nil {
		t.Fatal("LabelTenant on unknown IP = nil, want error")
	}
}
