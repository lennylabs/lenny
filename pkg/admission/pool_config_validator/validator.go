// SPDX-License-Identifier: MIT

// Package pool_config_validator implements the pure-decision logic of
// the lenny-pool-config-validator ValidatingAdmissionWebhook. The
// webhook is the sole admission gate for the semantic budget invariants
// of pool configuration (rule set 1) and a defense-in-depth backstop
// for the field-ownership authorization path (rule set 2); see
// spec/04_system-components.md §4.6.3 line 598 onward. It runs in Fail
// mode with a 5s timeout, so a configuration the webhook cannot inspect
// is rejected (spec/04_system-components.md §4.6.3 line 603).
//
// This package validates the §4.6.2 / §4.6.3 semantic budget
// invariants that are visible at the CRD layer — the relationships
// between minWarm, maxWarm, bootstrap and schedule overrides on a
// SandboxWarmPool, and the execution-mode acknowledgment and budget
// rules on a SandboxTemplate. The §5.2 / §10.1 agent-pod
// `terminationGracePeriodSeconds` floor
// (`maxConcurrent × max_tiered_checkpoint_cap + 30`) is enforced here
// against `spec.workspaceSizeLimitBytes`,
// `spec.terminationGracePeriodSeconds`, and
// `spec.maxTerminationGracePeriodSeconds` — see decideTerminationBudget.
// The floor omits `checkpointBarrierAckTimeoutSeconds`, which is the
// gateway's wait for `CheckpointBarrierAck` and belongs to the gateway
// pod's grace period (§10.1) rather than the agent pod the CRD field
// renders onto; the webhook still vets that field against the
// independent §10.1 BarrierAck floor. The floor is evaluated against the
// pool's effective grace period (the declared value, else the §4.6.1
// 120s agent default the podspec renders for an omitted field), so an
// omitted field that under-provisions is rejected fail-closed. The
// budget rule applies to EVERY SandboxTemplate write regardless of
// execution mode; only a service-mode pool fans the per-slot checkpoint
// cap across `maxConcurrent` slots, so a session-mode pool uses a
// multiplier of 1.
//
// The decision logic is split from the webhook HTTP/JSON-AdmissionReview
// transport so it can be unit-tested without the controller-runtime
// stack. The webhook adapter in pkg/admission/webhook wraps this
// package.
//
// A rejection carries the spec-mandated INVALID_POOL_CONFIGURATION
// reason code and HTTP 422 (spec/04_system-components.md §4.6.3 line
// 605; spec/05_runtime-registry-and-pool-model.md §5.2 line 515).
package pool_config_validator

