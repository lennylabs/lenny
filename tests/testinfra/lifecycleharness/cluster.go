// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
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

	heartbeat time.Duration

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

	mu         sync.Mutex
	failAttach chan struct{}
	attaches   atomic.Int64
}

// Attaches reports how many Attach streams the pod's adapter has opened.
func (p *Pod) Attaches() int64 { return p.attaches.Load() }

// FailAttach ends every Attach stream open on the pod's adapter with
// codes.Internal, as a broken CH-ATTACH stream ends. Streams opened later
// are not affected.
func (p *Pod) FailAttach() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failAttach != nil {
		close(p.failAttach)
	}
	p.failAttach = make(chan struct{})
}

// attachFailure returns the channel that FailAttach closes for the streams
// open now.
func (p *Pod) attachFailure() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failAttach == nil {
		p.failAttach = make(chan struct{})
	}
	return p.failAttach
}

// failableAttach is the adapter's stream interceptor that ends an Attach
// stream with codes.Internal when FailAttach is called. It cancels the
// stream's context and waits for the handler before it returns.
func (p *Pod) failableAttach(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
	if !strings.HasSuffix(info.FullMethod, "/Attach") {
		return h(srv, ss)
	}
	p.attaches.Add(1)
	fail := p.attachFailure()
	ctx, cancel := context.WithCancel(ss.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h(srv, &ctxStream{ServerStream: ss, ctx: ctx}) }()
	select {
	case err := <-done:
		return err
	case <-fail:
		cancel()
		<-done
		return status.Error(codes.Internal, "lifecycleharness: injected CH-ATTACH failure")
	}
}

// ctxStream is a server stream with a replaced context.
type ctxStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *ctxStream) Context() context.Context { return s.ctx }

// ClusterOptions configures the harness cluster.
type ClusterOptions struct {
	// Recycling, when true, makes the warm pool a recycling pool: its
	// template carries a sessionPolicy.recycle block, so a clean session
	// end recycles the pod rather than retiring it.
	Recycling bool
	// HeartbeatInterval, when set, is every pod adapter's heartbeat
	// interval.
	HeartbeatInterval time.Duration
}

// NewCluster starts envtest and seeds the warm pool and template.
func NewCluster(t testing.TB) *Cluster {
	return NewClusterWith(t, ClusterOptions{})
}

// NewClusterWith starts envtest and seeds the warm pool and a template
// configured by opts.
func NewClusterWith(t testing.TB, opts ClusterOptions) *Cluster {
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
		template(opts),
	} {
		if err := c.Create(ctx, o); err != nil {
			t.Fatalf("create %T: %v", o, err)
		}
	}
	return &Cluster{Client: c, pods: map[string]*Pod{}, nextIP: 10, heartbeat: opts.HeartbeatInterval}
}

// template is the warm pool's SandboxTemplate.
func template(opts ClusterOptions) *lennyv1.SandboxTemplate {
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-tmpl", Namespace: Namespace},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: RuntimeRef, IsolationProfile: string(isolation.ProfileSandboxed)},
	}
	if opts.Recycling {
		tmpl.Spec.SessionPolicy = &lennyv1.SessionPolicy{Recycle: &lennyv1.RecyclePolicy{ScrubProfile: "standard"}}
	}
	return tmpl
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

	rt := NewRecordingRuntime()
	srv := adapter.New("harness-" + name)
	srv.WorkspaceBase = t.TempDir()
	srv.CredentialsDir = t.TempDir()
	srv.Runtime = rt
	if c.heartbeat > 0 {
		srv.HeartbeatInterval = c.heartbeat
	}
	lis := bufconn.Listen(1 << 20)
	pod := &Pod{Name: name, IP: ip, Adapter: srv, Runtime: rt, lis: lis}
	gs := adapter.NewGRPCServer(srv, grpc.ChainStreamInterceptor(pod.failableAttach))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

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

// Received returns the messages every pod's runtime received for the
// session, in arrival order.
func (c *Cluster) Received(sessionID string) []Received {
	var out []Received
	for _, p := range c.Pods() {
		out = append(out, p.Runtime.Received(sessionID)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// ReceivedTexts returns the text of each message the session's runtimes
// received, in arrival order.
func (c *Cluster) ReceivedTexts(sessionID string) []string {
	var out []string
	for _, m := range c.Received(sessionID) {
		out = append(out, m.Text)
	}
	return out
}

// PodOf returns the pod whose Sandbox name is the session row's pod.
func (c *Cluster) PodOf(podName string) *Pod {
	p, _ := c.PodByName(podName)
	return p
}

// ClaimPhase returns the phase of the pod's SandboxClaim, and false when
// the pod has no claim.
func (c *Cluster) ClaimPhase(ctx context.Context, podName string) (string, bool, error) {
	var claim lennyv1.SandboxClaim
	err := c.Client.Get(ctx, client.ObjectKey{Namespace: Namespace, Name: "claim-" + podName}, &claim)
	if apierrors.IsNotFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return claim.Status.Phase, true, nil
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

// Retire moves the pod's Sandbox to draining, as the WarmPoolController
// retires a pod whose runtime crashed, so no later claim takes the pod.
// spec: §6.2 (Pod State Machine)
func (c *Cluster) Retire(ctx context.Context, p *Pod) error {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName(p.Name)
	u.SetNamespace(Namespace)
	_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": "draining", "podIP": p.IP}, "status")
	if err := c.Client.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		return fmt.Errorf("retire sandbox %s: %w", p.Name, err)
	}
	return nil
}

// RemoveSandbox deletes the pod's Sandbox, so no claim can take it.
func (c *Cluster) RemoveSandbox(ctx context.Context, p *Pod) error {
	return client.IgnoreNotFound(c.Client.Delete(ctx, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: Namespace},
	}))
}
