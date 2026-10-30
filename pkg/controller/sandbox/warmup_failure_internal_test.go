// SPDX-License-Identifier: MIT

package sandbox

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// countingInc records each increment the meter makes.
type countingInc struct {
	mu   sync.Mutex
	got  []string
	seen map[string]int
}

func (c *countingInc) inc(pool, runtimeClass, errorType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := pool + "|" + runtimeClass + "|" + errorType
	c.got = append(c.got, key)
	if c.seen == nil {
		c.seen = map[string]int{}
	}
	c.seen[key]++
}

func testMeter() (*warmupFailureMeter, *countingInc) {
	c := &countingInc{}
	m := newWarmupFailureMeter()
	m.inc = c.inc
	return m, c
}

func warmupSandbox(name string, phase state.State) *lennyv1.Sandbox {
	sb := &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "lenny-agents"}}
	sb.Spec.PoolRef = "claude-pool"
	sb.Status.Phase = string(phase)
	return sb
}

func warmupPod(uid string, mutate func(*corev1.Pod)) *corev1.Pod {
	rc := "gvisor"
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", UID: types.UID(uid)}}
	p.Spec.RuntimeClassName = &rc
	p.Status.Phase = corev1.PodPending
	if mutate != nil {
		mutate(p)
	}
	return p
}

func imagePullWaiting(reason string) func(*corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Status.ContainerStatuses = []corev1.ContainerStatus{
			{Name: podspec.AdapterContainerName, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			{Name: podspec.RuntimeContainerName, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}}},
		}
	}
}

func evicted(p *corev1.Pod) {
	p.Status.Phase = corev1.PodFailed
	p.Status.Reason = "Evicted"
}

