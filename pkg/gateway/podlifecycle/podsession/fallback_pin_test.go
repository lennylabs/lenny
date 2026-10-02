// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// agentPodFor builds the agent Pod backing the same-named Sandbox, carrying
// the §5.2 tenant pin when tenant is non-empty.
func agentPodFor(name, tenant string) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS, Labels: map[string]string{}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "k8s.gcr.io/pause"}}},
	}
	if tenant != "" {
		p.Labels[podclaim.LabelTenant] = tenant
	}
	return p
}

// fbCall is one client call the fallback recorder observed.
type fbCall struct {
	verb, kind, name string
	// drain marks a Pod Patch that writes the drain-request annotation.
	drain bool
	// underLock is true when the call was issued while the fake mirror's
	// ClaimIdle (its row lock) was open.
	underLock bool
}

// fbRecorder wraps an envtest client, records every call with the mirror's
// row-lock state, and injects failures or blocking by Pod name.
type fbRecorder struct {
	mirror *fakeMirror
	mu     sync.Mutex
	calls  []fbCall

	failPodGet   map[string]error
	blockPodGet  map[string]bool
	failPodPatch map[string]error
}

func (r *fbRecorder) record(c fbCall) {
	if r.mirror != nil {
		c.underLock = r.mirror.open.Load()
	}
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
}

func kindOf(obj client.Object) string {
	switch obj.(type) {
	case *corev1.Pod:
		return "Pod"
	case *lennyv1.SandboxClaim:
		return "SandboxClaim"
	case *lennyv1.Sandbox:
		return "Sandbox"
	default:
		return "other"
	}
}

func (r *fbRecorder) wrap(c client.WithWatch) client.WithWatch {
	return interceptor.NewClient(c, interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			r.record(fbCall{verb: "get", kind: kindOf(obj), name: key.Name})
			if _, ok := obj.(*corev1.Pod); ok {
				if r.blockPodGet[key.Name] {
					<-ctx.Done()
					return ctx.Err()
				}
				if err := r.failPodGet[key.Name]; err != nil {
					return err
				}
			}
			return cl.Get(ctx, key, obj, opts...)
		},
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			drain := false
			if data, err := patch.Data(obj); err == nil {
				drain = strings.Contains(string(data), lennyv1.AnnotationDrainRequest)
			}
			r.record(fbCall{verb: "patch", kind: kindOf(obj), name: obj.GetName(), drain: drain})
			if _, ok := obj.(*corev1.Pod); ok {
				if err := r.failPodPatch[obj.GetName()]; err != nil {
					return err
				}
			}
			return cl.Patch(ctx, obj, patch, opts...)
		},
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			r.record(fbCall{verb: "create", kind: kindOf(obj), name: obj.GetName()})
			return cl.Create(ctx, obj, opts...)
		},
	})
}

func (r *fbRecorder) snapshot() []fbCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fbCall(nil), r.calls...)
}

func (r *fbRecorder) drainPatches() []fbCall {
	var out []fbCall
	for _, c := range r.snapshot() {
		if c.verb == "patch" && c.drain {
			out = append(out, c)
		}
	}
	return out
}

// claimExists reports whether claim-<podName> exists.
func claimExists(t *testing.T, c client.Client, podName string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: podclaim.ClaimName(podName)}, &lennyv1.SandboxClaim{})
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatalf("get claim for %s: %v", podName, err)
	}
	return true
}

func podLabel(t *testing.T, c client.Client, name string) string {
	t.Helper()
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &pod); err != nil {
		t.Fatalf("get pod %s: %v", name, err)
	}
	return pod.Labels[podclaim.LabelTenant]
}

func podDrained(t *testing.T, c client.Client, name string) bool {
	t.Helper()
	var pod corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &pod); err != nil {
		t.Fatalf("get pod %s: %v", name, err)
	}
	_, ok := pod.Annotations[lennyv1.AnnotationDrainRequest]
	return ok
}

// fallbackFixture builds a Binder whose Kubernetes-API claim finds no idle
// pod (every Sandbox in objs that the case wants on the fallback path is
// unlabelled) and whose mirror reports idle in order.
func fallbackFixture(t *testing.T, idle []string, objs ...client.Object) (*podsession.Binder, client.WithWatch, *fakeMirror, *fbRecorder, *[]string) {
	t.Helper()
	srv := adapter.New("adapter-test")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	base := k8sClient(t, objs...)
	mirror := &fakeMirror{idle: map[string][]string{testPool: idle}, lag: 1}
	rec := &fbRecorder{mirror: mirror}
	binder := newBinder(rec.wrap(base), adapterDialer(t, srv))
	binder.Fallback = mirror
	var skipped []string
	binder.FallbackSkipped = func(reason string) { skipped = append(skipped, reason) }
	return binder, base, mirror, rec, &skipped
}