import (
	"fmt"
	"regexp"
	"time"

	cron "github.com/robfig/cron/v3"

	"github.com/lennylabs/lenny/pkg/admission/label_immutability"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// scaleToZeroCronParser matches the five-field standard cron grammar
// (plus @-descriptors) the §4.6.1 scaleToZero window accepts. It mirrors
// the parser the PoolScalingController uses so admission and runtime
// agree on what parses.
var scaleToZeroCronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// ReasonInvalidPoolConfiguration is the machine-readable failure code
// the webhook attaches to every rejection. The API server relays it to
// the offending client, and the PoolScalingController keys its
// admission-denial backoff on it (spec/04_system-components.md §4.6.3
// line 605, spec/16_observability.md line 128).
const ReasonInvalidPoolConfiguration = "INVALID_POOL_CONFIGURATION"

// codeInvalidPoolConfiguration is the HTTP status the webhook returns
// for a rule-set-1 rejection: 422 Unprocessable Entity
// (spec/04_system-components.md §4.6.3 line 605,
// spec/05_runtime-registry-and-pool-model.md §5.2 line 515). A
// malformed admission request the webhook cannot map to a known CRD
// kind is rejected fail-closed with HTTP 400 by the webhook adapter.
const codeInvalidPoolConfiguration = 422

// ReasonUnauthorizedPoolConfigWrite is the machine-readable failure
// code the webhook attaches to a rule-set-2 (authorization) rejection.
// A write to a SandboxTemplate or SandboxWarmPool spec from any
// principal other than the PoolScalingController ServiceAccount is
// rejected with this code (spec/04_system-components.md §4.6.3 line
// 601).
const ReasonUnauthorizedPoolConfigWrite = "UNAUTHORIZED_POOL_CONFIG_WRITE"

// codeUnauthorizedPoolConfigWrite is the HTTP status the webhook
// returns for a rule-set-2 rejection: 403 Forbidden. This mirrors the
// lenny-label-immutability webhook, which the spec names as the model
// for this userInfo-based authorization path
// (spec/04_system-components.md §4.6.3 line 601).
const codeUnauthorizedPoolConfigWrite = 403

// PoolScalingControllerSA is the only principal whose writes to a
// SandboxTemplate or SandboxWarmPool spec are admitted under rule set
// 2. It is the same ServiceAccount username the lenny-label-immutability
// webhook references, kept in one place so the two webhooks cannot
// drift (spec/04_system-components.md §4.6.3 line 601).
const PoolScalingControllerSA = label_immutability.PoolScalingControllerSA

// clockPattern matches a 24-hour HH:MM schedule-window boundary, the
// same pattern the SandboxWarmPool CRD OpenAPI schema pins on
// ScheduleWindow.Start and ScheduleWindow.End.
var clockPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Kind discriminates the CRD type under admission. The webhook is
// installed on SandboxWarmPool and SandboxTemplate only.
type Kind string

const (
	// KindSandboxWarmPool selects the §4.6.2 warm-count budget rules.
	KindSandboxWarmPool Kind = "SandboxWarmPool"
	// KindSandboxTemplate selects the §5.2 execution-mode rules.
	KindSandboxTemplate Kind = "SandboxTemplate"
)

// Decision is the admission outcome of one pool-config validation.
type Decision struct {
	// Allowed is true when the object passes every applicable
	// invariant.
	Allowed bool

	// Reason is the rejection message body when Allowed is false; empty
	// when Allowed is true. Every rejection message names the offending
	// field and the corrective action.
	Reason string

	// Code is the HTTP status the webhook surfaces to the API server:
	// 200 on allow, 422 for a rule-set-1 invariant violation, 400 for a
	// malformed request.
	Code int

	// Warnings carries advisory messages the API server relays to the
	// client without rejecting the request (AdmissionResponse.Warnings).
	// spec: §5.2 line 516 — `terminationGracePeriodSeconds` floor above
	// 600s emits a warning, not a rejection, unless
	// `maxTerminationGracePeriodSeconds` is set and breached.
	Warnings []string

	// BudgetExceeded marks a rejection caused by the §5.2 / §10.1
	// agent-pod grace-floor inequality (the computed preStop floor exceeds
	// the pool's effective terminationGracePeriodSeconds or the
	// maxTerminationGracePeriodSeconds ceiling). The webhook transport
	// increments `lenny_pool_termination_budget_exceeded_total` (labeled
	// by pool) on these rejections so the §16.1 line 129 counter surfaces
	// budget-driven admission failures. Other rule-set-1 rejections
	// (warm-count budgets, execution-mode acknowledgments, the BarrierAck
	// floor) leave it false.
	BudgetExceeded bool
}

// allow builds an admitting Decision.
func allow() Decision { return Decision{Allowed: true, Code: 200} }

// allowWithWarnings builds an admitting Decision carrying advisory
// warnings.
func allowWithWarnings(warnings ...string) Decision {
	return Decision{Allowed: true, Code: 200, Warnings: warnings}
}

// reject builds a rule-set-1 rejection carrying the
// INVALID_POOL_CONFIGURATION code and HTTP 422. The supplied message
// is prefixed with the reason code so the relayed client error is
// self-describing.
func reject(msg string) Decision {
	return Decision{
		Allowed: false,
		Reason:  ReasonInvalidPoolConfiguration + ": " + msg,
		Code:    codeInvalidPoolConfiguration,
	}
}

// rejectBudget builds a rule-set-1 rejection flagged as a
// termination-budget violation so the webhook transport increments
// `lenny_pool_termination_budget_exceeded_total` (spec/16_observability.md
// line 129). It is used only for the two grace-period-budget rejections
// in decideTerminationBudget — the floor-vs-terminationGracePeriodSeconds
// and floor-vs-maxTerminationGracePeriodSeconds comparisons named by
// spec/10_gateway-internals.md §10.1 line 119.
func rejectBudget(msg string) Decision {
	d := reject(msg)
	d.BudgetExceeded = true
	return d
}

// DecideAuthorization implements rule set 2 of §4.6.3: the
// userInfo-based authorization backstop. Manual `kubectl edit` or
// `kubectl apply` writes to a SandboxTemplate or SandboxWarmPool spec
// are rejected UNLESS the request's userInfo maps to the
// PoolScalingController ServiceAccount. The API server populates
// userInfo from the authenticated principal, which cannot be spoofed
// by the caller, so this is a sound authorization boundary
// (spec/04_system-components.md §4.6.3 line 601).
//
// The PoolScalingController is the sole writer of these spec fields;
// every other write is a manual edit that the PoolScalingController
// would silently overwrite on its next reconciliation. Rejecting the
// write at admission directs operators to the admin API instead. This
// rule applies in addition to rule set 1, which validates the semantic
// budget invariants of every write including the PoolScalingController's
// own (spec/04_system-components.md §4.6.3 line 600); the webhook adapter
// runs rule set 1 first and rule set 2 only when rule set 1 admits.
//
// username is the AdmissionRequest's userInfo.username. The webhook is
// scoped to the sandboxwarmpools and sandboxtemplates resources (not the
// `/status` subresource), so every request DecideAuthorization sees is a
// spec write.
func DecideAuthorization(username string) Decision {
	if username == PoolScalingControllerSA {
		return allow()
	}
	return Decision{
		Allowed: false,
		Reason: fmt.Sprintf(
			"%s: manual writes to SandboxTemplate/SandboxWarmPool spec are not permitted (got user %q); only the "+
				"PoolScalingController (%s) may write these fields. Pool configuration is Postgres-authoritative — use "+
				"the admin API to change a pool; a manual edit would be silently overwritten on the next reconciliation.",
			ReasonUnauthorizedPoolConfigWrite, username, PoolScalingControllerSA,
		),
		Code: codeUnauthorizedPoolConfigWrite,
	}
}

// DecideWarmPool validates a SandboxWarmPool against the §4.6.2 /
// §4.6.3 warm-count budget invariants visible at the CRD layer.
//
// The invariants, each with its spec citation:
//
//   - minWarm and maxWarm are non-negative. The SandboxWarmPool CRD
//     OpenAPI schema pins Minimum=0 on both
//     (pkg/apis/lenny/v1/sandboxwarmpool_types.go lines 97, 102); the
//     webhook re-checks them as a defense-in-depth backstop because a
//     negative count makes the §4.6.2 scaling formula ill-defined.
//
//   - minWarm <= maxWarm. maxWarm is the ceiling on idle pods and
//     minWarm is the floor; a floor above the ceiling is unsatisfiable.
//     The §4.6.1 warm-pool disruption text treats minWarm idle pods as
//     the normal steady state below the maxWarm ceiling
//     (spec/04_system-components.md §4.6.1 line 478).
//
//   - scalePolicy.bootstrapMinWarm <= maxWarm. bootstrapMinWarm is the
//     static warm-count target a new pool holds while in bootstrap mode
//     (spec/04_system-components.md §4.6.2 line ~493 "bootstrap mode";
//     pkg/apis/lenny/v1/sandboxwarmpool_types.go line 46). A bootstrap
//     target above the maxWarm ceiling would have the WarmPoolController
//     immediately violate maxWarm on the pool's first reconciliation.
//
//   - every scalePolicy.schedules[] window has a valid HH:MM start and
//     end, a non-empty duration (start != end), and a per-window
//     minWarm that is non-negative and does not exceed maxWarm. A
//     schedule window overrides the demand-derived warm count
//     (spec/04_system-components.md §4.6.1 line ~470 "time-of-day
//     schedules"; pkg/apis/lenny/v1/sandboxwarmpool_types.go lines
//     9-26). A window minWarm above maxWarm would push the pool past
//     its ceiling whenever the window is active.
func DecideWarmPool(pool *lennyv1.SandboxWarmPool) Decision {
	spec := pool.Spec

	if spec.MinWarm < 0 {
		return reject(fmt.Sprintf(
			"spec.minWarm (%d) must not be negative", spec.MinWarm,
		))
	}
	if spec.MaxWarm < 0 {
		return reject(fmt.Sprintf(
			"spec.maxWarm (%d) must not be negative", spec.MaxWarm,
		))
	}
	if spec.MinWarm > spec.MaxWarm {
		return reject(fmt.Sprintf(
			"spec.minWarm (%d) exceeds spec.maxWarm (%d); the warm floor cannot be above the warm ceiling",
			spec.MinWarm, spec.MaxWarm,
		))
	}

	if sp := spec.ScalePolicy; sp != nil {
		if sp.BootstrapMinWarm < 0 {
			return reject(fmt.Sprintf(
				"spec.scalePolicy.bootstrapMinWarm (%d) must not be negative", sp.BootstrapMinWarm,
			))
		}
		if sp.BootstrapMinWarm > spec.MaxWarm {
			return reject(fmt.Sprintf(
				"spec.scalePolicy.bootstrapMinWarm (%d) exceeds spec.maxWarm (%d); the bootstrap warm target cannot be above the warm ceiling",
				sp.BootstrapMinWarm, spec.MaxWarm,
			))
		}
		for i, win := range sp.Schedules {
			if d := decideScheduleWindow(i, win, spec.MaxWarm); !d.Allowed {
				return d
			}
		}
		if sp.ScaleToZero != nil {
			if d := decideScaleToZero(sp.ScaleToZero); !d.Allowed {
				return d
			}
		}
	}

	return allow()
}

// decideScaleToZero validates the §4.6.1 scale-to-zero window: both cron
// expressions must parse and the optional IANA timezone must load. A
// configuration the PoolScalingController's embedded cron scheduler
// cannot parse is rejected at admission so the controller never silently
// fails to evaluate the window (spec/04_system-components.md §4.6.1 line
// 400).
func decideScaleToZero(p *lennyv1.ScaleToZeroPolicy) Decision {
	if p.Timezone != "" {
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			return reject(fmt.Sprintf(
				"spec.scalePolicy.scaleToZero.timezone (%q) is not a valid IANA timezone", p.Timezone,
			))
		}
	}
	if _, err := scaleToZeroCronParser.Parse(p.Schedule); err != nil {
		return reject(fmt.Sprintf(
			"spec.scalePolicy.scaleToZero.schedule (%q) is not a valid cron expression", p.Schedule,
		))
	}
	if _, err := scaleToZeroCronParser.Parse(p.ResumeAt); err != nil {
		return reject(fmt.Sprintf(
			"spec.scalePolicy.scaleToZero.resumeAt (%q) is not a valid cron expression", p.ResumeAt,
		))
	}
	return allow()
}

