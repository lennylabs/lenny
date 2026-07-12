// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25.6 diagnostic degradation envelope —
// the partial-result path an agent reaches when a data source is down.
package ops_endpoints_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// opsServerWithDiagSource builds a §25 lenny-ops Server whose only wired
// subsystem is the diagnostic service over the given data source, so a
// contract test can drive the §25.6 degradation path with a source that
// reports a fallback.
func opsServerWithDiagSource(source diagnostics.DataSource) *opsserver.Server {
	return opsserver.New(opsserver.Options{
		Locks:       coordination.NewMemStore(),
		Diagnostics: diagnostics.NewService(source),
	})
}

// degradedSessionSource is a §25.6 DataSource standing in for a data
// source that served the session record from the Kubernetes API fallback
// after a Postgres outage. It reports the canonical degradation envelope
// §25.6 documents for that outage: actualSource "kubernetes",
// primarySource "postgres", and retryHistory / sessionMetadata among the
// unavailable fields. The contract under test is the opsserver mapping
// from a diagnosis carrying this envelope to the 207 partial-result
// response, independent of which source produced it.
type degradedSessionSource struct {
	stubDiagSource
}

func (d degradedSessionSource) Session(context.Context, string) (diagnostics.SessionRecord, error) {
	return diagnostics.SessionRecord{
		SessionID: "sess-degraded", State: "failed", Runtime: "python", Pool: "default-gvisor",
		Signals: diagnostics.Signals{ExitCode: 137, OOMKilled: true},
		Found:   true,
		Degradation: &conventions.Degradation{
			Level:             conventions.DegradationDegraded,
			PrimarySource:     "postgres",
			ActualSource:      "kubernetes",
			UnavailableFields: []string{"retryHistory", "sessionMetadata"},
		},
	}, nil
}

// TestDiagnoseSessionPartialContract confirms that when the session
// diagnostic is served from the §25.6 Kubernetes fallback (Postgres
// unreachable), the endpoint returns HTTP 207 and the response carries
// the canonical degradation envelope naming the fallback source and the
// fields it could not populate. §25.6 requires this DIAGNOSTICS_PARTIAL
// path so an agent trusts the partial diagnosis and knows which fields
// to disregard.
//
// spec: 25.6 (Postgres unreachable — 207 DIAGNOSTICS_PARTIAL, degradation
// actualSource "kubernetes", primarySource "postgres", unavailableFields
// ["retryHistory", "sessionMetadata"])
// diagnosis: The session diagnostic served from the K8s fallback returned
// 200 instead of 207, or dropped the degradation envelope. An agent that
// sees 200 treats a partial diagnosis as complete and acts on a cause
// chain missing the retry history and session metadata.
func TestDiagnoseSessionPartialContract(t *testing.T) {
	srv := opsServerWithDiagSource(degradedSessionSource{})
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/diagnostics/sessions/sess-degraded", nil, nil)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207 for a diagnosis served from the K8s fallback; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)

	deg, ok := body["degradation"].(map[string]any)
	if !ok {
		t.Fatalf("degradation = %v, want the §25.6 degradation envelope on a partial diagnosis", body["degradation"])
	}
	if deg["actualSource"] != "kubernetes" {
		t.Errorf("degradation.actualSource = %v, want kubernetes (the pod-state fallback)", deg["actualSource"])
	}
	if deg["primarySource"] != "postgres" {
		t.Errorf("degradation.primarySource = %v, want postgres", deg["primarySource"])
	}
	fields, ok := deg["unavailableFields"].([]any)
	if !ok {
		t.Fatalf("degradation.unavailableFields = %v, want the list of fields the fallback could not populate", deg["unavailableFields"])
	}
	if !containsString(fields, "retryHistory") {
		t.Errorf("unavailableFields = %v, want to contain retryHistory (no fallback for the Postgres retry log)", fields)
	}
	// The cause chain is still built from the K8s pod status: exit code
	// 137 plus the OOM flag classifies as OOM_KILLED.
	chain, ok := body["causeChain"].([]any)
	if !ok || len(chain) == 0 {
		t.Fatalf("causeChain = %v, want a non-empty cause chain built from the K8s fallback", body["causeChain"])
	}
	entry, _ := chain[0].(map[string]any)
	if entry["category"] != "OOM_KILLED" {
		t.Errorf("cause category = %v, want OOM_KILLED from the K8s pod status", entry["category"])
	}
}

// containsString reports whether the decoded JSON array contains want.
func containsString(arr []any, want string) bool {
	for _, v := range arr {
		if s, _ := v.(string); s == want {
			return true
		}
	}
	return false
}
