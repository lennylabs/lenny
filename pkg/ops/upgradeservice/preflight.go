// SPDX-License-Identifier: MIT

package upgradeservice

import (
	"context"
	"fmt"
	"time"
)

// Preflight check names, surfaced in the §25.8 preflight result and the
// details.failures list. They mirror the §25.8 Phase-1 validations (spec
// lines 3496-3503).
const (
	CheckNoUpgradeInProgress = "no_upgrade_in_progress"
	CheckVersionPrerequisite = "version_prerequisite"
	CheckPlatformHealthy     = "platform_healthy"
	CheckImagesPullable      = "images_pullable"
	CheckPostgresConnections = "postgres_connections"
)

// HealthChecker reports whether the platform is healthy, per the §25.8
// Phase-1 health gate (spec line 3497, gateway GET /v1/admin/health/
// summary). A nil checker skips the gate with a degradation note.
type HealthChecker interface {
	Healthy(ctx context.Context) (healthy bool, detail string, err error)
}

// ImagePullChecker reports whether a resolved image reference is pullable,
// per the §25.8 Phase-1 image gate (spec line 3500, HEAD to the registry
// manifest endpoint). A nil checker skips the gate with a degradation note
// (the resolved plan is still returned for the preview).
type ImagePullChecker interface {
	Pullable(ctx context.Context, ref string) (ok bool, detail string, err error)
}

// ConnChecker reports whether Postgres has enough free connections for the
// migration phase, per the §25.8 Phase-1 connection gate (spec line 3501).
// A nil checker skips the gate.
type ConnChecker interface {
	HasFreeConnections(ctx context.Context) (ok bool, detail string, err error)
}

// PreflightRequest is the resolved input to a §25.8 preflight. The handler
// fills CurrentVersion from the running build and ImagePlan from the
// registry service so the Preflighter stays free of registry semantics.
type PreflightRequest struct {
	// TargetVersion is the version the upgrade would converge on.
	TargetVersion string
	// CurrentVersion is the running platform version, compared against
	// MinUpgradeFrom.
	CurrentVersion string
	// MinUpgradeFrom is the release manifest's hard prerequisite (spec line
	// 3397). Empty disables the version-prerequisite gate.
	MinUpgradeFrom string
	// ImagePlan is the resolved per-component image references the upgrade
	// would pull, keyed by component short name. The Preflighter checks
	// each is pullable and returns the plan as the preview.
	ImagePlan map[string]string
}

// PreflightCheckResult is one §25.8 preflight gate outcome.
type PreflightCheckResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// PreflightResult is the §25.8 POST /v1/admin/platform/upgrade/preflight
// response: the per-gate results, the failure list, the resolved image
// plan preview, and whether every gate passed. It writes no upgrade state
// (spec line 3503: "Returns the upgrade plan as a preview").
type PreflightResult struct {
	// Passed reports whether every non-skipped gate passed.
	Passed bool `json:"passed"`
	// Checks holds each gate's outcome in evaluation order.
	Checks []PreflightCheckResult `json:"checks"`
	// Failures lists the names of the gates that failed.
	Failures []string `json:"failures,omitempty"`
	// UnpullableImages lists the image references the image gate could not
	// pull, so the handler can map an image-only failure to
	// UPGRADE_IMAGE_NOT_PULLABLE.
	UnpullableImages []string `json:"unpullableImages,omitempty"`
	// Plan is the resolved per-component image plan (the preview).
	Plan map[string]string `json:"plan"`
	// TargetVersion echoes the requested target version.
	TargetVersion string `json:"targetVersion"`
}

// Preflighter runs the §25.8 Phase-1 upgrade-safety gates and returns the
// upgrade plan as a preview without writing any state.
type Preflighter struct {
	store    Store
	health   HealthChecker
	images   ImagePullChecker
	conns    ConnChecker
	imageDur ImagePullCheckRecorder
}

// PreflighterOptions configures a Preflighter.
type PreflighterOptions struct {
	// Store is read to assert no other upgrade is in progress. Required.
	Store Store
	// Health, Images, Conns are the optional Phase-1 gate seams; a nil seam
	// skips its gate with a degradation note.
	Health HealthChecker
	Images ImagePullChecker
	Conns  ConnChecker
	// ImageDuration observes the latency of each per-component Images.
	// Pullable call on the lenny_platform_image_pull_check_duration_seconds
	// histogram (spec line 3619). A nil recorder drops the observation; it
	// is only consulted when Images is configured.
	ImageDuration ImagePullCheckRecorder
}

// NewPreflighter returns a Preflighter over opts. It panics when Store is
// nil (a wiring error).
func NewPreflighter(opts PreflighterOptions) *Preflighter {
	if opts.Store == nil {
		panic("upgradeservice: PreflighterOptions.Store is required")
	}
	return &Preflighter{
		store: opts.Store, health: opts.Health, images: opts.Images, conns: opts.Conns,
		imageDur: opts.ImageDuration,
	}
}