// decideScheduleWindow validates one ScalePolicy schedule window
// against the §4.6.2 schedule-override invariants. index is the
// window's position in spec.scalePolicy.schedules, used only in the
// rejection message.
func decideScheduleWindow(index int, win lennyv1.ScheduleWindow, maxWarm int32) Decision {
	if !clockPattern.MatchString(win.Start) {
		return reject(fmt.Sprintf(
			"spec.scalePolicy.schedules[%d].start (%q) is not a valid 24-hour HH:MM time",
			index, win.Start,
		))
	}
	if !clockPattern.MatchString(win.End) {
		return reject(fmt.Sprintf(
			"spec.scalePolicy.schedules[%d].end (%q) is not a valid 24-hour HH:MM time",
			index, win.End,
		))
	}
	if win.Start == win.End {
		return reject(fmt.Sprintf(
			"spec.scalePolicy.schedules[%d] start and end are both %q; a schedule window must have a non-zero duration",
			index, win.Start,
		))
	}
	if win.MinWarm < 0 {
		return reject(fmt.Sprintf(
			"spec.scalePolicy.schedules[%d].minWarm (%d) must not be negative",
			index, win.MinWarm,
		))
	}
	if win.MinWarm > maxWarm {
		return reject(fmt.Sprintf(
			"spec.scalePolicy.schedules[%d].minWarm (%d) exceeds spec.maxWarm (%d); a schedule override cannot push the pool above the warm ceiling",
			index, win.MinWarm, maxWarm,
		))
	}
	return allow()
}

