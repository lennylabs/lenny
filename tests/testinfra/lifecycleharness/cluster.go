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

// Namespace is the agent namespace a harness cluster on a private control
// plane serves. A cluster on a shared control plane serves a namespace of
// its own instead (ClusterOptions.ControlPlane), which Cluster.Namespace
// names.
const Namespace = "lenny-agents"

// RuntimeRef is the runtime every harness session runs.
const RuntimeRef = "echo"

// Pool is the exclusive warm pool every harness pod belongs to.
const Pool = "echo-pool"

// Cluster is one envtest API server holding the harness warm pool, its
// template, and its Sandboxes, with an in-process adapter behind each pod.
type Cluster struct {
	Client client.Client
	// Namespace is the agent namespace this cluster serves: the Namespace
	// constant on a private control plane, and a namespace unique to the
	// cluster on a shared one.
	Namespace string

	heartbeat time.Duration
	// transport is ClusterOptions.CheckpointTransport.
	transport adapter.CheckpointTransport

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
	// unary counts the unary RPCs the pod's adapter served, by method
	// name (the last element of the gRPC full method).
	unary map[string]int
}

// UnaryCalls reports how many unary RPCs named method (for example
// "SealSession" or "Shutdown") the pod's adapter has received.
func (p *Pod) UnaryCalls(method string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.unary[method]
}

// countUnary is the adapter's unary interceptor that counts each call by
// method name.
func (p *Pod) countUnary(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
	method := info.FullMethod[strings.LastIndex(info.FullMethod, "/")+1:]
	p.mu.Lock()
	if p.unary == nil {
		p.unary = map[string]int{}
	}
	p.unary[method]++
	p.mu.Unlock()
	return h(ctx, req)
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
	// ControlPlane, when set, runs the cluster on that shared envtest
	// control plane in a namespace unique to the cluster, rather than on a
	// private control plane in the Namespace constant. Starting a control
	// plane takes several seconds, so a package that builds many harnesses
	// shares one (TEST-GAPS T-17.4.22). Every object the harness and the
	// gateway code under it create is namespaced and is created in the
	// cluster's namespace, so clusters sharing one control plane do not see
	// each other's pods, claims, or pools.
	ControlPlane *envtest.Shared
	// CheckpointTransport, when set, is every pod adapter's object-store
	// transport, which a derived or replayed session's restore fetches its
	// presigned GETs through (§7.1 Derive restore semantics).
	CheckpointTransport adapter.CheckpointTransport
}

// NewCluster starts envtest and seeds the warm pool and template.
func NewCluster(t testing.TB) *Cluster {
	return NewClusterWith(t, ClusterOptions{})
}

// NewClusterWith starts envtest, or joins opts.ControlPlane, and seeds the
// warm pool and a template configured by opts.
func NewClusterWith(t testing.TB, opts ClusterOptions) *Cluster {
	t.Helper()
	var env *envtest.Environment
	ns := Namespace
	if opts.ControlPlane != nil {
		env = opts.ControlPlane.Start(t)
		ns = envtest.NamespaceName(t)
	} else {
		env = envtest.Start(t)
	}
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
	if opts.ControlPlane != nil {
		envtest.CreateNamespace(t, c, ns)
	} else if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	for _, o := range []client.Object{
		&lennyv1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{Name: Pool, Namespace: ns},
			Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "echo-tmpl", MinWarm: 1, MaxWarm: 64},
		},
		template(opts, ns),
	} {
		if err := c.Create(ctx, o); err != nil {
			t.Fatalf("create %T: %v", o, err)
		}
	}
	return &Cluster{Client: c, Namespace: ns, pods: map[string]*Pod{}, nextIP: 10, heartbeat: opts.HeartbeatInterval, transport: opts.CheckpointTransport}
}

// template is the warm pool's SandboxTemplate in namespace ns.
func template(opts ClusterOptions, ns string) *lennyv1.SandboxTemplate {
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-tmpl", Namespace: ns},
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
	srv.CheckpointTransport = c.transport
	if c.heartbeat > 0 {
		srv.HeartbeatInterval = c.heartbeat
	}
	lis := bufconn.Listen(1 << 20)
	pod := &Pod{Name: name, IP: ip, Adapter: srv, Runtime: rt, lis: lis}
	gs := adapter.NewGRPCServer(srv, grpc.ChainStreamInterceptor(pod.failableAttach), grpc.ChainUnaryInterceptor(pod.countUnary))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	ctx := context.Background()
	sb := &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: c.Namespace, Labels: map[string]string{warmpool.LabelPool: Pool},
	}}
	if err := c.Client.Create(ctx, sb); err != nil {
		t.Fatalf("create sandbox %s: %v", name, err)
	}
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName(name)
	u.SetNamespace(c.Namespace)
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
	if err := c.Client.List(ctx, &claims, client.InNamespace(c.Namespace)); err != nil {
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
	err := c.Client.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: "claim-" + podName}, &claim)
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
	return c.ProjectPhase(ctx, p, "draining")
}

// ProjectPhase writes the pod's Sandbox phase as the WarmPoolController's
// occupancy projection does, under its field manager. A scenario uses it for
// a projection the harness does not run, such as `reserved` after a
// recycle's scrub completes.
// spec: §4.6.1 (occupancy projection), §6.2 (Pod State Machine)
func (c *Cluster) ProjectPhase(ctx context.Context, p *Pod, phase string) error {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName(p.Name)
	u.SetNamespace(c.Namespace)
	_ = unstructured.SetNestedField(u.Object, map[string]interface{}{"phase": phase, "podIP": p.IP}, "status")
	if err := c.Client.Status().Patch(ctx, u, client.Apply, client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		return fmt.Errorf("project sandbox %s to %s: %w", p.Name, phase, err)
	}
	return nil
}

// SeedAgentPod creates the agent Pod object behind the pod's Sandbox, which
// the harness otherwise leaves out, carrying the pool label and labels. A
// scenario uses it where a gateway path reads the Pod itself, such as the
// within-hold rebind's runtime-process lifetime check, which skips a
// reserved candidate whose Pod read is NotFound. An existing Pod is left as
// it is.
// spec: §4.6.1 (Reserved hold; within-hold rebind), §5.2 (Runtime-process
// lifetime)
func (c *Cluster) SeedAgentPod(ctx context.Context, p *Pod, labels map[string]string) error {
	podLabels := map[string]string{warmpool.LabelPool: Pool}
	for k, v := range labels {
		podLabels[k] = v
	}
	err := c.Client.Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: c.Namespace, Labels: podLabels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "echo"}}},
	})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("seed agent pod %s: %w", p.Name, err)
	}
	return nil
}

// RemoveSandbox deletes the pod's Sandbox, so no claim can take it, and
// deletes the pod's claim, as the WarmPoolController does when it completes
// a pod's termination.
// spec: §4.6 (SandboxClaim deleted when the pod terminates)
func (c *Cluster) RemoveSandbox(ctx context.Context, p *Pod) error {
	if err := client.IgnoreNotFound(c.Client.Delete(ctx, &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: c.Namespace},
	})); err != nil {
		return err
	}
	return client.IgnoreNotFound(c.Client.Delete(ctx, &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-" + p.Name, Namespace: c.Namespace},
	}))
}

// retireByName moves the named pod's Sandbox to draining when the pod is
// still in the cluster.
func (c *Cluster) retireByName(ctx context.Context, pod string) {
	if p, ok := c.PodByName(pod); ok {
		_ = c.Retire(ctx, p)
	}
}
