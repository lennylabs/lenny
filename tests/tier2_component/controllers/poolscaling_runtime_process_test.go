// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// runtimeProcessRows is a poolstore.Store double whose List returns fixed
// rows. Gateway admission refuses `restart` until the intra-pod MCP servers
// rebind per runtime generation, so a `restart` row reaches the controller
// only through a double.
type runtimeProcessRows struct {
	poolstore.Store
	rows []poolstore.Pool
}

func (r runtimeProcessRows) List(context.Context, poolstore.ListFilter) ([]poolstore.Pool, error) {
	return r.rows, nil
}

// spec: §5.2 (Runtime-process lifetime), §4.6.3 (CRD field ownership and write boundaries)
// diagnosis: the PoolScalingController does not carry
// sessionPolicy.recycle.runtimeProcess onto SandboxTemplate.spec through the
// real CRD schema: the SandboxTemplate CRD manifest omits runtimeProcess (so
// the API server prunes it on write), its enum admits a value outside keep
// and restart, the PoolStoreSource drops the field, or a row that sets only
// the lifetime yields no recycle block. The WarmPoolController then creates
// pods without the pool's lifetime.
func TestPoolScalingControllerWritesRuntimeProcessUnpruned(t *testing.T) {
	env := envtest.Start(t)
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	const ns = "lenny-agents"
	mustCreate(t, ctx, c, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})

	row := func(name string, lifetime runtimestore.RuntimeProcessLifetime) poolstore.Pool {
		return poolstore.Pool{
			Name: name, RuntimeRef: "rt", IsolationProfile: isolation.ProfileSandboxed,
			ExecutionMode: runtimestore.ExecutionModeSession, WarmCount: 1,
			SessionPolicy: &runtimestore.SessionPolicy{
				AcknowledgeProcessLevelIsolation: true,
				Recycle: &runtimestore.RecyclePolicy{
					Enabled: true, AcknowledgeBestEffortScrub: true,
					MaxSessionsPerPod: 5, RuntimeProcess: lifetime,
				},
			},
		}
	}
	r := &poolscaling.Reconciler{
		Client: c,
		Source: &poolscaling.PoolStoreSource{
			Store:     runtimeProcessRows{rows: []poolstore.Pool{row("keep-pool", runtimestore.RuntimeProcessKeep), row("restart-pool", runtimestore.RuntimeProcessRestart)}},
			Namespace: ns,
		},
	}
	if err := r.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	for name, want := range map[string]string{"keep-pool": "keep", "restart-pool": "restart"} {
		var tmpl lennyv1.SandboxTemplate
		if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &tmpl); err != nil {
			t.Fatalf("get template %q: %v", name, err)
		}
		sp := tmpl.Spec.SessionPolicy
		if sp == nil || sp.Recycle == nil || sp.Recycle.RuntimeProcess != want {
			t.Errorf("%s: SandboxTemplate.spec.sessionPolicy = %+v, want recycle.runtimeProcess %q", name, sp, want)
		}
	}

	// The CRD enum refuses a value outside keep and restart.
	bad := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "bad-lifetime"},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:    "rt",
			SessionPolicy: &lennyv1.SessionPolicy{Recycle: &lennyv1.RecyclePolicy{RuntimeProcess: "forever"}},
		},
	}
	if err := c.Create(ctx, bad); !apierrors.IsInvalid(err) {
		t.Errorf("create with runtimeProcess forever: %v, want an Invalid rejection from the CRD enum", err)
	}
}