func bindAcme(binder *podsession.Binder, keeps bool) (*podsession.BindResult, error) {
	return binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code", KeepsRuntime: keeps,
	})
}

// spec: 5.2 (Tenant pinning), 4.6.1 (Postgres-backed fallback claim)
// diagnosis: the Postgres fallback binds a pod the primary path would refuse,
// leaves a refused mirror row claimed under the wrong tenant, ends a fallback
// that had an admissible row, leaves a claimed pod unpinned, or keeps a claim
// whose tenant stamp failed.
func TestFallbackClaimReadsTenantPin_spec_5_2(t *testing.T) {
	t.Run("refused older row", func(t *testing.T) {
		binder, base, mirror, _, _ := fallbackFixture(t, []string{"fb-old", "fb-young"},
			unlabeledSandbox("fb-old", "10.0.0.1"), unlabeledSandbox("fb-young", "10.0.0.2"),
			agentPodFor("fb-old", "globex"), agentPodFor("fb-young", ""))
		res, err := bindAcme(binder, true)
		if err != nil {
			t.Fatalf("Bind: %v", err)
		}
		defer res.Adapter.Close()
		if res.SandboxName != "fb-young" {
			t.Errorf("bound %q, want fb-young", res.SandboxName)
		}
		if claimExists(t, base, "fb-old") {
			t.Error("a claim was created on the refused pod")
		}
		if !slices.Contains(mirror.idle[testPool], "fb-old") {
			t.Error("the refused row left the idle set")
		}
		if got := podLabel(t, base, "fb-young"); got != "acme" {
			t.Errorf("claimed pod pin = %q, want acme", got)
		}
	})
	t.Run("only row refused", func(t *testing.T) {
		binder, base, _, rec, _ := fallbackFixture(t, []string{"fb-only"},
			unlabeledSandbox("fb-only", "10.0.0.1"), agentPodFor("fb-only", "globex"))
		if _, err := bindAcme(binder, true); !errors.Is(err, podclaim.ErrNoIdlePod) {
			t.Fatalf("Bind = %v, want ErrNoIdlePod", err)
		}
		if claimExists(t, base, "fb-only") {
			t.Error("a claim was created on the refused pod")
		}
		if got := rec.drainPatches(); len(got) != 0 {
			t.Errorf("drain patches %v on a pod refused to another tenant", got)
		}
	})
	t.Run("pin stamp fails", func(t *testing.T) {
		binder, base, _, rec, _ := fallbackFixture(t, []string{"fb-stamp"},
			unlabeledSandbox("fb-stamp", "10.0.0.1"), agentPodFor("fb-stamp", ""))
		boom := apierrors.NewInternalError(errors.New("stamp boom"))
		rec.failPodPatch = map[string]error{"fb-stamp": boom}
		if _, err := bindAcme(binder, true); err == nil || !errors.Is(err, boom) {
			t.Fatalf("Bind = %v, want the stamp error", err)
		}
		if claimExists(t, base, "fb-stamp") {
			t.Error("the claim survived a failed tenant stamp")
		}
	})
}

// spec: 5.2 (Tenant pinning), 4.6.1 (Postgres-backed fallback claim)
// diagnosis: a Patch or SandboxClaim call is issued while the fallback holds
// the row lock and its Postgres connection, the pin is read outside the row
// lock, or the fallback writes a drain stamp.
func TestFallbackWritesNothingUnderRowLock(t *testing.T) {
	for _, tc := range []struct {
		name   string
		keeps  bool
		oldPin string
	}{
		{name: "pool outside the process-reuse rule", keeps: false, oldPin: "acme"},
		{name: "pool that keeps its runtime", keeps: true, oldPin: "globex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binder, base, _, rec, _ := fallbackFixture(t, []string{"rl-old", "rl-young"},
				unlabeledSandbox("rl-old", "10.0.0.1"), unlabeledSandbox("rl-young", "10.0.0.2"),
				agentPodFor("rl-old", tc.oldPin), agentPodFor("rl-young", ""))
			res, err := bindAcme(binder, tc.keeps)
			if err != nil {
				t.Fatalf("Bind: %v", err)
			}
			defer res.Adapter.Close()
			if res.SandboxName != "rl-young" {
				t.Errorf("bound %q, want rl-young", res.SandboxName)
			}
			if podDrained(t, base, "rl-old") || len(rec.drainPatches()) != 0 {
				t.Error("the fallback wrote a drain request")
			}
			var podGets int
			for _, c := range rec.snapshot() {
				if !c.underLock {
					continue
				}
				if c.verb != "get" || c.kind != "Pod" {
					t.Errorf("call %+v issued under the row lock; only Pod Gets are allowed", c)
					continue
				}
				podGets++
			}
			if podGets < 2 {
				t.Errorf("%d Pod Gets under the row lock, want one per offered row (>= 2)", podGets)
			}
		})
	}
}

