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
// rules on a SandboxTemplate. The §10.1 tiered-checkpoint-cap budget
// inequalities reference Postgres-authoritative fields
// (workspaceSizeLimitBytes, checkpointBarrierAckTimeoutSeconds,
// terminationGracePeriodSeconds) that are not carried on the CRD spec;
// those are enforced by the gateway-side admission path against the
// pool definition rather than against the CRD object decoded here.
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

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
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
}

// allow builds an admitting Decision.
func allow() Decision { return Decision{Allowed: true, Code: 200} }

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

// DecideTemplate validates a SandboxTemplate against the §5.2
// execution-mode acknowledgment and budget invariants. The empty
// execution mode is treated as `session`, which carries no
// pool-config invariant beyond the CRD OpenAPI schema.
//
// The invariants, each with its spec citation:
//
//   - A task-mode pool must carry taskPolicy with
//     acknowledgeBestEffortScrub set true. The between-task workspace
//     scrub is best-effort and is not a tenant isolation boundary
//     (spec/05_runtime-registry-and-pool-model.md §5.2 line 473: "If
//     acknowledgeBestEffortScrub is absent or false, the pool
//     controller rejects the pool definition at validation time").
//
//   - A task-mode pool's taskPolicy must set maxTasksPerPod, which is
//     required with no default and must be at least 1
//     (spec/05_runtime-registry-and-pool-model.md §5.2 line 473 and
//     line 451: "maxTasksPerPod is required with no default — the
//     deployer must make an explicit choice").
//
//   - taskPolicy.allowCrossTenantReuse is permitted only when
//     isolationProfile is microvm
//     (spec/05_runtime-registry-and-pool-model.md §5.2 line 387: "The
//     pool controller rejects allowCrossTenantReuse: true on any pool
//     whose isolationProfile is not microvm at validation time").
//
//   - taskPolicy.microvmScrubMode `in-place` requires
//     taskPolicy.acknowledgeMicrovmResidualState set true
//     (spec/05_runtime-registry-and-pool-model.md §5.2 line 442: "The
//     pool controller rejects microvmScrubMode: in-place without this
//     acknowledgment").
//
//   - A concurrent-workspace pool (executionMode concurrent,
//     concurrencyStyle workspace) must carry concurrentWorkspacePolicy
//     with acknowledgeProcessLevelIsolation set true
//     (spec/05_runtime-registry-and-pool-model.md §5.2 line 496: "If
//     acknowledgeProcessLevelIsolation is absent or false, the pool
//     controller rejects the pool definition at validation time").
//
//   - A concurrent-workspace pool must not set allowCrossTenantReuse on
//     the SandboxTemplate; cross-tenant slot sharing has no isolation
//     boundary in concurrent-workspace mode
//     (spec/05_runtime-registry-and-pool-model.md §5.2 line 498: "The
//     pool controller explicitly rejects any concurrent-workspace pool
//     definition where allowCrossTenantReuse: true is set").
//
//   - A concurrent-workspace pool's
//     concurrentWorkspacePolicy.cleanupTimeoutSeconds must be at least
//     maxConcurrent x 5, so the per-slot cleanup budget
//     cleanupTimeoutSeconds / maxConcurrent stays above the 5s minimum
//     (spec/05_runtime-registry-and-pool-model.md §5.2 line 515: "The
//     SandboxWarmPool admission webhook rejects any pool configuration
//     where cleanupTimeoutSeconds / maxConcurrent < 5 ... 422
//     INVALID_POOL_CONFIGURATION").
func DecideTemplate(tpl *lennyv1.SandboxTemplate) Decision {
	spec := tpl.Spec

	switch spec.ExecutionMode {
	case "task":
		return decideTaskMode(spec)
	case "concurrent":
		if spec.ConcurrencyStyle == "workspace" {
			return decideConcurrentWorkspace(spec)
		}
		return allow()
	default:
		// "" (session) and "session" carry no pool-config invariant.
		return allow()
	}
}

