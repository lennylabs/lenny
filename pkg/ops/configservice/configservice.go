// SPDX-License-Identifier: MIT

// Package configservice implements the §25.8 platform configuration
// diff and apply surface: the lenny-ops operator endpoints
// GET /v1/admin/platform/config/diff and PUT /v1/admin/platform/config.
//
// The service is a thin orchestration layer over the gateway's own
// config API. The running config is fetched from the gateway via the
// GatewayConfig seam (GatewayClient.GetConfig in production); a diff is
// computed with the shared pkg/drift field-by-field differ; an apply
// validates the proposed config against a Validator, returns a dry-run
// impact preview unless the caller confirms, and proxies a confirmed
// change to the gateway. lenny-ops owns the operator-facing surface and
// the §25.2 envelope; the gateway owns the effective config.
//
// spec: §25.8 Config Diff and Config Apply (lines 3566-3574).
package configservice

import (
	"context"
	"errors"
	"sort"

	"github.com/lennylabs/lenny/pkg/drift"
)

// §25.8 config error codes (spec table line 3640-3641).
const (
	// CodeValidationFailed is CONFIG_VALIDATION_FAILED: the proposed config
	// failed schema validation. details.errors lists each violation.
	CodeValidationFailed = "CONFIG_VALIDATION_FAILED"
	// CodeRestartRequired is CONFIG_RESTART_REQUIRED: a setting change
	// takes effect only after a gateway restart. The change is applied; the
	// code signals the operator that a restart is needed.
	CodeRestartRequired = "CONFIG_RESTART_REQUIRED"
)

// ErrGatewayUnavailable reports that the running config could not be
// fetched from the gateway (the §25.8 line 3610 "gateway is down: config
// diff/apply fail" degradation).
var ErrGatewayUnavailable = errors.New("configservice: gateway config unavailable")

// GatewayConfig is the gateway-side config client the service proxies
// through. GatewayClient satisfies it in production; tests substitute a
// fixed implementation.
type GatewayConfig interface {
	// GetConfig returns the gateway's effective merged config as a flat
	// key→value map (secret values pre-redacted by the gateway).
	GetConfig(ctx context.Context) (map[string]any, error)
	// ApplyConfig proxies a confirmed config change to the gateway's
	// PUT /v1/admin/platform/config. restartRequired reports the gateway's
	// answer to whether the applied change needs a restart to take effect.
	ApplyConfig(ctx context.Context, desired map[string]any) (restartRequired bool, err error)
}

// ValidationError is one schema violation the §25.8 422
// CONFIG_VALIDATION_FAILED response lists under details.errors.
type ValidationError struct {
	// Field is the dotted config path that failed (empty for a
	// whole-document error).
	Field string `json:"field,omitempty"`
	// Message describes the violation (unknown key, type mismatch,
	// out-of-range value).
	Message string `json:"message"`
}

// Validator checks a proposed config against the known config schema.
// The production validator is generated from the pkg/chart/values Go
// structs (§17 line 655); a nil validator accepts any config (the
// cold-start posture). It returns the list of violations; an empty list
// means the config is valid.
type Validator interface {
	Validate(desired map[string]any) []ValidationError
}

// Service is the §25.8 config diff/apply orchestrator.
type Service struct {
	gateway   GatewayConfig
	validator Validator
}

// Options configures a Service.
type Options struct {
	// Gateway is the gateway-side config client. Required.
	Gateway GatewayConfig
	// Validator checks a proposed config against the schema. A nil
	// validator accepts any config.
	Validator Validator
}

// New returns a Service over opts. It panics when Gateway is nil, a
// wiring error rather than a runtime condition.
func New(opts Options) *Service {
	if opts.Gateway == nil {
		panic("configservice: Options.Gateway is required")
	}
	return &Service{gateway: opts.Gateway, validator: opts.Validator}
}