// spec: §16.1 (Metrics, the warm-up failure row: image_pull_error and
// node_pressure count a pod that fails to reach idle during warm-up)
func TestClassifyWarmupPod_spec_16_1(t *testing.T) {
	cases := []struct {
		name   string
		pod    *corev1.Pod
		want   string
		wantOK bool
	}{
		{name: "nil pod", pod: nil},
		{name: "pending, no statuses", pod: warmupPod("u", nil)},
		{name: "container creating", pod: warmupPod("u", imagePullWaiting("ContainerCreating"))},
		{name: "ErrImagePull", pod: warmupPod("u", imagePullWaiting("ErrImagePull")), want: warmupErrorImagePull, wantOK: true},
		{name: "ImagePullBackOff", pod: warmupPod("u", imagePullWaiting("ImagePullBackOff")), want: warmupErrorImagePull, wantOK: true},
		{name: "InvalidImageName", pod: warmupPod("u", imagePullWaiting("InvalidImageName")), want: warmupErrorImagePull, wantOK: true},
		{name: "ErrImageNeverPull", pod: warmupPod("u", imagePullWaiting("ErrImageNeverPull")), want: warmupErrorImagePull, wantOK: true},
		{name: "init container image pull", pod: warmupPod("u", func(p *corev1.Pod) {
			p.Status.InitContainerStatuses = []corev1.ContainerStatus{{
				Name: "lenny-supervisor-stage", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
			}}
		}), want: warmupErrorImagePull, wantOK: true},
		{name: "kubelet eviction", pod: warmupPod("u", evicted), want: warmupErrorNodePressure, wantOK: true},
		{name: "failed, not evicted", pod: warmupPod("u", func(p *corev1.Pod) { p.Status.Phase = corev1.PodFailed; p.Status.Reason = "DeadlineExceeded" })},
		{name: "Evicted reason on a running pod", pod: warmupPod("u", func(p *corev1.Pod) { p.Status.Phase = corev1.PodRunning; p.Status.Reason = "Evicted" })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := classifyWarmupPod(tc.pod)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("classifyWarmupPod = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// spec: §16.1 (Metrics), §17.2 (Namespace Layout, the ResourceQuota
// paragraph: a quota below the pool's target increments
// error_type="resource_quota_exceeded")
func TestClassifyCreateError_spec_16_1(t *testing.T) {
	gr := schema.GroupResource{Resource: "pods"}
	quota := apierrors.NewForbidden(gr, "p", errors.New("exceeded quota: agents, requested: pods=1, used: pods=10, limited: pods=10"))
	cases := []struct {
		name   string
		err    error
		wantOK bool
	}{
		{name: "nil", err: nil},
		{name: "quota forbidden", err: quota, wantOK: true},
		{name: "wrapped quota forbidden", err: fmt.Errorf("create pod: %w", quota), wantOK: true},
		{name: "webhook forbidden", err: apierrors.NewForbidden(gr, "p", errors.New("denied by lenny-pod-security"))},
		{name: "invalid", err: apierrors.NewInvalid(schema.GroupKind{Kind: "Pod"}, "p", nil)},
		{name: "plain error mentioning quota", err: errors.New("exceeded quota")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := classifyCreateError(tc.err)
			if ok != tc.wantOK || (ok && got != warmupErrorQuotaExceeded) {
				t.Errorf("classifyCreateError = (%q, %v), want ok=%v", got, ok, tc.wantOK)
			}
		})
	}
}

// spec: §16.1 (Metrics, labels pool, runtime_class, error_type; one count
// per pod that fails to reach idle), §6.2 (Pod State Machine, warm-up
// phases)
func TestWarmupFailureMeterCountsEachFailingPodOnce_spec_16_1(t *testing.T) {
	m, c := testMeter()
	sb := warmupSandbox("sb-1", state.Warming)
	pod := warmupPod("uid-1", imagePullWaiting("ImagePullBackOff"))

	for i := 0; i < 5; i++ {
		m.observePod(sb, pod, true)
	}
	if len(c.got) != 1 || c.got[0] != "claude-pool|gvisor|image_pull_error" {
		t.Fatalf("repeated reconciles of one stuck pod: increments = %v, want one claude-pool|gvisor|image_pull_error", c.got)
	}

	// The same pod later evicted is still one failing pod.
	m.observePod(sb, warmupPod("uid-1", evicted), true)
	if len(c.got) != 1 {
		t.Fatalf("the same pod evicted after an image pull failure was counted again: %v", c.got)
	}

	// A replacement pod for the same Sandbox name is a new failure.
	m.observePod(sb, warmupPod("uid-2", evicted), true)
	if len(c.got) != 2 || c.got[1] != "claude-pool|gvisor|node_pressure" {
		t.Fatalf("a new failing pod: increments = %v, want a second node_pressure count", c.got)
	}
}

// spec: §16.1 (Metrics), §6.2 (Pod State Machine: only a pod that has not
// reached idle is a warm-up failure)
func TestWarmupFailureMeterIgnoresPodsPastWarmup_spec_16_1(t *testing.T) {
	for _, phase := range []state.State{state.Idle, state.Claimed, state.Reserved, state.Draining, state.Failed, state.Terminated} {
		t.Run(string(phase), func(t *testing.T) {
			m, c := testMeter()
			m.observePod(warmupSandbox("sb", phase), warmupPod("u", evicted), true)
			if len(c.got) != 0 {
				t.Errorf("phase %s: increments = %v, want none", phase, c.got)
			}
		})
	}
	for _, phase := range []state.State{"", state.Warming, state.SDKConnecting} {
		t.Run("warm-up "+string(phase), func(t *testing.T) {
			m, c := testMeter()
			m.observePod(warmupSandbox("sb", phase), warmupPod("u", evicted), true)
			if len(c.got) != 1 {
				t.Errorf("phase %q: increments = %v, want one", phase, c.got)
			}
		})
	}
	t.Run("absent pod", func(t *testing.T) {
		m, c := testMeter()
		m.observePod(warmupSandbox("sb", state.Warming), &corev1.Pod{}, false)
		if len(c.got) != 0 {
			t.Errorf("absent pod: increments = %v, want none", c.got)
		}
	})
}

// spec: §16.1 (Metrics), §17.2 (Namespace Layout, the ResourceQuota
// paragraph)
func TestWarmupFailureMeterCountsQuotaRejectionOncePerSandbox_spec_16_1(t *testing.T) {
	m, c := testMeter()
	sb := warmupSandbox("sb-q", state.Warming)
	pod := warmupPod("", nil)
	quota := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "sb-q", errors.New("exceeded quota: agents"))

	for i := 0; i < 3; i++ {
		m.observeCreateError(sb, pod, quota)
	}
	m.observeCreateError(sb, pod, errors.New("connection refused"))
	if len(c.got) != 1 || c.got[0] != "claude-pool|gvisor|resource_quota_exceeded" {
		t.Fatalf("retried quota rejections: increments = %v, want one resource_quota_exceeded", c.got)
	}

	// A second Sandbox rejected by the same quota is its own failure.
	m.observeCreateError(warmupSandbox("sb-r", state.Warming), pod, quota)
	if len(c.got) != 2 {
		t.Fatalf("a second rejected Sandbox: increments = %v, want two", c.got)
	}

	// Once the Sandbox is deleted its record goes, so a later Sandbox
	// reusing the name counts afresh.
	m.forget(types.NamespacedName{Namespace: sb.Namespace, Name: sb.Name})
	m.observeCreateError(sb, pod, quota)
	if len(c.got) != 3 {
		t.Fatalf("after forget: increments = %v, want three", c.got)
	}
}

// spec: §16.1 (Metrics), §6.2 (Pod State Machine)
func TestWarmupFailureMeterForgetsSandboxPastWarmup_spec_16_1(t *testing.T) {
	m, c := testMeter()
	sb := warmupSandbox("sb", state.Warming)
	m.observePod(sb, warmupPod("u", imagePullWaiting("ErrImagePull")), true)
	m.observePod(warmupSandbox("sb", state.Idle), warmupPod("u", nil), true)
	m.mu.Lock()
	n := len(m.counted)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("a Sandbox past warm-up kept its record: %d entries", n)
	}
	if len(c.got) != 1 {
		t.Fatalf("increments = %v, want one", c.got)
	}
}

// spec: §16.1 (Metrics)
func TestWarmupFailureMeterConcurrentReconciles_spec_16_1(t *testing.T) {
	m, c := testMeter()
	pod := warmupPod("uid-c", imagePullWaiting("ImagePullBackOff"))
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				m.observePod(warmupSandbox("sb-c", state.Warming), pod, true)
				m.observePod(warmupSandbox(fmt.Sprintf("sb-%d", i), state.Warming), warmupPod(fmt.Sprintf("u-%d", i), evicted), true)
			}
		}()
	}
	wg.Wait()
	if got := c.seen["claude-pool|gvisor|image_pull_error"]; got != 1 {
		t.Errorf("one stuck pod reconciled concurrently counted %d times, want 1", got)
	}
	if got := c.seen["claude-pool|gvisor|node_pressure"]; got != 50 {
		t.Errorf("50 distinct evicted pods counted %d times, want 50", got)
	}
}

// spec: §16.1 (Metrics, lenny_warmpool_warmup_failure_total labeled by
// pool, runtime_class, error_type)
func TestWarmupFailureCounterLabels_spec_16_1(t *testing.T) {
	before := testutil.ToFloat64(warmupFailureTotal.WithLabelValues("label-pool", "kata", warmupErrorNodePressure))
	m := newWarmupFailureMeter()
	sb := warmupSandbox("sb-label", state.Warming)
	sb.Spec.PoolRef = "label-pool"
	rc := "kata"
	pod := warmupPod("uid-label", evicted)
	pod.Spec.RuntimeClassName = &rc
	m.observePod(sb, pod, true)
	after := testutil.ToFloat64(warmupFailureTotal.WithLabelValues("label-pool", "kata", warmupErrorNodePressure))
	if after-before != 1 {
		t.Fatalf("counter delta = %v, want 1", after-before)
	}
}
