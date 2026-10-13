// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"fmt"
	"net"
	"sync"
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

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/admission/ownership"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// Namespace is the agent namespace the harness cluster serves.
const Namespace = "lenny-agents"

// RuntimeRef is the runtime every harness session runs.
const RuntimeRef = "echo"

// Pool is the exclusive warm pool every harness pod belongs to.
const Pool = "echo-pool"

// Cluster is one envtest API server holding the harness warm pool, its
// template, and its Sandboxes, with an in-process adapter behind each pod.
type Cluster struct {
	Client client.Client

	mu       sync.Mutex
	pods     map[string]*Pod // by pod IP
	nextIP   int
	sandboxN int
}

// Pod is one harness Sandbox and the adapter serving it.
type Pod struct {
	Name    string
	IP      string
	Adapter *adapter.Server
	Runtime *RecordingRuntime
	lis     *bufconn.Listener
}

// NewCluster starts envtest and seeds the warm pool and template.
func NewCluster(t testing.TB) *Cluster {
	t.Helper()
	env := envtest.Start(t)
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: Namespace}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	for _, o := range []client.Object{
		&lennyv1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{Name: Pool, Namespace: Namespace},
			Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "echo-tmpl", MinWarm: 1, MaxWarm: 64},
		},
		&lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "echo-tmpl", Namespace: Namespace},
			Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: RuntimeRef, IsolationProfile: string(isolation.ProfileSandboxed)},
		},
	} {
		if err := c.Create(ctx, o); err != nil {
			t.Fatalf("create %T: %v", o, err)
		}
	}
	return &Cluster{Client: c, pods: map[string]*Pod{}, nextIP: 10}
}

// AddIdlePod creates one idle Sandbox and starts the adapter behind it.
func (c *Cluster) AddIdlePod(t testing.TB) *Pod {
	t.Helper()
	c.mu.Lock()
	c.sandboxN++
	c.nextIP++
	name := fmt.Sprintf("sbx-%d", c.sandboxN)
	ip := fmt.Sprintf("10.244.%d.%d", c.nextIP/250, c.nextIP%250)
	c.mu.Unlock()

	rt := &RecordingRuntime{}
	srv := adapter.New("harness-" + name)
	srv.WorkspaceBase = t.TempDir()
	srv.CredentialsDir = t.TempDir()
	srv.Runtime = rt
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	pod := &Pod{Name: name, IP: ip, Adapter: srv, Runtime: rt, lis: lis}

	ctx := context.Background()
	sb := &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: Namespace, Labels: map[string]string{warmpool.LabelPool: Pool},
	}}
	if err := c.Client.Create(ctx, sb); err != nil {
		t.Fatalf("create sandbox %s: %v", name, err)
	}
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName(name)
	u.SetNamespace(Namespace)
	_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": "idle", "podIP": ip}, "status")
	if err := c.Client.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed sandbox status %s: %v", name, err)
	}
	c.mu.Lock()
	c.pods[ip] = pod
	c.mu.Unlock()
	return pod
}

// Dial is the binder's DialAdapter: it connects to the in-process adapter
// serving the pod at addr's host.
func (c *Cluster) Dial(addr string) (*adapterclient.Client, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	pod, ok := c.pods[host]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("lifecycleharness: no pod at %s", host)
	}
	return adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return pod.lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
}

// PodByName returns the pod with the Sandbox name.
func (c *Cluster) PodByName(name string) (*Pod, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.pods {
		if p.Name == name {
			return p, true
		}
	}
	return nil, false
}

// BoundClaims returns the name of the pod each bound SandboxClaim names.
func (c *Cluster) BoundClaims(ctx context.Context) ([]string, error) {
	var claims lennyv1.SandboxClaimList
	if err := c.Client.List(ctx, &claims, client.InNamespace(Namespace)); err != nil {
		return nil, err
	}
	var out []string
	for _, cl := range claims.Items {
		if cl.Status.Phase == string(claimstate.Bound) {
			out = append(out, cl.Spec.SandboxRef)
		}
	}
	return out, nil
}

// RecordingRuntime is the runtime behind a harness pod. It records every
// message it receives.
type RecordingRuntime struct {
	mu       sync.Mutex
	started  []string
	envelope [][]byte
}

// Start records the session start.
func (r *RecordingRuntime) Start(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, sessionID)
	return nil
}

// WriteEnvelope records a delivered envelope.
func (r *RecordingRuntime) WriteEnvelope(_ string, env []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.envelope = append(r.envelope, append([]byte(nil), env...))
	return nil
}

// Interrupt does nothing.
func (r *RecordingRuntime) Interrupt(context.Context, string, bool) error { return nil }

// Close does nothing.
func (r *RecordingRuntime) Close(context.Context, string) error { return nil }

// Output returns a closed channel.
func (r *RecordingRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

// Break makes the pod's adapter refuse every workspace restore or
// materialization, so an attempt that claims it fails after its claim.
func (p *Pod) Break() { p.Adapter.WorkspaceBase = "" }

// Pods returns every pod the cluster holds.
func (c *Cluster) Pods() []*Pod {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*Pod, 0, len(c.pods))
	for _, p := range c.pods {
		out = append(out, p)
	}
	return out
}

// RemoveSandbox deletes the pod's Sandbox, so no claim can take it.
func (c *Cluster) RemoveSandbox(ctx context.Context, p *Pod) error {
	return client.IgnoreNotFound(c.Client.Delete(ctx, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: Namespace},
	}))
}
