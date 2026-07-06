// SPDX-License-Identifier: MIT

package warmpool_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

func managedPod(name, nodeName, stateLabel string, annotations map[string]string) *corev1.Pod {
	labels := map[string]string{
		warmpool.LabelManaged: "true",
		warmpool.LabelPool:    testPool,
	}
	if stateLabel != "" {
		labels[state.LabelState] = stateLabel
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   testNS,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			NodeName:   nodeName,
			Containers: []corev1.Container{{Name: "agent", Image: "example/agent:latest"}},
		},
	}
}

func clusterNode(name string, unschedulable bool) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: unschedulable},
	}
}

func reconcilePod(t *testing.T, r *warmpool.PodReconciler, name string) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: name},
	})
	if err != nil {
		t.Fatalf("PodReconciler.Reconcile(%s): %v", name, err)
	}
	return res
}

func getCorePod(t *testing.T, c client.Client, name string) corev1.Pod {
	t.Helper()
	var p corev1.Pod
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &p); err != nil {
		t.Fatalf("get pod %s: %v", name, err)
	}
	return p
}

func getSandbox(t *testing.T, c client.Client, name string) lennyv1.Sandbox {
	t.Helper()
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &sb); err != nil {
		t.Fatalf("get sandbox %s: %v", name, err)
	}
	return sb
}

// TestPodReconcileHostSchedulableLabel_spec_4_6 verifies the §4.6.1
// host-node schedulability label tracks the node's cordon state: it is
// "true" on a schedulable node and flips to "false" when the node is
// cordoned, within a single reconcile.
func TestPodReconcileHostSchedulableLabel_spec_4_6(t *testing.T) {
	s := newScheme(t)
	nd := clusterNode("node-a", false)
	pod := managedPod("pod-a", "node-a", string(state.Idle), nil)
	c := newClient(t, s, nd, pod)

	r := &warmpool.PodReconciler{Client: c}
	reconcilePod(t, r, "pod-a")

	if got := getCorePod(t, c, "pod-a").Labels[warmpool.LabelHostSchedulable]; got != "true" {
		t.Fatalf("host-schedulable label=%q after reconcile on a schedulable node, want %q", got, "true")
	}

	// Cordon the node and re-reconcile: the label must flip to "false".
	var live corev1.Node
	if err := c.Get(testContext(), client.ObjectKey{Name: "node-a"}, &live); err != nil {
		t.Fatalf("get node: %v", err)
	}
	live.Spec.Unschedulable = true
	if err := c.Update(testContext(), &live); err != nil {
		t.Fatalf("cordon node: %v", err)
	}
	reconcilePod(t, r, "pod-a")
	if got := getCorePod(t, c, "pod-a").Labels[warmpool.LabelHostSchedulable]; got != "false" {
		t.Fatalf("host-schedulable label=%q after cordon, want %q", got, "false")
	}
}

// TestPodReconcileUnscheduledPodNoLabel_spec_4_6 verifies that a pod with
// no spec.nodeName (still Pending at the scheduler) is not labeled, per
// §4.6.1 ("pods whose spec.nodeName is not yet set ... are not eligible").
func TestPodReconcileUnscheduledPodNoLabel_spec_4_6(t *testing.T) {
	s := newScheme(t)
	pod := managedPod("pod-pending", "", string(state.Warming), nil)
	c := newClient(t, s, pod)

	r := &warmpool.PodReconciler{Client: c}
	reconcilePod(t, r, "pod-pending")

	if _, ok := getCorePod(t, c, "pod-pending").Labels[warmpool.LabelHostSchedulable]; ok {
		t.Fatal("host-schedulable label set on an unscheduled pod")
	}
}

// TestPodReconcileForeignPodIgnored verifies the reconciler ignores a pod
// that is not controller-managed even if it is somehow enqueued.
func TestPodReconcileForeignPodIgnored(t *testing.T) {
	s := newScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "foreign", Namespace: testNS},
		Spec: corev1.PodSpec{
			NodeName:   "node-a",
			Containers: []corev1.Container{{Name: "x", Image: "example/x:latest"}},
		},
	}
	c := newClient(t, s, clusterNode("node-a", false), pod)

	r := &warmpool.PodReconciler{Client: c}
	reconcilePod(t, r, "foreign")

	if _, ok := getCorePod(t, c, "foreign").Labels[warmpool.LabelHostSchedulable]; ok {
		t.Fatal("host-schedulable label set on a non-managed pod")
	}
}

