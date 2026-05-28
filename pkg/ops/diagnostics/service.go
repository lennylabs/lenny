// SPDX-License-Identifier: MIT

package diagnostics

import (
	"context"
	"errors"
	"sort"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// §25.6 canonical diagnostic error codes. The HTTP layer maps each to
// its documented status code and §25.2 category.
const (
	// ErrCodeSessionNotFound is SESSION_NOT_FOUND.
	ErrCodeSessionNotFound = "SESSION_NOT_FOUND"
	// ErrCodePoolNotFound is POOL_NOT_FOUND.
	ErrCodePoolNotFound = "POOL_NOT_FOUND"
	// ErrCodeCredentialPoolNotFound is CREDENTIAL_POOL_NOT_FOUND.
	ErrCodeCredentialPoolNotFound = "CREDENTIAL_POOL_NOT_FOUND"
	// ErrCodeDiagnosticsPartial is DIAGNOSTICS_PARTIAL: some data sources
	// were unavailable and the diagnosis is a partial result.
	ErrCodeDiagnosticsPartial = "DIAGNOSTICS_PARTIAL"
)

// Error is a §25.6 diagnostic failure carrying the canonical error code
// so the HTTP handler maps it to the documented status without
// re-classifying.
type Error struct {
	Code    string
	Message string
}

// Error implements error.
func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// CodeOf returns the §25.6 canonical error code carried by err, or the
// empty string when err is not a diagnostics *Error.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// RetryAttempt is one §25.6 session retry-history entry.
type RetryAttempt struct {
	Attempt int    `json:"attempt"`
	Reason  string `json:"reason"`
	At      string `json:"at,omitempty"`
}

// LogReference is the §25.6 SessionDiagnosis pointer to a pod's
// container logs, so an agent can fetch them via GET /v1/admin/logs/-
// pods/{namespace}/{name} without kubectl.
type LogReference struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container,omitempty"`
}

// SessionDiagnosis is the §25.6 GET /v1/admin/diagnostics/sessions/{id}
// response: the structured cause chain for a failed session.
type SessionDiagnosis struct {
	SessionID        string                        `json:"sessionId"`
	State            string                        `json:"state"`
	Runtime          string                        `json:"runtime"`
	Pool             string                        `json:"pool"`
	CauseChain       []CauseChainEntry             `json:"causeChain"`
	RetryHistory     []RetryAttempt                `json:"retryHistory"`
	SuggestedActions []conventions.SuggestedAction `json:"suggestedActions"`
	RelatedLogs      *LogReference                 `json:"relatedLogs,omitempty"`
	Degradation      *conventions.Degradation      `json:"degradation,omitempty"`
}

// PodCountBreakdown is the §25.6 PoolDiagnosis pod-state breakdown.
type PodCountBreakdown struct {
	Idle    int `json:"idle"`
	Warming int `json:"warming"`
	Claimed int `json:"claimed"`
	Failed  int `json:"failed"`
}

// PoolConfigSummary is the §25.6 PoolDiagnosis configuration summary.
type PoolConfigSummary struct {
	MinWarm int    `json:"minWarm"`
	MaxWarm int    `json:"maxWarm,omitempty"`
	Runtime string `json:"runtime,omitempty"`
}

// SyncStatus is the §25.6 PoolDiagnosis CRD-sync status.
type SyncStatus struct {
	Synced bool   `json:"synced"`
	Detail string `json:"detail,omitempty"`
}

// PoolBottleneck is the §25.6 classified warm-pool bottleneck.
type PoolBottleneck struct {
	Category BottleneckCategory `json:"category"`
	Summary  string             `json:"summary"`
}

// PoolDiagnosis is the §25.6 GET /v1/admin/diagnostics/pools/{name}
// response: the warm-pool bottleneck analysis.
type PoolDiagnosis struct {
	Pool             string                        `json:"pool"`
	Status           string                        `json:"status"`
	PodCounts        PodCountBreakdown             `json:"podCounts"`
	Config           PoolConfigSummary             `json:"config"`
	Bottleneck       *PoolBottleneck               `json:"bottleneck,omitempty"`
	SuggestedActions []conventions.SuggestedAction `json:"suggestedActions"`
	CRDSyncStatus    SyncStatus                    `json:"crdSyncStatus"`
	Degradation      *conventions.Degradation      `json:"degradation,omitempty"`
}

