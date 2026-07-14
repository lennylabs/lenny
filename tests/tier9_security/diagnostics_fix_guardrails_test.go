// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §25.6 auto-remediation guardrails driven
// through the real REST surface.
//
// pkg/ops/doctor/doctor_test.go pins the maintenanceMode,
// admin.doctor.allowedFixes, and lenny.dev/doctor-optout guardrails by
// calling the Orchestrator's Run method directly in-process. No test
// drives the same guardrails through POST /v1/admin/diagnostics/run —
// the actual REST handler (pkg/ops/opsserver/doctor.go) an operator or
// agent invokes — so a wiring defect between the handler and the
// orchestrator (for example, the handler failing to construct the
// Orchestrator with the guardrail Config it was given, or intercepting
// the response before the skip reason reaches the wire) would not
// surface in the in-process orchestrator suite. This test builds the
// real *opsserver.Server and issues genuine HTTP requests against it,
// asserting that a request that would otherwise apply a remediation is
// skipped over the wire, with the skip reason in the JSON response, when
// each of the three guardrails is active.
//
// spec: §25.6 lines 2971-2976 ("Guardrails: ... Fixes never run when
// global.maintenanceMode=true. Fixes never run against components whose
// lenny.dev/doctor-optout: "true" annotation is set. The set of fixable
// findings is gated by admin.doctor.allowedFixes ...").

package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/doctor"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// diagFixRemediator is a deterministic test Remediator: Detect always
// reports the single finding it was constructed with, and Apply records
// that it ran. A guardrail that works correctly through the REST
// endpoint never calls Apply.
type diagFixRemediator struct {
	detected []doctor.Detected
	applied  []string
}

func (r *diagFixRemediator) Detect(context.Context) ([]doctor.Detected, error) {
	return r.detected, nil
}

func (r *diagFixRemediator) Apply(_ context.Context, d doctor.Detected) error {
	r.applied = append(r.applied, d.Code)
	return nil
}

// diagFixServer builds a real *opsserver.Server with the §25.6 doctor
// orchestrator wired over rem and cfg, mirroring how cmd/lenny-ops wires
// doctor.New into the deployed binary (services_wiring.go
// buildDoctorService). The request travels the actual HTTP handler
// (handleDiagnosticsRun) rather than calling Orchestrator.Run directly.
func diagFixServer(t *testing.T, rem doctor.Remediator, cfg doctor.Config) *opsserver.Server {
	t.Helper()
	o := doctor.New(rem, cfg)
	if o == nil {
		t.Fatal("doctor.New returned nil for a non-nil Remediator")
	}
	return opsserver.New(opsserver.Options{Doctor: o})
}

// postDiagnosticsRun issues POST /v1/admin/diagnostics/run?fix=true
// against srv and decodes the §25.6 RunReport response.
func postDiagnosticsRun(t *testing.T, srv *opsserver.Server, findings []string) doctor.RunReport {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"findings": findings})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/diagnostics/run?fix=true", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/admin/diagnostics/run?fix=true: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var report doctor.RunReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode RunReport: %v; body=%s", err, rec.Body.String())
	}
	return report
}

// spec: §25.6 line 2974 — "Fixes never run when global.maintenanceMode=true."
//
// diagnosis: a failure here means the maintenanceMode guardrail is
// enforced by the in-process Orchestrator but the REST handler either
// does not thread admin.doctor's maintenance-mode hook into the
// Orchestrator it constructs, or otherwise lets a remediation reach the
// live cluster while global.maintenanceMode is set — the exact
// misconfiguration the guardrail exists to prevent.
func TestDiagnosticsRunFixThroughREST_MaintenanceModeSkips_spec_25_6_2974(t *testing.T) {
	rem := &diagFixRemediator{detected: []doctor.Detected{
		{Code: doctor.FindingCoreDNSStuckEndpoint, Resource: "kube-system/coredns"},
	}}
	srv := diagFixServer(t, rem, doctor.Config{MaintenanceMode: func() bool { return true }})

	report := postDiagnosticsRun(t, srv, []string{doctor.FindingCoreDNSStuckEndpoint})

	if report.AppliedCount != 0 || report.SkippedCount != 1 {
		t.Fatalf("counts: applied=%d skipped=%d, want applied=0 skipped=1", report.AppliedCount, report.SkippedCount)
	}
	if len(rem.applied) != 0 {
		t.Fatalf("maintenance mode must not reach the Remediator over the REST endpoint; applied=%v", rem.applied)
	}
	if len(report.Findings) != 1 || report.Findings[0].Result != "skipped" || report.Findings[0].Reason != "maintenance_mode" {
		t.Fatalf("finding result over REST = %+v, want result=skipped reason=maintenance_mode", report.Findings)
	}
}