// TestPodReconcileCertExpiryDrainsIdlePod_spec_4_6 verifies that an idle
// pod whose certificate is inside the §4.6.1 replacement window has its
// backing Sandbox transitioned to draining (the pool reconciler then
// recreates a fresh pod).
func TestPodReconcileCertExpiryDrainsIdlePod_spec_4_6(t *testing.T) {
	s := newScheme(t)
	now := time.Now()
	sb := idleSandbox("pod-expiring")
	// Cert expires in 10 minutes — inside the 30-minute window.
	pod := managedPod("pod-expiring", "", string(state.Idle), map[string]string{
		warmpool.AnnotationCertNotAfter: now.Add(10 * time.Minute).Format(time.RFC3339),
	})
	c := newClient(t, s, sb, pod)

	r := &warmpool.PodReconciler{Client: c, Now: func() time.Time { return now }}
	res := reconcilePod(t, r, "pod-expiring")

	if got := getSandbox(t, c, "pod-expiring").Status.Phase; got != string(state.Draining) {
		t.Fatalf("sandbox phase=%q after cert-expiry reconcile, want %q", got, state.Draining)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("RequeueAfter=%v after draining, want 0", res.RequeueAfter)
	}
}

// TestPodReconcileCertExpiryKeepsFreshPod_spec_4_6 verifies that an idle
// pod whose certificate is comfortably outside the window is left idle
// and re-queued for a later re-check.
func TestPodReconcileCertExpiryKeepsFreshPod_spec_4_6(t *testing.T) {
	s := newScheme(t)
	now := time.Now()
	sb := idleSandbox("pod-fresh")
	pod := managedPod("pod-fresh", "", string(state.Idle), map[string]string{
		warmpool.AnnotationCertNotAfter: now.Add(2 * time.Hour).Format(time.RFC3339),
	})
	c := newClient(t, s, sb, pod)

	r := &warmpool.PodReconciler{Client: c, Now: func() time.Time { return now }}
	res := reconcilePod(t, r, "pod-fresh")

	if got := getSandbox(t, c, "pod-fresh").Status.Phase; got != string(state.Idle) {
		t.Fatalf("sandbox phase=%q for a fresh-cert idle pod, want %q", got, state.Idle)
	}
	// 2h expiry minus the 30m threshold leaves ~90m before the next check.
	if res.RequeueAfter <= 0 {
		t.Fatalf("RequeueAfter=%v for a fresh-cert idle pod, want > 0", res.RequeueAfter)
	}
}

// TestPodReconcileCertExpirySkipsClaimedPod_spec_4_6 verifies that a
// claimed (non-idle) pod is never drained for cert expiry — an active
// session must not be disrupted, and idle is the only legal source state
// for the idle→draining edge.
func TestPodReconcileCertExpirySkipsClaimedPod_spec_4_6(t *testing.T) {
	s := newScheme(t)
	now := time.Now()
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-claimed",
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
		Status: lennyv1.SandboxStatus{Phase: string(state.Claimed)},
	}
	pod := managedPod("pod-claimed", "", string(state.Claimed), map[string]string{
		warmpool.AnnotationCertNotAfter: now.Add(5 * time.Minute).Format(time.RFC3339),
	})
	c := newClient(t, s, sb, pod)

	r := &warmpool.PodReconciler{Client: c, Now: func() time.Time { return now }}
	reconcilePod(t, r, "pod-claimed")

	if got := getSandbox(t, c, "pod-claimed").Status.Phase; got != string(state.Claimed) {
		t.Fatalf("claimed pod drained for cert expiry: phase=%q, want %q", got, state.Claimed)
	}
}

