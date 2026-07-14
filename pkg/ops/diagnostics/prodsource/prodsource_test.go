// SPDX-License-Identifier: MIT

package prodsource

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
)

// fakePG is a Postgres seam stub for the §25.6 Source composition tests.
type fakePG struct {
	session     SessionRow
	sessionErr  error
	counts      diagnostics.PodCountBreakdown
	countsFound bool
	countsErr   error
	load        CredentialPoolLoad
	loadErr     error
}

func (f *fakePG) Session(context.Context, string) (SessionRow, error) {
	return f.session, f.sessionErr
}

func (f *fakePG) PoolPodCounts(context.Context, string) (diagnostics.PodCountBreakdown, bool, error) {
	return f.counts, f.countsFound, f.countsErr
}

func (f *fakePG) CredentialPoolLoad(context.Context, string) (CredentialPoolLoad, error) {
	return f.load, f.loadErr
}

type fakePods struct {
	sig   diagnostics.Signals
	found bool
	err   error
}

func (f *fakePods) Signals(context.Context, string) (diagnostics.Signals, bool, error) {
	return f.sig, f.found, f.err
}

type fakePools struct {
	cfg    diagnostics.PoolConfigSummary
	synced bool
	detail string
	found  bool
	err    error
}

func (f *fakePools) PoolConfig(context.Context, string) (diagnostics.PoolConfigSummary, bool, string, bool, error) {
	return f.cfg, f.synced, f.detail, f.found, f.err
}

type fakeConn struct {
	deps []diagnostics.ConnectivityDependency
	err  error
}

func (f *fakeConn) Probe(context.Context) ([]diagnostics.ConnectivityDependency, error) {
	return f.deps, f.err
}

// TestSourceSessionNotFound — an unknown session id surfaces Found=false
// so the Service maps it to SESSION_NOT_FOUND. spec: §25.6 line 2885.
func TestSourceSessionNotFound_spec_25_6_2885(t *testing.T) {
	s := &Source{PG: &fakePG{session: SessionRow{Found: false}}}
	rec, err := s.Session(context.Background(), "abc")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if rec.Found {
		t.Fatalf("want Found=false for unknown session")
	}
}