// decideTaskMode validates the §5.2 task-mode taskPolicy invariants.
func decideTaskMode(spec lennyv1.SandboxTemplateSpec) Decision {
	tp := spec.TaskPolicy
	if tp == nil {
		return reject(
			"spec.executionMode is \"task\" but spec.taskPolicy is absent; task mode requires a taskPolicy with " +
				"acknowledgeBestEffortScrub: true and maxTasksPerPod set (Section 5.2)",
		)
	}
	if !tp.AcknowledgeBestEffortScrub {
		return reject(
			"spec.taskPolicy.acknowledgeBestEffortScrub must be true for a task-mode pool; the between-task " +
				"workspace scrub is best-effort and is not a tenant isolation boundary (Section 5.2)",
		)
	}
	if tp.MaxTasksPerPod < 1 {
		return reject(fmt.Sprintf(
			"spec.taskPolicy.maxTasksPerPod (%d) must be at least 1 for a task-mode pool; it is required with no "+
				"default so the deployer makes an explicit pod-reuse limit choice (Section 5.2)", tp.MaxTasksPerPod,
		))
	}
	if tp.AllowCrossTenantReuse && spec.IsolationProfile != "microvm" {
		return reject(fmt.Sprintf(
			"spec.taskPolicy.allowCrossTenantReuse is true but spec.isolationProfile is %q; cross-tenant pod reuse "+
				"is permitted only with isolationProfile: microvm (Section 5.2)", isolationLabel(spec.IsolationProfile),
		))
	}
	if tp.MicrovmScrubMode == "in-place" && !tp.AcknowledgeMicrovmResidualState {
		return reject(
			"spec.taskPolicy.microvmScrubMode is \"in-place\" but spec.taskPolicy.acknowledgeMicrovmResidualState " +
				"is not true; in-place scrub leaves guest-kernel residual state across tenants and requires the " +
				"acknowledgment (Section 5.2)",
		)
	}
	return allow()
}

// decideConcurrentWorkspace validates the §5.2 concurrent-workspace
// concurrentWorkspacePolicy invariants.
func decideConcurrentWorkspace(spec lennyv1.SandboxTemplateSpec) Decision {
	cw := spec.ConcurrentWorkspacePolicy
	if cw == nil {
		return reject(
			"spec.executionMode is \"concurrent\" with spec.concurrencyStyle \"workspace\" but " +
				"spec.concurrentWorkspacePolicy is absent; concurrent-workspace mode requires a " +
				"concurrentWorkspacePolicy with acknowledgeProcessLevelIsolation: true (Section 5.2)",
		)
	}
	if !cw.AcknowledgeProcessLevelIsolation {
		return reject(
			"spec.concurrentWorkspacePolicy.acknowledgeProcessLevelIsolation must be true for a " +
				"concurrent-workspace pool; concurrent slots share the pod process namespace, /tmp, cgroup memory, " +
				"network stack, and credential group-read access (Section 5.2)",
		)
	}
	if spec.TaskPolicy != nil && spec.TaskPolicy.AllowCrossTenantReuse {
		return reject(
			"spec.taskPolicy.allowCrossTenantReuse is true on a concurrent-workspace pool; cross-tenant slot " +
				"sharing has no isolation boundary in concurrent-workspace mode (Section 5.2)",
		)
	}
	// maxConcurrent is the per-pod slot count; the CRD OpenAPI schema
	// pins Minimum=1 and the empty value defaults to a single slot.
	maxConcurrent := spec.MaxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	floor := int64(maxConcurrent) * 5
	if cw.CleanupTimeoutSeconds < floor {
		return reject(fmt.Sprintf(
			"spec.concurrentWorkspacePolicy.cleanupTimeoutSeconds (%ds) divided by spec.maxConcurrent (%d) would "+
				"produce a per-slot cleanup timeout below the 5s minimum; set cleanupTimeoutSeconds >= maxConcurrent x 5 (%ds)",
			cw.CleanupTimeoutSeconds, maxConcurrent, floor,
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