// warmingSandbox returns a Sandbox in the §4.6.1 warming (pre-idle) phase
// so the §10.3 line 342 issuance-grace drain has a live object to patch.
func warmingSandbox(name string) *lennyv1.Sandbox {
	return &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNS,
			Labels:    map[string]string{warmpool.LabelPool: testPool},
		},
		Status: lennyv1.SandboxStatus{Phase: string(state.Warming)},
	}
}

// recordingCertMetric captures the §10.3 lenny_cert_expiry_seconds Set/Clear
// calls the reconciler makes through the real Reconcile path.
type recordingCertMetric struct {
	set   map[string]float64
	clear map[string]int
}

func newRecordingCertMetric() *recordingCertMetric {
	return &recordingCertMetric{set: map[string]float64{}, clear: map[string]int{}}
}

func (m *recordingCertMetric) Set(ns, pod string, s float64) { m.set[ns+"/"+pod] = s }
func (m *recordingCertMetric) Clear(ns, pod string)          { m.clear[ns+"/"+pod]++ }

// TestPodReconcileCertIssuanceFailureDrainsPreIdlePod_spec_10_3 verifies the
// §10.3 line 342 cert-issuance grace: a pre-idle pod that has not presented
// a valid certificate within the grace window of its creation is drained for
// replacement (its Sandbox transitions to draining) when the check is enabled.
func TestPodReconcileCertIssuanceFailureDrainsPreIdlePod_spec_10_3(t *testing.T) {
	s := newScheme(t)
	sb := warmingSandbox("pod-noissue")
	pod := managedPod("pod-noissue", "", string(state.Warming), nil) // no cert annotation
	c := newClient(t, s, sb, pod)

	created := getCorePod(t, c, "pod-noissue").CreationTimestamp.Time
	r := &warmpool.PodReconciler{
		Client:              c,
		RequireCertIssuance: true,
		CertIssuanceGrace:   60 * time.Second,
		Now:                 func() time.Time { return created.Add(90 * time.Second) }, // 90s > 60s grace
	}
	reconcilePod(t, r, "pod-noissue")

	if got := getSandbox(t, c, "pod-noissue").Status.Phase; got != string(state.Draining) {
		t.Fatalf("sandbox phase=%q after issuance-grace expiry, want %q", got, state.Draining)
	}
}

// TestPodReconcileCertIssuanceInsideGraceKeepsPreIdlePod_spec_10_3 verifies a
// pre-idle pod still inside the §10.3 grace window is left warming and
// re-queued rather than drained.
func TestPodReconcileCertIssuanceInsideGraceKeepsPreIdlePod_spec_10_3(t *testing.T) {
	s := newScheme(t)
	sb := warmingSandbox("pod-young")
	pod := managedPod("pod-young", "", string(state.Warming), nil)
	c := newClient(t, s, sb, pod)

	created := getCorePod(t, c, "pod-young").CreationTimestamp.Time
	r := &warmpool.PodReconciler{
		Client:              c,
		RequireCertIssuance: true,
		CertIssuanceGrace:   60 * time.Second,
		Now:                 func() time.Time { return created.Add(15 * time.Second) }, // inside grace
	}
	res := reconcilePod(t, r, "pod-young")

	if got := getSandbox(t, c, "pod-young").Status.Phase; got != string(state.Warming) {
		t.Fatalf("sandbox phase=%q for a pod inside the issuance grace, want %q", got, state.Warming)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("RequeueAfter=%v inside the issuance grace, want > 0", res.RequeueAfter)
	}
}

// TestPodReconcileCertIssuanceDisabledKeepsPreIdlePod_spec_10_3 verifies the
// default posture (RequireCertIssuance=false): a pre-idle pod past the grace
// with no certificate is not drained, preserving the no-cert-producer path.
func TestPodReconcileCertIssuanceDisabledKeepsPreIdlePod_spec_10_3(t *testing.T) {
	s := newScheme(t)
	sb := warmingSandbox("pod-default")
	pod := managedPod("pod-default", "", string(state.Warming), nil)
	c := newClient(t, s, sb, pod)

	created := getCorePod(t, c, "pod-default").CreationTimestamp.Time
	r := &warmpool.PodReconciler{
		Client: c,
		Now:    func() time.Time { return created.Add(10 * time.Minute) },
	}
	reconcilePod(t, r, "pod-default")

	if got := getSandbox(t, c, "pod-default").Status.Phase; got != string(state.Warming) {
		t.Fatalf("sandbox phase=%q with issuance check disabled, want %q (unchanged)", got, state.Warming)
	}
}

