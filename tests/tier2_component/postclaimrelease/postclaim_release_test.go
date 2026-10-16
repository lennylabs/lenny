// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component coverage for the release of a pod claim when a binder call
// fails after the claim.
//
// A binder call claims a warm pod by creating its per-pod SandboxClaim, runs
// the caller's post-claim hook, and then dials the pod's adapter for the
// version handshake. When the hook fails, or the handshake fails, the call
// returns an error and no session holds the pod, so the binder deletes the
// claim before it returns. The outcome is the SandboxClaim object in the
// kube-apiserver, so the cases run against a real one: each asserts the
// binder issued exactly one delete for the claim and that no claim is left
// for the orphan-claim GC to collect.
//
// spec: §7.3 (Retry and Resume), §4.6.1 (Warm Pool Controller (Pod
// Lifecycle)).
package postclaimrelease_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const (
	claimNS   = "lenny-agents"
	claimPool = "claude-worker"
	claimPod  = "sbx-1"
)

// claimFixture is one idle pod in a real kube-apiserver and a binder whose
// SandboxClaim deletes are counted.
type claimFixture struct {
	kube    client.WithWatch
	binder  *podsession.Binder
	deletes *atomic.Int32
}

// newClaimFixture starts the apiserver with the idle pod and wires the binder
// to dial through dial.
func newClaimFixture(t *testing.T, dial func(string) (*adapterclient.Client, error)) *claimFixture {
	t.Helper()
	kube := claimKubeClient(t)
	deletes := &atomic.Int32{}
	counted := interceptor.NewClient(kube, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); ok && obj.GetName() == podclaim.ClaimName(claimPod) {
				deletes.Add(1)
			}
			return c.Delete(ctx, obj, opts...)
		},
	})
	return &claimFixture{
		kube: kube,
		binder: &podsession.Binder{
			Client:           counted,
			Namespace:        claimNS,
			AdapterPort:      50051,
			AcceptedVersions: []string{adapter.ProtocolVersionV1},
			DialAdapter:      dial,
		},
		deletes: deletes,
	}
}

// claimCount lists the SandboxClaims in the namespace, which is every claim
// the binder could have left behind.
func (f *claimFixture) claimCount(t *testing.T) int {
	t.Helper()
	var claims lennyv1.SandboxClaimList
	if err := f.kube.List(context.Background(), &claims, client.InNamespace(claimNS)); err != nil {
		t.Fatalf("list SandboxClaims: %v", err)
	}
	return len(claims.Items)
}

// assertReleasedOnce fails unless the binder deleted the pod's claim exactly
// once and no claim remains.
func (f *claimFixture) assertReleasedOnce(t *testing.T) {
	t.Helper()
	if got := f.deletes.Load(); got != 1 {
		t.Errorf("SandboxClaim deletes = %d, want exactly 1", got)
	}
	if got := f.claimCount(t); got != 0 {
		t.Errorf("SandboxClaims left = %d, want 0 (a leaked claim holds the pod until the orphan-claim GC)", got)
	}
}

// unimplementedAdapter answers every RPC Unimplemented, so the version
// handshake fails after the claim.
type unimplementedAdapter struct {
	adapterv1.UnimplementedAdapterServer
}

