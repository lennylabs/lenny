// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// spec: §4.7, §5.3
// diagnosis: the PoolScalingController (the §4.6.3 Postgres→CRD channel)
// failed to carry the §5.3 acknowledgeNonceOnlyAuth opt-in onto
// PodSet.spec through the real CRD schema. Either the
// PodSet CRD manifest in charts/lenny/crds omits the
// acknowledgeNonceOnlyAuth field (so the API server drops it on write),
// the PoolStoreSource does not copy the store-row flag into the rendered
// spec, or the controller's full-spec overwrite clears it. Without the
// mirror the WarmPoolController render gate cannot require the
// acknowledgment, so a directly-applied Runtime CR could put an
// unacknowledged pool into §4.7 nonce-only mode.
//
// TestPoolScalingControllerMirrorsNonceOnlyAck drives the
// PoolScalingController Sync from a PoolStoreSource against an envtest API
// server with the lenny.dev CRDs installed. Unlike the fake-client unit
// test in pkg/controller/poolscaling, this asserts the field survives a
// round-trip through the real PodSet CRD schema and the status
// subresource configuration.
func TestPoolScalingControllerMirrorsNonceOnlyAck(t *testing.T) {
	env := sharedEnv.Start(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))

	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()

	ns := testNamespaces.Create(t, c)

	// Seed the gateway pool store with one acknowledged and one
	// unacknowledged nonce-only-eligible pool. The PoolStoreSource adapts
	// the store to the PoolScalingController's PoolConfigSource.
	store := poolstore.NewMemory()
	seed := func(p poolstore.Pool) {
		t.Helper()
		if err := store.Create(ctx, p); err != nil {
			t.Fatalf("seed pool %q: %v", p.Name, err)
		}
	}
	seed(poolstore.Pool{
		Name:                     "nonce-ack",
		RuntimeRef:               "gvisor-runtime",
		IsolationProfile:         isolation.ProfileSandboxed,
		ExecutionMode:            runtimestore.ExecutionModeSession,
		WarmCount:                1,
		AcknowledgeNonceOnlyAuth: true,
	})
	seed(poolstore.Pool{
		Name:             "no-ack",
		RuntimeRef:       "gvisor-runtime",
		IsolationProfile: isolation.ProfileSandboxed,
		ExecutionMode:    runtimestore.ExecutionModeSession,
		WarmCount:        1,
	})

	r := &poolscaling.Reconciler{
		Client: c,
		Source: &poolscaling.PoolStoreSource{Store: store, Namespace: ns},
	}
	if err := r.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	podSetAck := func(name string) bool {
		t.Helper()
		var ps lennyv1.PodSet
		if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &ps); err != nil {
			t.Fatalf("get PodSet %q: %v", name, err)
		}
		return ps.Spec.AcknowledgeNonceOnlyAuth
	}

	if !podSetAck("nonce-ack") {
		t.Error("nonce-ack PodSet.spec.acknowledgeNonceOnlyAuth = false after Sync, want true")
	}
	if podSetAck("no-ack") {
		t.Error("no-ack PodSet.spec.acknowledgeNonceOnlyAuth = true after Sync, want false")
	}
}