// TestPodReconcileCertExpiryGaugeEmitted_spec_10_3 verifies the §10.3 line
// 342/343 lenny_cert_expiry_seconds gauge is published through the real
// Reconcile path for a managed pod with a derivable expiry.
func TestPodReconcileCertExpiryGaugeEmitted_spec_10_3(t *testing.T) {
	s := newScheme(t)
	now := time.Now()
	sb := idleSandbox("pod-gauge")
	pod := managedPod("pod-gauge", "", string(state.Idle), map[string]string{
		warmpool.AnnotationCertNotAfter: now.Add(45 * time.Minute).Format(time.RFC3339),
	})
	c := newClient(t, s, sb, pod)

	m := newRecordingCertMetric()
	r := &warmpool.PodReconciler{Client: c, CertMetrics: m, Now: func() time.Time { return now }}
	reconcilePod(t, r, "pod-gauge")

	got, ok := m.set[testNS+"/pod-gauge"]
	if !ok {
		t.Fatalf("cert-expiry gauge not set for pod-gauge; set=%v", m.set)
	}
	if want := (45 * time.Minute).Seconds(); got < want-2 || got > want+2 {
		t.Fatalf("cert-expiry gauge=%v, want ~%v", got, want)
	}
}

// TestPodReconcileCertExpiryGaugeClearedOnMissingPod_spec_10_3 verifies a
// reconcile for a pod that no longer exists clears its gauge series so a
// retired pod cannot pin the CertExpiryImminent alert (§10.3 line 343).
func TestPodReconcileCertExpiryGaugeClearedOnMissingPod_spec_10_3(t *testing.T) {
	s := newScheme(t)
	c := newClient(t, s) // no pods seeded
	m := newRecordingCertMetric()
	r := &warmpool.PodReconciler{Client: c, CertMetrics: m}
	reconcilePod(t, r, "pod-gone")

	if m.clear[testNS+"/pod-gone"] == 0 {
		t.Fatalf("gauge not cleared for a missing pod; clear=%v", m.clear)
	}
}

// TestPodsOnNodeEnqueuesManagedPodsOnNode_spec_4_6 verifies the §4.6.1
// Node→Pod fan-out: a Node event enqueues exactly the managed pods
// scheduled onto that node, and ignores pods on other nodes and
// non-managed pods on the same node.
func TestPodsOnNodeEnqueuesManagedPodsOnNode_spec_4_6(t *testing.T) {
	s := newScheme(t)
	onA1 := managedPod("on-a-1", "node-a", string(state.Idle), nil)
	onA2 := managedPod("on-a-2", "node-a", string(state.Idle), nil)
	onB := managedPod("on-b", "node-b", string(state.Idle), nil)
	foreignOnA := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "foreign-on-a", Namespace: testNS},
		Spec: corev1.PodSpec{
			NodeName:   "node-a",
			Containers: []corev1.Container{{Name: "x", Image: "example/x:latest"}},
		},
	}
	c := newClient(t, s, clusterNode("node-a", false), clusterNode("node-b", false), onA1, onA2, onB, foreignOnA)

	r := &warmpool.PodReconciler{Client: c}
	reqs := r.PodsOnNodeForTest(testContext(), clusterNode("node-a", false))

	got := map[string]bool{}
	for _, rq := range reqs {
		got[rq.Name] = true
	}
	if len(got) != 2 || !got["on-a-1"] || !got["on-a-2"] {
		t.Fatalf("podsOnNode(node-a) enqueued %v, want exactly on-a-1 and on-a-2", got)
	}
}

// uptimeAnnotation returns the annotation map delivering a
// maxPodUptimeSeconds cap to the WarmPoolController's reconcileUptime arm.
func uptimeAnnotation(seconds int64) map[string]string {
	return map[string]string{
		lennyv1.AnnotationMaxPodUptimeSeconds: strconv.FormatInt(seconds, 10),
	}
}

