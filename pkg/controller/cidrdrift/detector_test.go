// SPDX-License-Identifier: MIT

package cidrdrift_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/controller/cidrdrift"
)

const agentNS = "lenny-agents"

func detectorScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	return s
}

// node builds a Node reporting the given pod CIDR via spec.podCIDRs.
func node(name string, podCIDRs ...string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{PodCIDRs: podCIDRs},
	}
}

// internetPolicy builds a broad-internet egress NetworkPolicy in the
// agent namespace whose 0.0.0.0/0 ipBlock peer carries the given
// except entries.
func internetPolicy(name string, except ...string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: agentNS},
		Spec: networkingv1.NetworkPolicySpec{
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To: []networkingv1.NetworkPolicyPeer{{
					IPBlock: &networkingv1.IPBlock{
						CIDR:   "0.0.0.0/0",
						Except: except,
					},
				}},
			}},
		},
	}
}

// runScan runs one detector scan against a client seeded with objs and
// returns the (policyName, pod_cidr) drift-counter delta across the
// scan. The detector audits the agent namespace only.
func runScan(t *testing.T, policyName string, objs ...client.Object) float64 {
	t.Helper()
	return runScanField(t, &cidrdrift.Detector{AgentNamespaces: []string{agentNS}},
		policyName, cidrdrift.FieldPodCIDR, objs...)
}

// runScanField runs one scan of the supplied detector (Client and Now
// are filled in) against a client seeded with objs and returns the
// (policyLabel, field) drift-counter delta.
func runScanField(t *testing.T, d *cidrdrift.Detector, policyLabel, field string, objs ...client.Object) float64 {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(detectorScheme(t)).
		WithObjects(objs...).
		Build()
	d.Client = c
	if d.Now == nil {
		d.Now = func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) }
	}
	before := cidrdrift.DriftCountField(policyLabel, field)
	if err := d.ScanForTest(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return cidrdrift.DriftCountField(policyLabel, field) - before
}

const systemNS = "lenny-system"

// kubernetesService builds the apiserver ClusterIP Service the §13.2
// NET-065 service-CIDR probe reads. The first IP populates the legacy
// single-stack ClusterIP field; all IPs populate the dual-stack
// ClusterIPs field.
func kubernetesService(clusterIPs ...string) *corev1.Service {
	s := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"},
		Spec:       corev1.ServiceSpec{ClusterIPs: clusterIPs},
	}
	if len(clusterIPs) > 0 {
		s.Spec.ClusterIP = clusterIPs[0]
	}
	return s
}

// systemPolicy builds a broad-internet egress NetworkPolicy in the
// release namespace (the gateway / lenny-ops surfaces NET-065 audits).
func systemPolicy(name string, except ...string) *networkingv1.NetworkPolicy {
	np := internetPolicy(name, except...)
	np.Namespace = systemNS
	return np
}

func TestDetectorIncrementsMetricOnDrift(t *testing.T) {
	// The node's pod CIDR is in the CGNAT range; the policy's except
	// block only lists RFC1918 aggregates — drift.
	delta := runScan(
		t, "drift-policy-a",
		node("node-1", "100.64.0.0/24"),
		internetPolicy("drift-policy-a", "10.0.0.0/8", "192.168.0.0/16"),
	)
	if delta != 1 {
		t.Errorf("drift counter delta = %v, want 1", delta)
	}
}

func TestDetectorNoIncrementWhenExceptCoversCIDR(t *testing.T) {
	delta := runScan(
		t, "clean-policy-a",
		node("node-1", "10.244.0.0/16"),
		internetPolicy("clean-policy-a", "10.244.0.0/16", "169.254.169.254/32"),
	)
	if delta != 0 {
		t.Errorf("drift counter delta = %v, want 0 (except covers the node CIDR)", delta)
	}
}

func TestDetectorAggregatesMultipleNodeCIDRs(t *testing.T) {
	// Two nodes report two distinct pod CIDRs; the policy excepts only
	// one of them, so exactly one drift is recorded.
	delta := runScan(
		t, "partial-policy-a",
		node("node-1", "10.244.0.0/24"),
		node("node-2", "10.244.1.0/24"),
		internetPolicy("partial-policy-a", "10.244.0.0/24"),
	)
	if delta != 1 {
		t.Errorf("drift counter delta = %v, want 1 (one of two node CIDRs uncovered)", delta)
	}
}

func TestDetectorFallsBackToDeprecatedPodCIDRField(t *testing.T) {
	// A node that only sets the older single-stack spec.podCIDR (not
	// podCIDRs) is still aggregated.
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-node"},
		Spec:       corev1.NodeSpec{PodCIDR: "10.244.9.0/24"},
	}
	delta := runScan(
		t, "legacy-policy-a",
		n,
		internetPolicy("legacy-policy-a", "10.0.0.0/8"),
	)
	// 10.0.0.0/8 supernets 10.244.9.0/24 — no drift.
	if delta != 0 {
		t.Errorf("drift counter delta = %v, want 0 (supernet except covers podCIDR)", delta)
	}
}

