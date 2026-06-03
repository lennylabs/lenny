// SPDX-License-Identifier: MIT

// Package prodsource is the production §25.6 diagnostic DataSource. The
// diagnostics.Service holds the pure cause-classification, bottleneck,
// and degradation logic; this package supplies the records it classifies
// by reading the real platform sources lenny-ops connects to: Postgres
// (sessions, agent_pod_state, credential_leases), the Kubernetes API
// (pod failure signals), the gateway admin API (warm-pool config and CRD
// sync status), and the §25.4 dependency probes (connectivity).
//
// Each source is a small seam so the composition logic has one tested
// implementation independent of the live backends, and so a source that
// is not wired in a given deployment (no Kubernetes connection, no
// metrics source) degrades gracefully: the §25.6 Degradation envelope
// records which fields could not be served and the handler returns 207
// rather than failing the whole diagnosis. spec: §25.6 lines 2885-2920.
// F-25.6.1.
package prodsource

import (
	"context"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
)

// SessionRow is the §25.6 session-and-pod state the Postgres reader
// returns: the sessions row joined to its agent_pod_state row. The pod
// fields are empty when no pod is currently assigned to the session.
type SessionRow struct {
	SessionID     string
	State         string
	Runtime       string
	Pool          string
	FailureClass  string
	FailureReason string
	// PodID, PodState, and NodeName come from the joined agent_pod_state
	// row. PodID is the Kubernetes pod name the signal reader looks up.
	PodID    string
	PodState string
	NodeName string
	// Found is false when no session of that id exists.
	Found bool
}

// CredentialPoolLoad is the §25.6 credential-pool load the Postgres
// reader derives from the platform-global credential_leases table.
type CredentialPoolLoad struct {
	// ActiveLeases is the total number of outstanding leases on the pool.
	ActiveLeases int
	// LeasesByCredential maps each credential id to its active-lease
	// count, the input to the §25.6 hot-key analysis.
	LeasesByCredential map[string]int
	// Found is false when the pool has no lease rows. The lease table is
	// the only platform-global view of a credential pool lenny-ops can
	// read without the tenant-scoped pool config, so a pool with no
	// active leases is not visible here.
	Found bool
}

// Postgres reads the §25.6 session, warm-pool, and credential-pool state
// from the platform Postgres. It is the authoritative source for
// resource existence and state.
type Postgres interface {
	// Session returns the sessions row joined to its agent_pod_state row.
	Session(ctx context.Context, sessionID string) (SessionRow, error)
	// PoolPodCounts returns the per-state pod-count breakdown for a warm
	// pool from agent_pod_state. found is false when the pool has no pod
	// rows.
	PoolPodCounts(ctx context.Context, poolName string) (counts diagnostics.PodCountBreakdown, found bool, err error)
	// CredentialPoolLoad returns the active-lease load for a credential
	// pool from credential_leases.
	CredentialPoolLoad(ctx context.Context, poolName string) (CredentialPoolLoad, error)
}

// PodSignals reads the §25.6 pod failure signals (exit code, OOM kill,
// image-pull failure, node pressure) from the Kubernetes API. It is
// optional: a deployment without a Kubernetes connection serves session
// diagnoses from Postgres alone and marks the pod-signal fields degraded.
type PodSignals interface {
	// Signals returns the failure signals for the named pod. found is
	// false when no such pod exists in the API.
	Signals(ctx context.Context, podName string) (sig diagnostics.Signals, found bool, err error)
}

// PoolConfig reads a warm pool's configuration summary and CRD sync
// status from the gateway admin API. It is optional: without it the pool
// diagnosis serves pod counts from Postgres and marks the config and
// sync-status fields degraded.
type PoolConfig interface {
	// PoolConfig returns the pool's config summary and CRD sync status.
	// found is false when the gateway reports no such pool.
	PoolConfig(ctx context.Context, poolName string) (cfg diagnostics.PoolConfigSummary, synced bool, syncDetail string, found bool, err error)
}

// Connectivity runs the §25.6 dependency connectivity probes.
type Connectivity interface {
	Probe(ctx context.Context) ([]diagnostics.ConnectivityDependency, error)
}

// Source composes the per-backend readers into a diagnostics.DataSource.
// PG and Conn are required; Pods and Pools are optional and their
// absence degrades the affected diagnosis rather than failing it.
type Source struct {
	PG    Postgres
	Pods  PodSignals
	Pools PoolConfig
	Conn  Connectivity
	// PodNamespace is the Kubernetes namespace agent pods run in; it is
	// stamped on the §25.6 SessionDiagnosis.relatedLogs reference so an
	// agent can fetch the pod's logs without knowing the namespace.
	PodNamespace string
}

// Compile-time assertion that *Source satisfies the diagnostics seam.
var _ diagnostics.DataSource = (*Source)(nil)

