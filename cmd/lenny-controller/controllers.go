// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"github.com/lennylabs/lenny/pkg/controller/cidrdrift"
	"github.com/lennylabs/lenny/pkg/controller/controllermetrics"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	runtimecontroller "github.com/lennylabs/lenny/pkg/controller/runtime"
	"github.com/lennylabs/lenny/pkg/controller/sandbox"
	"github.com/lennylabs/lenny/pkg/controller/statusdedup"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
)

// registerCoreControllers registers the §4.6.1 controllers that run in every
// lenny-controller posture regardless of the Postgres/Redis/namespace flags:
// the WarmPoolController, the Sandbox-to-Pod reconciler, the per-pod
// certificate/schedulability reconciler, and the claim-driven occupancy
// projection. Each shares the manager and the bounded work-queue factory; the
// WarmPoolController additionally carries the §16.6 ops emitter and the
// agent_pod_state mirror (when --postgres-dsn supplied one).
//
// spec: §4.6.1 — the WarmPoolController reconciles each SandboxWarmPool toward
// its minWarm/maxWarm target, the Sandbox reconciler materializes each Sandbox
// into a backing Pod, the per-pod reconciler drives idle-pod cert expiry and
// host-schedulability, and the occupancy reconciler projects the occupied
// Sandbox phases from per-pod SandboxClaim binding state.
func (w *controllerWiring) registerCoreControllers() {
	f := w.f
	mgr := w.mgr

	warmPool := &warmpool.Reconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Events: w.opsEmitter,
		// §5.3 line 675: validate the pool's RuntimeClass exists before
		// sizing it. The reader-backed checker uses the manager's uncached
		// API reader so the RuntimeClass get needs only the `get` verb the
		// §4.6.3 controller RBAC grants, not a cluster-wide watch.
		RuntimeClasses:            warmpool.NewReaderRuntimeClassChecker(mgr.GetAPIReader()),
		RuntimeClassNameOverrides: w.runtimeClassOverrides,
		InitialFillGracePeriod:    f.initialFillGrace,
		MaxConcurrentReconciles:   f.maxConcurrentReconciles,
		QueueFactory:              w.queueFactory,
	}
	// A nil *agentpodstatepg.Store assigned to the agentpodstate.Store
	// interface field would be a non-nil interface; only assign when a
	// store was actually constructed so the controller's nil check holds.
	if w.mirror != nil {
		warmPool.Mirror = w.mirror
	}
	if err := warmPool.SetupWithManager(mgr); err != nil {
		log.Fatalf("lenny-controller: set up WarmPoolController: %v", err)
	}

	if err := (&sandbox.Reconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		AdapterImage:            f.adapterImage,
		AdapterUID:              f.adapterUID,
		AgentUID:                f.agentUID,
		CredReadersGID:          f.credReadersGID,
		GatewayGRPCAddr:         f.gatewayGRPCAddr,
		EgressCaptureImage:      f.egressCaptureImage,
		DevMode:                 f.devMode,
		SATokenAudience:         f.saTokenAudience,
		AgentServiceAccountName: f.agentServiceAccount,
		DedicatedDNSClusterIP:   f.dedicatedDNSClusterIP,
		// spec: §13.2 — the dedicated CoreDNS Service lives in the release
		// namespace (lenny-system); it is the first dnsConfig search domain.
		ReleaseNamespace:          f.leaderElectNS,
		RuntimeClassNameOverrides: w.runtimeClassOverrides,
		StatusDedup:               statusdedup.New(f.statusDedupWindow),
		MaxConcurrentReconciles:   f.maxConcurrentReconciles,
		QueueFactory:              w.queueFactory,
		// spec: §5.2 / §6.4 line 413 — the resolved resource-class registry
		// the reconciler consults to stamp container CPU/memory requirements.
		ResourceClasses: w.resourceClasses,
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("lenny-controller: set up Sandbox reconciler: %v", err)
	}

	// §4.6.1 per-pod reconciler: the WarmPoolController is the sole
	// evaluator of per-pod host-node schedulability (the
	// lenny.dev/host-schedulable label the §6.2 task_cleanup precondition
	// reads) and of idle-pod certificate expiry (proactive drain-and-
	// recreate of any idle pod inside the cert-replacement window). It
	// watches managed Pods and Nodes; the Node watch re-labels every pod
	// on a node when the node is cordoned or uncordoned.
	if err := (&warmpool.PodReconciler{
		Client:              mgr.GetClient(),
		CertTTL:             f.certTTL,
		CertExpiryThreshold: f.certExpiryThreshold,
		CertIssuanceGrace:   f.certIssuanceGrace,
		RequireCertIssuance: f.requireCertIssuance,
		CertMetrics:         controllermetrics.CertExpiry{},
		// §16.1 lenny_controller_pod_retirement_total: the controller owns the
		// uptime_limit retirement count because the level-triggered
		// maxPodUptimeSeconds drain runs in this process, not the gateway
		// (which suppresses its own uptime_limit emission).
		OnUptimeRetirement:      warmpool.IncControllerUptimeRetirement,
		MaxConcurrentReconciles: f.maxConcurrentReconciles,
		QueueFactory:            w.queueFactory,
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("lenny-controller: set up per-pod reconciler: %v", err)
	}

	// §4.6.1 claim-driven occupancy projection: the WarmPoolController is the
	// sole writer of the coarse Sandbox.status.phase and projects the
	// occupied phases (claimed, reserved, the recycle sdk_connecting re-warm
	// leg, and the claim-DELETE return to idle or draining) as a
	// level-triggered function of the per-pod SandboxClaim binding state. It
	// watches Sandboxes and SandboxClaims (mapping each claim to its owning
	// Sandbox) so a gateway binding-state transition re-projects the pod
	// phase within one reconcile cycle.
	if err := (&warmpool.OccupancyReconciler{
		Client:                  mgr.GetClient(),
		MaxConcurrentReconciles: f.maxConcurrentReconciles,
		QueueFactory:            w.queueFactory,
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("lenny-controller: set up occupancy-projection reconciler: %v", err)
	}
}

// registerOptionalControllers registers the §5.1 RuntimeReconciler, which
// mirrors declarative Runtime CRDs into the gateway runtime registry. It needs
// the Postgres registry buildStores constructs under --postgres-dsn, so it is
// registered only when that registry is present; an in-memory-only controller
// posture has no shared registry to mirror into.
//
// spec: §5.1 — the RuntimeReconciler mirrors each Runtime CRD into the
// runtime_definitions registry the gateway reads at session creation and sets
// the Runtime's Registered condition.
func (w *controllerWiring) registerOptionalControllers() {
	f := w.f
	mgr := w.mgr

	if w.runtimeRegistry != nil {
		if err := (&runtimecontroller.Reconciler{
			Client:                  mgr.GetClient(),
			Scheme:                  mgr.GetScheme(),
			Store:                   w.runtimeRegistry,
			DevMode:                 f.devMode,
			MaxConcurrentReconciles: f.maxConcurrentReconciles,
			QueueFactory:            w.queueFactory,
		}).SetupWithManager(mgr); err != nil {
			log.Fatalf("lenny-controller: set up RuntimeReconciler: %v", err)
		}
	}
}

// registerLeaderRunnables registers the §4.6.1 leader-elected background
// runnables: the agent_pod_state mirror reconciler and the orphan-claim GC
// (Postgres + agent-namespace gated), the §4.6.2/§10.7 PoolScalingController
// (Postgres + agent-namespace gated), the §13.2 NET-022 cluster-CIDR drift
// detector (agent-namespace or release-namespace gated), and the §4.6.1
// leader-lease renewal monitor (leader-election gated). Each runs only on the
// replica holding the controller Lease.
//
// spec: §4.6.1 — mirror reconciliation on recovery and orphan-claim GC are
// leader-elected runnables scoped to the agent namespaces; §4.6.2/§10.7 — the
// PoolScalingController reconciles the admin API's Postgres pool definitions
// into their SandboxTemplate + SandboxWarmPool CRD pair and manages the A/B
// experiment variant-pool lifecycle; §13.2 — the cluster-CIDR drift detector
// audits the broad-internet egress NetworkPolicies; §16.5 — the leader-lease
// monitor publishes lenny_controller_leader_lease_renewal_age_seconds.
func (w *controllerWiring) registerLeaderRunnables() {
	f := w.f
	mgr := w.mgr

	// §4.6.1 mirror reconciliation on recovery and orphan-claim GC are
	// leader-elected runnables scoped to the agent namespaces. Both
	// require Postgres: mirror recovery converges the agent_pod_state
	// table, and the GC loop checks the session store before reclaiming a
	// claim. They are registered only when both --postgres-dsn and
	// --agent-namespaces are set.
	if w.mirror != nil && len(w.agentNamespaces) > 0 {
		if err := mgr.Add(&warmpool.MirrorReconciler{
			Client:     mgr.GetClient(),
			Mirror:     w.mirror,
			Namespaces: w.agentNamespaces,
		}); err != nil {
			log.Fatalf("lenny-controller: set up mirror reconciler: %v", err)
		}
		if err := mgr.Add(&warmpool.ClaimGarbageCollector{
			Client:            mgr.GetClient(),
			Sessions:          w.sessionLookup,
			Namespaces:        w.agentNamespaces,
			OrphanTimeout:     f.claimOrphanTimeout,
			ReservedHoldGrace: f.reservedHoldGrace,
		}); err != nil {
			log.Fatalf("lenny-controller: set up orphan-claim GC: %v", err)
		}
	}

	// §4.6.2 / §10.7 PoolScalingController: reconcile the admin API's
	// Postgres pool definitions into their SandboxTemplate +
	// SandboxWarmPool CRD pair on a 30s timer, and manage the A/B
	// experiment variant-pool lifecycle (active sizing, paused drain,
	// concluded drain-and-delete) plus the base-pool adjustment. It is a
	// leader-elected Runnable that needs the Postgres pool registry and a
	// single target agent namespace for the derived CRDs (§5.1 pools are
	// platform-global; their CRDs materialize in the agent namespace).
	if w.poolScalingPools != nil && len(w.agentNamespaces) > 0 {
		poolSource := poolscaling.PoolConfigSource(&poolscaling.PoolStoreSource{
			Store:     w.poolScalingPools,
			Namespace: w.agentNamespaces[0],
		})
		if w.poolScalingExperiments != nil {
			poolSource = &poolscaling.ExperimentVariantSource{
				Inner:       poolSource,
				Experiments: w.poolScalingExperiments,
				BasePools:   poolscaling.PoolStoreBasePoolResolver{Store: w.poolScalingPools},
			}
		}
		if err := mgr.Add(&poolscaling.Runnable{
			Reconciler: &poolscaling.Reconciler{
				Client: mgr.GetClient(),
				Source: poolSource,
			},
		}); err != nil {
			log.Fatalf("lenny-controller: set up PoolScalingController: %v", err)
		}
	}

	// The §13.2 NET-022 / NET-065 cluster-CIDR drift detector is a
	// leader-elected Runnable: every 5 minutes it compares the cluster's
	// actual pod CIDRs (Node spec.podCIDR) and service range (the
	// `kubernetes` Service ClusterIP) against the broad-internet egress
	// NetworkPolicies on the three audited surfaces — the agent
	// `internet` profile in the agent namespaces plus the gateway
	// `allow-gateway-egress-llm-upstream` and `lenny-ops-egress` rules in
	// the release namespace — and increments
	// lenny_network_policy_cidr_drift_total on drift. It runs whenever an
	// agent or release namespace is configured.
	if len(w.agentNamespaces) > 0 || f.leaderElectNS != "" {
		if err := mgr.Add(&cidrdrift.Detector{
			Client:          mgr.GetClient(),
			AgentNamespaces: w.agentNamespaces,
			SystemNamespace: f.leaderElectNS,
		}); err != nil {
			log.Fatalf("lenny-controller: set up cluster-CIDR drift detector: %v", err)
		}
	}

	// §4.6.1 controller leader-election monitoring: publish the
	// lenny_controller_leader_lease_renewal_age_seconds gauge so the §16.5
	// ControllerLeaderElectionFailed alert can detect a renewal stall. The
	// lease exists only under leader election, so the monitor is registered
	// only then. The monitor uses the manager's uncached API reader so the
	// Lease read needs only the `get` verb the §4.6.3 RBAC grants.
	if f.leaderElect {
		if err := mgr.Add(&controllermetrics.LeaseRenewalMonitor{
			Reader:     mgr.GetAPIReader(),
			Namespace:  f.leaderElectNS,
			Name:       "lenny-warm-pool-controller",
			Controller: "lenny-warm-pool-controller",
		}); err != nil {
			log.Fatalf("lenny-controller: set up leader-lease monitor: %v", err)
		}
	}
}
