// SPDX-License-Identifier: MIT

// Package doctor implements the §25.6 `fix=true` auto-remediation
// orchestrator that backs `POST /v1/admin/diagnostics/run` and the
// `lenny-ctl doctor[--fix]` CLI. The orchestrator detects the fixable
// findings present in the cluster, applies the narrow set of safe,
// idempotent remediations the spec enumerates, and reports what it did
// through the §25.2 progress envelope and the five `diagnostics.fix_*`
// audit events.
//
// The cluster-facing detection and remediation live behind the
// Remediator seam so the orchestration logic — request scoping,
// guardrails, audit emission, and the progress envelope — is exercised
// without a Kubernetes API. cmd/lenny-ops supplies the production
// Remediator over its client-go clients.
//
// spec: §25.6 lines 2941-2982 (Auto-remediation); §24.2 rows 2-3
// (`lenny-ctl doctor` / `doctor --fix`). F-25.6.2, F-24.2.3.
package doctor

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	auditcat "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// The §25.6 fixable finding codes (lines 2961-2967). Each maps to a
// detection signal and an idempotent remediation in the Remediator.
const (
	FindingCoreDNSStuckEndpoint   = "coreDnsStuckEndpoint"
	FindingBootstrapConfigDrift   = "bootstrapConfigDrift"
	FindingCertManagerExpiring    = "certManagerExpiring"
	FindingPrometheusRuleMissing  = "prometheusRuleMissing"
	FindingWarmPoolStuckReplenish = "warmPoolStuckReplenish"
)

// DefaultFixableFindings is the §25.6 fixable-finding table, used as the
// default for admin.doctor.allowedFixes when no allowlist is configured.
// The order is the spec's table order so a report reads predictably.
var DefaultFixableFindings = []string{
	FindingCoreDNSStuckEndpoint,
	FindingBootstrapConfigDrift,
	FindingCertManagerExpiring,
	FindingPrometheusRuleMissing,
	FindingWarmPoolStuckReplenish,
}

// fixableSet is the membership view of DefaultFixableFindings.
var fixableSet = func() map[string]bool {
	m := make(map[string]bool, len(DefaultFixableFindings))
	for _, f := range DefaultFixableFindings {
		m[f] = true
	}
	return m
}()

// IsFixable reports whether code is one of the §25.6 auto-remediable
// findings. A code outside this set is reported as `remediation: manual`.
func IsFixable(code string) bool { return fixableSet[code] }

// The §25.6 per-finding outcome strings reported in the run report.
const (
	resultDetected = "detected" // read-only mode: the finding is present
	resultApplied  = "applied"  // the remediation ran and converged
	resultSkipped  = "skipped"  // a guardrail prevented the fix
	resultFailed   = "failed"   // the remediation was attempted and errored
	resultManual   = "manual"   // not a §25.6 fixable finding
)

// The §25.6 remediation classes reported per finding.
const (
	remediationAuto   = "auto"
	remediationManual = "manual"
)

// The §25.6 skip reasons, recorded on a fix_skipped event's `reason`.
const (
	reasonMaintenanceMode = "maintenance_mode" // global.maintenanceMode=true
	reasonNotAllowed      = "not_allowed"      // not in admin.doctor.allowedFixes
	reasonDoctorOptOut    = "doctor_optout"    // resource has lenny.dev/doctor-optout
	reasonNotDetected     = "not_detected"     // requested but not present in cluster
	reasonNotFixable      = "manual"           // requested code outside the fixable table
)

// ErrManualRemediation is returned by a Remediator.Apply when the
// finding is detectable but cannot be auto-applied from lenny-ops (for
// example a re-apply that requires the Helm-rendered template the ops
// service does not hold). The orchestrator records it as a manual
// recommendation rather than a failure.
var ErrManualRemediation = errors.New("finding requires manual remediation")

// Detected is one §25.6 fixable finding observed in the cluster, bound
// to the concrete resource its remediation would act on.
type Detected struct {
	// Code is one of the Finding* codes.
	Code string
	// Resource is the human-readable resource id the fix acts on, e.g.
	// "kube-system/coredns" or "lenny-system/lenny-bootstrap". It appears
	// on the fix_applied audit event's `resource` field.
	Resource string
	// OptOut reports whether Resource carries the lenny.dev/doctor-optout:
	// "true" annotation, which the spec line 2974 makes a hard skip.
	OptOut bool
	// Detail is an optional human-readable detection note surfaced in the
	// report (e.g. "Certificate expires in 4 days").
	Detail string
}

