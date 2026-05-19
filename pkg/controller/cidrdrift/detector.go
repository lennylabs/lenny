// SPDX-License-Identifier: MIT

package cidrdrift

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// DefaultInterval is the §13.2 NET-022 drift re-check cadence. The
// spec fixes the continuous drift goroutine at a 5-minute period.
const DefaultInterval = 5 * time.Minute

// fieldPodCIDR is the value of the lenny_network_policy_cidr_drift_total
// `field` label for a pod-CIDR drift. The detector aggregates node
// spec.podCIDR, so every Finding it reports is a pod-CIDR drift.
const fieldPodCIDR = "pod_cidr"

// driftTotal is the §16.1 lenny_network_policy_cidr_drift_total
// counter. It is labelled `policy` (the drifting NetworkPolicy's name)
// and `field` (pod_cidr or service_cidr) per §13.2 NET-022. The
// detector registers it against the controller-runtime metrics
// registry so it is exposed on the controller's existing /metrics
// endpoint.
//
// Registration happens once at package init. A duplicate registration
// (for example two detectors in one process) is tolerated by
// metrics.MustRegister.
var driftTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_network_policy_cidr_drift_total",
		Help: "NetworkPolicy CIDR drift detections: a cluster pod or service CIDR not covered by a broad-internet egress policy's except block.",
	}, []string{"policy", "field"})
	if err != nil {
		panic(fmt.Sprintf("cidrdrift: build drift counter: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, c)
	return c
}()

// Detector is the §13.2 NET-022 continuous cluster-CIDR drift
// detector. It is a leader-elected manager.Runnable: on a 5-minute
// timer it aggregates the cluster's pod CIDRs from Node objects, reads
// the broad-internet egress NetworkPolicies in the configured agent
// namespaces, and increments lenny_network_policy_cidr_drift_total for
// every cluster CIDR an installed `except` block fails to cover.
type Detector struct {
	// Client is the controller-runtime client. The detector needs
	// get/list on Nodes (cluster-scoped) and get/list on
	// NetworkPolicies in the agent namespaces.
	Client client.Client
	// AgentNamespaces is the set of namespaces that hold agent pods —
	// the namespaces whose broad-internet egress NetworkPolicies the
	// detector audits. An empty slice disables the detector (it logs
	// and performs no scan).
	AgentNamespaces []string
	// Interval is the drift re-check cadence. A non-positive value
	// selects DefaultInterval.
	Interval time.Duration
	// Now returns the current time. It is a field so tests can pin the
	// clock. When nil, time.Now is used.
	Now func() time.Time
}

var (
	_ manager.Runnable               = (*Detector)(nil)
	_ manager.LeaderElectionRunnable = (*Detector)(nil)
)

// NeedLeaderElection reports that only the elected leader runs the
// drift scan, so replicas do not multiply-count the same drift.
func (d *Detector) NeedLeaderElection() bool { return true }

// Start runs the drift-detection loop until ctx is cancelled. It scans
// once immediately so a freshly-elected leader does not wait a full
// interval before auditing, then re-scans every Interval. A scan
// failure is logged and the loop continues so the next tick retries.
func (d *Detector) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("cidrdrift")
	interval := d.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	if len(d.AgentNamespaces) == 0 {
		logger.Info("no agent namespaces configured; cluster-CIDR drift detection disabled")
		return nil
	}

	if err := d.scan(ctx); err != nil {
		logger.Error(err, "initial cluster-CIDR drift scan failed")
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := d.scan(ctx); err != nil {
				logger.Error(err, "cluster-CIDR drift scan failed")
			}
		}
	}
}