// Preflight runs the §25.8 Phase-1 gates. A transport failure (store or a
// gate's underlying call) returns a non-nil error; a completed run whose
// gates failed returns a PreflightResult with Passed=false and no error,
// so the handler maps the structured failure to the canonical envelope.
//
// spec: §25.8 Phase 1 (lines 3496-3503), POST .../upgrade/preflight.
func (p *Preflighter) Preflight(ctx context.Context, req PreflightRequest) (PreflightResult, error) {
	res := PreflightResult{Plan: req.ImagePlan, TargetVersion: req.TargetVersion, Passed: true}

	// 1. No other upgrade in progress (spec line 3498).
	if prev, ok, err := p.store.Load(ctx); err != nil {
		return PreflightResult{}, err
	} else if ok && prev.Active() {
		res.add(PreflightCheckResult{
			Name: CheckNoUpgradeInProgress, Passed: false,
			Detail: fmt.Sprintf("an upgrade to %s is already in progress (phase %s)", prev.TargetVersion, prev.Phase),
		})
	} else {
		res.add(PreflightCheckResult{Name: CheckNoUpgradeInProgress, Passed: true})
	}

	// 2. Current version meets minUpgradeFrom (spec line 3499).
	switch {
	case req.MinUpgradeFrom == "":
		res.add(PreflightCheckResult{
			Name: CheckVersionPrerequisite, Passed: true, Skipped: true,
			Detail: "no minUpgradeFrom prerequisite advertised",
		})
	case CompareSemver(req.CurrentVersion, req.MinUpgradeFrom) < 0:
		res.add(PreflightCheckResult{
			Name: CheckVersionPrerequisite, Passed: false,
			Detail: fmt.Sprintf("running %s is below the required minimum %s", req.CurrentVersion, req.MinUpgradeFrom),
		})
	default:
		res.add(PreflightCheckResult{Name: CheckVersionPrerequisite, Passed: true})
	}

	// 3. Platform health is green (spec line 3497).
	if p.health == nil {
		res.add(PreflightCheckResult{
			Name: CheckPlatformHealthy, Passed: true, Skipped: true,
			Detail: "health gate not configured",
		})
	} else {
		healthy, detail, err := p.health.Healthy(ctx)
		if err != nil {
			return PreflightResult{}, err
		}
		res.add(PreflightCheckResult{Name: CheckPlatformHealthy, Passed: healthy, Detail: detail})
	}

	// 4. Target images are pullable (spec line 3500).
	if p.images == nil {
		res.add(PreflightCheckResult{
			Name: CheckImagesPullable, Passed: true, Skipped: true,
			Detail: "image pullability gate not configured; plan returned unverified",
		})
	} else {
		var unpullable []string
		var firstDetail string
		for _, component := range sortedKeys(req.ImagePlan) {
			ref := req.ImagePlan[component]
			start := time.Now()
			ok, detail, err := p.images.Pullable(ctx, ref)
			if p.imageDur != nil {
				p.imageDur(component, time.Since(start))
			}
			if err != nil {
				return PreflightResult{}, err
			}
			if !ok {
				unpullable = append(unpullable, ref)
				if firstDetail == "" {
					firstDetail = detail
				}
			}
		}
		if len(unpullable) > 0 {
			res.UnpullableImages = unpullable
			res.add(PreflightCheckResult{
				Name: CheckImagesPullable, Passed: false,
				Detail: fmt.Sprintf("%d image(s) not pullable: %s", len(unpullable), firstDetail),
			})
		} else {
			res.add(PreflightCheckResult{Name: CheckImagesPullable, Passed: true})
		}
	}

	// 5. Postgres has free connections for migration (spec line 3501).
	if p.conns == nil {
		res.add(PreflightCheckResult{
			Name: CheckPostgresConnections, Passed: true, Skipped: true,
			Detail: "connection gate not configured",
		})
	} else {
		ok, detail, err := p.conns.HasFreeConnections(ctx)
		if err != nil {
			return PreflightResult{}, err
		}
		res.add(PreflightCheckResult{Name: CheckPostgresConnections, Passed: ok, Detail: detail})
	}

	return res, nil
}

// add appends a gate result and folds a failure into Passed/Failures.
func (r *PreflightResult) add(c PreflightCheckResult) {
	r.Checks = append(r.Checks, c)
	if !c.Passed && !c.Skipped {
		r.Passed = false
		r.Failures = append(r.Failures, c.Name)
	}
}

// sortedKeys returns the keys of m in deterministic order so preflight
// output and the unpullable list are stable across runs.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// small fixed set (component short names); insertion sort keeps it
	// dependency-free.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// OnlyImageGateFailed reports whether the sole failure was the image gate,
// so the handler maps to UPGRADE_IMAGE_NOT_PULLABLE rather than the
// generic UPGRADE_PREFLIGHT_FAILED.
func (r PreflightResult) OnlyImageGateFailed() bool {
	return len(r.Failures) == 1 && r.Failures[0] == CheckImagesPullable
}
