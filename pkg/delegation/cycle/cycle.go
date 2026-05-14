// SPDX-License-Identifier: MIT

// Package cycle implements the §8.2 cycle-detection decision matrix
// for recursive delegation:
//
//  1. Outer gate — gateway.cycleDetection.mode (enforce | warn | permissive)
//  2. Three-layer AND gate (platform | runtime | policy) under enforce
//  3. Decision records which layer blocked the hop
//
// The gateway uses cycle detection on every delegation admission: when
// the resolved target's (runtime_name, pool_name) tuple already appears
// in the caller's lineage chain, the hop is a self-recursion candidate
// and Decide(...) below resolves whether it is admitted or rejected.
//
// This package is pure: no Redis, no Postgres, no HTTP. The gateway
// builds the inputs (lineage, target, mode, three-layer values) from
// the session record and DelegationPolicy and calls Decide. The full
// audit emission (`delegation.self_recursion_allowed`,
// `delegation.cycle_warning`, etc.) lives in the gateway binary.
package cycle

import "fmt"

// Mode is the outer-gate setting from Helm's
// gateway.cycleDetection.mode value.
type Mode string

const (
	// ModeEnforce: three-layer AND gate decides. Default.
	ModeEnforce Mode = "enforce"
	// ModeWarn: always admit; emit delegation.cycle_warning audit
	// event and increment the "would have blocked" counter for any
	// layer that would have rejected under enforce.
	ModeWarn Mode = "warn"
	// ModePermissive: always admit; no event emitted; cycle
	// detection is structurally disabled. Dev clusters only;
	// CycleDetectionModeUnsafe alert fires while in effect.
	ModePermissive Mode = "permissive"
)

// AllModes returns the closed §8.2 enum.
func AllModes() []Mode { return []Mode{ModeEnforce, ModeWarn, ModePermissive} }

// IsValid reports whether m is one of the three §8.2 modes.
func (m Mode) IsValid() bool {
	for _, v := range AllModes() {
		if m == v {
			return true
		}
	}
	return false
}

// Layer is one of the three AND-gate layers from §8.2. The order is
// the §8.2 declared order (platform → runtime → policy) and BlockedBy
// reports the first false layer in that order.
type Layer string

const (
	LayerPlatform Layer = "platform"
	LayerRuntime  Layer = "runtime"
	LayerPolicy   Layer = "policy"
)

// LayerOrder is the canonical evaluation order from §8.2 for BlockedBy
// resolution.
func LayerOrder() []Layer { return []Layer{LayerPlatform, LayerRuntime, LayerPolicy} }

// Identity is a (runtime_name, pool_name) tuple used for lineage
// comparisons. The spec explicitly notes that pool-differentiated
// cycles (same runtime in different pools) are NOT cycles; Identity
// equality is therefore over both fields.
type Identity struct {
	RuntimeName string
	PoolName    string
}

// Equals reports whether two identities are the same per the §8.2
// rule (same runtime AND same pool).
func (i Identity) Equals(o Identity) bool {
	return i.RuntimeName == o.RuntimeName && i.PoolName == o.PoolName
}

// String returns "<runtime>/<pool>" for log lines and audit events.
func (i Identity) String() string {
	return i.RuntimeName + "/" + i.PoolName
}

// Lineage is the chain of identities from root to the calling
// session, in order. Used for cycle detection.
type Lineage []Identity

// Depth returns the number of hops in the lineage (root has Depth 0).
func (l Lineage) Depth() int { return len(l) }

// Contains reports whether target appears anywhere in the lineage.
// True ⇒ the next hop would close a cycle and trigger the three-layer
// gate.
func (l Lineage) Contains(target Identity) bool {
	for _, x := range l {
		if x.Equals(target) {
			return true
		}
	}
	return false
}

// Settings captures the three-layer AND-gate inputs plus the outer
// mode. AllowSelfRecursion at each layer is the per-layer opt-in;
// false means the layer rejects every self-recursive hop.
type Settings struct {
	Mode                 Mode
	PlatformAllowSelfRec bool
	RuntimeAllowSelfRec  bool
	PolicyAllowSelfRec   bool
}

// Outcome reports the §8.2 outcome of a delegation hop's admission.
type Outcome string

const (
	// OutcomeAdmitted: the hop is admitted (either via the three-
	// layer gate, or via the warn / permissive bypass).
	OutcomeAdmitted Outcome = "admitted"
	// OutcomeWouldHaveBlocked: the hop is admitted because mode is
	// warn, but the three-layer gate would have rejected it under
	// enforce. Drives delegation.cycle_warning audit + counter.
	OutcomeWouldHaveBlocked Outcome = "would_have_blocked"
	// OutcomeRejected: the hop is rejected with
	// DELEGATION_CYCLE_DETECTED.
	OutcomeRejected Outcome = "rejected"
)