func TestDetectorSkipsAllowlistOnlyPolicy(t *testing.T) {
	// A policy with only a narrow ipBlock peer (no 0.0.0.0/0) is
	// allowlist-only and is not audited.
	base := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-pod-egress-base", Namespace: agentNS},
		Spec: networkingv1.NetworkPolicySpec{
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To: []networkingv1.NetworkPolicyPeer{{
					IPBlock: &networkingv1.IPBlock{CIDR: "10.96.0.10/32"},
				}},
			}},
		},
	}
	delta := runScan(
		t, "allow-pod-egress-base",
		node("node-1", "10.244.0.0/16"),
		base,
	)
	if delta != 0 {
		t.Errorf("drift counter delta = %v, want 0 (allowlist-only policy not audited)", delta)
	}
}

func TestDetectorNoNodesIsClean(t *testing.T) {
	// With no Node objects there are no cluster CIDRs to compare; the
	// scan is a clean no-op even though a broad policy exists.
	delta := runScan(
		t, "nonode-policy-a",
		internetPolicy("nonode-policy-a"),
	)
	if delta != 0 {
		t.Errorf("drift counter delta = %v, want 0 when no nodes report a pod CIDR", delta)
	}
}

func TestDetectorStartScansImmediatelyThenStops(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(detectorScheme(t)).
		WithObjects(
			node("node-1", "100.64.0.0/24"),
			internetPolicy("start-policy-a", "10.0.0.0/8"),
		).
		Build()
	d := &cidrdrift.Detector{
		Client:          c,
		AgentNamespaces: []string{agentNS},
		Interval:        time.Hour, // long, so only the immediate scan runs
	}

	before := cidrdrift.DriftCount("start-policy-a")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Start(ctx) }()

	// The immediate scan should record the drift quickly.
	deadline := time.After(2 * time.Second)
	for {
		if cidrdrift.DriftCount("start-policy-a")-before >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("detector did not perform its immediate startup scan")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned %v, want nil on context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}

func TestDetectorWithNoAgentNamespacesIsDisabled(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(detectorScheme(t)).Build()
	d := &cidrdrift.Detector{Client: c}
	// Start returns immediately when no agent namespaces are configured.
	if err := d.Start(context.Background()); err != nil {
		t.Errorf("Start returned %v, want nil when disabled", err)
	}
}

func TestDetectorNeedsLeaderElection(t *testing.T) {
	d := &cidrdrift.Detector{}
	if !d.NeedLeaderElection() {
		t.Error("the cluster-CIDR drift detector must run only on the elected leader")
	}
}

// spec: §13.2 NET-065 — the drift audit covers the cluster service CIDR
// (probed via the `kubernetes` Service ClusterIP), reporting under the
// service_cidr field. The node pod CIDR is excepted (no pod drift) but
// the service IP is not, so exactly one service_cidr drift is recorded.
func TestDetectorServiceCIDRDrift_spec_13_2_NET065(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(detectorScheme(t)).
		WithObjects(
			node("node-1", "10.244.0.0/16"),
			kubernetesService("100.64.0.1"), // CGNAT service range, not excepted
			internetPolicy("svc-drift-policy", "10.244.0.0/16"),
		).
		Build()
	d := &cidrdrift.Detector{Client: c, AgentNamespaces: []string{agentNS}}

	podBefore := cidrdrift.DriftCountField("svc-drift-policy", cidrdrift.FieldPodCIDR)
	svcBefore := cidrdrift.DriftCountField("svc-drift-policy", cidrdrift.FieldServiceCIDR)
	if err := d.ScanForTest(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := cidrdrift.DriftCountField("svc-drift-policy", cidrdrift.FieldServiceCIDR) - svcBefore; got != 1 {
		t.Errorf("service_cidr drift delta = %v, want 1", got)
	}
	if got := cidrdrift.DriftCountField("svc-drift-policy", cidrdrift.FieldPodCIDR) - podBefore; got != 0 {
		t.Errorf("pod_cidr drift delta = %v, want 0 (node CIDR is excepted)", got)
	}
}

// spec: §13.2 NET-065 — a service CIDR the except block covers (here a
// supernet of the apiserver ClusterIP) records no drift.
func TestDetectorServiceCIDRCoveredNoDrift(t *testing.T) {
	delta := runScanField(
		t, &cidrdrift.Detector{AgentNamespaces: []string{agentNS}},
		"svc-clean-policy", cidrdrift.FieldServiceCIDR,
		node("node-1", "10.244.0.0/16"),
		kubernetesService("10.96.0.1"),
		internetPolicy("svc-clean-policy", "10.244.0.0/16", "10.96.0.0/12"),
	)
	if delta != 0 {
		t.Errorf("service_cidr drift delta = %v, want 0 (except supernets the service IP)", delta)
	}
}

// spec: §13.2 NET-062 — the service-CIDR probe is dual-stack: each
// ClusterIP is compared against the same-family broad peer. An IPv6
// service IP missing from the ::/0 except block drifts.
func TestDetectorServiceCIDRDualStack(t *testing.T) {
	dualStack := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-v6-policy", Namespace: agentNS},
		Spec: networkingv1.NetworkPolicySpec{
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To: []networkingv1.NetworkPolicyPeer{
					{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{"10.96.0.0/12"}}},
					{IPBlock: &networkingv1.IPBlock{CIDR: "::/0"}}, // no v6 except
				},
			}},
		},
	}
	delta := runScanField(
		t, &cidrdrift.Detector{AgentNamespaces: []string{agentNS}},
		"svc-v6-policy", cidrdrift.FieldServiceCIDR,
		kubernetesService("10.96.0.1", "fd00:1234::1"),
		dualStack,
	)
	// The IPv4 service IP is covered (10.96.0.0/12); the IPv6 one is not.
	if delta != 1 {
		t.Errorf("service_cidr drift delta = %v, want 1 (IPv6 service IP uncovered)", delta)
	}
}

