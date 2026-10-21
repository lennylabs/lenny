// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// spec: §4.6.1 (Orphaned SandboxClaim detection), §7.3 (Resume driver)
//
// A crashed replica's binder issues no Kubernetes write, so a claim release
// it detached before the crash cannot delete the claim the claim-orphan GC
// is meant to reclaim. Reads keep working, and writes before the crash pass.
func TestCrashableClientRefusesWritesAfterTheCrash_spec_4_6_1(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	claim := func(name string) *lennyv1.SandboxClaim {
		return &lennyv1.SandboxClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace}}
	}
	inner := ctrlfake.NewClientBuilder().WithScheme(s).
		WithObjects(claim("kept"), claim("deleted")).
		WithStatusSubresource(&lennyv1.SandboxClaim{}).Build()
	var crashed atomic.Bool
	c := crashableClient{Client: inner, crashed: &crashed}

	if err := c.Delete(ctx, claim("deleted")); err != nil {
		t.Fatalf("delete before the crash: %v", err)
	}
	crashed.Store(true)

	key := client.ObjectKey{Namespace: Namespace, Name: "kept"}
	writes := map[string]func() error{
		"Create":        func() error { return c.Create(ctx, claim("new")) },
		"Update":        func() error { return c.Update(ctx, claim("kept")) },
		"Patch":         func() error { return c.Patch(ctx, claim("kept"), client.MergeFrom(claim("kept"))) },
		"Delete":        func() error { return c.Delete(ctx, claim("kept")) },
		"DeleteAllOf":   func() error { return c.DeleteAllOf(ctx, &lennyv1.SandboxClaim{}, client.InNamespace(Namespace)) },
		"Status.Update": func() error { return c.Status().Update(ctx, claim("kept")) },
		"Status.Patch":  func() error { return c.Status().Patch(ctx, claim("kept"), client.MergeFrom(claim("kept"))) },
		"Status.Create": func() error { return c.Status().Create(ctx, claim("kept"), claim("sub")) },
		"SubResource.Patch": func() error {
			return c.SubResource("status").Patch(ctx, claim("kept"), client.MergeFrom(claim("kept")))
		},
		"SubResource.Update": func() error { return c.SubResource("status").Update(ctx, claim("kept")) },
		"SubResource.Create": func() error {
			return c.SubResource("status").Create(ctx, claim("kept"), claim("sub"))
		},
	}
	for name, write := range writes {
		if err := write(); !errors.Is(err, errReplicaCrashed) {
			t.Errorf("%s after the crash = %v, want the crash error", name, err)
		}
	}
	var got lennyv1.SandboxClaim
	if err := c.Get(ctx, key, &got); err != nil {
		t.Errorf("read after the crash: %v; the claim the crashed replica tried to delete is expected to remain", err)
	}
}

// spec: §4.6.1 (Warm Pool Controller (Pod Lifecycle)), §7.3 (Resume driver)
//
// The claim log records each SandboxClaim create the kube-apiserver
// accepted, by the Sandbox it names, and keeps the record after the claim
// is deleted. A refused create and a create of another kind record nothing,
// and a client with no log records nothing without failing.
func TestCrashableClientRecordsAcceptedClaimCreates_spec_4_6_1(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	claim := func(name, pod string) *lennyv1.SandboxClaim {
		return &lennyv1.SandboxClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace},
			Spec:       lennyv1.SandboxClaimSpec{SandboxRef: pod},
		}
	}
	inner := ctrlfake.NewClientBuilder().WithScheme(s).Build()
	log := &ClaimLog{}
	var crashed atomic.Bool
	c := crashableClient{Client: inner, crashed: &crashed, claims: log}

	if err := c.Create(ctx, claim("claim-a", "pod-a")); err != nil {
		t.Fatalf("create claim-a: %v", err)
	}
	if err := c.Create(ctx, claim("claim-a", "pod-a")); err == nil {
		t.Fatal("a second create of claim-a succeeded, want the conflict")
	}
	if err := c.Delete(ctx, claim("claim-a", "pod-a")); err != nil {
		t.Fatalf("delete claim-a: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "pod-b", Namespace: Namespace}}); err != nil {
		t.Fatalf("create a sandbox: %v", err)
	}
	if err := c.Create(ctx, claim("claim-c", "pod-c")); err != nil {
		t.Fatalf("create claim-c: %v", err)
	}
	crashed.Store(true)
	_ = c.Create(ctx, claim("claim-d", "pod-d"))

	if got := log.Len(); got != 2 {
		t.Fatalf("log length = %d, want 2", got)
	}
	if got := log.Since(0); len(got) != 2 || got[0] != "pod-a" || got[1] != "pod-c" {
		t.Errorf("log = %v, want [pod-a pod-c]", got)
	}
	if got := log.Since(1); len(got) != 1 || got[0] != "pod-c" {
		t.Errorf("log since 1 = %v, want [pod-c]", got)
	}
	if got := log.Since(5); got != nil {
		t.Errorf("log since 5 = %v, want none", got)
	}

	crashed.Store(false)
	bare := crashableClient{Client: inner, crashed: &crashed}
	if err := bare.Create(ctx, claim("claim-e", "pod-e")); err != nil {
		t.Errorf("create with no log: %v", err)
	}
}