// RenderedConfigMap is one §25.6 Helm-rendered ConfigMap the
// bootstrapConfigDrift fix re-applies. Data is the canonical key/value
// content the chart renders; Hash is the content hash the release
// annotation records so detection can compare it against the live
// ConfigMap without re-hashing the chart output. spec: §25.6 line 2953.
type RenderedConfigMap struct {
	// Name is the ConfigMap name (e.g. "lenny-bootstrap-values").
	Name string
	// Data is the rendered key/value content the fix re-applies.
	Data map[string]string
	// Hash is the content hash the drift detection compares against the
	// live ConfigMap's release annotation.
	Hash string
}

// RenderedMonitoring is the §25.6 Helm-rendered monitoring bundle the
// prometheusRuleMissing fix re-applies: the PrometheusRule and
// ServiceMonitor objects the chart's monitoring.yaml template produces
// when monitoring.enabled=true. Each object is an unstructured manifest
// keyed by its group/version/resource so the remediator can apply it
// through the dynamic client. spec: §25.6 line 2955.
type RenderedMonitoring struct {
	// Objects is the rendered PrometheusRule/ServiceMonitor manifests.
	Objects []RenderedObject
}

// RenderedObject is one Helm-rendered custom resource the doctor
// re-applies, carrying the group/version/resource the dynamic client
// addresses and the object's JSON manifest.
type RenderedObject struct {
	Group     string
	Version   string
	Resource  string
	Namespace string
	Name      string
	// Manifest is the object body as an unstructured map the dynamic
	// client server-side-applies.
	Manifest map[string]any
}

// HelmRenderSource yields the §25.6 Helm-rendered references the
// bootstrapConfigDrift and prometheusRuleMissing remediations compare
// against the live cluster and re-apply. lenny-ops does not hold the
// chart, so the source is injected from the values the operator
// installs with (threaded through chart values to lenny-ops). A nil
// HelmRenderSource on the production remediator leaves both findings
// undetectable, so they report not_detected rather than a false
// success. spec: §25.6 lines 2953, 2955.
type HelmRenderSource interface {
	// BootstrapConfigMap returns the rendered lenny-bootstrap ConfigMap
	// the drift fix re-applies. A false ok means the operator did not
	// supply the bootstrap render, so bootstrapConfigDrift is not detected.
	BootstrapConfigMap(ctx context.Context) (cm RenderedConfigMap, ok bool, err error)
	// Monitoring returns the rendered PrometheusRule/ServiceMonitor bundle
	// the prometheusRuleMissing fix re-applies. A false ok means monitoring
	// is not enabled in the release, so prometheusRuleMissing is not detected.
	Monitoring(ctx context.Context) (m RenderedMonitoring, ok bool, err error)
}

// Remediator detects the §25.6 fixable findings present in the cluster
// and applies each finding's idempotent remediation. A nil Remediator
// leaves the doctor surface unconfigured (the endpoint reports 503).
type Remediator interface {
	// Detect reports the fixable findings currently present. A detection
	// failure (Kubernetes API unreachable) is returned so the handler can
	// fail the run rather than report "nothing wrong".
	Detect(ctx context.Context) ([]Detected, error)
	// Apply applies the idempotent remediation for d. It returns nil once
	// the remediation has converged, ErrManualRemediation when the finding
	// cannot be auto-applied, or another error on failure. The orchestrator
	// bounds ctx with the per-fix timeout before calling.
	Apply(ctx context.Context, d Detected) error
}

// Event is a §25.6 `diagnostics.fix_*` audit event the orchestrator
// hands to the configured sink. Fields carries the spec's documented
// per-event payload.
type Event struct {
	Type        auditcat.EventType
	OperationID string
	Fields      map[string]any
}

