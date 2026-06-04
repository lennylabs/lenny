// SPDX-License-Identifier: MIT

package cidrdrift

import (
	"context"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// DefaultInterval is the §13.2 NET-022 drift re-check cadence. The
// spec fixes the continuous drift goroutine at a 5-minute period.
const DefaultInterval = 5 * time.Minute

// kubernetesServiceNamespace / kubernetesServiceName identify the
// always-present apiserver ClusterIP Service the detector probes for the
// §13.2 NET-065 service-CIDR audit. Its ClusterIP is the canonical first
// address of the cluster service range, so an `except` block that fails
// to cover it has failed to exclude the service CIDR.
const (
	kubernetesServiceNamespace = "default"
	kubernetesServiceName      = "kubernetes"
)

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

// Detector is the §13.2 NET-022 / NET-065 continuous cluster-CIDR
// drift detector. It is a leader-elected manager.Runnable: on a
// 5-minute timer it aggregates the cluster's pod CIDRs from Node
// objects, probes the cluster service range from the `kubernetes`
// Service ClusterIP, reads the broad-internet egress NetworkPolicies in
// the configured agent namespaces and the release namespace (the
// gateway and lenny-ops egress rules), and increments
// lenny_network_policy_cidr_drift_total for every cluster pod or
// service CIDR an installed `except` block fails to cover.
type Detector struct {
	// Client is the controller-runtime client. The detector needs
	// get/list on Nodes (cluster-scoped), get on the `kubernetes`
	// Service, and get/list on NetworkPolicies in the audited
	// namespaces.
	Client client.Client
	// AgentNamespaces is the set of namespaces that hold agent pods —
	// the namespaces whose broad-internet egress NetworkPolicies (the
	// `internet` profile) the detector audits.
	AgentNamespaces []string
	// SystemNamespace is the release namespace (lenny-system) that holds
	// the gateway `allow-gateway-egress-llm-upstream` and `lenny-ops-egress`
	// broad-egress rules. §13.2 NET-065 requires the drift audit to cover
	// these two surfaces in addition to the agent `internet` profile. An
	// empty value scans the agent namespaces only.
	SystemNamespace string
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
	if len(d.AgentNamespaces) == 0 && d.SystemNamespace == "" {
		logger.Info("no agent or release namespaces configured; cluster-CIDR drift detection disabled")
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
// CIDRs, probes the cluster service range, reads the broad-internet
// egress NetworkPolicies across the agent and release namespaces, runs
// the pure Detect / DetectServiceCIDRs comparisons, and increments the
// drift counter per Finding.
func (d *Detector) scan(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("cidrdrift")

	podCIDRs, err := d.clusterPodCIDRs(ctx)
	if err != nil {
		return fmt.Errorf("aggregate cluster pod CIDRs: %w", err)
	}
	serviceCIDRs, err := d.clusterServiceCIDRs(ctx)
	if err != nil {
		return fmt.Errorf("probe cluster service CIDR: %w", err)
	}
	if len(podCIDRs) == 0 && len(serviceCIDRs) == 0 {
		// No node reported a pod CIDR and the apiserver Service carried
		// no usable ClusterIP. Normal on a control plane that has not
		// yet scheduled nodes; there is nothing to compare against.
		logger.V(1).Info("no cluster pod or service CIDRs discovered; skipping drift comparison")
		return nil
	}

	policies, err := d.broadEgressPolicies(ctx)
	if err != nil {
		return fmt.Errorf("read audited NetworkPolicies: %w", err)
	}

	if len(podCIDRs) == 0 && hasBroadEgress(policies) {
		// §13.2 NET-022 gap: a managed CNI (EKS without VPC CNI metadata,
		// GKE Autopilot) that does not write Node.spec.podCIDR leaves the
		// pod-CIDR audit unable to run while broad-egress policies are in
		// force. Surface it at Info so the silent-skip is observable
		// rather than passing as clean; the operator must set
		// egressCIDRs.excludeClusterPodCIDR explicitly.
		logger.Info("no Node pod CIDRs reported but broad-egress policies are present; "+
			"pod-CIDR drift detection cannot run on this cluster — set egressCIDRs.excludeClusterPodCIDR explicitly",
			"policies", len(policies))
	}

	findings := Detect(podCIDRs, policies)
	findings = append(findings, DetectServiceCIDRs(serviceCIDRs, policies)...)
	for _, f := range findings {
		driftTotal.WithLabelValues(CanonicalPolicyLabel(f.Policy), f.Field).Inc()
		logger.Info("cluster-CIDR drift detected",
			"namespace", f.Namespace,
			"policy", f.Policy,
			"field", f.Field,
			"clusterCIDR", f.ClusterCIDR,
			"family", f.Family,
			"remediation", "re-run helm upgrade with the corrected egressCIDRs.excludeClusterPodCIDR / excludeClusterServiceCIDR value")
	}
	if len(findings) == 0 {
		logger.V(1).Info("cluster-CIDR drift scan clean",
			"podCIDRs", len(podCIDRs),
			"serviceCIDRs", len(serviceCIDRs),
			"policies", len(policies))
	}
	return nil
}

// hasBroadEgress reports whether any audited policy carries a
// broad-internet egress peer — the precondition for the pod-CIDR audit
// to be meaningful when no Node CIDRs were discovered.
func hasBroadEgress(policies []PolicyEgress) bool {
	for _, pe := range policies {
		if HasBroadInternetEgress(pe) {
			return true
		}
	}
	return false
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

// auditNamespaces returns the deduplicated set of namespaces whose
// NetworkPolicies the detector audits: the agent namespaces (the
// `internet` profile) plus the release namespace (the gateway and
// lenny-ops egress rules, §13.2 NET-065). The release namespace is
// commonly also listed in AgentNamespaces in single-namespace
// deployments, so it is added only when distinct.
func (d *Detector) auditNamespaces() []string {
	out := append([]string(nil), d.AgentNamespaces...)
	if d.SystemNamespace == "" {
		return out
	}
	for _, ns := range out {
		if ns == d.SystemNamespace {
			return out
		}
	}
	return append(out, d.SystemNamespace)
}

// broadEgressPolicies reads every NetworkPolicy in the audited
// namespaces and reduces each to a PolicyEgress. Only the ipBlock peers
// are extracted; pod and namespace selectors carry no `except` block
// and are irrelevant to the CIDR audit.
func (d *Detector) broadEgressPolicies(ctx context.Context) ([]PolicyEgress, error) {
	var out []PolicyEgress
	for _, ns := range d.auditNamespaces() {
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

// clusterServiceCIDRs probes the cluster service range for the §13.2
// NET-065 audit. It reads the always-present `kubernetes` apiserver
// Service in the default namespace and returns its ClusterIP(s) as
// host routes (/32 for IPv4, /128 for IPv6). The apiserver ClusterIP is
// the canonical first address of the service CIDR, so a service IP that
// no `except` block covers means the service range is unexcluded — the
// drift NET-065 closes on clusters with non-RFC1918 service CIDRs. A
// headless or absent Service contributes nothing rather than failing
// the scan.
func (d *Detector) clusterServiceCIDRs(ctx context.Context) ([]string, error) {
	var svc corev1.Service
	err := d.Client.Get(ctx, client.ObjectKey{
		Namespace: kubernetesServiceNamespace,
		Name:      kubernetesServiceName,
	}, &svc)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ips := svc.Spec.ClusterIPs
	if len(ips) == 0 && svc.Spec.ClusterIP != "" {
		ips = []string{svc.Spec.ClusterIP}
	}
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range ips {
		if raw == "" || raw == corev1.ClusterIPNone {
			continue
		}
		host, err := hostCIDR(raw)
		if err != nil {
			log.FromContext(ctx).WithName("cidrdrift").V(1).Info(
				"skipping unparseable kubernetes Service ClusterIP", "clusterIP", raw)
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	sort.Strings(out)
	return out, nil
}

// hostCIDR renders a bare IP as a single-host CIDR (/32 for IPv4, /128
// for IPv6) in canonical masked form so it can be compared against an
// ipBlock `except` block by the same logic that audits pod CIDRs.
func hostCIDR(ip string) (string, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", fmt.Errorf("parse IP %q", ip)
	}
	suffix := "/128"
	if parsed.To4() != nil {
		suffix = "/32"
	}
	return NormalizeCIDR(ip + suffix)
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