// §13.1 CAP_NET_RAW gate (not enforced here): §5.2 names a
// "SandboxWarmPool CRD validation webhook" rejection for a pool whose
// `maxConcurrentSessions > 1` while the pod template grants CAP_NET_RAW,
// mitigating cross-slot raw-socket sniffing on the shared network
// namespace. This webhook decodes only the SandboxTemplate /
// SandboxWarmPool object. Neither SandboxTemplateSpec nor
// SandboxWarmPoolSpec models a pod-template securityContext, and the CRD
// carries no maxConcurrentSessions field at all (it lives on the
// gateway-side sessionPolicy mirror), so the admission object the
// validator receives carries no CAP_NET_RAW signal to gate on. The gate
// is therefore enforced where the pod template is materialized: the
// WarmPoolController pod-builder (pkg/controller/sandbox/podspec) drops
// ALL capabilities and adds none on every container, so no agent pod can
// hold CAP_NET_RAW regardless of maxConcurrentSessions (see
// TestBuildDropsNetRawOnEveryContainer in that package). Because the
// builder is the sole author of the pod template (it is not
// deployer-supplied), the §13.1 invariant holds without a webhook gate.
// spec: §13.1 (pod security), §5.2 (concurrent-session acknowledgment).

// DecideTemplate validates a SandboxTemplate against the §5.2
// execution-mode acknowledgment and budget invariants visible at the
// CRD layer. The empty execution mode is treated as `session`, which
// carries no pool-config invariant beyond the CRD OpenAPI schema.
//
// The SandboxTemplate models only the cross-tenant scrub-profile
// residual-state gate of the §5.2 sessionPolicy.recycle block; the
// remaining sessionPolicy acknowledgment derivations
// (acknowledgeBestEffortScrub when recycling, acknowledgeProcessLevelIsolation
// when maxConcurrentSessions > 1, the microvm cross-tenant gate, the T4
// cross-tenant prohibition, and the maxConcurrentSessions > 1 categorical
// cross-tenant rejection) key off the gateway-side sessionPolicy mirror,
// which the gateway poolstore enforces at admin admission. The CRD type
// carries no maxConcurrentSessions and no pod-template securityContext, so
// those derivations and the §13.1 CAP_NET_RAW gate are not reachable from
// the SandboxTemplate object this webhook decodes.
//
// The invariants enforced here, each with its spec citation:
//
//   - sessionPolicy.recycle.scrubProfile `in-place` requires
//     sessionPolicy.recycle.acknowledgeMicrovmResidualState set true:
//     in-place scrub reuses the running guest and leaves guest-kernel
//     residual state (DNS cache, TCP TIME_WAIT, page cache,
//     inotify/fanotify registrations) across tenants, so it requires the
//     explicit acknowledgment. See decideRecycleScrubProfile.
//     spec: §5.2 (Kata/microvm scrub variant).
//
//   - The §5.2 / §10.1 agent-pod terminationGracePeriodSeconds floor
//     (`maxConcurrent × max_tiered_checkpoint_cap + 30`, evaluated
//     against the pool's effective grace period) is satisfied for every
//     mode. A service-mode pool fans the per-slot checkpoint cap across
//     `maxConcurrent` slots; every other mode uses a multiplier of 1. See
//     decideTerminationBudget. spec: §5.2, §10.1.
func DecideTemplate(tpl *lennyv1.SandboxTemplate) Decision {
	spec := tpl.Spec

	// spec: §13.2 line 439 — the §13.2 egress/delivery coherence
	// cross-controls are independent of executionMode, so they run before
	// the mode-specific switch. This is the pool-registration-validation
	// layer (layer 1) of the NET-006 mutual exclusivity; the
	// lenny-direct-mode-isolation webhook is the second admission layer.
	if d := decideEgressDeliveryCombo(spec); !d.Allowed {
		return d
	}

	// Execution-mode acknowledgment invariants. A rejection here
	// short-circuits; on allow, the §10.1 termination-budget rule below
	// still runs for every mode. The §5.2 sessionPolicy acknowledgment
	// derivations (acknowledgeBestEffortScrub when recycling,
	// acknowledgeProcessLevelIsolation when maxConcurrentSessions > 1,
	// the cross-tenant microvm and T4 gates) re-key onto the gateway-side
	// sessionPolicy mirror in the pool-config-validator step; the CRD
	// carries only the scrub-profile residual-state gate, enforced here so
	// the fail-closed `in-place` acknowledgment survives the mode collapse.
	if d := decideRecycleScrubProfile(spec); !d.Allowed {
		return d
	}

	// spec: §5.2, §10.1 — the agent-pod grace-floor rule applies to EVERY
	// admission request that mutates a SandboxTemplate.spec, not only
	// service-mode pools. A session-mode pool that allows a 512 MB
	// workspace but whose effective terminationGracePeriodSeconds is below
	// the 90s + 30s floor would be SIGKILL'd mid-checkpoint on drain
	// exactly as a service-mode pool that fans the cap across its slots
	// would; the rule is not subject to the executionMode the deployer
	// happened to pick.
	return decideTerminationBudget(spec, effectiveMaxConcurrent(spec))
}