// CredentialPoolDiagnosis is the §25.6 GET /v1/admin/diagnostics/-
// credential-pools/{name} response: the credential-pool health
// diagnosis.
type CredentialPoolDiagnosis struct {
	Pool             string                        `json:"pool"`
	Status           string                        `json:"status"`
	Utilization      float64                       `json:"utilization"`
	HotKeys          []string                      `json:"hotKeys,omitempty"`
	SuggestedActions []conventions.SuggestedAction `json:"suggestedActions"`
	Degradation      *conventions.Degradation      `json:"degradation,omitempty"`
}

// ConnectivityDependency is one §25.6 dependency probe result.
type ConnectivityDependency struct {
	Name       string `json:"name"`
	Reachable  bool   `json:"reachable"`
	DurationMs int64  `json:"durationMs"`
	Detail     string `json:"detail,omitempty"`
}

// ConnectivityReport is the §25.6 GET /v1/admin/diagnostics/connectivity
// response: the dependency connectivity checks.
type ConnectivityReport struct {
	Healthy      bool                     `json:"healthy"`
	Dependencies []ConnectivityDependency `json:"dependencies"`
}

// SessionRecord is the §25.6 session-and-pod state the cause chain
// cross-references. The DiagnosticService's data source returns it from
// Postgres (sessions + agent_pod_state) or, on a Postgres outage, from
// the Kubernetes API.
type SessionRecord struct {
	SessionID    string
	State        string
	Runtime      string
	Pool         string
	Signals      Signals
	RetryHistory []RetryAttempt
	Logs         *LogReference
	// Found is false when no session of that id exists in any shard.
	Found bool
}

// PoolRecord is the §25.6 warm-pool state the pool diagnosis reads.
type PoolRecord struct {
	Name      string
	PodCounts PodCountBreakdown
	Config    PoolConfigSummary
	Signals   PoolSignals
	CRDSynced bool
	CRDDetail string
	// Found is false when no pool of that name is registered.
	Found bool
}

// CredentialPoolRecord is the §25.6 credential-pool state the
// credential-pool diagnosis reads.
type CredentialPoolRecord struct {
	Name        string
	Utilization float64
	HotKeys     []string
	RateLimited bool
	// Found is false when no credential pool of that name exists.
	Found bool
}

// DataSource supplies the §25.6 diagnostic inputs. The production
// implementation reads Postgres, the Kubernetes API, and the gateway
// admin API (with the §25.6 fallbacks); tests supply fixed records.
type DataSource interface {
	// Session returns the §25.6 session-and-pod record for diagnosis.
	Session(ctx context.Context, sessionID string) (SessionRecord, error)
	// Pool returns the §25.6 warm-pool record for bottleneck analysis.
	Pool(ctx context.Context, poolName string) (PoolRecord, error)
	// CredentialPool returns the §25.6 credential-pool record.
	CredentialPool(ctx context.Context, poolName string) (CredentialPoolRecord, error)
	// Connectivity runs the §25.6 dependency probes (Postgres, Redis,
	// MinIO, the Kubernetes API, the gateway, and registered connectors).
	Connectivity(ctx context.Context) ([]ConnectivityDependency, error)
}

// DiagnosticService is the §25.6 diagnostic-endpoint contract: it
// encapsulates the diagnosis steps from the operational runbooks so an
// agent calls an API instead of kubectl, psql, or redis-cli.
type DiagnosticService interface {
	DiagnoseSession(ctx context.Context, sessionID string) (*SessionDiagnosis, error)
	DiagnosePool(ctx context.Context, poolName string) (*PoolDiagnosis, error)
	CheckConnectivity(ctx context.Context) (*ConnectivityReport, error)
	DiagnoseCredentialPool(ctx context.Context, poolName string) (*CredentialPoolDiagnosis, error)
}

// Service is the §25.6 DiagnosticService over a DataSource. It applies
// the pure cause-classification and bottleneck-classification logic in
// this package to the records the DataSource returns.
type Service struct {
	source DataSource
}

// NewService returns a DiagnosticService reading from source.
func NewService(source DataSource) *Service {
	return &Service{source: source}
}