// spec: §13.2 NET-065 — the audit extends to the release namespace's
// gateway `allow-gateway-egress-llm-upstream` rule, reported under the
// canonical `gateway-llm-upstream` policy label rather than the object
// name.
func TestDetectorAuditsSystemGatewayRule_spec_13_2_NET065(t *testing.T) {
	delta := runScanField(
		t,
		&cidrdrift.Detector{AgentNamespaces: []string{agentNS}, SystemNamespace: systemNS},
		"gateway-llm-upstream", cidrdrift.FieldPodCIDR,
		node("node-1", "100.64.0.0/24"),
		systemPolicy("allow-gateway-egress-llm-upstream", "10.0.0.0/8"),
	)
	if delta != 1 {
		t.Errorf("gateway-llm-upstream pod_cidr drift delta = %v, want 1", delta)
	}
}

// spec: §13.2 NET-065 — the `lenny-ops-egress` webhook rule in the
// release namespace is audited and reported under the `ops-egress`
// label. A missing cluster CIDR on this surface is the SSRF gap NET-065
// closes.
func TestDetectorAuditsSystemOpsEgressRule(t *testing.T) {
	delta := runScanField(
		t,
		&cidrdrift.Detector{AgentNamespaces: []string{agentNS}, SystemNamespace: systemNS},
		"ops-egress", cidrdrift.FieldPodCIDR,
		node("node-1", "100.64.0.0/24"),
		systemPolicy("lenny-ops-egress", "10.0.0.0/8"),
	)
	if delta != 1 {
		t.Errorf("ops-egress pod_cidr drift delta = %v, want 1", delta)
	}
}

// spec: §13.2 NET-065 — the detector runs even with no agent namespaces
// so long as the release namespace is configured, so the gateway and
// ops surfaces are still audited on a control-plane-only configuration.
func TestDetectorRunsForSystemNamespaceOnly(t *testing.T) {
	delta := runScanField(
		t,
		&cidrdrift.Detector{SystemNamespace: systemNS},
		"ops-egress", cidrdrift.FieldServiceCIDR,
		kubernetesService("100.64.0.1"),
		systemPolicy("lenny-ops-egress", "10.0.0.0/8"),
	)
	if delta != 1 {
		t.Errorf("ops-egress service_cidr drift delta = %v, want 1 (system-only audit ran)", delta)
	}
}

// spec: §13.2 NET-065 — the service-CIDR audit runs even when no Node
// reports a pod CIDR (a managed CNI), so the service surface is not
// skipped along with the pod surface.
func TestDetectorServiceCIDRDriftWithoutNodes(t *testing.T) {
	delta := runScanField(
		t, &cidrdrift.Detector{AgentNamespaces: []string{agentNS}},
		"nonode-svc-policy", cidrdrift.FieldServiceCIDR,
		kubernetesService("100.64.0.1"),
		internetPolicy("nonode-svc-policy"),
	)
	if delta != 1 {
		t.Errorf("service_cidr drift delta = %v, want 1 with no nodes but a probed service IP", delta)
	}
}

// spec: §13.2 NET-022 — the detector is disabled only when neither an
// agent nor a release namespace is configured.
func TestDetectorDisabledWhenNoNamespaces(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(detectorScheme(t)).Build()
	d := &cidrdrift.Detector{Client: c}
	if err := d.Start(context.Background()); err != nil {
		t.Errorf("Start returned %v, want nil when both namespace sets are empty", err)
	}
}