// TestPodReconcileDrainRequestDrainsClaimedPod_spec_4_6 pins the §4.6.3
// drain-request consumer: a claimed (active) pod carrying a gateway-stamped
// lenny.dev/drain-request annotation is transitioned to draining. This is the
// F-5.2.31 regression — the annotation was stamped by the gateway and read by
// no controller, so the unhealthy-threshold and per-release maxSessionsPerPod
// drains never fired end to end. It asserts the corrected outcome (the pod now
// drains) and would fail against the pre-fix reconciler, which read only the
// cert annotations and left a claimed pod untouched.
// spec: 4.6.1, 4.6.3 (gateway stamps drain-request; WarmPoolController writes the drain), 5.2 (unhealthy-slot trigger)
func TestPodReconcileDrainRequestDrainsClaimedPod_spec_4_6(t *testing.T) {
	s := newScheme(t)
	now := time.Now()
	sb := claimedSandbox("pod-drainreq")
	pod := managedPod("pod-drainreq", "", string(state.Claimed), map[string]string{
		lennyv1.AnnotationDrainRequest: now.Format(time.RFC3339Nano),
	})
	c := newClient(t, s, sb, pod)

	r := &warmpool.PodReconciler{Client: c, Now: func() time.Time { return now }}
	reconcilePod(t, r, "pod-drainreq")

	if got := getSandbox(t, c, "pod-drainreq").Status.Phase; got != string(state.Draining) {
		t.Fatalf("sandbox phase=%q after drain-request reconcile, want %q", got, state.Draining)
	}
}

// TestPodReconcileDrainRequestIgnoredWhenAbsent_spec_4_6 verifies a claimed
// pod with no drain-request annotation is not drained: the consumer must act
// only on a live stamp, never on every claimed pod.
// spec: 4.6.3 (drain only when the gateway stamps drain-request)
func TestPodReconcileDrainRequestIgnoredWhenAbsent_spec_4_6(t *testing.T) {
	s := newScheme(t)
	sb := claimedSandbox("pod-noreq")
	pod := managedPod("pod-noreq", "", string(state.Claimed), nil)
	c := newClient(t, s, sb, pod)

	r := &warmpool.PodReconciler{Client: c}
	reconcilePod(t, r, "pod-noreq")

	if got := getSandbox(t, c, "pod-noreq").Status.Phase; got != string(state.Claimed) {
		t.Fatalf("sandbox phase=%q for a pod with no drain-request, want %q", got, state.Claimed)
	}
}

// TestPodReconcileDrainRequestCorruptStampIgnored_spec_4_6 verifies a
// drain-request annotation whose value is not a parseable RFC3339Nano instant
// (a corrupt stamp) does not drive the drain: the consumer must fail closed
// against acting on a malformed stamp.
// spec: 4.6.3 (the stamp value is the RFC3339Nano request instant)
func TestPodReconcileDrainRequestCorruptStampIgnored_spec_4_6(t *testing.T) {
	s := newScheme(t)
	sb := claimedSandbox("pod-corrupt")
	pod := managedPod("pod-corrupt", "", string(state.Claimed), map[string]string{
		lennyv1.AnnotationDrainRequest: "not-a-timestamp",
	})
	c := newClient(t, s, sb, pod)

	r := &warmpool.PodReconciler{Client: c}
	reconcilePod(t, r, "pod-corrupt")

	if got := getSandbox(t, c, "pod-corrupt").Status.Phase; got != string(state.Claimed) {
		t.Fatalf("sandbox phase=%q for a corrupt drain-request stamp, want %q", got, state.Claimed)
	}
}