// spec: §25.6 line 2974 — "Fixes never run against components whose
// lenny.dev/doctor-optout: \"true\" annotation is set."
//
// diagnosis: a failure here means a resource carrying the opt-out
// annotation still gets remediated when the request travels the real
// HTTP handler, even though the in-process Orchestrator suite shows the
// guardrail works when Run is called directly — a handler-level wiring
// gap the in-process suite cannot see.
func TestDiagnosticsRunFixThroughREST_DoctorOptOutSkips_spec_25_6_2974(t *testing.T) {
	rem := &diagFixRemediator{detected: []doctor.Detected{
		{Code: doctor.FindingCoreDNSStuckEndpoint, Resource: "kube-system/coredns", OptOut: true},
	}}
	srv := diagFixServer(t, rem, doctor.Config{})

	report := postDiagnosticsRun(t, srv, []string{doctor.FindingCoreDNSStuckEndpoint})

	if report.AppliedCount != 0 || report.SkippedCount != 1 {
		t.Fatalf("counts: applied=%d skipped=%d, want applied=0 skipped=1", report.AppliedCount, report.SkippedCount)
	}
	if len(rem.applied) != 0 {
		t.Fatalf("an opted-out resource must not reach the Remediator over the REST endpoint; applied=%v", rem.applied)
	}
	if len(report.Findings) != 1 || report.Findings[0].Result != "skipped" || report.Findings[0].Reason != "doctor_optout" {
		t.Fatalf("finding result over REST = %+v, want result=skipped reason=doctor_optout", report.Findings)
	}
}

// spec: §25.6 line 2976 — "The set of fixable findings is gated by
// admin.doctor.allowedFixes (Helm value, defaults to the full list
// above). Operators can narrow the list per environment."
//
// diagnosis: a failure here means a finding code an operator narrowed
// out of admin.doctor.allowedFixes still gets applied when requested
// over the REST endpoint, defeating the per-environment narrowing the
// guardrail exists to enforce.
func TestDiagnosticsRunFixThroughREST_NotAllowedSkips_spec_25_6_2976(t *testing.T) {
	rem := &diagFixRemediator{detected: []doctor.Detected{
		{Code: doctor.FindingWarmPoolStuckReplenish, Resource: "lenny-agents/default-gvisor"},
	}}
	// The operator's allowlist narrows §25.6's fixable set to CoreDNS
	// only, excluding warmPoolStuckReplenish.
	srv := diagFixServer(t, rem, doctor.Config{AllowedFixes: []string{doctor.FindingCoreDNSStuckEndpoint}})

	report := postDiagnosticsRun(t, srv, []string{doctor.FindingWarmPoolStuckReplenish})

	if report.AppliedCount != 0 || report.SkippedCount != 1 {
		t.Fatalf("counts: applied=%d skipped=%d, want applied=0 skipped=1", report.AppliedCount, report.SkippedCount)
	}
	if len(rem.applied) != 0 {
		t.Fatalf("a finding narrowed out of allowedFixes must not reach the Remediator over the REST endpoint; applied=%v", rem.applied)
	}
	if len(report.Findings) != 1 || report.Findings[0].Result != "skipped" || report.Findings[0].Reason != "not_allowed" {
		t.Fatalf("finding result over REST = %+v, want result=skipped reason=not_allowed", report.Findings)
	}
}

// spec: §25.6 lines 2943-2969 — a finding that is detected, allowed, not
// opted out, and not in maintenance mode is applied over the REST
// endpoint. This is the guardrail suite's control case: it confirms the
// three skip assertions above are exercising a request that would
// otherwise succeed, not a request the handler rejects for an unrelated
// reason.
//
// diagnosis: a failure here means the fix=true happy path over the REST
// endpoint does not apply a detected, allowed, non-opted-out finding when
// no guardrail should block it. That also invalidates the three skip
// assertions above, which rely on this control case to prove they skip a
// request that would otherwise be applied.
func TestDiagnosticsRunFixThroughREST_AppliesWhenGuardrailsClear(t *testing.T) {
	rem := &diagFixRemediator{detected: []doctor.Detected{
		{Code: doctor.FindingCoreDNSStuckEndpoint, Resource: "kube-system/coredns"},
	}}
	srv := diagFixServer(t, rem, doctor.Config{})

	report := postDiagnosticsRun(t, srv, []string{doctor.FindingCoreDNSStuckEndpoint})

	if report.AppliedCount != 1 || report.SkippedCount != 0 {
		t.Fatalf("counts: applied=%d skipped=%d, want applied=1 skipped=0", report.AppliedCount, report.SkippedCount)
	}
	if len(rem.applied) != 1 || rem.applied[0] != doctor.FindingCoreDNSStuckEndpoint {
		t.Fatalf("applied = %v, want [%s]", rem.applied, doctor.FindingCoreDNSStuckEndpoint)
	}
	if len(report.Findings) != 1 || report.Findings[0].Result != "applied" {
		t.Fatalf("finding result over REST = %+v, want result=applied", report.Findings)
	}
}
