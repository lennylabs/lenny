// SPDX-License-Identifier: MIT

// Package pdbwatcher hosts the §10.4 PodDisruptionBudget status
// poller. The poller looks up a named PDB in the gateway namespace
// periodically and inspects Status.DisruptionsAllowed: when zero, the
// PDB is currently rejecting voluntary evictions. Each cycle that
// observes the blocking condition advances the §16
// lenny_pdb_blocked_evictions_total counter so the §16.5
// PDBBlockedEvictions alert (`increase(...) > 5 over 10m`) has a
// usable signal.
//
// The metric semantics here are "polling sample observed blocking",
// not the canonical "API server denied an eviction request". The
// canonical signal lives in the kube-apiserver audit log, which v1
// does not ingest. The poller's count tracks the time the PDB spent
// at DisruptionsAllowed=0, which is the operational state the
// PDBBlockedEvictions alert is designed to surface: voluntary
// evictions arriving while the PDB is in this state would be rejected.
//
// The metric is the §16.1 lenny_pdb_blocked_evictions_total, and the
// alert is the §16.5 PDBBlockedEvictions (§10.4, F-10.4.4).
//
// spec: GW
package pdbwatcher