// Session implements diagnostics.DataSource. It reads the session and
// pod state from Postgres, enriches the pod failure signals from the
// Kubernetes API when a pod is assigned, and records a degradation
// envelope when the pod signals cannot be read.
func (s *Source) Session(ctx context.Context, sessionID string) (diagnostics.SessionRecord, error) {
	if s.PG == nil {
		return diagnostics.SessionRecord{Found: false}, nil
	}
	row, err := s.PG.Session(ctx, sessionID)
	if err != nil {
		return diagnostics.SessionRecord{}, err
	}
	if !row.Found {
		return diagnostics.SessionRecord{Found: false}, nil
	}
	rec := diagnostics.SessionRecord{
		SessionID:     row.SessionID,
		State:         row.State,
		Runtime:       row.Runtime,
		Pool:          row.Pool,
		FailureClass:  row.FailureClass,
		FailureReason: row.FailureReason,
		Found:         true,
	}
	// A pod is currently (or was last) assigned: surface its log
	// reference and enrich the cause chain with its failure signals.
	if row.PodID != "" {
		rec.Logs = &diagnostics.LogReference{Namespace: s.PodNamespace, Pod: row.PodID}
		switch {
		case s.Pods == nil:
			rec.Degradation = podSignalsUnavailable("no Kubernetes connection is configured")
		default:
			sig, found, kerr := s.Pods.Signals(ctx, row.PodID)
			if kerr == nil && found {
				rec.Signals = sig
			} else {
				detail := "the pod was not found in the Kubernetes API"
				if kerr != nil {
					detail = "the Kubernetes API is unreachable: " + kerr.Error()
				}
				rec.Degradation = podSignalsUnavailable(detail)
			}
		}
	}
	return rec, nil
}

// Pool implements diagnostics.DataSource. It reads the pod-count
// breakdown from Postgres (agent_pod_state) and the config plus CRD sync
// status from the gateway admin API, recording a degradation envelope
// for the config and the demand-rate signals that require a source not
// wired into lenny-ops.
func (s *Source) Pool(ctx context.Context, poolName string) (diagnostics.PoolRecord, error) {
	if s.PG == nil {
		return diagnostics.PoolRecord{Found: false}, nil
	}
	counts, countsFound, err := s.PG.PoolPodCounts(ctx, poolName)
	if err != nil {
		return diagnostics.PoolRecord{}, err
	}
	rec := diagnostics.PoolRecord{
		Name:      poolName,
		PodCounts: counts,
		// Default the CRD sync status to synced so an unread status does
		// not falsely classify a CRD_SYNC_LAG bottleneck; an explicit
		// negative is set only when the gateway reports one.
		CRDSynced: true,
	}
	unavailable := []string{"bottleneck.claimRate", "bottleneck.replenishmentRate"}
	cfgFound := false
	if s.Pools != nil {
		cfg, synced, detail, found, perr := s.Pools.PoolConfig(ctx, poolName)
		switch {
		case perr != nil:
			unavailable = append(unavailable, "config", "crdSyncStatus")
		case found:
			rec.Config = cfg
			rec.CRDSynced = synced
			rec.CRDDetail = detail
			cfgFound = true
		}
	} else {
		unavailable = append(unavailable, "config", "crdSyncStatus")
		rec.CRDDetail = "sync status unavailable: no gateway admin connection is configured"
	}
	if !countsFound && !cfgFound {
		return diagnostics.PoolRecord{Found: false}, nil
	}
	rec.Found = true
	// The demand bottleneck (claim rate vs replenishment rate) needs a
	// metrics source lenny-ops does not query in v1; the diagnosis still
	// classifies the infrastructure and CRD-lag bottlenecks, so the
	// result is partial rather than absent. spec: §25.6 lines 2861-2867.
	rec.Degradation = &conventions.Degradation{
		Level:             conventions.DegradationDegraded,
		PrimarySource:     "prometheus",
		ActualSource:      "postgres+gateway-admin",
		FallbackPath:      []string{"postgres", "gateway-admin"},
		UnavailableFields: unavailable,
		Warnings:          []string{"demand-bottleneck rates require a metrics source not wired into lenny-ops"},
	}
	return rec, nil
}

// CredentialPool implements diagnostics.DataSource. It derives the
// credential-pool hot-key load from the platform-global credential_leases
// table. The capacity-relative utilization and rate-limited state require
// the tenant-scoped credential-pool config, which is gated by row-level
// security; those fields are marked degraded.
func (s *Source) CredentialPool(ctx context.Context, poolName string) (diagnostics.CredentialPoolRecord, error) {
	if s.PG == nil {
		return diagnostics.CredentialPoolRecord{Found: false}, nil
	}
	load, err := s.PG.CredentialPoolLoad(ctx, poolName)
	if err != nil {
		return diagnostics.CredentialPoolRecord{}, err
	}
	if !load.Found {
		return diagnostics.CredentialPoolRecord{Found: false}, nil
	}
	rec := diagnostics.CredentialPoolRecord{
		Name:    poolName,
		HotKeys: hotKeys(load.LeasesByCredential),
		Found:   true,
		Degradation: &conventions.Degradation{
			Level:             conventions.DegradationDegraded,
			PrimarySource:     "credential-pool config",
			ActualSource:      "credential_leases",
			FallbackPath:      []string{"credential_leases"},
			UnavailableFields: []string{"utilization", "rateLimited"},
			Warnings:          []string{"capacity-relative utilization and rate-limited state require the tenant-scoped pool config"},
		},
	}
	return rec, nil
}

// Connectivity implements diagnostics.DataSource.
func (s *Source) Connectivity(ctx context.Context) ([]diagnostics.ConnectivityDependency, error) {
	if s.Conn == nil {
		return nil, nil
	}
	return s.Conn.Probe(ctx)
}

// podSignalsUnavailable builds the §25.6 degradation envelope for a
// session diagnosis whose pod failure signals (exit code, OOM kill,
// image-pull, node pressure) could not be read. The session state and
// any budget- or credential-derived cause level still come from
// Postgres, so the diagnosis is degraded rather than absent.
func podSignalsUnavailable(detail string) *conventions.Degradation {
	return &conventions.Degradation{
		Level:             conventions.DegradationDegraded,
		PrimarySource:     "kubernetes",
		ActualSource:      "postgres",
		FallbackPath:      []string{"postgres"},
		UnavailableFields: []string{"causeChain.podSignals"},
		Warnings:          []string{"pod failure signals unavailable: " + detail},
	}
}