// DiagnoseSession builds the §25.6 cause chain for a session. It
// returns SESSION_NOT_FOUND when the session id is unknown in any
// shard.
func (s *Service) DiagnoseSession(ctx context.Context, sessionID string) (*SessionDiagnosis, error) {
	rec, err := s.source.Session(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !rec.Found {
		return nil, &Error{Code: ErrCodeSessionNotFound, Message: "no session " + sessionID}
	}
	chain := PodFailureChain(rec.Signals)
	if chain == nil {
		chain = []CauseChainEntry{}
	}
	diag := &SessionDiagnosis{
		SessionID:        rec.SessionID,
		State:            rec.State,
		Runtime:          rec.Runtime,
		Pool:             rec.Pool,
		CauseChain:       chain,
		RetryHistory:     rec.RetryHistory,
		SuggestedActions: []conventions.SuggestedAction{},
		RelatedLogs:      rec.Logs,
	}
	if diag.RetryHistory == nil {
		diag.RetryHistory = []RetryAttempt{}
	}
	return diag, nil
}

// DiagnosePool builds the §25.6 warm-pool bottleneck analysis. It
// returns POOL_NOT_FOUND when the pool name is not registered.
func (s *Service) DiagnosePool(ctx context.Context, poolName string) (*PoolDiagnosis, error) {
	rec, err := s.source.Pool(ctx, poolName)
	if err != nil {
		return nil, err
	}
	if !rec.Found {
		return nil, &Error{Code: ErrCodePoolNotFound, Message: "no pool " + poolName}
	}
	diag := &PoolDiagnosis{
		Pool:             rec.Name,
		PodCounts:        rec.PodCounts,
		Config:           rec.Config,
		SuggestedActions: []conventions.SuggestedAction{},
		CRDSyncStatus:    SyncStatus{Synced: rec.CRDSynced, Detail: rec.CRDDetail},
	}
	// spec: §25.6 line 2865 — CRD_SYNC_LAG is a §25.6 bottleneck category
	// derived from PoolRecord.CRDSynced, so pass it to the classifier as
	// a PoolSignals flag without requiring every DataSource impl to set it.
	signals := rec.Signals
	if !rec.CRDSynced {
		signals.CRDSyncLag = true
	}
	if category, found := ClassifyPoolBottleneck(signals); found {
		diag.Bottleneck = &PoolBottleneck{
			Category: category,
			Summary:  bottleneckSummary[category],
		}
		diag.Status = "unhealthy"
	} else {
		diag.Status = "healthy"
	}
	return diag, nil
}

// DiagnoseCredentialPool builds the §25.6 credential-pool health
// diagnosis. It returns CREDENTIAL_POOL_NOT_FOUND when the pool is
// unknown.
func (s *Service) DiagnoseCredentialPool(ctx context.Context, poolName string) (*CredentialPoolDiagnosis, error) {
	rec, err := s.source.CredentialPool(ctx, poolName)
	if err != nil {
		return nil, err
	}
	if !rec.Found {
		return nil, &Error{Code: ErrCodeCredentialPoolNotFound, Message: "no credential pool " + poolName}
	}
	status := "healthy"
	switch {
	case rec.RateLimited || rec.Utilization >= 0.9:
		status = "unhealthy"
	case rec.Utilization >= 0.7:
		status = "degraded"
	}
	hotKeys := append([]string(nil), rec.HotKeys...)
	sort.Strings(hotKeys)
	return &CredentialPoolDiagnosis{
		Pool:             rec.Name,
		Status:           status,
		Utilization:      rec.Utilization,
		HotKeys:          hotKeys,
		SuggestedActions: []conventions.SuggestedAction{},
	}, nil
}

// CheckConnectivity runs the §25.6 dependency connectivity checks. The
// report is healthy only when every probed dependency is reachable.
func (s *Service) CheckConnectivity(ctx context.Context) (*ConnectivityReport, error) {
	deps, err := s.source.Connectivity(ctx)
	if err != nil {
		return nil, err
	}
	healthy := true
	for _, d := range deps {
		if !d.Reachable {
			healthy = false
		}
	}
	if deps == nil {
		deps = []ConnectivityDependency{}
	}
	return &ConnectivityReport{Healthy: healthy, Dependencies: deps}, nil
}

// bottleneckSummary is the §25.6 human-readable summary for each
// warm-pool bottleneck category.
var bottleneckSummary = map[BottleneckCategory]string{
	BottleneckDemandExceedsSupply: "claim rate exceeds the warm-pool replenishment rate",
	BottleneckImagePull:           "pods cannot warm because the container image will not pull",
	BottleneckNodePressure:        "pods cannot schedule because of node resource pressure",
	BottleneckQuotaExhausted:      "pods cannot be created against the namespace resource quota",
	BottleneckSetupFailure:        "pods fail during workspace setup",
	BottleneckCRDSyncLag:          "the pool CRD has not converged to the desired state",
}
