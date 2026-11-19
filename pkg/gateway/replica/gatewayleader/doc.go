// SPDX-License-Identifier: MIT

// Package gatewayleader implements the §12.5 gateway-scoped leader
// election that lets exactly one gateway replica run the §12.5 GC
// orchestrator and the other gateway-singleton background sweeps
// (artifact GC, tombstone hard-prune, audit-retention pruner, EventBus
// retranscribe worker, legal-hold reconciler, and the §12.5 T4
// KMS probe).
//
// §12.5 places the GC job inside the gateway process as a leader-elected
// goroutine rather than a CronJob, and runs the gateway-scoped singleton
// jobs under the lenny-gateway-leader Lease, within the failover window
// that section bounds.
//
// The Lease-based LeaseElector mirrors the §25.4 lenny-ops elector and the
// §4.6.1 controller-runtime leader election the WarmPoolController uses,
// but with the gateway lease name and the §12.5 failover bound. The
// AlwaysLeader elector is the single-process / dev fallback used when the
// gateway is not running in-cluster: there is only one replica, so it is
// always the GC writer and every sweep runs.
//
// spec: GW
package gatewayleader