// Decision is the output of Decide. When Outcome == OutcomeRejected
// or OutcomeWouldHaveBlocked, BlockedBy carries the first false layer
// in canonical order. WouldHaveBlockedLayers carries the full set of
// layers that evaluated to false under enforce (relevant for the
// warn-mode "would have blocked" counter that is keyed on each
// failing layer individually).
type Decision struct {
	Outcome                Outcome
	IsSelfRecursive        bool
	BlockedBy              Layer
	WouldHaveBlockedLayers []Layer
	EffectiveSettings      Settings
}

// Decide applies the §8.2 cycle-detection decision matrix to a
// candidate hop. The function does NOT mutate any state — the caller
// emits audit events and increments counters based on the Decision.
//
// Inputs:
//   - lineage: the calling session's lineage from root.
//   - target:  the (runtime_name, pool_name) of the resolved delegation
//     target.
//   - settings: outer mode + three-layer allowSelfRecursion values.
//
// Behaviour:
//   - When target is NOT in the lineage, the hop is admitted regardless
//     of mode (not a cycle).
//   - Mode permissive: always admitted; no recording.
//   - Mode warn: admitted; if any layer is false, the decision records
//     WouldHaveBlocked and the failing layers.
//   - Mode enforce: rejected iff any layer is false; BlockedBy names
//     the first false layer in canonical order.
func Decide(lineage Lineage, target Identity, settings Settings) Decision {
	d := Decision{EffectiveSettings: settings}
	if !lineage.Contains(target) {
		d.Outcome = OutcomeAdmitted
		return d
	}
	d.IsSelfRecursive = true

	failing := whichLayersWouldBlock(settings)

	switch settings.Mode {
	case ModePermissive:
		d.Outcome = OutcomeAdmitted
		return d
	case ModeWarn:
		if len(failing) == 0 {
			d.Outcome = OutcomeAdmitted
			return d
		}
		d.Outcome = OutcomeWouldHaveBlocked
		d.WouldHaveBlockedLayers = failing
		d.BlockedBy = failing[0]
		return d
	case ModeEnforce:
		if len(failing) == 0 {
			d.Outcome = OutcomeAdmitted
			return d
		}
		d.Outcome = OutcomeRejected
		d.WouldHaveBlockedLayers = failing
		d.BlockedBy = failing[0]
		return d
	}

	// Unknown mode falls through to enforce. The outer gateway is
	// responsible for rejecting Helm values that aren't in the enum;
	// this path is defensive.
	d.Outcome = OutcomeRejected
	d.WouldHaveBlockedLayers = failing
	if len(failing) > 0 {
		d.BlockedBy = failing[0]
	}
	return d
}

// whichLayersWouldBlock returns the layers (in §8.2 canonical order)
// whose allowSelfRecursion is false. Used by both the enforce-mode
// rejection path and the warn-mode would-have-blocked attribution.
func whichLayersWouldBlock(s Settings) []Layer {
	out := []Layer{}
	if !s.PlatformAllowSelfRec {
		out = append(out, LayerPlatform)
	}
	if !s.RuntimeAllowSelfRec {
		out = append(out, LayerRuntime)
	}
	if !s.PolicyAllowSelfRec {
		out = append(out, LayerPolicy)
	}
	return out
}

// Rejection is the typed error the gateway can return to the caller
// for DELEGATION_CYCLE_DETECTED. It carries the spec's `details`
// fields: blockedBy, effectiveSettings, cycleRuntimeName,
// cyclePoolName.
type Rejection struct {
	BlockedBy         Layer
	EffectiveSettings Settings
	CycleRuntimeName  string
	CyclePoolName     string
}

// Error implements the error interface so callers can return
// Rejection directly.
func (e *Rejection) Error() string {
	return fmt.Sprintf("delegation: cycle detected on %s/%s; blocked by %s layer", e.CycleRuntimeName, e.CyclePoolName, e.BlockedBy)
}

// ToError converts a rejected Decision plus the target identity into
// a *Rejection error. Returns nil when the decision is not a
// rejection.
func ToError(d Decision, target Identity) error {
	if d.Outcome != OutcomeRejected {
		return nil
	}
	return &Rejection{
		BlockedBy:         d.BlockedBy,
		EffectiveSettings: d.EffectiveSettings,
		CycleRuntimeName:  target.RuntimeName,
		CyclePoolName:     target.PoolName,
	}
}
