// SPDX-License-Identifier: MIT

package t4_node_isolation_test

import (
	"strings"
	"testing"

	t4 "github.com/lennylabs/lenny/pkg/admission/t4_node_isolation"
)

// spec: §6.4 (STR-003) / §12.9 — the lenny-t4-node-isolation webhook
// requires a T4 pod to pin a T4 node and tolerate the T4 taint, and
// rejects a non-T4 pod that carries a T4 selector or toleration.

// t4Selector is the deployer-provisioned T4 node label as a
// nodeSelector map.
func t4Selector() map[string]string {
	return map[string]string{t4.NodeLabelKey: t4.NodeLabelValue}
}

// t4Toleration is an Equal toleration matching the §6.4 T4 node taint.
func t4Toleration() t4.Toleration {
	return t4.Toleration{Key: t4.NodeTaintKey, Operator: "Equal", Value: t4.NodeTaintValue}
}

func TestAdmitsT4PodWithSelectorAndToleration(t *testing.T) {
	d := t4.Decide(t4.Request{
		PodName:      "claude-t4-abc",
		Namespace:    "lenny-agents-kata",
		IsT4:         true,
		NodeSelector: t4Selector(),
		Tolerations:  []t4.Toleration{t4Toleration()},
	})
	if !d.Allowed {
		t.Fatalf("a T4 pod with the selector and toleration should be admitted: %+v", d)
	}
	if d.Code != 200 {
		t.Errorf("code = %d, want 200", d.Code)
	}
}

func TestRejectsT4PodMissingToleration(t *testing.T) {
	d := t4.Decide(t4.Request{
		PodName:      "claude-t4-abc",
		Namespace:    "lenny-agents-kata",
		IsT4:         true,
		NodeSelector: t4Selector(),
		// No toleration.
	})
	if d.Allowed {
		t.Fatal("a T4 pod without the T4 taint toleration must be rejected")
	}
	if d.Code != 403 {
		t.Errorf("code = %d, want 403", d.Code)
	}
	// §6.4 pins the STR-003 rejection string verbatim.
	if !strings.Contains(d.Reason, "STR-003") {
		t.Errorf("reason %q does not carry STR-003", d.Reason)
	}
	if !strings.Contains(d.Reason, "missing required nodeSelector/toleration") {
		t.Errorf("reason %q is not the §6.4 message", d.Reason)
	}
}

func TestRejectsT4PodMissingSelector(t *testing.T) {
	d := t4.Decide(t4.Request{
		PodName:     "claude-t4-abc",
		Namespace:   "lenny-agents-kata",
		IsT4:        true,
		Tolerations: []t4.Toleration{t4Toleration()},
		// No nodeSelector and no nodeAffinity.
	})
	if d.Allowed {
		t.Fatal("a T4 pod without a T4 node selector must be rejected")
	}
	if !strings.Contains(d.Reason, "STR-003") {
		t.Errorf("reason %q does not carry STR-003", d.Reason)
	}
}

func TestRejectsT4PodWithNoSchedulingConstraints(t *testing.T) {
	// The fully-bare case: a T4 pod with neither selector nor
	// toleration is rejected — the spec's primary failure mode.
	d := t4.Decide(t4.Request{IsT4: true})
	if d.Allowed {
		t.Fatal("a T4 pod with no scheduling constraints must be rejected")
	}
}

func TestAdmitsT4PodViaNodeAffinityIn(t *testing.T) {
	// §6.4: the T4 node label may be pinned via nodeAffinity rather
	// than nodeSelector. An In match expression on the T4 label
	// satisfies requirement (a).
	d := t4.Decide(t4.Request{
		IsT4: true,
		NodeAffinityTerms: []t4.NodeAffinityTerm{
			{Key: t4.NodeLabelKey, Operator: "In", Values: []string{t4.NodeLabelValue}},
		},
		Tolerations: []t4.Toleration{t4Toleration()},
	})
	if !d.Allowed {
		t.Fatalf("a T4 pod pinning the T4 label via nodeAffinity In should be admitted: %+v", d)
	}
}

func TestAdmitsT4PodViaNodeAffinityExists(t *testing.T) {
	// An Exists match expression on the T4 label also pins the pod to
	// T4-labeled nodes.
	d := t4.Decide(t4.Request{
		IsT4: true,
		NodeAffinityTerms: []t4.NodeAffinityTerm{
			{Key: t4.NodeLabelKey, Operator: "Exists"},
		},
		Tolerations: []t4.Toleration{t4Toleration()},
	})
	if !d.Allowed {
		t.Fatalf("a T4 pod pinning the T4 label via nodeAffinity Exists should be admitted: %+v", d)
	}
}