// TestPodReconcileDrainRequestRecordsReason_spec_4_6 pins the CODE-E "record
// the transition reason" clause: the drain-request consumer, on the edge that
// flips a non-draining Sandbox to draining, emits a structured record naming
// the pool, pod, and reason=drain_request. The drain-request path emits no
// retirement counter (the gateway path that stamped the annotation already
// counted the pod), so the reason record is a log rather than a metric. It
// asserts the record is emitted exactly once on the transition and is not
// re-emitted on a later reconcile of the already-draining pod. It would fail
// against the pre-fix consumer, which drove the transition but recorded no
// reason.
// spec: 4.6.1, 4.6.3 (WarmPoolController-written drain records the transition reason)
func TestPodReconcileDrainRequestRecordsReason_spec_4_6(t *testing.T) {
	s := newScheme(t)
	now := time.Now()
	sb := claimedSandbox("pod-reason")
	pod := managedPod("pod-reason", "", string(state.Claimed), map[string]string{
		lennyv1.AnnotationDrainRequest: now.Format(time.RFC3339Nano),
	})
	c := newClient(t, s, sb, pod)

	var mu sync.Mutex
	var records []logRecord
	ctx := logf.IntoContext(context.Background(), funcr.New(func(prefix, args string) {
		mu.Lock()
		defer mu.Unlock()
		records = append(records, logRecord{msg: prefix, args: args})
	}, funcr.Options{}))

	r := &warmpool.PodReconciler{Client: c, Now: func() time.Time { return now }}
	// First reconcile flips the pod to draining and records the reason.
	if _, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: "pod-reason"},
	}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	// Second reconcile re-reaches the drain-request arm (the pod is still present
	// with a nil DeletionTimestamp while it drains), but the transition is a
	// no-op, so the reason must not be re-recorded.
	if _, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: "pod-reason"},
	}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var reasonRecords int
	for _, rec := range records {
		if strings.Contains(rec.args, "drain_request") {
			reasonRecords++
			if !strings.Contains(rec.args, testPool) || !strings.Contains(rec.args, "pod-reason") {
				t.Fatalf("drain-request reason record %q missing pool %q or pod name", rec.args, testPool)
			}
		}
	}
	if reasonRecords != 1 {
		t.Fatalf("drain-request reason recorded %d times, want exactly 1 (transition edge only)", reasonRecords)
	}
}

// logRecord captures one logr message and its rendered key/value args for the
// reason-record assertion above.
type logRecord struct {
	msg  string
	args string
}

// TestPodReconcileUptimeDrainsOverUptimePod_spec_4_6 pins the §4.6.1/§4.6.3
// level-triggered maxPodUptimeSeconds drain: a claimed pod whose age exceeds
// the gateway-delivered cap is transitioned to draining and the transition
// edge fires OnUptimeRetirement exactly once. This is the F-5.2.31 regression
// — no controller code implemented the CreationTimestamp-derived uptime drain,
// so a claimed/active over-uptime pod (whose placement is frozen and which
// never sees another session release) could never be reclaimed. It asserts the
// corrected outcome and would fail against the pre-fix reconciler.
// spec: 4.6.1, 4.6.3 (CreationTimestamp-derived uptime drain, WarmPoolController-written), 6.2 (concurrent-occupancy uptime edge)
func TestPodReconcileUptimeDrainsOverUptimePod_spec_4_6(t *testing.T) {
	s := newScheme(t)
	sb := claimedSandbox("pod-overuptime")
	pod := managedPod("pod-overuptime", "", string(state.Claimed), uptimeAnnotation(3600))
	c := newClient(t, s, sb, pod)

	created := getCorePod(t, c, "pod-overuptime").CreationTimestamp.Time
	var retired []string
	r := &warmpool.PodReconciler{
		Client:             c,
		Now:                func() time.Time { return created.Add(2 * time.Hour) }, // 2h > 1h cap
		OnUptimeRetirement: func(pool, _ string) { retired = append(retired, pool) },
	}
	reconcilePod(t, r, "pod-overuptime")

	if got := getSandbox(t, c, "pod-overuptime").Status.Phase; got != string(state.Draining) {
		t.Fatalf("sandbox phase=%q after uptime drain, want %q", got, state.Draining)
	}
	if len(retired) != 1 || retired[0] != testPool {
		t.Fatalf("OnUptimeRetirement calls = %v, want exactly one for %q", retired, testPool)
	}
}

