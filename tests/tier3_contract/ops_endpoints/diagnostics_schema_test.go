// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests that pin each whole §25.6 diagnostic response
// body to a committed JSON Schema derived from the §25.6 Response Types
// (SessionDiagnosis, PoolDiagnosis) and the credential-pool and
// connectivity diagnoses, plus the canonical §25.2 degradation envelope
// each carries. The other diagnostics contract tests assert individual
// field presence; these validate the entire body against a schema with
// additionalProperties disabled, so a renamed field, a dropped field,
// a dropped degradation envelope, or a wrong type fails a test rather
// than shipping. Each endpoint is exercised with a fixture that
// populates the optional fields (bottleneck, relatedLogs, hotKeys, and
// degradation) so the schema pins those branches, and a second fixture
// that omits them so the schema also accepts the primary-source body.
package ops_endpoints_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const (
	sessionDiagnosisSchema   = "tests/tier3_contract/ops_endpoints/testdata/schema/diagnostics_session.json"
	poolDiagnosisSchema      = "tests/tier3_contract/ops_endpoints/testdata/schema/diagnostics_pool.json"
	connectivitySchema       = "tests/tier3_contract/ops_endpoints/testdata/schema/diagnostics_connectivity.json"
	credentialPoolDiagSchema = "tests/tier3_contract/ops_endpoints/testdata/schema/diagnostics_credential_pool.json"
)

// richDiagSource is a §25.6 DataSource whose records populate every
// optional response field — the classified bottleneck, the pod-log
// reference, the credential hot keys, and the canonical degradation
// envelope — so the schema conformance tests validate the fallback body
// an agent reads, not only the primary-source body.
type richDiagSource struct{}

func (richDiagSource) Session(context.Context, string) (diagnostics.SessionRecord, error) {
	return diagnostics.SessionRecord{
		SessionID: "sess-known", State: "failed", Runtime: "python", Pool: "default-gvisor",
		Signals:      diagnostics.Signals{ExitCode: 137, OOMKilled: true},
		RetryHistory: []diagnostics.RetryAttempt{{Attempt: 1, Reason: "OOM_KILLED", At: "2026-04-16T10:20:00Z"}},
		Logs:         &diagnostics.LogReference{Namespace: "lenny-agents", Pod: "sess-known-abc", Container: "agent"},
		Degradation: &conventions.Degradation{
			Level:             conventions.DegradationDegraded,
			PrimarySource:     "postgres",
			ActualSource:      "kubernetes",
			UnavailableFields: []string{"retryHistory"},
		},
		Found: true,
	}, nil
}

func (richDiagSource) Pool(context.Context, string) (diagnostics.PoolRecord, error) {
	return diagnostics.PoolRecord{
		Name: "default-gvisor", Found: true, CRDSynced: true,
		PodCounts: diagnostics.PodCountBreakdown{Idle: 2, Warming: 1, Claimed: 8, Failed: 3},
		Config: diagnostics.PoolConfigSummary{
			MinWarm: 5, MaxWarm: 20, MaxPods: 40, Image: "registry.acme.com/agent:1.2", Runtime: "gvisor",
		},
		Signals: diagnostics.PoolSignals{ImagePullFailures: 2},
		Degradation: &conventions.Degradation{
			Level:         conventions.DegradationDegraded,
			PrimarySource: "prometheus",
			ActualSource:  "gateway-scrape",
		},
	}, nil
}

func (richDiagSource) CredentialPool(context.Context, string) (diagnostics.CredentialPoolRecord, error) {
	return diagnostics.CredentialPoolRecord{
		Name: "anthropic", Utilization: 0.95, Found: true,
		HotKeys: []string{"key-a", "key-b"},
		Degradation: &conventions.Degradation{
			Level:         conventions.DegradationDegraded,
			PrimarySource: "gateway",
			ActualSource:  "in-memory",
		},
	}, nil
}

func (richDiagSource) Connectivity(context.Context) ([]diagnostics.ConnectivityDependency, error) {
	return []diagnostics.ConnectivityDependency{
		{Name: "postgres", Reachable: true, DurationMs: 3},
		{Name: "redis", Reachable: false, DurationMs: 2000, Detail: "dial tcp: i/o timeout"},
	}, nil
}

