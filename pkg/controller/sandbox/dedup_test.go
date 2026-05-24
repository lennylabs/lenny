// SPDX-License-Identifier: MIT

package sandbox_test

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox"
	"github.com/lennylabs/lenny/pkg/controller/statusdedup"
)

// applySandboxStatus seeds a Sandbox status via SSA Apply under the same
// field manager the reconciler uses, so the reconciler's subsequent Apply
// is a same-manager update rather than a cross-manager conflict.
func applySandboxStatus(t *testing.T, c client.Client, name string, status lennyv1.SandboxStatus) {
	t.Helper()
	patch := &lennyv1.Sandbox{
		TypeMeta:   metav1.TypeMeta{APIVersion: lennyv1.GroupVersion.String(), Kind: "Sandbox"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
	}
	patch.Status = status
	if err := c.Status().Patch(context.Background(), patch, client.Apply,
		client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed status: %v", err)
	}
}

func reconcileResult(t *testing.T, r *sandbox.Reconciler) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testName},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return res
}

// spec: §4.6.1 statusUpdateDeduplicationWindow — a Sandbox status write
// within the window of the previous write for the same Sandbox is
// deferred and the reconcile requeued, while the live status is left
// unchanged until the window expires.
func TestReconcileDefersStatusWriteWithinDedupWindow(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, runtimeCR(), sandboxCR("warming"), podCR("Running", true))

	r := &sandbox.Reconciler{
		Client:       c,
		Scheme:       s,
		AdapterImage: "ghcr.io/lennylabs/lenny-adapter:v1",
		StatusDedup:  statusdedup.New(time.Hour),
	}

	// First reconcile: warming + ready pod → idle, status written. The
	// dedup gate records the write time.
	if res := reconcileResult(t, r); res.RequeueAfter != 0 {
		t.Fatalf("first reconcile RequeueAfter = %v, want 0 (write permitted)", res.RequeueAfter)
	}
	if sb := getSandbox(t, c); sb.Status.Phase != "idle" {
		t.Fatalf("after first reconcile phase = %q, want idle", sb.Status.Phase)
	}

	// Simulate drift: flip the live status back to warming under the same
	// field manager, so the next reconcile again wants to write idle.
	applySandboxStatus(t, c, testName, lennyv1.SandboxStatus{Phase: "warming"})

	// Second reconcile within the 1h window: the pending idle write is
	// deferred and the reconcile requeues; the live status stays warming.
	res := reconcileResult(t, r)
	if res.RequeueAfter <= 0 {
		t.Errorf("second reconcile RequeueAfter = %v, want > 0 (write deferred)", res.RequeueAfter)
	}
	if sb := getSandbox(t, c); sb.Status.Phase != "warming" {
		t.Errorf("deferred reconcile wrote phase = %q, want warming unchanged", sb.Status.Phase)
	}
}

// Without a dedup gate the second reconcile writes immediately (control
// for the test above).
func TestReconcileWithoutDedupWritesImmediately(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s, runtimeCR(), sandboxCR("warming"), podCR("Running", true))

	r := &sandbox.Reconciler{Client: c, Scheme: s, AdapterImage: "ghcr.io/lennylabs/lenny-adapter:v1"}
	reconcileResult(t, r)
	applySandboxStatus(t, c, testName, lennyv1.SandboxStatus{Phase: "warming"})
	if res := reconcileResult(t, r); res.RequeueAfter != 0 {
		t.Errorf("no-gate reconcile RequeueAfter = %v, want 0", res.RequeueAfter)
	}
	if sb := getSandbox(t, c); sb.Status.Phase != "idle" {
		t.Errorf("no-gate reconcile phase = %q, want idle", sb.Status.Phase)
	}
}