// spec: 5.2 (Tenant pinning), 4.6.1 (Pool exhaustion behavior)
// diagnosis: a failed pin read inside the fallback is reported as pool
// exhaustion or a fallback skip, binds or writes to a pod, or moves a mirror
// row off idle.
func TestFallbackPinReadErrorEndsTheAcquisition_spec_5_2(t *testing.T) {
	binder, base, mirror, rec, skipped := fallbackFixture(t, []string{"pe-old", "pe-young"},
		unlabeledSandbox("pe-old", "10.0.0.1"), unlabeledSandbox("pe-young", "10.0.0.2"),
		agentPodFor("pe-old", "acme"), agentPodFor("pe-young", ""))
	boom := apierrors.NewInternalError(errors.New("pin read boom"))
	rec.failPodGet = map[string]error{"pe-young": boom}
	_, err := bindAcme(binder, false)
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("Bind = %v, want the pin read error", err)
	}
	if errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Error("a failed pin read was reported as ErrNoIdlePod")
	}
	if len(*skipped) != 0 {
		t.Errorf("FallbackSkipped called with %v", *skipped)
	}
	for _, c := range rec.snapshot() {
		if c.verb == "patch" && c.kind == "Pod" {
			t.Errorf("Pod patch %+v on a failed pin read", c)
		}
	}
	if claimExists(t, base, "pe-old") || claimExists(t, base, "pe-young") {
		t.Error("a claim was created")
	}
	if !slices.Equal(mirror.idle[testPool], []string{"pe-old", "pe-young"}) {
		t.Errorf("mirror idle rows = %v, want both still idle", mirror.idle[testPool])
	}
}

// spec: 5.2 (Tenant pinning), 4.6.1 (Postgres-backed fallback claim)
// diagnosis: the fallback reads, offers, or locks a pod the idle scan
// already refused, so a refused pod is read twice and its row lock is held
// for no purpose.
func TestFallbackSkipsScanRefusals_spec_5_2(t *testing.T) {
	binder, _, mirror, rec, _ := fallbackFixture(t, []string{"sr-pinned", "sr-free"},
		idleSandbox("sr-pinned", "10.0.0.1"), unlabeledSandbox("sr-free", "10.0.0.2"),
		agentPodFor("sr-pinned", "globex"), agentPodFor("sr-free", ""))
	res, err := binder.Claim(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", KeepsRuntime: true,
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if res.SandboxName != "sr-free" {
		t.Errorf("claimed %q, want sr-free", res.SandboxName)
	}
	var pinnedGets []fbCall
	for _, c := range rec.snapshot() {
		if c.verb == "get" && c.kind == "Pod" && c.name == "sr-pinned" {
			pinnedGets = append(pinnedGets, c)
		}
	}
	if len(pinnedGets) != 1 || pinnedGets[0].underLock {
		t.Errorf("pinned pod Gets = %+v, want exactly one, before ClaimIdle", pinnedGets)
	}
	if len(mirror.skips) != 1 || !slices.Contains(mirror.skips[0], "sr-pinned") {
		t.Errorf("ClaimIdle skip lists = %v, want one naming sr-pinned", mirror.skips)
	}
	if slices.Contains(mirror.offered, "sr-pinned") {
		t.Error("the fallback offered the refused pod to the pin callback")
	}
}

// spec: 4.6.1 (Postgres-backed fallback claim), 5.2 (Tenant pinning)
// diagnosis: the ClaimIdle transaction outlives the resolved
// podClaimFallbackMaxMirrorLagSeconds, so a hung Pod read holds a mirror row
// lock and a Postgres connection, or the timeout is reported as exhaustion.
func TestFallbackPinReadIsBounded_spec_4_6_1(t *testing.T) {
	binder, base, mirror, rec, skipped := fallbackFixture(t, []string{"bd-pod"},
		unlabeledSandbox("bd-pod", "10.0.0.1"), agentPodFor("bd-pod", ""))
	mirror.lag = 0
	binder.FallbackMaxMirrorLagSeconds = 0.1
	rec.blockPodGet = map[string]bool{"bd-pod": true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	_, err := binder.Bind(ctx, podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code", KeepsRuntime: true,
	})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Bind took %v, want the pin read bounded well under 5s", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Bind = %v, want an error wrapping context.DeadlineExceeded", err)
	}
	if errors.Is(err, podclaim.ErrNoIdlePod) || len(*skipped) != 0 {
		t.Errorf("a timed-out pin read was reported as exhaustion (err=%v skipped=%v)", err, *skipped)
	}
	if mirror.open.Load() {
		t.Error("ClaimIdle is still open after Bind returned")
	}
	if claimExists(t, base, "bd-pod") {
		t.Error("a claim was created")
	}
}

