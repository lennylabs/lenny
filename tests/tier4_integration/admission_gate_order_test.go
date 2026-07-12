// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.8 / §11.6 pre-chain AdmissionController
// gate ordering (POL-014) composed through the real cmd/lenny-gateway
// binary. §4.8 states the AdmissionController (circuit breakers) is a
// pre-chain gate evaluated after AuthEvaluator completes at PreAuth and
// before the PostAuth and PreDelegation interceptor chains run: "External
// interceptors at priorities 101-999 run AFTER circuit-breaker evaluation
// — a tripped breaker short-circuits the request before any interceptor
// (built-in or external) is invoked." Per-component tier-1 unit tests pin
// the middleware and the audit reporter in isolation; nothing exercised
// the ordering as one composed request, where a tripped session-creation
// breaker must reject a POST /v1/sessions/start at the HTTP pre-chain gate
// before the registered PostAuth external interceptor (default priority
// 500) ever receives a gRPC call, and must emit the distinct
// admission.circuit_breaker_rejected audit row carrying the authenticated
// caller identity resolved at PreAuth.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	stubinterceptor "github.com/lennylabs/lenny/tests/testinfra/stubs/interceptor"
)

// spec: §4.8 (POL-014) — "`AdmissionController` is evaluated after
// `AuthEvaluator` completes at `PreAuth` and before the `PostAuth` and
// `PreDelegation` interceptor chains run ... External interceptors at
// priorities 101–999 run AFTER circuit-breaker evaluation — a tripped
// breaker short-circuits the request before any interceptor (built-in or
// external) is invoked. A circuit-breaker REJECT produces a distinct
// audit event type `admission.circuit_breaker_rejected` ... carrying the
// breaker name, open-state reason, and ... the authenticated caller
// identity (`caller_sub`, `caller_tenant_id`) because evaluation happens
// after `PreAuth`." §11.6 (circuit breakers).
// diagnosis: the composed POL-014 pre-chain ordering is broken — either a
// tripped session-creation breaker did not short-circuit POST
// /v1/sessions/start at the HTTP admission gate (a registered PostAuth
// external interceptor still received the request, so the breaker runs
// after, not before, the interceptor chain), or the breaker rejection did
// not emit the distinct `admission.circuit_breaker_rejected` audit row
// with the authenticated caller identity that PreAuth resolves. Either is
// a policy-ordering or audit-fidelity defect on the operator safety gate.
func TestCircuitBreakerGateShortCircuitsBeforePostAuthChain_spec_4_8(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})

	// A recording external interceptor registered on the PostAuth phase.
	// The default registration priority is 500, inside the 101–999 band
	// POL-014 says runs strictly after the circuit-breaker gate. It ALLOWs
	// every request so a create the breaker admits proceeds normally; the
	// point is what it records, not what it decides.
	stub := stubinterceptor.Start(t, stubinterceptor.Allow())
	// The CREATE-privileged DDL DSN provisions the per-tenant audit
	// sequence (audit_seq_<tenant>) at tenant creation. The
	// admission.circuit_breaker_rejected row lands on the acme chain, so
	// without it the acme sequence never exists and the durable write is
	// silently dropped. The Postgres container's superuser DSN is
	// CREATE-privileged, so it doubles as the DDL login here.
	gw := gateway.StartWith(t, "--dev-mode", "--agent-runtime", "echo",
		"--postgres-dsn="+pg.DSN,
		"--postgres-billing-audit-ddl-dsn="+pg.DSN,
		"--external-interceptor=name=recorder,endpoint="+stub.Addr()+",phase=PostAuth")
	do := gateReq(t, gw.BaseURL())

	// Bootstrap the acme tenant + echo runtime so a session create is
	// otherwise admissible.
	code, boot := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name":   "echo",
			"image":  "lenny/echo@sha256:abc",
			"labels": map[string]string{"tier": "test"},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d (%v)", code, boot)
	}

	// Control: with no breaker open, a session create reaches the PostAuth
	// chain, so the recorder is invoked. This proves the interceptor is
	// genuinely on the create path — the zero-invocation assertion under an
	// open breaker below is then meaningful rather than vacuous.
	code, created := do(http.MethodPost, "/v1/sessions/start", "", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		t.Fatalf("control create-and-start: status %d (%v), want 201", code, created)
	}
	baseline := stub.Requests()
	if len(baseline) == 0 {
		t.Fatal("PostAuth external interceptor received no request on the control create; it is not on the session-creation path, so a later zero-invocation check would be vacuous")
	}
	if got := baseline[len(baseline)-1].GetPhase(); got != "PostAuth" {
		t.Fatalf("control interceptor invocation phase = %q, want PostAuth", got)
	}

	// Open an operation_type=session_creation breaker via the admin API.
	// The pre-chain gate matches POST /v1/sessions/start (defaultExtract
	// maps it to the session_creation operation tier) on the next request.
	const breakerName = "session-freeze"
	const breakerReason = "operator freeze during incident"
	code, opened := do(http.MethodPost, "/v1/admin/circuit-breakers/"+breakerName+"/open", "platform-admin", map[string]any{
		"reason":     breakerReason,
		"limit_tier": "operation_type",
		"scope":      map[string]any{"operation_type": "session_creation"},
	})
	if code != http.StatusOK {
		t.Fatalf("open breaker: status %d (%v), want 200", code, opened)
	}

	// The tripped breaker must reject the create at the HTTP pre-chain gate.
	code, rej := do(http.MethodPost, "/v1/sessions/start", "", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusServiceUnavailable {
		t.Fatalf("create with open session_creation breaker: status %d (%v), want 503", code, rej)
	}
	errBody, _ := rej["error"].(map[string]any)
	if errBody == nil {
		t.Fatalf("breaker rejection missing error envelope: %v", rej)
	}
	if c, _ := errBody["code"].(string); c != "CIRCUIT_BREAKER_OPEN" {
		t.Errorf("rejection error code = %q, want CIRCUIT_BREAKER_OPEN", c)
	}

	// POL-014 core invariant: the breaker short-circuits before any
	// interceptor at priority 101–999 runs. The PostAuth recorder must have
	// received no additional request beyond the control invocation.
	if after := stub.Requests(); len(after) != len(baseline) {
		t.Fatalf("PostAuth external interceptor was invoked %d time(s) after the breaker tripped (baseline %d); a pre-chain gate must short-circuit before the PostAuth chain runs, so the count must not grow",
			len(after)-len(baseline), len(baseline))
	}

	// The rejection must emit the distinct admission.circuit_breaker_rejected
	// audit row carrying the authenticated caller identity PreAuth resolved.
	// The row is written synchronously to the durable per-tenant chain on
	// the request path; poll briefly to absorb commit latency.
	payload := waitForBreakerRejectionRow(t, pg, "acme")
	if got, _ := payload["circuit_name"].(string); got != breakerName {
		t.Errorf("audit circuit_name = %q, want %q", got, breakerName)
	}
	if got, _ := payload["reason"].(string); got != breakerReason {
		t.Errorf("audit reason = %q, want %q", got, breakerReason)
	}
	// caller_sub / caller_tenant_id are populated only because the gate runs
	// after PreAuth; an empty caller identity would mean the gate ran before
	// authentication resolved the principal.
	if got, _ := payload["caller_sub"].(string); got != "alice@acme.com" {
		t.Errorf("audit caller_sub = %q, want alice@acme.com (authenticated identity resolved at PreAuth)", got)
	}
	if got, _ := payload["caller_tenant_id"].(string); got != "acme" {
		t.Errorf("audit caller_tenant_id = %q, want acme", got)
	}
}