// effectiveMaxConcurrent resolves the per-pod slot multiplier the §10.1
// line 119 / §5.2 line 516 termination-budget floor uses. A service-mode
// pool fans the per-slot checkpoint cap across `maxConcurrent` slots (the
// floor sums per-slot caps); a single-workspace session pool checkpoints
// one workspace, so the multiplier is 1. The session-mode
// maxConcurrentSessions multiplier lives on the gateway-side sessionPolicy
// mirror, which the pool-config-validator step folds in. An unset or sub-1
// maxConcurrent collapses to a single slot.
//
// spec: §10.1 line 119 (single-workspace floor), §5.2 line 516 (per-slot
// checkpoint floor).
func effectiveMaxConcurrent(spec lennyv1.SandboxTemplateSpec) int32 {
	if spec.ExecutionMode == "service" && spec.MaxConcurrent > 1 {
		return spec.MaxConcurrent
	}
	return 1
}

// decideEgressDeliveryCombo enforces the §13.2 NET-006 mutual
// exclusivity between deliveryMode and egressProfile at the
// pool-config-validation (CRD admission) layer. A pool that sets both
// deliveryMode: proxy and egressProfile: provider-direct is rejected:
// proxy mode routes LLM traffic through the gateway to keep API keys off
// the pod, while provider-direct egress opens a direct CIDR path to the
// same provider endpoints, the silent bypass the spec calls an
// "incoherent security posture". The rejection names the spec's
// InvalidPoolEgressDeliveryCombo sub-code while keeping the webhook's
// INVALID_POOL_CONFIGURATION reason so the PoolScalingController's
// admission-denial backoff keys consistently across every rule-set-1
// rejection.
//
// spec: §13.2 lines 438-442 (NET-006).
func decideEgressDeliveryCombo(spec lennyv1.SandboxTemplateSpec) Decision {
	if spec.DeliveryMode == "proxy" && spec.EgressProfile == "provider-direct" {
		return reject(
			"InvalidPoolEgressDeliveryCombo: spec.deliveryMode \"proxy\" is mutually exclusive with " +
				"spec.egressProfile \"provider-direct\" (NET-006); provider-direct egress would give a " +
				"proxy-mode pod a direct CIDR bypass to provider endpoints the proxy is meant to mediate. " +
				"Use egressProfile: restricted with deliveryMode: proxy, or egressProfile: provider-direct " +
				"with deliveryMode: direct (Section 13.2)",
		)
	}
	// spec: §13.2 line 450 (NET-002) — the `internet` egress profile
	// requires a sandboxed isolation profile (`sandboxed` or `microvm`). A
	// `standard` (runc) pod shares the host kernel, so broad internet
	// egress plus a runc escape reaches the node directly. An unset
	// isolation profile defaults to `standard` (§5.3 line 677), so it is
	// rejected here as well; the deployer must set sandboxed/microvm
	// explicitly to combine with internet egress.
	if spec.EgressProfile == "internet" && spec.IsolationProfile != "sandboxed" && spec.IsolationProfile != "microvm" {
		return reject(
			"spec.egressProfile \"internet\" requires spec.isolationProfile \"sandboxed\" or \"microvm\"; " +
				isolationLabel(spec.IsolationProfile) + " (runc) shares the host kernel and cannot be granted " +
				"broad internet egress (NET-002, Section 13.2)",
		)
	}
	return allow()
}