func TestT4PodNodeAffinityWrongValueRejected(t *testing.T) {
	// A nodeAffinity term on the T4 label key but a non-T4 value does
	// not pin the pod to a T4 node.
	d := t4.Decide(t4.Request{
		IsT4: true,
		NodeAffinityTerms: []t4.NodeAffinityTerm{
			{Key: t4.NodeLabelKey, Operator: "In", Values: []string{"t3"}},
		},
		Tolerations: []t4.Toleration{t4Toleration()},
	})
	if d.Allowed {
		t.Fatal("a nodeAffinity term with a non-T4 value must not satisfy the T4 selector requirement")
	}
}

func TestAdmitsT4PodWithExistsToleration(t *testing.T) {
	// A key-scoped Exists toleration on the T4 taint key tolerates the
	// T4 taint.
	d := t4.Decide(t4.Request{
		IsT4:         true,
		NodeSelector: t4Selector(),
		Tolerations:  []t4.Toleration{{Key: t4.NodeTaintKey, Operator: "Exists"}},
	})
	if !d.Allowed {
		t.Fatalf("a key-scoped Exists toleration should satisfy the T4 taint requirement: %+v", d)
	}
}

func TestAdmitsT4PodWithWildcardToleration(t *testing.T) {
	// An empty-key Exists toleration tolerates every taint, including
	// the T4 taint.
	d := t4.Decide(t4.Request{
		IsT4:         true,
		NodeSelector: t4Selector(),
		Tolerations:  []t4.Toleration{{Operator: "Exists"}},
	})
	if !d.Allowed {
		t.Fatalf("a wildcard Exists toleration should satisfy the T4 taint requirement: %+v", d)
	}
}

func TestAdmitsNonT4PodWithNoT4Scheduling(t *testing.T) {
	// A plain non-T4 pod with no T4 selector or toleration is admitted.
	d := t4.Decide(t4.Request{
		PodName:      "claude-worker-xyz",
		Namespace:    "lenny-agents",
		IsT4:         false,
		NodeSelector: map[string]string{"kubernetes.io/os": "linux"},
	})
	if !d.Allowed {
		t.Fatalf("a non-T4 pod without T4 scheduling should be admitted: %+v", d)
	}
}

func TestRejectsNonT4PodWithT4Selector(t *testing.T) {
	// §6.4: a non-T4 pod carrying a T4 node selector must be rejected
	// so a non-T4 workload cannot occupy a T4-dedicated node.
	d := t4.Decide(t4.Request{
		PodName:      "claude-worker-xyz",
		Namespace:    "lenny-agents",
		IsT4:         false,
		NodeSelector: t4Selector(),
	})
	if d.Allowed {
		t.Fatal("a non-T4 pod with a T4 node selector must be rejected")
	}
	if d.Code != 403 {
		t.Errorf("code = %d, want 403", d.Code)
	}
	if !strings.Contains(d.Reason, "STR-003") {
		t.Errorf("reason %q does not carry STR-003", d.Reason)
	}
}

func TestRejectsNonT4PodWithT4Toleration(t *testing.T) {
	// A non-T4 pod that tolerates the T4 taint must also be rejected.
	d := t4.Decide(t4.Request{
		PodName:     "claude-worker-xyz",
		Namespace:   "lenny-agents",
		IsT4:        false,
		Tolerations: []t4.Toleration{t4Toleration()},
	})
	if d.Allowed {
		t.Fatal("a non-T4 pod that tolerates the T4 taint must be rejected")
	}
	if !strings.Contains(d.Reason, "STR-003") {
		t.Errorf("reason %q does not carry STR-003", d.Reason)
	}
}

func TestRejectsNonT4PodWithT4ToleranceViaNodeAffinity(t *testing.T) {
	// A non-T4 pod pinning the T4 node label via nodeAffinity is also
	// rejected — the selector check is symmetric across nodeSelector
	// and nodeAffinity.
	d := t4.Decide(t4.Request{
		IsT4: false,
		NodeAffinityTerms: []t4.NodeAffinityTerm{
			{Key: t4.NodeLabelKey, Operator: "In", Values: []string{t4.NodeLabelValue}},
		},
	})
	if d.Allowed {
		t.Fatal("a non-T4 pod pinning the T4 node label via nodeAffinity must be rejected")
	}
}
