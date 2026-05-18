// SPDX-License-Identifier: MIT

// Package t4_node_isolation implements the pure-decision logic of the
// lenny-t4-node-isolation ValidatingAdmissionWebhook per spec §6.4
// (STR-003), §12.8, and §12.9. The webhook is deployed with
// failurePolicy: Fail and intercepts CREATE and UPDATE on Pod
// resources in agent namespaces.
//
// The §6.4 T4 dedicated-node requirement. Node-level disk encryption
// does not provide per-tenant key isolation for T4 workspace data on
// emptyDir; a node compromise exposes every T4 tenant co-located on
// that node. To bound the blast radius, T4 workloads must run on
// dedicated node pools — nodes that carry no non-T4 agent pods. The
// webhook is the admission-time half of the enforcement (the pool
// nodeSelector and the node taint are the other half).
//
// The §6.4 predicate, exactly:
//
//   - A pod is T4 when it carries the lenny.dev/workspace-tier: t4
//     label that the pool controller injects for a T4 Runtime.
//   - A T4 pod is admitted only when both (a) its nodeSelector or
//     nodeAffinity pins a T4 node label and (b) its tolerations
//     include the T4 NoSchedule taint. Failing either rejects with
//     the §6.4 message string.
//   - A non-T4 pod that carries a T4 node selector or a T4 toleration
//     is rejected — this stops a non-T4 workload from landing on a
//     T4-dedicated node.
//
// Fail-closed. The decision logic admits a pod only when the predicate
// holds. A webhook outage rejects via failurePolicy: Fail, so a T4 pod
// cannot reach a shared node while the webhook is down.
//
// The decision logic is split from the webhook HTTP/AdmissionReview
// transport so it can be unit-tested without the controller-runtime
// stack.
package t4_node_isolation

import "fmt"

// The §6.4 deployer-provisioned T4 node-pool identifiers. The pool
// controller injects WorkspaceTierLabel onto every T4 pod; deployers
// label T4 nodes with NodeLabelKey=NodeLabelValue and taint them with
// NodeTaintKey=NodeTaintValue:NoSchedule.
const (
	// WorkspaceTierLabel is the pod label key the pool controller sets
	// to identify a pod's §12.9 workspace tier.
	WorkspaceTierLabel = "lenny.dev/workspace-tier"

	// WorkspaceTierT4 is the WorkspaceTierLabel value marking a T4 pod.
	WorkspaceTierT4 = "t4"

	// NodeLabelKey / NodeLabelValue is the deployer-provisioned label
	// on a dedicated T4 node. A T4 pod must pin it via nodeSelector or
	// nodeAffinity; a non-T4 pod must not.
	NodeLabelKey   = "lenny.dev/workspace-tier"
	NodeLabelValue = "t4"

	// NodeTaintKey / NodeTaintValue is the §6.4 taint
	// (lenny.dev/workspace-tier=t4:NoSchedule) on a dedicated T4 node.
	// A T4 pod must tolerate it; a non-T4 pod must not.
	NodeTaintKey   = "lenny.dev/workspace-tier"
	NodeTaintValue = "t4"
)

// rejectMessage is the §6.4 STR-003 rejection string for a T4 pod that
// is missing the dedicated-node nodeSelector or toleration. The spec
// pins this string verbatim.
const rejectMessage = "T4 pod missing required nodeSelector/toleration for dedicated T4 node pool (STR-003)"

// RejectCode is the error code surfaced on every rejection from this
// webhook.
const RejectCode = "T4_NODE_ISOLATION_VIOLATION"

// NodeAffinityTerm is one node-affinity match expression flattened
// from a pod's nodeAffinity. The webhook flattens
// requiredDuringSchedulingIgnoredDuringExecution match expressions and
// match fields into this form so the decision logic does not depend on
// the corev1 node-affinity type tree.
type NodeAffinityTerm struct {
	// Key is the node label key the term matches on.
	Key string
	// Operator is the selator operator ("In", "Exists", and so on).
	Operator string
	// Values are the accepted values for an "In" operator.
	Values []string
}

// Toleration is one pod toleration flattened to the fields the §6.4
// predicate inspects.
type Toleration struct {
	// Key is the taint key the toleration matches. Empty with operator
	// "Exists" tolerates every taint.
	Key string
	// Operator is "Equal" or "Exists". Empty defaults to "Equal" per
	// the Kubernetes toleration semantics.
	Operator string
	// Value is the taint value matched when Operator is "Equal".
	Value string
}