// decideRecycleScrubProfile carries forward the §5.2 cross-tenant
// in-place scrub acknowledgment gate onto the sessionPolicy.recycle CRD
// block. The `in-place` scrub profile reuses the running guest and leaves
// guest-kernel residual state (DNS cache, TCP TIME_WAIT, page cache,
// inotify/fanotify registrations) across tenants, so it requires the
// explicit acknowledgeMicrovmResidualState acknowledgment. The gate is
// fail-closed: a pool that selects `in-place` without the acknowledgment
// is rejected. The remaining sessionPolicy acknowledgment derivations
// (acknowledgeBestEffortScrub, acknowledgeProcessLevelIsolation, the
// cross-tenant microvm and T4 reuse gates) key off the gateway-side
// sessionPolicy mirror, which the pool-config-validator step folds in.
//
// spec: §5.2 (deployer acknowledgment for recycling, Kata/microvm scrub
// variant).
func decideRecycleScrubProfile(spec lennyv1.SandboxTemplateSpec) Decision {
	if spec.SessionPolicy == nil || spec.SessionPolicy.Recycle == nil {
		return allow()
	}
	r := spec.SessionPolicy.Recycle
	if r.ScrubProfile == "in-place" && !r.AcknowledgeMicrovmResidualState {
		return reject(
			"spec.sessionPolicy.recycle.scrubProfile is \"in-place\" but " +
				"spec.sessionPolicy.recycle.acknowledgeMicrovmResidualState is not true; in-place scrub " +
				"reuses the running guest and leaves guest-kernel residual state across tenants, so it " +
				"requires the acknowledgment (Section 5.2)",
		)
	}
	return allow()
}