// Config carries the §25.6 guardrails and the orchestrator's test seams.
type Config struct {
	// AllowedFixes is admin.doctor.allowedFixes: the gated set of fixable
	// findings (line 2976). An empty slice means the full default table.
	AllowedFixes []string
	// FixTimeout is admin.doctor.fixTimeoutSeconds (line 2973, default
	// 120s): the per-remediation timeout. A zero value applies the default.
	FixTimeout time.Duration
	// MaintenanceMode reports global.maintenanceMode (line 2974). When it
	// returns true every fix is skipped with reason maintenance_mode. A nil
	// hook is treated as "not in maintenance".
	MaintenanceMode func() bool
	// Audit receives each §25.6 fix lifecycle event. A nil sink disables
	// audit emission (the dev path).
	Audit func(Event)
	// Now overrides the clock for the progress envelope timestamps (tests).
	Now func() time.Time
	// NewID overrides the operationId generator (tests). The default is
	// "doctor-" + a v4 UUID, matching the §25.2 line 1779
	// <kind-prefix>-<natural-key> convention.
	NewID func() string
}

// DefaultFixTimeout is the §25.6 line 2973 per-operation timeout default.
const DefaultFixTimeout = 120 * time.Second

// RunRequest is one §25.6 doctor invocation.
type RunRequest struct {
	// Findings is the requested finding set from the request body. Empty
	// means "act on every detected fixable finding".
	Findings []string
	// Fix selects auto-remediation mode (`?fix=true`). When false the run
	// is read-only and reports detected findings without applying.
	Fix bool
	// Principal is the authenticated caller subject, recorded on the
	// fix_started audit event (line 2980).
	Principal string
}

// FindingResult is the per-finding outcome in a run report.
type FindingResult struct {
	Finding     string `json:"finding"`
	Resource    string `json:"resource,omitempty"`
	Remediation string `json:"remediation"`      // "auto" | "manual"
	Result      string `json:"result"`           // detected|applied|skipped|failed|manual
	Reason      string `json:"reason,omitempty"` // skip reason
	Detail      string `json:"detail,omitempty"`
	Error       string `json:"error,omitempty"`
}

// RunReport is the §25.6 doctor response: the long-running operation
// envelope (§25.2 progress) plus the per-finding outcomes.
type RunReport struct {
	OperationID  string                `json:"operationId"`
	Fix          bool                  `json:"fix"`
	Findings     []FindingResult       `json:"findings"`
	AppliedCount int                   `json:"appliedCount"`
	SkippedCount int                   `json:"skippedCount"`
	FailedCount  int                   `json:"failedCount"`
	Progress     *conventions.Progress `json:"progress,omitempty"`
}

// Service is the orchestrator interface the HTTP handler depends on, so
// the handler can be tested against a fake.
type Service interface {
	Run(ctx context.Context, req RunRequest) (*RunReport, error)
}

// Orchestrator runs the §25.6 doctor flow over a Remediator.
type Orchestrator struct {
	rem     Remediator
	cfg     Config
	allowed map[string]bool
}

// New returns an Orchestrator. A nil Remediator yields a nil
// Orchestrator so callers can treat "no remediator" as "unconfigured".
func New(rem Remediator, cfg Config) *Orchestrator {
	if rem == nil {
		return nil
	}
	allowed := map[string]bool{}
	src := cfg.AllowedFixes
	if len(src) == 0 {
		src = DefaultFixableFindings
	}
	for _, f := range src {
		// An allowlist entry outside the fixable table is inert: it can
		// never match a detected fixable finding.
		if IsFixable(f) {
			allowed[f] = true
		}
	}
	return &Orchestrator{rem: rem, cfg: cfg, allowed: allowed}
}

func (o *Orchestrator) now() time.Time {
	if o.cfg.Now != nil {
		return o.cfg.Now()
	}
	return time.Now()
}

func (o *Orchestrator) newID() string {
	if o.cfg.NewID != nil {
		return o.cfg.NewID()
	}
	return "doctor-" + uuid.NewString()
}

func (o *Orchestrator) fixTimeout() time.Duration {
	if o.cfg.FixTimeout > 0 {
		return o.cfg.FixTimeout
	}
	return DefaultFixTimeout
}

func (o *Orchestrator) emit(ev Event) {
	if o.cfg.Audit != nil {
		o.cfg.Audit(ev)
	}
}