// spec: §4.6.1 (Orphaned SandboxClaim detection), §5.2 (Slot assignment
// atomicity)
//
// The departure scenarios arm a ClaimFault on one replica's binder to fail
// or hold a SandboxClaim write. The fault sees every claim create, status
// write, and delete with the claim's name, a refused write never reaches the
// kube-apiserver, a write of another kind bypasses it, and disarming it lets
// the writes through again.
func TestClaimFaultInterceptsClaimWrites_spec_4_6_1(t *testing.T) {
	ctx := context.Background()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	claim := func(name string) *lennyv1.SandboxClaim {
		return &lennyv1.SandboxClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace}}
	}
	inner := ctrlfake.NewClientBuilder().WithScheme(s).WithObjects(claim("kept")).
		WithStatusSubresource(&lennyv1.SandboxClaim{}).Build()
	var crashed atomic.Bool
	var fault atomic.Pointer[ClaimFault]
	c := crashableClient{Client: inner, crashed: &crashed, claims: &ClaimLog{}, fault: &fault}
	var seen []string
	refuse := errors.New("injected")
	f := ClaimFault(func(_ context.Context, op ClaimOp, name string) error {
		seen = append(seen, fmt.Sprintf("%d:%s", op, name))
		return refuse
	})
	fault.Store(&f)

	if err := c.Create(ctx, claim("new")); !errors.Is(err, refuse) {
		t.Errorf("create = %v, want the injected error", err)
	}
	if err := c.Status().Update(ctx, claim("kept")); !errors.Is(err, refuse) {
		t.Errorf("status update = %v, want the injected error", err)
	}
	if err := c.SubResource("status").Patch(ctx, claim("kept"), client.MergeFrom(claim("kept"))); !errors.Is(err, refuse) {
		t.Errorf("status patch = %v, want the injected error", err)
	}
	if err := c.Delete(ctx, claim("kept")); !errors.Is(err, refuse) {
		t.Errorf("delete = %v, want the injected error", err)
	}
	if err := c.Create(ctx, &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: Namespace}}); err != nil {
		t.Errorf("a sandbox create went through the claim fault: %v", err)
	}
	want := []string{
		fmt.Sprintf("%d:new", ClaimCreate), fmt.Sprintf("%d:kept", ClaimStatus),
		fmt.Sprintf("%d:kept", ClaimStatus), fmt.Sprintf("%d:kept", ClaimDelete),
	}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("fault saw %v, want %v", seen, want)
	}
	if err := c.Get(ctx, client.ObjectKey{Namespace: Namespace, Name: "kept"}, &lennyv1.SandboxClaim{}); err != nil {
		t.Errorf("the refused delete reached the kube-apiserver: %v", err)
	}
	fault.Store(nil)
	if err := c.Delete(ctx, claim("kept")); err != nil {
		t.Errorf("delete after disarming: %v", err)
	}
}