// diagStatusOK reports whether a §25.6 diagnosis status is a success
// verdict: 200 for a primary-source body or 207 for a body served with
// a degradation envelope (§25.6 partial results).
func diagStatusOK(code int) bool {
	return code == http.StatusOK || code == http.StatusMultiStatus
}

// TestSessionDiagnosisSchemaConformance validates the whole §25.6
// GET /v1/admin/diagnostics/sessions/{id} body against the
// SessionDiagnosis schema, for both a body served from the Kubernetes
// fallback (relatedLogs and the degradation envelope present) and the
// primary-source body (both absent).
//
// spec: 25.6 (SessionDiagnosis — sessionId, state, runtime, pool,
// causeChain, retryHistory, suggestedActions, relatedLogs omitempty,
// degradation omitempty "canonical envelope")
// diagnosis: the SessionDiagnosis body drifted from the §25.6 schema — a
// renamed or dropped identity field, a dropped cause-chain entry field,
// a dropped degradation envelope, or a wrong type. An agent reads the
// cause chain to choose a remediation and the degradation envelope to
// know which fields to disregard, so a malformed body breaks the flow.
func TestSessionDiagnosisSchemaConformance(t *testing.T) {
	schema := schematest.Compile(t, sessionDiagnosisSchema)

	rich := opsServerWithDiagSource(richDiagSource{})
	rec, body := request(t, rich, http.MethodGet, "/v1/admin/diagnostics/sessions/sess-known", nil, nil)
	if !diagStatusOK(rec.Code) {
		t.Fatalf("status = %d, want 200 or 207; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	if _, ok := body["relatedLogs"].(map[string]any); !ok {
		t.Fatalf("fixture did not exercise relatedLogs: %v", body["relatedLogs"])
	}
	if _, ok := body["degradation"].(map[string]any); !ok {
		t.Fatalf("fixture did not exercise the degradation envelope: %v", body["degradation"])
	}
	if err := schema.Validate(body); err != nil {
		t.Fatalf("SessionDiagnosis body violates the §25.6 schema: %v\nbody=%v", err, body)
	}

	// Primary-source body: relatedLogs and degradation absent.
	plain := opsServer(t)
	prec, pbody := request(t, plain, http.MethodGet, "/v1/admin/diagnostics/sessions/sess-known", nil, nil)
	if prec.Code != http.StatusOK {
		t.Fatalf("primary status = %d, want 200; body=%v", prec.Code, pbody)
	}
	if err := schema.Validate(pbody); err != nil {
		t.Fatalf("primary-source SessionDiagnosis body violates the §25.6 schema: %v\nbody=%v", err, pbody)
	}
}

// TestPoolDiagnosisSchemaConformance validates the whole §25.6
// GET /v1/admin/diagnostics/pools/{name} body against the PoolDiagnosis
// schema, for both a body carrying a classified bottleneck plus a
// degradation envelope and the primary-source body.
//
// spec: 25.6 (PoolDiagnosis — pool, status, podCounts, config,
// bottleneck omitempty, suggestedActions, crdSyncStatus, degradation
// omitempty "canonical envelope")
// diagnosis: the PoolDiagnosis body drifted from the §25.6 schema — a
// renamed field, a dropped pod-count breakdown, a dropped bottleneck
// classification, or a dropped degradation envelope. An agent reads the
// bottleneck category to choose between scaling and escalating, so a
// malformed body breaks the warm-pool remediation flow.
func TestPoolDiagnosisSchemaConformance(t *testing.T) {
	schema := schematest.Compile(t, poolDiagnosisSchema)

	rich := opsServerWithDiagSource(richDiagSource{})
	rec, body := request(t, rich, http.MethodGet, "/v1/admin/diagnostics/pools/default-gvisor", nil, nil)
	if !diagStatusOK(rec.Code) {
		t.Fatalf("status = %d, want 200 or 207; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	if _, ok := body["bottleneck"].(map[string]any); !ok {
		t.Fatalf("fixture did not exercise the classified bottleneck: %v", body["bottleneck"])
	}
	if _, ok := body["degradation"].(map[string]any); !ok {
		t.Fatalf("fixture did not exercise the degradation envelope: %v", body["degradation"])
	}
	if err := schema.Validate(body); err != nil {
		t.Fatalf("PoolDiagnosis body violates the §25.6 schema: %v\nbody=%v", err, body)
	}

	plain := opsServer(t)
	prec, pbody := request(t, plain, http.MethodGet, "/v1/admin/diagnostics/pools/default-gvisor", nil, nil)
	if prec.Code != http.StatusOK {
		t.Fatalf("primary status = %d, want 200; body=%v", prec.Code, pbody)
	}
	if err := schema.Validate(pbody); err != nil {
		t.Fatalf("primary-source PoolDiagnosis body violates the §25.6 schema: %v\nbody=%v", err, pbody)
	}
}

// TestConnectivityReportSchemaConformance validates the whole §25.6
// GET /v1/admin/diagnostics/connectivity body against the
// ConnectivityReport schema: the aggregate healthy verdict and the
// per-dependency probe results, including the optional detail on a
// failed probe.
//
// spec: 25.6 (CheckConnectivity — parallel dependency probes with the
// aggregate reachability verdict)
// diagnosis: the ConnectivityReport body drifted from the §25.6 schema —
// a renamed field, a dropped dependency-probe field, or a wrong type.
// The connectivity check is the probe an agent runs to confirm the
// platform's dependencies are reachable, so a malformed body breaks it.
func TestConnectivityReportSchemaConformance(t *testing.T) {
	schema := schematest.Compile(t, connectivitySchema)

	rich := opsServerWithDiagSource(richDiagSource{})
	rec, body := request(t, rich, http.MethodGet, "/v1/admin/diagnostics/connectivity", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	deps, ok := body["dependencies"].([]any)
	if !ok || len(deps) == 0 {
		t.Fatalf("fixture did not exercise the dependency probes: %v", body["dependencies"])
	}
	if err := schema.Validate(body); err != nil {
		t.Fatalf("ConnectivityReport body violates the §25.6 schema: %v\nbody=%v", err, body)
	}
}

// TestCredentialPoolDiagnosisSchemaConformance validates the whole §25.6
// GET /v1/admin/diagnostics/credential-pools/{name} body against the
// CredentialPoolDiagnosis schema, for both a body carrying the hot-keys
// list plus a degradation envelope and the primary-source body.
//
// spec: 25.6 (DiagnoseCredentialPool — the credential-pool health
// diagnosis with utilization and hot-key attribution; the canonical
// degradation envelope on a fallback body)
// diagnosis: the CredentialPoolDiagnosis body drifted from the §25.6
// schema — a renamed field, a dropped utilization, a dropped hot-keys
// list, or a dropped degradation envelope. An agent reads utilization to
// decide whether to add credentials, so a malformed body breaks it.
func TestCredentialPoolDiagnosisSchemaConformance(t *testing.T) {
	schema := schematest.Compile(t, credentialPoolDiagSchema)

	rich := opsServerWithDiagSource(richDiagSource{})
	rec, body := request(t, rich, http.MethodGet, "/v1/admin/diagnostics/credential-pools/anthropic", nil, nil)
	if !diagStatusOK(rec.Code) {
		t.Fatalf("status = %d, want 200 or 207; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	if _, ok := body["hotKeys"].([]any); !ok {
		t.Fatalf("fixture did not exercise the hotKeys list: %v", body["hotKeys"])
	}
	if _, ok := body["degradation"].(map[string]any); !ok {
		t.Fatalf("fixture did not exercise the degradation envelope: %v", body["degradation"])
	}
	if err := schema.Validate(body); err != nil {
		t.Fatalf("CredentialPoolDiagnosis body violates the §25.6 schema: %v\nbody=%v", err, body)
	}

	plain := opsServer(t)
	prec, pbody := request(t, plain, http.MethodGet, "/v1/admin/diagnostics/credential-pools/anthropic", nil, nil)
	if prec.Code != http.StatusOK {
		t.Fatalf("primary status = %d, want 200; body=%v", prec.Code, pbody)
	}
	if err := schema.Validate(pbody); err != nil {
		t.Fatalf("primary-source CredentialPoolDiagnosis body violates the §25.6 schema: %v\nbody=%v", err, pbody)
	}
}