// Run executes one §25.6 doctor invocation. It detects the fixable
// findings, scopes them to the request, and — in fix mode — applies the
// remediations under the guardrails, emitting the five audit events.
func (o *Orchestrator) Run(ctx context.Context, req RunRequest) (*RunReport, error) {
	opID := o.newID()
	started := o.now()

	detected, err := o.rem.Detect(ctx)
	if err != nil {
		return nil, err
	}
	// A finding code can be detected on more than one independent resource
	// (two Certificates within 7 days of expiry both raise
	// certManagerExpiring). Group every detection under its code so each
	// resource is remediated and reported; keying by code alone would drop
	// all but one detection and leave the rest unfixed. spec: §25.6 lines
	// 2949, 2968, 2985.
	byCode := make(map[string][]Detected, len(detected))
	for _, d := range detected {
		byCode[d.Code] = append(byCode[d.Code], d)
	}

	// targets is the ordered set of finding codes to report on. When the
	// request names findings, those drive the report (so a requested but
	// absent finding is still reported as not_detected); otherwise every
	// detected fixable finding is a target.
	targets := o.scopeTargets(req.Findings, detected)

	report := &RunReport{OperationID: opID, Fix: req.Fix}

	if !req.Fix {
		for _, code := range targets {
			report.Findings = append(report.Findings, o.readOnlyResults(code, byCode)...)
		}
		return report, nil
	}

	o.emit(Event{
		Type:        auditcat.EventDiagnosticsFixStarted,
		OperationID: opID,
		Fields: map[string]any{
			"operationId": opID,
			"findings":    targets,
			"principal":   req.Principal,
		},
	})

	for _, code := range targets {
		for _, res := range o.applyCode(ctx, opID, code, byCode) {
			report.Findings = append(report.Findings, res)
			switch res.Result {
			case resultApplied:
				report.AppliedCount++
			case resultFailed:
				report.FailedCount++
			default: // skipped, manual
				report.SkippedCount++
			}
		}
	}

	o.emit(Event{
		Type:        auditcat.EventDiagnosticsFixCompleted,
		OperationID: opID,
		Fields: map[string]any{
			"operationId":  opID,
			"appliedCount": report.AppliedCount,
			"skippedCount": report.SkippedCount,
			"failedCount":  report.FailedCount,
		},
	})

	// Each remediation acted on is a progress step, so total is the number
	// of per-resource outcomes rather than the number of finding codes.
	report.Progress = o.terminalProgress(started, len(report.Findings))
	return report, nil
}

// scopeTargets returns the ordered finding codes the run reports on.
func (o *Orchestrator) scopeTargets(requested []string, detected []Detected) []string {
	if len(requested) > 0 {
		return dedupePreserveOrder(requested)
	}
	out := make([]string, 0, len(detected))
	for _, d := range detected {
		out = append(out, d.Code)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return fixableOrder(out[i]) < fixableOrder(out[j])
	})
	return dedupePreserveOrder(out)
}

// readOnlyResults builds the read-only report entries for one finding
// code. A code can be detected on several resources, so this returns one
// entry per detected resource (or a single not_detected/manual entry when
// the code is absent or non-fixable).
func (o *Orchestrator) readOnlyResults(code string, byCode map[string][]Detected) []FindingResult {
	if !IsFixable(code) {
		return []FindingResult{{Finding: code, Remediation: remediationManual, Result: resultManual}}
	}
	ds := byCode[code]
	if len(ds) == 0 {
		// A fixable finding the caller asked about but that is not present:
		// report it as detected:false via a skipped/not_detected entry so
		// the read-only report is complete.
		return []FindingResult{{Finding: code, Remediation: remediationAuto, Result: resultSkipped, Reason: reasonNotDetected}}
	}
	out := make([]FindingResult, 0, len(ds))
	for _, d := range ds {
		out = append(out, FindingResult{
			Finding:     code,
			Resource:    d.Resource,
			Remediation: remediationAuto,
			Result:      resultDetected,
			Detail:      d.Detail,
		})
	}
	return out
}