// TestSourceSessionPodSignals — a session with an assigned pod enriches
// the cause signals from the Kubernetes API and carries no degradation.
// spec: §25.6 lines 2899-2906.
func TestSourceSessionPodSignals_spec_25_6_2899(t *testing.T) {
	s := &Source{
		PG: &fakePG{session: SessionRow{
			SessionID: "s1", State: "failed", Runtime: "claude", Pool: "p1",
			PodID: "pod-1", Found: true,
		}},
		Pods:         &fakePods{sig: diagnostics.Signals{ExitCode: 137, OOMKilled: true}, found: true},
		PodNamespace: "lenny-system",
	}
	rec, err := s.Session(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !rec.Found || rec.Signals.ExitCode != 137 || !rec.Signals.OOMKilled {
		t.Fatalf("want enriched OOM signals, got %+v", rec.Signals)
	}
	if rec.Degradation != nil {
		t.Fatalf("want no degradation when pod signals read cleanly, got %+v", rec.Degradation)
	}
	if rec.Logs == nil || rec.Logs.Namespace != "lenny-system" || rec.Logs.Pod != "pod-1" {
		t.Fatalf("want relatedLogs reference, got %+v", rec.Logs)
	}
}

// TestSourceSessionNoKubernetes — a session with a pod but no Kubernetes
// connection degrades: the pod-signal fields are unavailable, but the
// session record is still served. spec: §25.6 lines 2908-2920.
func TestSourceSessionNoKubernetes_spec_25_6_2908(t *testing.T) {
	s := &Source{PG: &fakePG{session: SessionRow{
		SessionID: "s1", State: "failed", PodID: "pod-1", Found: true,
	}}}
	rec, err := s.Session(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if rec.Degradation == nil {
		t.Fatalf("want degradation when no Kubernetes connection")
	}
	if got := rec.Degradation.UnavailableFields; len(got) != 1 || got[0] != "causeChain.podSignals" {
		t.Fatalf("want podSignals unavailable, got %v", got)
	}
}

// TestSourceSessionK8sError — a Kubernetes API error degrades the
// session diagnosis rather than failing it. spec: §25.6 lines 2908-2920.
func TestSourceSessionK8sError_spec_25_6_2908(t *testing.T) {
	s := &Source{
		PG:   &fakePG{session: SessionRow{SessionID: "s1", PodID: "pod-1", Found: true}},
		Pods: &fakePods{err: errors.New("api down")},
	}
	rec, err := s.Session(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if rec.Degradation == nil {
		t.Fatalf("want degradation on Kubernetes error")
	}
}

// TestSourceSessionNoPodNoDegradation — a session that failed for a
// budget reason and was never assigned a pod has no missing pod signals,
// so no degradation is set; the budget cause comes from session state.
// spec: §25.6 line 2890.
func TestSourceSessionNoPodNoDegradation_spec_25_6_2890(t *testing.T) {
	s := &Source{PG: &fakePG{session: SessionRow{
		SessionID: "s1", State: "failed", FailureReason: "budget_exceeded", Found: true,
	}}}
	rec, err := s.Session(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if rec.Degradation != nil {
		t.Fatalf("want no degradation when the session never had a pod, got %+v", rec.Degradation)
	}
}

// TestSourceSessionNilPG — with no Postgres the session diagnosis reports
// not-found (cold start) rather than panicking. spec: §25.6 line 2885.
func TestSourceSessionNilPG_spec_25_6_2885(t *testing.T) {
	s := &Source{}
	rec, err := s.Session(context.Background(), "s1")
	if err != nil || rec.Found {
		t.Fatalf("want not-found cold start, got rec=%+v err=%v", rec, err)
	}
}

// TestSourcePoolFound — a pool with pod counts and gateway config is
// served; only the demand-rate fields are degraded. spec: §25.6 lines
// 2861-2867.
func TestSourcePoolFound_spec_25_6_2861(t *testing.T) {
	s := &Source{
		PG:    &fakePG{counts: diagnostics.PodCountBreakdown{Idle: 2, Failed: 1}, countsFound: true},
		Pools: &fakePools{cfg: diagnostics.PoolConfigSummary{MinWarm: 5, Runtime: "claude"}, synced: true, detail: "synced", found: true},
	}
	rec, err := s.Pool(context.Background(), "p1")
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	if !rec.Found || rec.Config.MinWarm != 5 || !rec.CRDSynced {
		t.Fatalf("want populated pool record, got %+v", rec)
	}
	if rec.Degradation == nil {
		t.Fatalf("want degradation for the unavailable demand rates")
	}
}

// TestSourcePoolNotFound — a pool with no pod rows and no gateway config
// reports not-found. spec: §25.6 line 2885 (POOL_NOT_FOUND).
func TestSourcePoolNotFound_spec_25_6_2885(t *testing.T) {
	s := &Source{
		PG:    &fakePG{countsFound: false},
		Pools: &fakePools{found: false},
	}
	rec, err := s.Pool(context.Background(), "p1")
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	if rec.Found {
		t.Fatalf("want not-found for an unregistered pool")
	}
}

// TestSourcePoolGatewayUnavailable — when the gateway admin config is
// unreadable the pool is still served from Postgres pod counts, the
// config/sync fields are degraded, and CRDSynced stays true so the
// Service does not falsely classify a CRD_SYNC_LAG bottleneck. spec:
// §25.6 lines 2865, 2908-2920.
func TestSourcePoolGatewayUnavailable_spec_25_6_2865(t *testing.T) {
	s := &Source{
		PG:    &fakePG{counts: diagnostics.PodCountBreakdown{Idle: 3}, countsFound: true},
		Pools: &fakePools{err: errors.New("gateway down")},
	}
	rec, err := s.Pool(context.Background(), "p1")
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	if !rec.Found {
		t.Fatalf("want pool served from pod counts")
	}
	if !rec.CRDSynced {
		t.Fatalf("want CRDSynced default true so no false CRD_SYNC_LAG")
	}
	found := false
	for _, f := range rec.Degradation.UnavailableFields {
		if f == "crdSyncStatus" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want crdSyncStatus in unavailable fields, got %v", rec.Degradation.UnavailableFields)
	}
}

// TestSourceCredentialPoolHotKeys — the credential-pool diagnosis derives
// the hot-key list from active leases and degrades the capacity-relative
// utilization. spec: §25.6 hot-key analysis.
func TestSourceCredentialPoolHotKeys_spec_25_6(t *testing.T) {
	s := &Source{PG: &fakePG{load: CredentialPoolLoad{
		ActiveLeases:       6,
		LeasesByCredential: map[string]int{"cred-a": 4, "cred-b": 2},
		Found:              true,
	}}}
	rec, err := s.CredentialPool(context.Background(), "cp1")
	if err != nil {
		t.Fatalf("CredentialPool: %v", err)
	}
	if !rec.Found {
		t.Fatalf("want found credential pool")
	}
	if len(rec.HotKeys) != 2 || rec.HotKeys[0] != "cred-a" {
		t.Fatalf("want cred-a first by lease count, got %v", rec.HotKeys)
	}
	if rec.Degradation == nil {
		t.Fatalf("want degradation for the capacity-relative utilization")
	}
}

// TestSourceCredentialPoolNotFound — a credential pool with no leases is
// not visible in the platform-global lease table. spec: §25.6 line 2885.
func TestSourceCredentialPoolNotFound_spec_25_6_2885(t *testing.T) {
	s := &Source{PG: &fakePG{load: CredentialPoolLoad{Found: false}}}
	rec, err := s.CredentialPool(context.Background(), "cp1")
	if err != nil {
		t.Fatalf("CredentialPool: %v", err)
	}
	if rec.Found {
		t.Fatalf("want not-found for a pool with no active leases")
	}
}

// TestSourceConnectivity — the connectivity report comes from the probe
// reader. spec: §25.6 line 2906.
func TestSourceConnectivity_spec_25_6_2906(t *testing.T) {
	s := &Source{Conn: &fakeConn{deps: []diagnostics.ConnectivityDependency{
		{Name: "postgres", Reachable: true},
		{Name: "redis", Reachable: false, Detail: "dial timeout"},
	}}}
	deps, err := s.Connectivity(context.Background())
	if err != nil {
		t.Fatalf("Connectivity: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("want 2 dependencies, got %d", len(deps))
	}
}

// TestSourceSessionPGError — a Postgres error on the session read
// propagates (it is not a not-found). spec: §25.6 line 2885.
func TestSourceSessionPGError(t *testing.T) {
	s := &Source{PG: &fakePG{sessionErr: errors.New("db down")}}
	if _, err := s.Session(context.Background(), "s1"); err == nil {
		t.Fatalf("want error propagated on Postgres failure")
	}
}

// TestSourceSessionPostgresUnreachableKubernetesFallback pins the §25.6
// Postgres-unreachable fallback: when Postgres cannot be read, the
// session diagnosis must fall back to the Kubernetes API for pod state
// and return a partial result whose degradation envelope reports
// actualSource "kubernetes" and primarySource "postgres", rather than
// propagating the raw Postgres error (which the HTTP layer maps to 500).
// A Kubernetes connection is present here, so the fallback source exists.
//
// This test is skipped: the production Source does not implement the
// fallback yet, and building it faithfully is blocked on an unresolved
// cross-section question — the spec locates the pod via the label
// selector lenny.dev/session-id={id}, but no product code stamps that
// label on pods (the §4.6.1 claim model is per-pod and carries no
// per-session pod annotation), so the fallback as specified cannot find
// the pod in a running cluster. Un-skip once the platform records the
// session→pod mapping the K8s fallback keys on.
//
// spec: §25.6 lines 2918-2922 (Postgres unreachable → 207
// DIAGNOSTICS_PARTIAL, degradation.actualSource="kubernetes",
// primarySource="postgres"; both Postgres and Kubernetes unreachable →
// 503).
func TestSourceSessionPostgresUnreachableKubernetesFallback_spec_25_6_2918(t *testing.T) {
	t.Skip("Postgres-unreachable Kubernetes fallback is unimplemented: spec locates the pod by the lenny.dev/session-id label that no product code stamps; blocked on the session→pod recovery decision")

	// Postgres unreachable, Kubernetes reachable: the diagnosis is served
	// from the K8s API as a partial result.
	s := &Source{
		PG:           &fakePG{sessionErr: errors.New("connection refused")},
		Pods:         &fakePods{sig: diagnostics.Signals{ExitCode: 137, OOMKilled: true}, found: true},
		PodNamespace: "lenny-system",
	}
	rec, err := s.Session(context.Background(), "s1")
	if err != nil {
		t.Fatalf("want K8s fallback, got propagated error: %v", err)
	}
	if !rec.Found {
		t.Fatalf("want the session served from the Kubernetes fallback, got not-found")
	}
	if rec.Degradation == nil {
		t.Fatalf("want a degradation envelope on the Postgres-unreachable fallback")
	}
	if rec.Degradation.ActualSource != "kubernetes" || rec.Degradation.PrimarySource != "postgres" {
		t.Fatalf("want actualSource=kubernetes primarySource=postgres, got %+v", rec.Degradation)
	}

	// Both Postgres and Kubernetes unreachable: no fallback source, so the
	// diagnosis is unavailable (the HTTP layer maps this to 503).
	both := &Source{PG: &fakePG{sessionErr: errors.New("connection refused")}}
	if _, err := both.Session(context.Background(), "s1"); err == nil {
		t.Fatalf("want an unavailable error when both Postgres and Kubernetes are unreachable")
	}
}

// TestSourcePoolPostgresUnreachableKubernetesFallback pins the §25.6 pool
// diagnosis Postgres-unreachable Kubernetes fallback: when the
// agent_pod_state read fails, pool diagnosis must list the pool's pods via
// the Kubernetes API (label selector lenny.dev/pool={name}) and rebuild the
// PodCountBreakdown from pod state, returning a partial result whose
// degradation envelope reports actualSource "kubernetes" and primarySource
// "postgres", rather than propagating the raw Postgres error (which the HTTP
// layer maps to 500). When both Postgres and Kubernetes are unreachable the
// diagnosis is unavailable and the HTTP layer maps it to 503.
//
// This test is skipped: the production Source implements no list-by-pool
// fallback, and building it faithfully is blocked on an unresolved
// cross-section contradiction. The spec derives the four-bucket breakdown
// (Warming, Idle, Claimed, Failed) from .status.phase and a
// lenny.dev/pod-state label, but the platform stamps no such label. It
// stamps the coarse lenny.dev/state label (pkg/sandbox/state), which carries
// only idle, active, and draining: warming and failed carry no label and
// claimed collapses into active, so the coarse label plus pod phase cannot
// reconstruct the four buckets. Un-skip once the platform records a pod-state
// label vocabulary the K8s fallback can key on to rebuild the breakdown.
//
// spec: §25.6 lines 2899 (K8s fallback: list pods by lenny.dev/pool={name},
// derive the state breakdown from .status.phase and lenny.dev/pod-state),
// 2918 (Postgres unreachable → 207 DIAGNOSTICS_PARTIAL,
// degradation.actualSource="kubernetes", primarySource="postgres"), 2922
// (both Postgres and Kubernetes unreachable → 503).
func TestSourcePoolPostgresUnreachableKubernetesFallback_spec_25_6_2899(t *testing.T) {
	t.Skip("Postgres-unreachable Kubernetes fallback for the pool pod-count breakdown is unimplemented: the spec derives the breakdown from a lenny.dev/pod-state label that no product code stamps (the platform stamps only the coarse lenny.dev/state, which cannot reconstruct the Warming/Idle/Claimed/Failed buckets); blocked on the pod-state label-vocabulary reconciliation")

	// Postgres unreachable, Kubernetes reachable: the pod-count breakdown is
	// served from the K8s API as a partial result.
	s := &Source{PG: &fakePG{countsErr: errors.New("connection refused")}}
	rec, err := s.Pool(context.Background(), "acme-pool")
	if err != nil {
		t.Fatalf("want K8s fallback, got propagated error: %v", err)
	}
	if !rec.Found {
		t.Fatalf("want the pool served from the Kubernetes fallback, got not-found")
	}
	if rec.Degradation == nil {
		t.Fatalf("want a degradation envelope on the Postgres-unreachable fallback")
	}
	if rec.Degradation.ActualSource != "kubernetes" || rec.Degradation.PrimarySource != "postgres" {
		t.Fatalf("want actualSource=kubernetes primarySource=postgres, got %+v", rec.Degradation)
	}

	// Both Postgres and Kubernetes unreachable: no fallback source, so the
	// diagnosis is unavailable (the HTTP layer maps this to 503).
	both := &Source{PG: &fakePG{countsErr: errors.New("connection refused")}}
	if _, err := both.Pool(context.Background(), "acme-pool"); err == nil {
		t.Fatalf("want an unavailable error when both Postgres and Kubernetes are unreachable")
	}
}
