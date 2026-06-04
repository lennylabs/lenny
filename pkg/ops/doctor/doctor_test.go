// SPDX-License-Identifier: MIT

package doctor

import (
	"context"
	"errors"
	"testing"
	"time"

	auditcat "github.com/lennylabs/lenny/pkg/observability/audit"
)

// fakeRemediator is a test seam for the §25.6 cluster-facing Remediator.
type fakeRemediator struct {
	detected  []Detected
	detectErr error
	applied   []string // codes Apply was called with, in order
	applyErr  map[string]error
}

func (f *fakeRemediator) Detect(context.Context) ([]Detected, error) {
	return f.detected, f.detectErr
}

func (f *fakeRemediator) Apply(_ context.Context, d Detected) error {
	f.applied = append(f.applied, d.Code)
	if f.applyErr != nil {
		return f.applyErr[d.Code]
	}
	return nil
}

// collectAudit returns an audit sink and a pointer to the captured events.
func collectAudit() (func(Event), *[]Event) {
	var got []Event
	return func(ev Event) { got = append(got, ev) }, &got
}

func fixedClock() func() time.Time {
	t := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func newOrch(t *testing.T, rem Remediator, cfg Config) *Orchestrator {
	t.Helper()
	if cfg.NewID == nil {
		cfg.NewID = func() string { return "doctor-test" }
	}
	if cfg.Now == nil {
		cfg.Now = fixedClock()
	}
	o := New(rem, cfg)
	if o == nil {
		t.Fatal("New returned nil for non-nil remediator")
	}
	return o
}

// spec: §25.6 line 2941 — read-only mode reports detected fixable
// findings without applying any remediation or emitting fix events.
func TestRun_ReadOnly_ReportsDetectedWithoutApplying_spec_25_6_2941(t *testing.T) {
	rem := &fakeRemediator{detected: []Detected{
		{Code: FindingCoreDNSStuckEndpoint, Resource: "kube-system/coredns", Detail: "endpoints stale"},
	}}
	audit, got := collectAudit()
	o := newOrch(t, rem, Config{Audit: audit})

	rep, err := o.Run(context.Background(), RunRequest{Fix: false})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Result != resultDetected {
		t.Fatalf("want one detected finding, got %+v", rep.Findings)
	}
	if rep.Findings[0].Remediation != remediationAuto || rep.Findings[0].Resource != "kube-system/coredns" {
		t.Fatalf("unexpected finding: %+v", rep.Findings[0])
	}
	if len(rem.applied) != 0 {
		t.Fatalf("read-only must not apply, applied=%v", rem.applied)
	}
	if len(*got) != 0 {
		t.Fatalf("read-only must not emit audit events, got %d", len(*got))
	}
	if rep.Progress != nil {
		t.Fatalf("read-only report has no progress envelope, got %+v", rep.Progress)
	}
}

// spec: §25.6 lines 2975-2982 — fix mode applies the remediation and
// emits fix_started, fix_applied, fix_completed with the documented
// fields.
func TestRun_Fix_HappyPath_AppliesAndEmits_spec_25_6_2975(t *testing.T) {
	rem := &fakeRemediator{detected: []Detected{
		{Code: FindingCertManagerExpiring, Resource: "lenny-system/lenny-gateway-tls"},
	}}
	audit, got := collectAudit()
	o := newOrch(t, rem, Config{Audit: audit})

	rep, err := o.Run(context.Background(), RunRequest{Fix: true, Principal: "alice@acme.com"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.AppliedCount != 1 || rep.SkippedCount != 0 || rep.FailedCount != 0 {
		t.Fatalf("counts: applied=%d skipped=%d failed=%d", rep.AppliedCount, rep.SkippedCount, rep.FailedCount)
	}
	if got, want := rem.applied, []string{FindingCertManagerExpiring}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("applied=%v want %v", got, want)
	}
	types := eventTypes(*got)
	want := []auditcat.EventType{
		auditcat.EventDiagnosticsFixStarted,
		auditcat.EventDiagnosticsFixApplied,
		auditcat.EventDiagnosticsFixCompleted,
	}
	if !equalTypes(types, want) {
		t.Fatalf("audit sequence=%v want %v", types, want)
	}
	// fix_started carries operationId, findings, principal.
	start := (*got)[0]
	if start.Fields["principal"] != "alice@acme.com" {
		t.Fatalf("fix_started principal=%v", start.Fields["principal"])
	}
	if start.OperationID != "doctor-test" {
		t.Fatalf("operationId=%q", start.OperationID)
	}
	// fix_completed carries the three counts.
	done := (*got)[2]
	if done.Fields["appliedCount"] != 1 || done.Fields["skippedCount"] != 0 || done.Fields["failedCount"] != 0 {
		t.Fatalf("fix_completed fields=%+v", done.Fields)
	}
	// §25.2 progress envelope is terminal.
	if rep.Progress == nil || rep.Progress.Percent == nil || *rep.Progress.Percent != 100 {
		t.Fatalf("progress=%+v", rep.Progress)
	}
	if rep.Progress.CurrentStep != "completed" {
		t.Fatalf("currentStep=%q", rep.Progress.CurrentStep)
	}
}

// spec: §25.6 line 2974 — fixes never run when global.maintenanceMode is
// true; the finding is skipped with reason maintenance_mode.
func TestRun_Fix_MaintenanceMode_Skips_spec_25_6_2974(t *testing.T) {
	rem := &fakeRemediator{detected: []Detected{{Code: FindingCoreDNSStuckEndpoint, Resource: "kube-system/coredns"}}}
	audit, got := collectAudit()
	o := newOrch(t, rem, Config{Audit: audit, MaintenanceMode: func() bool { return true }})

	rep, err := o.Run(context.Background(), RunRequest{Fix: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.SkippedCount != 1 || rep.AppliedCount != 0 {
		t.Fatalf("counts: applied=%d skipped=%d", rep.AppliedCount, rep.SkippedCount)
	}
	if len(rem.applied) != 0 {
		t.Fatalf("maintenance mode must not apply, applied=%v", rem.applied)
	}
	if r := rep.Findings[0]; r.Result != resultSkipped || r.Reason != reasonMaintenanceMode {
		t.Fatalf("finding=%+v", r)
	}
	if !hasSkipReason(*got, reasonMaintenanceMode) {
		t.Fatalf("missing fix_skipped(maintenance_mode), got %+v", *got)
	}
}

// spec: §25.6 line 2976 — admin.doctor.allowedFixes gates the fixable
// set; a finding outside the allowlist is skipped with reason not_allowed.
func TestRun_Fix_NotAllowed_Skips_spec_25_6_2976(t *testing.T) {
	rem := &fakeRemediator{detected: []Detected{{Code: FindingWarmPoolStuckReplenish, Resource: "p1"}}}
	audit, got := collectAudit()
	// Allowlist excludes warmPoolStuckReplenish.
	o := newOrch(t, rem, Config{Audit: audit, AllowedFixes: []string{FindingCoreDNSStuckEndpoint}})

	rep, err := o.Run(context.Background(), RunRequest{Fix: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r := rep.Findings[0]; r.Result != resultSkipped || r.Reason != reasonNotAllowed {
		t.Fatalf("finding=%+v", r)
	}
	if len(rem.applied) != 0 {
		t.Fatalf("disallowed fix must not apply")
	}
	if !hasSkipReason(*got, reasonNotAllowed) {
		t.Fatalf("missing fix_skipped(not_allowed)")
	}
}

// spec: §25.6 line 2974 — a resource carrying lenny.dev/doctor-optout is
// skipped with reason doctor_optout.
func TestRun_Fix_OptOut_Skips_spec_25_6_2974(t *testing.T) {
	rem := &fakeRemediator{detected: []Detected{
		{Code: FindingCoreDNSStuckEndpoint, Resource: "kube-system/coredns", OptOut: true},
	}}
	audit, got := collectAudit()
	o := newOrch(t, rem, Config{Audit: audit})

	rep, err := o.Run(context.Background(), RunRequest{Fix: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r := rep.Findings[0]; r.Result != resultSkipped || r.Reason != reasonDoctorOptOut {
		t.Fatalf("finding=%+v", r)
	}
	if len(rem.applied) != 0 {
		t.Fatalf("opt-out resource must not apply")
	}
	if !hasSkipReason(*got, reasonDoctorOptOut) {
		t.Fatalf("missing fix_skipped(doctor_optout)")
	}
}

// spec: §25.6 line 2979 — a remediation that errors is recorded as failed
// and emits fix_failed carrying the error.
func TestRun_Fix_ApplyError_Fails_spec_25_6_2979(t *testing.T) {
	rem := &fakeRemediator{
		detected: []Detected{{Code: FindingCoreDNSStuckEndpoint, Resource: "kube-system/coredns"}},
		applyErr: map[string]error{FindingCoreDNSStuckEndpoint: errors.New("rollout timed out")},
	}
	audit, got := collectAudit()
	o := newOrch(t, rem, Config{Audit: audit})

	rep, err := o.Run(context.Background(), RunRequest{Fix: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.FailedCount != 1 || rep.AppliedCount != 0 {
		t.Fatalf("counts: applied=%d failed=%d", rep.AppliedCount, rep.FailedCount)
	}
	if r := rep.Findings[0]; r.Result != resultFailed || r.Error == "" {
		t.Fatalf("finding=%+v", r)
	}
	var failed *Event
	for i := range *got {
		if (*got)[i].Type == auditcat.EventDiagnosticsFixFailed {
			failed = &(*got)[i]
		}
	}
	if failed == nil || failed.Fields["error"] != "rollout timed out" {
		t.Fatalf("fix_failed=%+v", failed)
	}
}

// spec: §25.6 line 2969 — a finding the remediator detects but cannot
// auto-apply (ErrManualRemediation) is reported manual, not failed.
func TestRun_Fix_ManualRemediation_ReportedManual_spec_25_6_2969(t *testing.T) {
	rem := &fakeRemediator{
		detected: []Detected{{Code: FindingBootstrapConfigDrift, Resource: "lenny-system/lenny-bootstrap"}},
		applyErr: map[string]error{FindingBootstrapConfigDrift: ErrManualRemediation},
	}
	audit, got := collectAudit()
	o := newOrch(t, rem, Config{Audit: audit})

	rep, err := o.Run(context.Background(), RunRequest{Fix: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.FailedCount != 0 || rep.AppliedCount != 0 || rep.SkippedCount != 1 {
		t.Fatalf("counts: applied=%d skipped=%d failed=%d", rep.AppliedCount, rep.SkippedCount, rep.FailedCount)
	}
	if r := rep.Findings[0]; r.Result != resultManual || r.Remediation != remediationManual {
		t.Fatalf("finding=%+v", r)
	}
	if hasType(*got, auditcat.EventDiagnosticsFixFailed) {
		t.Fatalf("manual remediation must not emit fix_failed")
	}
}

// spec: §25.6 line 2969 — a requested code outside the fixable table is
// reported as a manual recommendation.
func TestRun_Fix_NonFixableRequested_Manual_spec_25_6_2969(t *testing.T) {
	rem := &fakeRemediator{}
	o := newOrch(t, rem, Config{})

	rep, err := o.Run(context.Background(), RunRequest{Fix: true, Findings: []string{"somethingElse"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r := rep.Findings[0]; r.Result != resultManual || r.Remediation != remediationManual {
		t.Fatalf("finding=%+v", r)
	}
}

// A requested fixable finding that is not present in the cluster is
// skipped with reason not_detected.
func TestRun_Fix_RequestedButNotDetected_Skips(t *testing.T) {
	rem := &fakeRemediator{} // nothing detected
	o := newOrch(t, rem, Config{})

	rep, err := o.Run(context.Background(), RunRequest{Fix: true, Findings: []string{FindingCertManagerExpiring}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r := rep.Findings[0]; r.Result != resultSkipped || r.Reason != reasonNotDetected {
		t.Fatalf("finding=%+v", r)
	}
	if len(rem.applied) != 0 {
		t.Fatalf("absent finding must not apply")
	}
}

// A detect failure propagates so the handler can fail the run rather than
// report a clean bill of health against an unreachable cluster.
func TestRun_DetectError_Propagates(t *testing.T) {
	rem := &fakeRemediator{detectErr: errors.New("kube API unreachable")}
	o := newOrch(t, rem, Config{})

	if _, err := o.Run(context.Background(), RunRequest{Fix: true}); err == nil {
		t.Fatal("want detect error to propagate")
	}
}

// When the request names no findings, every detected fixable finding is a
// target and the report is ordered by the §25.6 table.
func TestRun_Fix_AllDetected_OrderedByTable(t *testing.T) {
	rem := &fakeRemediator{detected: []Detected{
		{Code: FindingWarmPoolStuckReplenish, Resource: "p1"},
		{Code: FindingCoreDNSStuckEndpoint, Resource: "kube-system/coredns"},
	}}
	o := newOrch(t, rem, Config{})

	rep, err := o.Run(context.Background(), RunRequest{Fix: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Findings) != 2 ||
		rep.Findings[0].Finding != FindingCoreDNSStuckEndpoint ||
		rep.Findings[1].Finding != FindingWarmPoolStuckReplenish {
		t.Fatalf("order=%+v", rep.Findings)
	}
}

// New returns nil for a nil remediator so callers treat it as
// unconfigured.
func TestNew_NilRemediator(t *testing.T) {
	if o := New(nil, Config{}); o != nil {
		t.Fatalf("want nil orchestrator for nil remediator, got %v", o)
	}
}

// The default operationId carries the §25.2 doctor- kind prefix.
func TestNewID_DoctorPrefix(t *testing.T) {
	o := New(&fakeRemediator{}, Config{})
	id := o.newID()
	if len(id) < len("doctor-") || id[:len("doctor-")] != "doctor-" {
		t.Fatalf("operationId=%q want doctor- prefix", id)
	}
}

func eventTypes(evs []Event) []auditcat.EventType {
	out := make([]auditcat.EventType, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}

func equalTypes(a, b []auditcat.EventType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasType(evs []Event, t auditcat.EventType) bool {
	for _, e := range evs {
		if e.Type == t {
			return true
		}
	}
	return false
}

func hasSkipReason(evs []Event, reason string) bool {
	for _, e := range evs {
		if e.Type == auditcat.EventDiagnosticsFixSkipped && e.Fields["reason"] == reason {
			return true
		}
	}
	return false
}
