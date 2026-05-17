// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
)

const (
	testNS   = "lenny-agents"
	testPool = "claude-worker"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func sandboxIn(pool, name, phase string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: pool},
		},
		Status: lennyv1.SandboxStatus{Phase: phase},
	}
}

func claimerFor(t *testing.T, objs ...client.Object) (*podclaim.Claimer, client.Client) {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&lennyv1.Sandbox{}, &lennyv1.SandboxClaim{}).
		Build()
	return &podclaim.Claimer{Client: c, Namespace: testNS}, c
}

func TestClaimBindsAnIdlePod(t *testing.T) {
	claimer, c := claimerFor(t, sandboxIn(testPool, "sbx-1", "idle"))

	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "sbx-1" || claim.Spec.SessionID != "sess-1" || claim.Spec.TenantID != "acme" {
		t.Errorf("claim spec = %+v, want a binding of sess-1/acme to sbx-1", claim.Spec)
	}

	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-1"}, &sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sb.Status.Phase != "claimed" {
		t.Errorf("sandbox phase = %q, want claimed", sb.Status.Phase)
	}

	var stored lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: claim.Name}, &stored); err != nil {
		t.Errorf("the SandboxClaim was not persisted: %v", err)
	}
}

func TestClaimReturnsErrNoIdlePodWhenPoolEmpty(t *testing.T) {
	claimer, _ := claimerFor(t)

	_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod for an empty pool", err)
	}
}

func TestClaimSkipsNonIdleSandboxes(t *testing.T) {
	claimer, _ := claimerFor(
		t,
		sandboxIn(testPool, "sbx-warming", "warming"),
		sandboxIn(testPool, "sbx-claimed", "claimed"),
		sandboxIn(testPool, "sbx-draining", "draining"),
	)
	_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod when no pod is idle", err)
	}
}

func TestClaimPicksTheIdleSandboxAmongMixedPhases(t *testing.T) {
	claimer, _ := claimerFor(
		t,
		sandboxIn(testPool, "sbx-warming", "warming"),
		sandboxIn(testPool, "sbx-idle", "idle"),
		sandboxIn(testPool, "sbx-claimed", "claimed"),
	)
	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef != "sbx-idle" {
		t.Errorf("claimed %q, want the idle sandbox sbx-idle", claim.Spec.SandboxRef)
	}
}

func TestClaimScopesToTheRequestedPool(t *testing.T) {
	// The only idle pod belongs to a different pool.
	claimer, _ := claimerFor(t, sandboxIn("other-pool", "sbx-other", "idle"))

	_, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if !errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Errorf("error = %v, want ErrNoIdlePod; an idle pod in another pool must not be claimed", err)
	}
}

// TestClaimSkipsAPodLostToAConcurrentClaimer exercises the §4.6.1
// optimistic-locking retry: a status-update conflict on one pod (a
// competing gateway replica won it) makes Claim move to the next idle
// pod rather than fail.
func TestClaimSkipsAPodLostToAConcurrentClaimer(t *testing.T) {
	conflicted := false
	c := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(
			sandboxIn(testPool, "sbx-a", "idle"),
			sandboxIn(testPool, "sbx-b", "idle"),
		).
		WithStatusSubresource(&lennyv1.Sandbox{}, &lennyv1.SandboxClaim{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, cl client.Client, sr string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if obj.GetName() == "sbx-a" && !conflicted {
					conflicted = true
					return apierrors.NewConflict(
						schema.GroupResource{Group: "lenny.dev", Resource: "sandboxes"},
						"sbx-a", errors.New("claimed by a competing replica"),
					)
				}
				return cl.Status().Update(ctx, obj, opts...)
			},
		}).
		Build()
	claimer := &podclaim.Claimer{Client: c, Namespace: testNS}

	claim, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{Pool: testPool, SessionID: "s"})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.Spec.SandboxRef == "sbx-a" {
		t.Error("Claim bound sbx-a despite the conflicting status update")
	}
	if !conflicted {
		t.Error("the conflicting-update path was not exercised")
	}
}