// TestPodReconcileUptimePassesRuntimeClass_spec_16_1 pins the CODE-F seam
// contract: the level-triggered uptime drain reports the pod's resolved
// runtime_class (pod.Spec.RuntimeClassName) alongside its pool, so the
// controller-owned lenny_controller_pod_retirement_total series is labeled the
// way §16.1 requires. Against the pre-CODE-F seam (which reported only pool)
// the runtime_class would be empty, so this asserts the corrected outcome.
// spec: 16.1 (lenny_controller_pod_retirement_total{reason,pool,runtime_class})
func TestPodReconcileUptimePassesRuntimeClass_spec_16_1(t *testing.T) {
	s := newScheme(t)
	// envtest runs the RuntimeClass admission plugin, which rejects a pod that
	// references a RuntimeClass that does not exist. Register node/v1 and create
	// the RuntimeClass so the labeled pod is admitted.
	if err := nodev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme nodev1: %v", err)
	}
	rc := "gvisor"
	runtimeClass := &nodev1.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{Name: rc},
		Handler:    rc,
	}
	sb := claimedSandbox("pod-rc-uptime")
	pod := managedPod("pod-rc-uptime", "", string(state.Claimed), uptimeAnnotation(3600))
	pod.Spec.RuntimeClassName = &rc
	c := newClient(t, s, runtimeClass, sb, pod)

	created := getCorePod(t, c, "pod-rc-uptime").CreationTimestamp.Time
	var gotPool, gotRC string
	var calls int
	r := &warmpool.PodReconciler{
		Client: c,
		Now:    func() time.Time { return created.Add(2 * time.Hour) },
		OnUptimeRetirement: func(pool, runtimeClass string) {
			gotPool, gotRC = pool, runtimeClass
			calls++
		},
	}
	reconcilePod(t, r, "pod-rc-uptime")

	if calls != 1 {
		t.Fatalf("OnUptimeRetirement fired %d times, want 1", calls)
	}
	if gotPool != testPool || gotRC != rc {
		t.Fatalf("OnUptimeRetirement(pool=%q, runtimeClass=%q), want (%q, %q)", gotPool, gotRC, testPool, rc)
	}
}

// TestPodReconcileUptimeCountsOncePerPod_spec_4_6 verifies the retirement is
// counted once per pod rather than once per reconcile: a second reconcile of
// the already-draining over-uptime pod re-reaches the level branch (a draining
// Sandbox persists with a nil DeletionTimestamp while its sessions drain) but
// patchSandboxDraining reports no transition, so OnUptimeRetirement does not
// re-fire. This pins the transition-edge gate that prevents the summing
// recording rule from over-reporting.
// spec: 4.6.1 (uptime drain counted once per pod)
func TestPodReconcileUptimeCountsOncePerPod_spec_4_6(t *testing.T) {
	s := newScheme(t)
	sb := claimedSandbox("pod-recount")
	pod := managedPod("pod-recount", "", string(state.Claimed), uptimeAnnotation(3600))
	c := newClient(t, s, sb, pod)

	created := getCorePod(t, c, "pod-recount").CreationTimestamp.Time
	var count int
	r := &warmpool.PodReconciler{
		Client:             c,
		Now:                func() time.Time { return created.Add(2 * time.Hour) },
		OnUptimeRetirement: func(string, string) { count++ },
	}
	reconcilePod(t, r, "pod-recount")
	reconcilePod(t, r, "pod-recount") // pod is already draining now
	reconcilePod(t, r, "pod-recount")

	if count != 1 {
		t.Fatalf("OnUptimeRetirement fired %d times, want exactly 1 (transition-edge gate)", count)
	}
}

