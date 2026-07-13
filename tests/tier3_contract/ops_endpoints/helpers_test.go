// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §25 agent-operability endpoint surface
// served by lenny-ops. The suite drives the pkg/ops/opsserver HTTP
// handler via httptest and verifies the §25 request/response shapes,
// the §25.2/§25.4 shared API conventions (pagination, the dry-run/
// confirm pattern, the canonical error envelope), and the §25.12 MCP
// management server.
//
// This file holds the suite's shared construction and request helpers.
package ops_endpoints_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/runbooks"
)

// parseRunbookFrontMatter parses the §25.7 front matter of the contract
// suite's stub runbook.
func parseRunbookFrontMatter() (runbooks.FrontMatter, error) {
	return runbooks.Parse([]byte(warmPoolRunbookMarkdown))
}

// fixedRunningState is a §25.10 RunningStateReader returning a fixed
// running state for the drift contract tests.
type fixedRunningState struct{ state map[string]any }

func (f fixedRunningState) RunningState(context.Context, string) (map[string]any, error) {
	return f.state, nil
}

// stubDiagSource is a §25.6 diagnostics DataSource returning fixed
// records for the diagnostics contract tests. A request for an id that
// does not match the seeded record yields a not-found record so the
// suite can exercise the §25.6 not-found error codes.
type stubDiagSource struct {
	session diagnostics.SessionRecord
	pool    diagnostics.PoolRecord
	creds   diagnostics.CredentialPoolRecord
	deps    []diagnostics.ConnectivityDependency
}

func (s stubDiagSource) Session(_ context.Context, id string) (diagnostics.SessionRecord, error) {
	if id == s.session.SessionID {
		return s.session, nil
	}
	return diagnostics.SessionRecord{Found: false}, nil
}

func (s stubDiagSource) Pool(_ context.Context, name string) (diagnostics.PoolRecord, error) {
	if name == s.pool.Name {
		return s.pool, nil
	}
	return diagnostics.PoolRecord{Found: false}, nil
}

func (s stubDiagSource) CredentialPool(_ context.Context, name string) (diagnostics.CredentialPoolRecord, error) {
	if name == s.creds.Name {
		return s.creds, nil
	}
	return diagnostics.CredentialPoolRecord{Found: false}, nil
}

func (s stubDiagSource) Connectivity(context.Context) ([]diagnostics.ConnectivityDependency, error) {
	return s.deps, nil
}

// stubRunbookSource is a §25.7 RunbookSource serving a fixed runbook so
// the contract suite exercises the runbook index without filesystem
// I/O.
type stubRunbookSource struct{}

// warmPoolRunbookMarkdown is the §25.7 runbook markdown the contract
// suite indexes: front matter for discovery plus one access-pathed step.
const warmPoolRunbookMarkdown = `---
triggers:
  - alert: WarmPoolExhausted
    severity: critical
components:
  - warmPools
symptoms:
  - "session creation returns RUNTIME_UNAVAILABLE"
tags:
  - scaling
  - warm-pool
requires:
  - admin-api
---

# Warm Pool Exhaustion

### Step 1: Check pool status

<!-- access: lenny-ctl -->
` + "```bash\nlenny-ctl diagnose pool <pool-name>\n```" + `

<!-- access: api method=GET path=/v1/admin/diagnostics/pools/{name} -->
` + "```\nGET /v1/admin/diagnostics/pools/<pool-name>\n```" + `

<!-- access: kubectl requires=cluster-access -->
` + "```bash\nkubectl get sandboxes -n lenny-agents -l pool=<pool-name>\nkubectl describe sandbox <pod-name> -n lenny-agents\n```" + `
`

// Runbooks returns the single indexed runbook.
func (stubRunbookSource) Runbooks() []opsserver.Runbook {
	fm, _ := parseRunbookFrontMatter()
	return []opsserver.Runbook{{Name: "warm-pool-exhaustion", FrontMatter: fm}}
}

// Markdown returns the runbook's markdown.
func (stubRunbookSource) Markdown(name string) ([]byte, bool) {
	if name == "warm-pool-exhaustion" {
		return []byte(warmPoolRunbookMarkdown), true
	}
	return nil, false
}