// MaxTieredCheckpointCapSeconds returns the §10.1 line 104 tiered
// checkpoint cap matching workspaceSizeLimitBytes. Unknown/unset sizes
// (workspaceSizeLimitBytes <= 0) fall back to the conservative 90s
// maximum tier per §10.1 line 108 — the same behaviour the gateway
// applies when last_checkpoint_workspace_bytes is NULL.
//
// Tier table:
//   - ≤ 100 MB → 30s
//   - ≤ 300 MB → 60s
//   - any larger or unknown → 90s (the §4.4 hard cap is 512 MB)
//
// spec: §10.1 lines 104-108.
func MaxTieredCheckpointCapSeconds(workspaceSizeLimitBytes int64) int64 {
	const (
		mb        = int64(1) << 20
		tierSmall = 100 * mb
		tierMid   = 300 * mb
	)
	if workspaceSizeLimitBytes <= 0 {
		return 90
	}
	switch {
	case workspaceSizeLimitBytes <= tierSmall:
		return 30
	case workspaceSizeLimitBytes <= tierMid:
		return 60
	default:
		return 90
	}
}

// defaultCheckpointBarrierAckTimeoutSeconds is the §10.1 line 122 wall-
// clock deadline default the gateway waits for `CheckpointBarrierAck`
// from every coordinated pod during a rolling drain.
const defaultCheckpointBarrierAckTimeoutSeconds = 90

// minStreamDrainSeconds is the §5.2 line 516 / §10.1 line 122
// preStop-Stage-3 stream-drain budget the webhook adds to the per-slot
// checkpoint budget when computing the terminationGracePeriodSeconds
// floor.
const minStreamDrainSeconds = 30

// nodeDrainWarnSeconds is the §5.2 line 516 typical cluster-automation
// node drain timeout. A computed terminationGracePeriodSeconds floor
// above this value emits an advisory warning unless
// MaxTerminationGracePeriodSeconds is set and breached.
const nodeDrainWarnSeconds = 600

// agentDefaultTerminationGraceSeconds is the §4.6.1 agent-pod
// terminationGracePeriodSeconds default the podspec renders when the
// SandboxWarmPool/SandboxTemplate field is omitted. The webhook uses it
// as the effective grace period against which the §5.2 agent-pod floor
// is evaluated for an omitted field, so an omitted field that
// under-provisions is rejected fail-closed rather than silently
// admitted. spec: §4.6.1 (warm-pool controller pod lifecycle).
const agentDefaultTerminationGraceSeconds = 120