// scan performs one drift-detection pass: it aggregates cluster pod
// CIDRs, reads the agent-namespace NetworkPolicies, runs the pure
// Detect comparison, and increments the drift counter per Finding.
func (d *Detector) scan(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("cidrdrift")

	clusterCIDRs, err := d.clusterPodCIDRs(ctx)
	if err != nil {
		return fmt.Errorf("aggregate cluster pod CIDRs: %w", err)
	}
	if len(clusterCIDRs) == 0 {
		// No node reported a pod CIDR. This is normal on a control
		// plane that has not yet scheduled nodes, or on a CNI that does
		// not write spec.podCIDR; there is nothing to compare against.
		logger.V(1).Info("no node pod CIDRs reported; skipping drift comparison")
		return nil
	}

	policies, err := d.broadEgressPolicies(ctx)
	if err != nil {
		return fmt.Errorf("read agent-namespace NetworkPolicies: %w", err)
	}

	findings := Detect(clusterCIDRs, policies)
	for _, f := range findings {
		driftTotal.WithLabelValues(f.Policy, fieldPodCIDR).Inc()
		logger.Info("cluster-CIDR drift detected",
			"namespace", f.Namespace,
			"policy", f.Policy,
			"clusterCIDR", f.ClusterCIDR,
			"family", f.Family,
			"remediation", "re-run helm upgrade with the corrected egressCIDRs.excludeClusterPodCIDR value")
	}
	if len(findings) == 0 {
		logger.V(1).Info("cluster-CIDR drift scan clean",
			"clusterCIDRs", len(clusterCIDRs),
			"policies", len(policies))
	}
	return nil
}

// clusterPodCIDRs aggregates the deduplicated, canonical-form pod
// CIDRs from every Node's spec.podCIDR and spec.podCIDRs. A node that
// reports an unparseable CIDR contributes nothing rather than failing
// the whole scan.
func (d *Detector) clusterPodCIDRs(ctx context.Context) ([]string, error) {
	var nodes corev1.NodeList
	if err := d.Client.List(ctx, &nodes); err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		raw := node.Spec.PodCIDRs
		if len(raw) == 0 && node.Spec.PodCIDR != "" {
			// podCIDRs is the dual-stack-aware field; podCIDR is the
			// older single-stack field. Fall back to podCIDR only when
			// podCIDRs is absent so a dual-stack node is not undercounted.
			raw = []string{node.Spec.PodCIDR}
		}
		for _, c := range raw {
			norm, err := NormalizeCIDR(c)
			if err != nil {
				log.FromContext(ctx).WithName("cidrdrift").V(1).Info(
					"skipping unparseable node pod CIDR", "node", node.Name, "cidr", c,
				)
				continue
			}
			seen[norm] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

// broadEgressPolicies reads every NetworkPolicy in the configured
// agent namespaces and reduces each to a PolicyEgress. Only the
// ipBlock peers are extracted; pod and namespace selectors carry no
// `except` block and are irrelevant to the CIDR audit.
func (d *Detector) broadEgressPolicies(ctx context.Context) ([]PolicyEgress, error) {
	var out []PolicyEgress
	for _, ns := range d.AgentNamespaces {
		var list networkingv1.NetworkPolicyList
		if err := d.Client.List(ctx, &list, client.InNamespace(ns)); err != nil {
			return nil, fmt.Errorf("list NetworkPolicies in %s: %w", ns, err)
		}
		for i := range list.Items {
			out = append(out, policyEgressOf(&list.Items[i]))
		}
	}
	return out, nil
}

// policyEgressOf reduces a NetworkPolicy to the PolicyEgress the
// drift comparison consumes: its identity plus every ipBlock peer
// across all egress rules.
func policyEgressOf(np *networkingv1.NetworkPolicy) PolicyEgress {
	pe := PolicyEgress{Namespace: np.Namespace, Name: np.Name}
	for _, rule := range np.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock == nil {
				continue
			}
			pe.IPBlocks = append(pe.IPBlocks, IPBlockPeer{
				CIDR:   peer.IPBlock.CIDR,
				Except: peer.IPBlock.Except,
			})
		}
	}
	return pe
}