// gateReq issues an HTTP request against a running gateway with the
// acme/alice dev-header identity and returns the status plus the decoded
// JSON body.
func gateReq(t *testing.T, base string) func(method, path, roles string, body any) (int, map[string]any) {
	t.Helper()
	client := http.DefaultClient
	return func(method, path, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		if roles != "" {
			req.Header.Set("X-Lenny-Roles", roles)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out
	}
}

// waitForBreakerRejectionRow polls the durable audit chain for the
// admission.circuit_breaker_rejected row on tenantID's chain and returns
// its decoded JSON payload. It fails the test if none appears within the
// deadline.
func waitForBreakerRejectionRow(t *testing.T, pg *containers.Postgres, tenantID string) map[string]any {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var raw []byte
		err := pg.Pool.QueryRow(ctx,
			`SELECT payload FROM audit_log
			 WHERE tenant_id = $1 AND event_type = 'admission.circuit_breaker_rejected'
			 ORDER BY sequence_number DESC LIMIT 1`, tenantID).Scan(&raw)
		if err == nil {
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatalf("decode admission.circuit_breaker_rejected payload: %v", err)
			}
			return payload
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("no admission.circuit_breaker_rejected audit row appeared on the acme chain within 10s; the breaker gate did not emit the distinct POL-014 audit event")
	return nil
}