// decideTerminationBudget enforces the §5.2 / §10.1 agent-pod
// `terminationGracePeriodSeconds` floor. It runs for every
// SandboxTemplate execution mode (DecideTemplate calls it after the
// mode-specific acknowledgment checks); maxConcurrent is the slot
// multiplier, which effectiveMaxConcurrent collapses to 1 for a
// session-mode pool and resolves to spec.maxConcurrent for service mode.
// The webhook computes
//
//	floor = maxConcurrent * max_tiered_checkpoint_cap
//	      + minStreamDrainSeconds
//
// where `max_tiered_checkpoint_cap` is resolved from
// `spec.workspaceSizeLimitBytes` via MaxTieredCheckpointCapSeconds. The
// floor omits `checkpointBarrierAckTimeoutSeconds`: that term is the
// gateway's wall-clock wait for `CheckpointBarrierAck` from the pods it
// coordinates and belongs to the gateway pod's grace period (§10.1),
// which the agent pod's own preStop drain does not incur. For a
// single-slot default-tier pool the floor is 1 * 90 + 30 = 120s, which
// equals the §4.6.1 agent default, so the default pool admits without a
// declared value.
//
// The floor is evaluated against the pool's effective grace period: the
// declared `spec.terminationGracePeriodSeconds` when set, otherwise the
// §4.6.1 agent default (agentDefaultTerminationGraceSeconds, 120s) the
// podspec renders for an omitted field.
//
// Rejection rules:
//   - When the effective grace period (declared value, else the §4.6.1
//     default) is below the floor, the configuration is rejected (the
//     budget would SIGKILL pods mid-checkpoint on drain). An omitted
//     field is rejected fail-closed when its 120s default under-provisions.
//   - When spec.maxTerminationGracePeriodSeconds is set and the floor
//     exceeds it, the configuration is rejected (deployers opt into a
//     hard ceiling per §5.2 line 516).
//   - When checkpointBarrierAckTimeoutSeconds < max_tiered_checkpoint_cap,
//     the configuration is rejected per §10.1 line 124 BarrierAck-floor
//     rule (a BarrierAck below the tier cap would declare a legitimately
//     slow uploader unresponsive). This is an independent rule; the
//     BarrierAck term does not enter the grace floor above.
//
// Advisory warnings (admit + emit warning, not reject):
//   - When the computed floor exceeds nodeDrainWarnSeconds (600s) without
//     a maxTerminationGracePeriodSeconds breach, the deployer is asked to
//     verify the cluster node-drain timeout per §5.2 line 516.
func decideTerminationBudget(spec lennyv1.SandboxTemplateSpec, maxConcurrent int32) Decision {
	workspaceLimit := int64(0)
	if spec.WorkspaceSizeLimitBytes != nil {
		workspaceLimit = *spec.WorkspaceSizeLimitBytes
	}
	tierCap := MaxTieredCheckpointCapSeconds(workspaceLimit)
	barrierAck := int64(defaultCheckpointBarrierAckTimeoutSeconds)
	if spec.CheckpointBarrierAckTimeoutSeconds != nil && *spec.CheckpointBarrierAckTimeoutSeconds > 0 {
		barrierAck = *spec.CheckpointBarrierAckTimeoutSeconds
	}
	if barrierAck < tierCap {
		return reject(fmt.Sprintf(
			"spec.checkpointBarrierAckTimeoutSeconds (%ds) must be >= max_tiered_checkpoint_cap (%ds) for the "+
				"configured workspaceSizeLimitBytes; increase checkpointBarrierAckTimeoutSeconds",
			barrierAck, tierCap,
		))
	}
	floor := int64(maxConcurrent)*tierCap + int64(minStreamDrainSeconds)
	if spec.MaxTerminationGracePeriodSeconds != nil && floor > *spec.MaxTerminationGracePeriodSeconds {
		return rejectBudget(fmt.Sprintf(
			"computed terminationGracePeriodSeconds floor (%ds) exceeds spec.maxTerminationGracePeriodSeconds (%ds); "+
				"reduce spec.maxConcurrent (%d) or spec.workspaceSizeLimitBytes, or raise the hard ceiling",
			floor, *spec.MaxTerminationGracePeriodSeconds, maxConcurrent,
		))
	}
	// Evaluate the floor against the pool's effective grace period: the
	// declared field value when set, otherwise the §4.6.1 agent default
	// the podspec renders. Guarding the comparison behind a non-nil field
	// would leave an omitted field unvetted, so an omitted field whose
	// effective default under-provisions is rejected fail-closed.
	effGrace := int64(agentDefaultTerminationGraceSeconds)
	graceDefaulted := true
	if spec.TerminationGracePeriodSeconds != nil && *spec.TerminationGracePeriodSeconds > 0 {
		effGrace = *spec.TerminationGracePeriodSeconds
		graceDefaulted = false
	}
	if effGrace < floor {
		graceSource := "declared"
		if graceDefaulted {
			graceSource = "the §4.6.1 default"
		}
		return rejectBudget(fmt.Sprintf(
			"the pool's effective terminationGracePeriodSeconds (%ds, %s) is below the §5.2 agent-pod floor for this "+
				"pool (%ds = %d x %d tier cap + %d drain); set spec.terminationGracePeriodSeconds at or above the floor, "+
				"or reduce spec.maxConcurrent / spec.workspaceSizeLimitBytes",
			effGrace, graceSource, floor, maxConcurrent, tierCap, minStreamDrainSeconds,
		))
	}
	if floor > nodeDrainWarnSeconds {
		return allowWithWarnings(fmt.Sprintf(
			"terminationGracePeriodSeconds floor (%ds) exceeds %ds; verify that cluster node drain timeout is "+
				"configured to accommodate this value (Section 5.2)",
			floor, nodeDrainWarnSeconds,
		))
	}
	return allow()
}

// isolationLabel renders an isolationProfile for a rejection message,
// substituting a readable token for the empty (schema-default) value.
func isolationLabel(profile string) string {
	if profile == "" {
		return "standard (unset)"
	}
	return profile
}