// Change is one field-level difference between the desired and running
// config in a §25.8 diff response.
type Change struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Desired  any    `json:"desired,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Severity string `json:"severity"`
}

// DiffResult is the GET /v1/admin/platform/config/diff response: the
// field-by-field differences between the desired and running config.
type DiffResult struct {
	// Changes are the drifted fields, sorted by path for a stable diff.
	Changes []Change `json:"changes"`
	// InSync reports that the desired and running config match (no
	// changes).
	InSync bool `json:"inSync"`
}

// Diff compares the desired config against the gateway's running config
// and returns the field-by-field differences (§25.8 line 3568, used for
// GitOps reconciliation). A gateway-fetch failure returns
// ErrGatewayUnavailable.
func (s *Service) Diff(ctx context.Context, desired map[string]any) (DiffResult, error) {
	running, err := s.gateway.GetConfig(ctx)
	if err != nil {
		return DiffResult{}, errors.Join(ErrGatewayUnavailable, err)
	}
	changes := toChanges(drift.Diff(desired, running))
	return DiffResult{Changes: changes, InSync: len(changes) == 0}, nil
}

// ApplyRequest is the PUT /v1/admin/platform/config body.
type ApplyRequest struct {
	// Desired is the proposed config (the same flat key→value map the
	// gateway exposes).
	Desired map[string]any `json:"desired"`
	// Confirm gates the actual apply. Without it the call is a dry-run
	// returning the impact preview; with it the change is proxied to the
	// gateway (§25.2 canonical dry-run/confirm pattern).
	Confirm bool `json:"confirm"`
}

// ApplyResult is the PUT /v1/admin/platform/config response. The dry-run
// (Confirm=false) and the confirmed apply share the shape; Applied
// distinguishes them.
type ApplyResult struct {
	// DryRun reports that no change was applied (Confirm was false).
	DryRun bool `json:"dryRun"`
	// Applied reports that the change was proxied to the gateway.
	Applied bool `json:"applied"`
	// Diff is the field-by-field impact preview.
	Diff DiffResult `json:"diff"`
	// RestartRequired reports whether the change needs a gateway restart
	// to take effect.
	RestartRequired bool `json:"restartRequired"`
	// Warnings carries advisory impact notes (for example reducing a warm
	// pool minimum below current demand).
	Warnings []string `json:"warnings,omitempty"`
}

// ValidationFailed reports that a proposed config failed schema
// validation. The handler maps it to the §25.8 422 CONFIG_VALIDATION_FAILED
// envelope with details.errors.
type ValidationFailed struct {
	Errors []ValidationError
}

// Error satisfies the error interface.
func (e *ValidationFailed) Error() string { return "configservice: config schema validation failed" }

// Apply validates the proposed config, returns a dry-run impact preview
// unless the caller confirms, and proxies a confirmed change to the
// gateway. It returns *ValidationFailed when the config is invalid
// (§25.8 422 CONFIG_VALIDATION_FAILED) and ErrGatewayUnavailable when the
// running config cannot be fetched.
//
// spec: §25.8 lines 3570-3574.
func (s *Service) Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error) {
	// 1. Schema validation. Unknown keys, type mismatches, and
	//    out-of-range values are rejected before any diff or apply.
	if s.validator != nil {
		if errs := s.validator.Validate(req.Desired); len(errs) > 0 {
			return ApplyResult{}, &ValidationFailed{Errors: errs}
		}
	}
	// 2. Impact preview: the diff against the running config plus any
	//    advisory warnings. Computed for both the dry-run and the apply.
	diff, err := s.Diff(ctx, req.Desired)
	if err != nil {
		return ApplyResult{}, err
	}
	res := ApplyResult{Diff: diff, Warnings: warningsFor(diff)}
	if !req.Confirm {
		res.DryRun = true
		// The dry-run cannot know the gateway's restart verdict without
		// applying; it reports the conservative "no restart unless applied"
		// preview. The confirmed apply returns the authoritative flag.
		return res, nil
	}
	// 3. Apply: proxy the confirmed change to the gateway.
	restart, err := s.gateway.ApplyConfig(ctx, req.Desired)
	if err != nil {
		return ApplyResult{}, errors.Join(ErrGatewayUnavailable, err)
	}
	res.Applied = true
	res.RestartRequired = restart
	return res, nil
}

// toChanges maps pkg/drift Changes onto the §25.8 config diff shape.
// pkg/drift.Diff already classifies each change's severity and orders by
// path; the sort here keeps the contract explicit.
func toChanges(in []drift.Change) []Change {
	out := make([]Change, 0, len(in))
	for _, c := range in {
		out = append(out, Change{
			Path:     c.Path,
			Kind:     string(c.Kind),
			Desired:  c.Desired,
			Actual:   c.Actual,
			Severity: string(c.Severity),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// warningsFor derives advisory impact warnings from the diff. The §25.8
// example is "reducing warm pool minimum below current demand"; the v1
// heuristic flags any change that lowers a *.min* numeric field, which is
// the spec's named case. Additional heuristics plug in here.
func warningsFor(diff DiffResult) []string {
	var warnings []string
	for _, c := range diff.Changes {
		if c.Kind != string(drift.Modified) {
			continue
		}
		if lowersMinimum(c) {
			warnings = append(warnings, "reducing "+c.Path+" below its current value may starve in-flight demand")
		}
	}
	return warnings
}

// lowersMinimum reports whether a changed field names a minimum and the
// desired value is numerically below the running one.
func lowersMinimum(c Change) bool {
	if !containsFold(c.Path, "min") {
		return false
	}
	d, okD := asFloat(c.Desired)
	a, okA := asFloat(c.Actual)
	return okD && okA && d < a
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// containsFold reports a case-insensitive substring match without
// pulling in strings.ToLower allocations on the hot path.
func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if lowerByte(s[i+j]) != lowerByte(sub[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func lowerByte(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
