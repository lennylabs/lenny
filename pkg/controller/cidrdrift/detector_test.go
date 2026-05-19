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
// returns the drift-counter delta for policyName observed across the
// scan.
func runScan(t *testing.T, policyName string, objs ...client.Object) float64 {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(detectorScheme(t)).
		WithObjects(objs...).
		Build()
	d := &cidrdrift.Detector{
		Client:          c,
		AgentNamespaces: []string{agentNS},
		Now:             func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) },
	}
	before := cidrdrift.DriftCount(policyName)
	if err := d.ScanForTest(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return cidrdrift.DriftCount(policyName) - before
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