// applyCode runs the remediation for every resource detected under one
// finding code, returning one report entry per resource. A code that is
// absent or non-fixable yields a single not_detected/manual entry.
func (o *Orchestrator) applyCode(ctx context.Context, opID, code string, byCode map[string][]Detected) []FindingResult {
	if !IsFixable(code) {
		o.emitSkip(opID, code, "", reasonNotFixable)
		return []FindingResult{{Finding: code, Remediation: remediationManual, Result: resultManual, Reason: reasonNotFixable}}
	}
	ds := byCode[code]
	if len(ds) == 0 {
		o.emitSkip(opID, code, "", reasonNotDetected)
		return []FindingResult{{Finding: code, Remediation: remediationAuto, Result: resultSkipped, Reason: reasonNotDetected}}
	}
	out := make([]FindingResult, 0, len(ds))
	for _, d := range ds {
		out = append(out, o.applyOne(ctx, opID, d))
	}
	return out
}

// applyOne runs the §25.6 guardrails and, when they pass, the remediation
// for one detected resource, emitting the matching audit event.
func (o *Orchestrator) applyOne(ctx context.Context, opID string, d Detected) FindingResult {
	code := d.Code
	base := FindingResult{Finding: code, Resource: d.Resource, Remediation: remediationAuto, Detail: d.Detail}

	if o.cfg.MaintenanceMode != nil && o.cfg.MaintenanceMode() {
		o.emitSkip(opID, code, d.Resource, reasonMaintenanceMode)
		base.Result, base.Reason = resultSkipped, reasonMaintenanceMode
		return base
	}
	if !o.allowed[code] {
		o.emitSkip(opID, code, d.Resource, reasonNotAllowed)
		base.Result, base.Reason = resultSkipped, reasonNotAllowed
		return base
	}
	if d.OptOut {
		o.emitSkip(opID, code, d.Resource, reasonDoctorOptOut)
		base.Result, base.Reason = resultSkipped, reasonDoctorOptOut
		return base
	}

	fixCtx, cancel := context.WithTimeout(ctx, o.fixTimeout())
	defer cancel()
	switch err := o.rem.Apply(fixCtx, d); {
	case err == nil:
		o.emit(Event{
			Type:        auditcat.EventDiagnosticsFixApplied,
			OperationID: opID,
			Fields: map[string]any{
				"operationId": opID,
				"finding":     code,
				"resource":    d.Resource,
				"result":      resultApplied,
			},
		})
		base.Result = resultApplied
		return base
	case errors.Is(err, ErrManualRemediation):
		// Detectable but not auto-applicable from lenny-ops: surface as a
		// manual recommendation, not a failure (spec line 2969).
		o.emitSkip(opID, code, d.Resource, reasonNotFixable)
		base.Remediation, base.Result, base.Reason = remediationManual, resultManual, reasonNotFixable
		return base
	default:
		o.emit(Event{
			Type:        auditcat.EventDiagnosticsFixFailed,
			OperationID: opID,
			Fields: map[string]any{
				"operationId": opID,
				"finding":     code,
				"error":       err.Error(),
			},
		})
		base.Result, base.Error = resultFailed, err.Error()
		return base
	}
}

func (o *Orchestrator) emitSkip(opID, finding, resource, reason string) {
	fields := map[string]any{"operationId": opID, "finding": finding, "reason": reason}
	if resource != "" {
		fields["resource"] = resource
	}
	o.emit(Event{Type: auditcat.EventDiagnosticsFixSkipped, OperationID: opID, Fields: fields})
}

// terminalProgress builds the §25.2 progress envelope for a synchronous
// run that has completed. Every step is done, so percent is 100 and the
// envelope reports the terminal step. A zero-target run reports a null
// percent (no meaningful basis).
func (o *Orchestrator) terminalProgress(started time.Time, total int) *conventions.Progress {
	now := o.now()
	p := &conventions.Progress{
		CurrentStep:    "completed",
		StartedAt:      started.UTC().Format(time.RFC3339),
		LastProgressAt: now.UTC().Format(time.RFC3339),
		EtaSeconds:     intPtr(0),
		EtaMethod:      conventions.EtaNone,
	}
	if total > 0 {
		pct := 100.0
		p.Percent = &pct
		p.CompletedSteps = intPtr(total)
		p.TotalSteps = intPtr(total)
	}
	return p
}

func intPtr(n int) *int { return &n }

// fixableOrder returns the §25.6 table position of a fixable code, used
// to sort the detected set into the spec's reading order. Unknown codes
// sort last.
func fixableOrder(code string) int {
	for i, f := range DefaultFixableFindings {
		if f == code {
			return i
		}
	}
	return len(DefaultFixableFindings)
}

func dedupePreserveOrder(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