// TestPodReconcileUptimeRequeuesInsideWindow_spec_4_6 verifies a claimed pod
// still inside its uptime window is left claimed and re-queued for a re-check
// as the deadline approaches, rather than drained early.
// spec: 4.6.1 (drain only past the CreationTimestamp-derived deadline)
func TestPodReconcileUptimeRequeuesInsideWindow_spec_4_6(t *testing.T) {
	s := newScheme(t)
	sb := claimedSandbox("pod-young-uptime")
	pod := managedPod("pod-young-uptime", "", string(state.Claimed), uptimeAnnotation(3600))
	c := newClient(t, s, sb, pod)

	created := getCorePod(t, c, "pod-young-uptime").CreationTimestamp.Time
	var count int
	r := &warmpool.PodReconciler{
		Client:             c,
		Now:                func() time.Time { return created.Add(10 * time.Minute) }, // 10m < 60m cap
		OnUptimeRetirement: func(string, string) { count++ },
	}
	res := reconcilePod(t, r, "pod-young-uptime")

	if got := getSandbox(t, c, "pod-young-uptime").Status.Phase; got != string(state.Claimed) {
		t.Fatalf("sandbox phase=%q for a pod inside its uptime window, want %q", got, state.Claimed)
	}
	if count != 0 {
		t.Fatalf("OnUptimeRetirement fired %d times inside the window, want 0", count)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("RequeueAfter=%v inside the uptime window, want > 0", res.RequeueAfter)
	}
}

// TestPodReconcileUptimeNoCapNoDrain_spec_4_6 verifies a pod with no
// maxPodUptimeSeconds annotation (a pool that sets no cap) is never drained for
// uptime, however old it is: the gateway stamps the annotation only for a pool
// that sets the cap, and its absence disables the check. This guards against
// draining every no-cap pool.
// spec: 4.6.1 (optional cap; absent annotation disables the uptime drain)
func TestPodReconcileUptimeNoCapNoDrain_spec_4_6(t *testing.T) {
	s := newScheme(t)
	sb := claimedSandbox("pod-nocap")
	pod := managedPod("pod-nocap", "", string(state.Claimed), nil) // no uptime annotation
	c := newClient(t, s, sb, pod)

	created := getCorePod(t, c, "pod-nocap").CreationTimestamp.Time
	var count int
	r := &warmpool.PodReconciler{
		Client:             c,
		Now:                func() time.Time { return created.Add(1000 * time.Hour) },
		OnUptimeRetirement: func(string, string) { count++ },
	}
	reconcilePod(t, r, "pod-nocap")

	if got := getSandbox(t, c, "pod-nocap").Status.Phase; got != string(state.Claimed) {
		t.Fatalf("sandbox phase=%q for a no-cap pod, want %q (never drained for uptime)", got, state.Claimed)
	}
	if count != 0 {
		t.Fatalf("OnUptimeRetirement fired %d times for a no-cap pod, want 0", count)
	}
}

// TestPodReconcileUptimePrecedesDrainRequest_spec_4_6 pins the ordering that
// keeps a both-caps pod counted exactly once (§9 D9): a pod that is over its
// uptime cap and also carries a drain-request stamp (its gateway
// session_count_limit counter suppressed) is transitioned by reconcileUptime
// before the non-counting drain-request consumer can claim the edge, so
// OnUptimeRetirement fires once. Were the drain-request consumer evaluated
// first it would flip the pod, leaving reconcileUptime a no-op and the
// retirement counted zero times across the two processes.
// spec: 4.6.1, 4.6.3 (reconcileUptime precedes the drain-request consumer)
func TestPodReconcileUptimePrecedesDrainRequest_spec_4_6(t *testing.T) {
	s := newScheme(t)
	now := time.Now()
	sb := claimedSandbox("pod-bothcaps")
	ann := uptimeAnnotation(3600)
	ann[lennyv1.AnnotationDrainRequest] = now.Format(time.RFC3339Nano)
	pod := managedPod("pod-bothcaps", "", string(state.Claimed), ann)
	c := newClient(t, s, sb, pod)

	created := getCorePod(t, c, "pod-bothcaps").CreationTimestamp.Time
	var count int
	r := &warmpool.PodReconciler{
		Client:             c,
		Now:                func() time.Time { return created.Add(2 * time.Hour) },
		OnUptimeRetirement: func(string, string) { count++ },
	}
	reconcilePod(t, r, "pod-bothcaps")

	if got := getSandbox(t, c, "pod-bothcaps").Status.Phase; got != string(state.Draining) {
		t.Fatalf("sandbox phase=%q for a both-caps pod, want %q", got, state.Draining)
	}
	if count != 1 {
		t.Fatalf("OnUptimeRetirement fired %d times for a both-caps pod, want exactly 1", count)
	}
}