// Request is the input to Decide: the residency-relevant scheduling
// fields of the admitted Pod.
type Request struct {
	// PodName + Namespace identify the pod, used in rejection messages.
	PodName   string
	Namespace string

	// IsT4 is true when the pod carries lenny.dev/workspace-tier: t4 —
	// the label the pool controller injects for a T4 Runtime.
	IsT4 bool

	// NodeSelector is the pod's spec.nodeSelector map.
	NodeSelector map[string]string

	// NodeAffinityTerms is the pod's
	// nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution
	// match expressions and match fields, flattened.
	NodeAffinityTerms []NodeAffinityTerm

	// Tolerations is the pod's spec.tolerations, flattened.
	Tolerations []Toleration
}

// Decision is the admission outcome.
type Decision struct {
	// Allowed is true when the pod satisfies the §6.4 T4
	// dedicated-node predicate.
	Allowed bool
	// Reason is the rejection message relayed to the offending client
	// when Allowed is false; empty when Allowed is true.
	Reason string
	// Code mirrors the HTTP status the webhook surfaces: 200 on allow,
	// 403 on rejection.
	Code int
}

// Decide applies the §6.4 T4 node-isolation predicate.
//
// For a T4 pod it requires both a T4 node selector (via nodeSelector
// or nodeAffinity) and a T4 taint toleration, rejecting with the §6.4
// STR-003 message when either is absent. For a non-T4 pod it rejects
// any T4 node selector or T4 toleration so a non-T4 workload cannot
// occupy a T4-dedicated node. A non-T4 pod with neither is admitted.
func Decide(r Request) Decision {
	hasSelector := r.pinsT4Node()
	hasToleration := r.toleratesT4Taint()

	if r.IsT4 {
		if hasSelector && hasToleration {
			return Decision{Allowed: true, Code: 200}
		}
		return reject(rejectMessage)
	}

	// Non-T4 pod. It must carry neither a T4 node selector nor a T4
	// toleration — either would let it schedule onto a T4-dedicated
	// node and break the dedicated-node guarantee for T4 tenants.
	if hasSelector {
		return reject(fmt.Sprintf(
			"non-T4 pod %s/%s carries the T4 node selector %s=%s; only T4 pods may "+
				"schedule onto T4-dedicated nodes (STR-003)",
			r.Namespace, r.PodName, NodeLabelKey, NodeLabelValue))
	}
	if hasToleration {
		return reject(fmt.Sprintf(
			"non-T4 pod %s/%s tolerates the T4 node taint %s=%s:NoSchedule; only T4 pods "+
				"may schedule onto T4-dedicated nodes (STR-003)",
			r.Namespace, r.PodName, NodeTaintKey, NodeTaintValue))
	}
	return Decision{Allowed: true, Code: 200}
}

// pinsT4Node reports whether the pod pins a T4 node, via either a
// spec.nodeSelector entry or a required nodeAffinity match expression.
func (r Request) pinsT4Node() bool {
	if r.NodeSelector[NodeLabelKey] == NodeLabelValue {
		return true
	}
	for _, term := range r.NodeAffinityTerms {
		if term.Key != NodeLabelKey {
			continue
		}
		switch term.Operator {
		case "In":
			for _, v := range term.Values {
				if v == NodeLabelValue {
					return true
				}
			}
		case "Exists":
			// An Exists term on the T4 label pins the pod to nodes
			// carrying that label — the §6.4 dedicated T4 nodes.
			return true
		}
	}
	return false
}

// toleratesT4Taint reports whether the pod tolerates the §6.4 T4 node
// taint. A key-scoped Exists toleration, a wildcard Exists toleration
// (empty key), and an Equal toleration matching the taint value all
// count.
func (r Request) toleratesT4Taint() bool {
	for _, tol := range r.Tolerations {
		op := tol.Operator
		if op == "" {
			op = "Equal"
		}
		switch op {
		case "Exists":
			// Empty key with Exists tolerates every taint, including
			// the T4 taint; a key-scoped Exists must name the T4 key.
			if tol.Key == "" || tol.Key == NodeTaintKey {
				return true
			}
		case "Equal":
			if tol.Key == NodeTaintKey && tol.Value == NodeTaintValue {
				return true
			}
		}
	}
	return false
}

// reject builds a denied Decision carrying the §6.4 STR-003 rejection
// code and message.
func reject(msg string) Decision {
	return Decision{
		Allowed: false,
		Code:    403,
		Reason:  fmt.Sprintf("%s: %s", RejectCode, msg),
	}
}