// serveDialer serves gs over an in-memory connection and returns a dialer
// onto it.
func serveDialer(t *testing.T, gs *grpc.Server) func(string) (*adapterclient.Client, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return func(string) (*adapterclient.Client, error) {
		return adapterclient.Dial("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

func negotiateFailingDialer(t *testing.T) func(string) (*adapterclient.Client, error) {
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, &unimplementedAdapter{})
	return serveDialer(t, gs)
}

func workingDialer(t *testing.T) func(string) (*adapterclient.Client, error) {
	srv := adapter.New("tier2-postclaim")
	srv.WorkspaceBase = t.TempDir()
	return serveDialer(t, adapter.NewGRPCServer(srv))
}

var errHook = errors.New("post-claim hook failed")

func failingHook(context.Context) error { return errHook }

// binderCall is one exclusive binder entry point driven with a hook.
type binderCall struct {
	name string
	call func(context.Context, *podsession.Binder, func(context.Context) error) error
}

func binderCalls() []binderCall {
	return []binderCall{
		{name: "claim", call: func(ctx context.Context, b *podsession.Binder, hook func(context.Context) error) error {
			_, err := b.Claim(ctx, podsession.BindRequest{
				Pool: claimPool, SessionID: "sess-alice", TenantID: "acme", Runtime: "echo", OnClaimed: hook,
			})
			return err
		}},
		{name: "bind", call: func(ctx context.Context, b *podsession.Binder, hook func(context.Context) error) error {
			_, err := b.Bind(ctx, podsession.BindRequest{
				Pool: claimPool, SessionID: "sess-alice", TenantID: "acme", Runtime: "echo",
				Plan: &adapterv1.WorkspacePlan{}, OnClaimed: hook,
			})
			return err
		}},
		{name: "resume", call: func(ctx context.Context, b *podsession.Binder, hook func(context.Context) error) error {
			_, err := b.Resume(ctx, podsession.ResumeRequest{
				Pool: claimPool, SessionID: "sess-alice", TenantID: "acme", Runtime: "echo",
				CheckpointID: "ckpt-1", MaxConcurrentSessions: 1, OnClaimed: hook,
			})
			return err
		}},
	}
}

// spec: 7.3 (Retry and Resume), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
// diagnosis: a binder call that failed after its claim did not delete the
// SandboxClaim exactly once. A claim left in the apiserver holds a warm pod
// for the orphan-claim GC window per failed attempt, which a retrying restore
// multiplies until the pool is exhausted. A second delete means two paths
// release the same claim, and on a recycled pod the second can remove a
// claim a later session made.
func TestPostClaimFailureDeletesTheClaimOnce_spec_7_3(t *testing.T) {
	for _, c := range binderCalls() {
		t.Run("hook_error/"+c.name, func(t *testing.T) {
			f := newClaimFixture(t, workingDialer(t))
			err := c.call(context.Background(), f.binder, failingHook)
			if !errors.Is(err, errHook) {
				t.Fatalf("binder err = %v, want the hook error", err)
			}
			f.assertReleasedOnce(t)
		})
		t.Run("negotiate_failure/"+c.name, func(t *testing.T) {
			f := newClaimFixture(t, negotiateFailingDialer(t))
			if err := c.call(context.Background(), f.binder, nil); err == nil {
				t.Fatal("binder err = nil, want the handshake failure")
			}
			f.assertReleasedOnce(t)
		})
	}
}

// claimKubeClient starts an envtest kube-apiserver holding one idle Sandbox
// in the pool. The apiserver is private to the test process, so no state
// survives a killed run.
func claimKubeClient(t *testing.T) client.WithWatch {
	t.Helper()
	env := envtest.Start(t)
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme lenny: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	c, err := client.NewWithWatch(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.NewWithWatch: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: claimNS},
	}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{
		Name:      claimPod,
		Namespace: claimNS,
		Labels:    map[string]string{warmpool.LabelPool: claimPool},
	}}); err != nil {
		t.Fatalf("create Sandbox: %v", err)
	}
	// §4.6.3 field ownership: the WarmPoolController owns the phase and
	// the pod address, so the seed is applied under its field manager.
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName(claimPod)
	u.SetNamespace(claimNS)
	if err := unstructured.SetNestedField(u.Object,
		map[string]interface{}{"phase": "idle", "podIP": "10.244.2.5"}, "status"); err != nil {
		t.Fatalf("build Sandbox status seed: %v", err)
	}
	if err := c.Status().Patch(ctx, u, client.Apply,
		client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed Sandbox status: %v", err)
	}
	return c
}
