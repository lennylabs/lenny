// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"errors"
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