// opsServer builds a fully-wired §25 lenny-ops Server with in-memory
// remediation-lock, escalation, drift, and diagnostic services and a
// stub runbook index so the contract suite exercises every §25
// operability endpoint.
func opsServer(t *testing.T) *opsserver.Server {
	t.Helper()

	driftStore := driftservice.NewMemSnapshotStore()
	if err := driftStore.Put(context.Background(), driftservice.Snapshot{
		ID:           driftservice.SnapshotLive,
		DesiredState: map[string]any{"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(5)}}},
		Source:       driftservice.SourceHelmValues,
		WrittenAt:    time.Now().UTC(),
		WrittenBy:    "helm",
	}); err != nil {
		t.Fatalf("seed drift snapshot: %v", err)
	}
	driftSvc := driftservice.NewService(driftStore, fixedRunningState{
		state: map[string]any{"pools": map[string]any{"default-gvisor": map[string]any{"minWarm": float64(12)}}},
	})

	diagSource := stubDiagSource{
		session: diagnostics.SessionRecord{
			SessionID: "sess-known", State: "failed", Runtime: "python", Pool: "default-gvisor",
			Signals: diagnostics.Signals{ExitCode: 137, OOMKilled: true}, Found: true,
		},
		pool: diagnostics.PoolRecord{
			Name: "default-gvisor", Found: true, CRDSynced: true,
			Signals: diagnostics.PoolSignals{ImagePullFailures: 2},
		},
		creds: diagnostics.CredentialPoolRecord{Name: "anthropic", Utilization: 0.95, Found: true},
		deps: []diagnostics.ConnectivityDependency{
			{Name: "postgres", Reachable: true, DurationMs: 3},
			{Name: "redis", Reachable: true, DurationMs: 1},
		},
	}

	return opsserver.New(opsserver.Options{
		Locks:       coordination.NewMemStore(),
		Escalations: escalation.NewService(nil),
		Drift:       driftSvc,
		Diagnostics: diagnostics.NewService(diagSource),
		Runbooks:    stubRunbookSource{},
	})
}

// opsServerWithAuth builds the same §25 lenny-ops Server as opsServer,
// wired behind the real §25.4 OIDC authentication + role gate (an HMAC
// bearer verifier), so a caller's JWT scope claim reaches the §25.12
// MCP scope-enforcement layer through the production authentication
// path rather than the local-development X-Lenny-Scope header
// fallback.
func opsServerWithAuth(t *testing.T) (*opsserver.Server, *jwt.HMACSigner) {
	t.Helper()

	diagSource := stubDiagSource{
		pool: diagnostics.PoolRecord{
			Name: "default-gvisor", Found: true, CRDSynced: true,
			Signals: diagnostics.PoolSignals{ImagePullFailures: 2},
		},
	}
	signer := jwt.NewHMACSigner("ops-scope-test", []byte("ops-scope-test-secret"))
	srv := opsserver.New(opsserver.Options{
		Locks:       coordination.NewMemStore(),
		Diagnostics: diagnostics.NewService(diagSource),
		Auth: &opsserver.AuthConfig{
			Options:     authmw.Options{Verifier: signer},
			RateLimiter: opsserver.NewRateLimiter(1000, 1000),
		},
	})
	return srv, signer
}

// mintScopedToken signs a bearer carrying sub, the platform-admin role,
// and the given RFC 9068 space-separated scope claim, so a contract
// test can drive the §25.12 MCP scope-enforcement layer with a real
// JWT rather than the dev-only X-Lenny-Scope header.
func mintScopedToken(t *testing.T, signer *jwt.HMACSigner, scope string) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "sa-prod-watchdog-01",
		TenantID: "default",
		Roles:    []auth.Role{auth.RolePlatformAdmin},
		Scope:    scope,
	})
	if err != nil {
		t.Fatalf("sign scoped token: %v", err)
	}
	return tok
}

// request issues an HTTP request against the §25 lenny-ops Server and
// decodes the JSON response body into a map.
func request(t *testing.T, srv *opsserver.Server, method, url string, headers map[string]string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, url, reader)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

// errorEnvelope extracts the §25.2 canonical error envelope's inner
// object from a decoded response body, failing the test when the body
// is not an error envelope.
func errorEnvelope(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	obj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("response is not a §25.2 error envelope: %v", body)
	}
	return obj
}

// jsonHeader is the JSON content type the contract suite asserts on
// every operability response.
const jsonHeader = "application/json"

// assertJSONContentType checks the §25 operability response carries the
// JSON content type.
func assertJSONContentType(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != jsonHeader {
		t.Errorf("Content-Type = %q, want %s", ct, jsonHeader)
	}
}

var _ = http.StatusOK