// spec: 4.6.1 (Pool exhaustion behavior), 5.2 (Tenant pinning)
// diagnosis: a failed pin read on the Kubernetes-API claim starts the
// Postgres fallback, which then binds a pod without the primary path ever
// reading the pin of the pod it failed on.
func TestPrimaryPinReadErrorStartsNoFallback_spec_4_6_1(t *testing.T) {
	binder, base, mirror, rec, _ := fallbackFixture(t, []string{"pp-fallback"},
		idleSandbox("pp-primary", "10.0.0.1"), unlabeledSandbox("pp-fallback", "10.0.0.2"),
		agentPodFor("pp-primary", ""), agentPodFor("pp-fallback", ""))
	boom := apierrors.NewInternalError(errors.New("primary pin read boom"))
	rec.failPodGet = map[string]error{"pp-primary": boom}
	_, err := bindAcme(binder, true)
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("Bind = %v, want the primary pin read error", err)
	}
	if errors.Is(err, podclaim.ErrNoIdlePod) {
		t.Error("a failed primary pin read was reported as ErrNoIdlePod")
	}
	if mirror.calls != 0 {
		t.Errorf("ClaimIdle called %d times; a failed primary pin read starts no fallback", mirror.calls)
	}
	if claimExists(t, base, "pp-primary") || claimExists(t, base, "pp-fallback") {
		t.Error("a claim was created")
	}
}

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions)), 4.6.1 (Reserved hold)
// diagnosis: on a pool outside the process-reuse rule the idle scan does not
// drain a pod that served a session, the fallback stamps it a second time or
// claims it, or a resume claim ignores the pool's process-reuse rule.
func TestOutsideRuleScanDrainsAndFallbackAddsNoStamp_spec_5_2(t *testing.T) {
	newFixture := func(t *testing.T) (*podsession.Binder, client.WithWatch, *fakeMirror, *fbRecorder) {
		binder, base, mirror, rec, _ := fallbackFixture(t, []string{"or-pod"},
			idleSandbox("or-pod", "10.0.0.1"), agentPodFor("or-pod", "acme"))
		return binder, base, mirror, rec
	}
	assertOneEarlyDrain := func(t *testing.T, rec *fbRecorder) {
		t.Helper()
		drains := rec.drainPatches()
		if len(drains) != 1 || drains[0].name != "or-pod" || drains[0].underLock {
			t.Errorf("drain patches = %+v, want one on or-pod before ClaimIdle", drains)
		}
	}

	t.Run("claim", func(t *testing.T) {
		binder, _, mirror, rec := newFixture(t)
		_, err := binder.Claim(context.Background(), podsession.BindRequest{
			Pool: testPool, SessionID: "sess-1", TenantID: "acme",
		})
		if !errors.Is(err, podclaim.ErrNoIdlePod) {
			t.Fatalf("Claim = %v, want ErrNoIdlePod", err)
		}
		if !slices.Contains(mirror.idle[testPool], "or-pod") {
			t.Error("the mirror row left idle")
		}
		assertOneEarlyDrain(t, rec)
	})
	t.Run("resume outside the rule", func(t *testing.T) {
		binder, _, _, rec := newFixture(t)
		_, err := binder.Resume(context.Background(), podsession.ResumeRequest{
			Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code", CheckpointID: "ckpt-1",
		})
		if !errors.Is(err, podclaim.ErrNoIdlePod) {
			t.Fatalf("Resume = %v, want ErrNoIdlePod", err)
		}
		assertOneEarlyDrain(t, rec)
	})
	t.Run("resume on a pool that keeps its runtime", func(t *testing.T) {
		binder, base, _, rec := newFixture(t)
		binder.DialAdapter = func(string) (*adapterclient.Client, error) {
			return nil, errors.New("adapter unreachable")
		}
		_, _ = binder.Resume(context.Background(), podsession.ResumeRequest{
			Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
			CheckpointID: "ckpt-1", KeepsRuntime: true,
		})
		if !claimExists(t, base, "or-pod") {
			t.Error("the resume claim refused the pod pinned to its own tenant")
		}
		if got := rec.drainPatches(); len(got) != 0 {
			t.Errorf("drain patches %+v on a pool that keeps its runtime", got)
		}
	})
}
