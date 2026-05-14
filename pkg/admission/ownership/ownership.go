// SPDX-License-Identifier: MIT

// Package ownership encodes the §4.6.3 CRD field-ownership matrix as a
// Go-callable validator. The Kubernetes API server's Server-Side Apply
// mechanism enforces ownership in production through field-manager
// conflict detection; this package mirrors the same matrix so the
// admission webhook layer, the controller test scaffolding, and any
// post-hoc audit tooling can reason about ownership without round-
// tripping through the API server.
//
// The §4.6.3 table assigns disjoint field subsets per CRD to the
// WarmPoolController, the PoolScalingController, and the gateway:
//
//	SandboxTemplate.spec.*                        : PoolScalingController
//	SandboxTemplate.status.*                      : WarmPoolController
//	SandboxWarmPool.spec.minWarm                  : PoolScalingController
//	SandboxWarmPool.spec.maxWarm                  : PoolScalingController
//	SandboxWarmPool.spec.scalePolicy.*            : PoolScalingController
//	SandboxWarmPool.spec.sdkWarmDisabled          : PoolScalingController
//	SandboxWarmPool.status.sdkWarmCircuitBreaker.*: PoolScalingController (carve-out)
//	SandboxWarmPool.status.*                      : WarmPoolController (default)
//	Sandbox.spec.*                                : WarmPoolController
//	Sandbox.status.*                              : WarmPoolController
//	SandboxClaim.spec.*                           : Gateway
//	SandboxClaim.status.*                         : Gateway
//
// The matrix is deliberately limited to v1 surface; future fields are
// added explicitly rather than wildcarded.
package ownership

import (
	"fmt"
	"strings"
)

// Controller identifies a writer in the §4.6.3 matrix. The strings are
// the field-manager names the SSA layer uses in production.
type Controller string

const (
	WarmPoolController    Controller = "lenny-warm-pool-controller"
	PoolScalingController Controller = "lenny-pool-scaling-controller"
	Gateway               Controller = "lenny-gateway"
)

// CRD enumerates the Lenny CRD kinds covered by the matrix.
type CRD string

const (
	SandboxTemplate CRD = "SandboxTemplate"
	SandboxWarmPool CRD = "SandboxWarmPool"
	Sandbox         CRD = "Sandbox"
	SandboxClaim    CRD = "SandboxClaim"
)

// Validate reports whether controller may write fieldPath on crd.
// fieldPath is a dotted path rooted at the resource (e.g.,
// "spec.minWarm", "status.sdkWarmCircuitBreaker.openedAt"). A nil
// error indicates the write is owner-permitted; a non-nil
// *OwnershipError describes the violation.
//
// The matrix is exhaustive: fieldPaths that fall outside spec.* and
// status.* (e.g., metadata.labels) return a *OwnershipError because
// those are owned by the Kubernetes apiserver / kubectl rather than
// any Lenny controller and Lenny controllers must not touch them
// through SSA. Callers that mutate metadata fields (e.g., the gateway
// patching pod labels) do so via the dedicated label-immutability
// path, not via this validator.
func Validate(controller Controller, crd CRD, fieldPath string) error {
	if fieldPath == "" {
		return &OwnershipError{Controller: controller, CRD: crd, FieldPath: fieldPath, Reason: "fieldPath is empty"}
	}
	owner, ok := lookup(crd, fieldPath)
	if !ok {
		return &OwnershipError{Controller: controller, CRD: crd, FieldPath: fieldPath, Reason: "field is not in the §4.6.3 ownership matrix"}
	}
	if owner != controller {
		return &OwnershipError{Controller: controller, CRD: crd, FieldPath: fieldPath, Reason: fmt.Sprintf("owned by %s", owner)}
	}
	return nil
}

// Owner returns the controller that owns fieldPath on crd. The boolean
// is false when the field is not in the matrix.
func Owner(crd CRD, fieldPath string) (Controller, bool) {
	return lookup(crd, fieldPath)
}

// lookup is the matrix lookup function. It applies the most-specific
// rule first (the SandboxWarmPool status carve-out beats the
// SandboxWarmPool status default).
func lookup(crd CRD, fieldPath string) (Controller, bool) {
	switch crd {
	case SandboxTemplate:
		switch {
		case isPrefix(fieldPath, "spec"):
			return PoolScalingController, true
		case isPrefix(fieldPath, "status"):
			return WarmPoolController, true
		}
	case SandboxWarmPool:
		switch {
		case fieldPath == "spec.minWarm",
			fieldPath == "spec.maxWarm",
			fieldPath == "spec.sdkWarmDisabled",
			isPrefix(fieldPath, "spec.scalePolicy"):
			return PoolScalingController, true
		case isPrefix(fieldPath, "status.sdkWarmCircuitBreaker"):
			return PoolScalingController, true
		case isPrefix(fieldPath, "status"):
			return WarmPoolController, true
		}
	case Sandbox:
		switch {
		case isPrefix(fieldPath, "spec"), isPrefix(fieldPath, "status"):
			return WarmPoolController, true
		}
	case SandboxClaim:
		switch {
		case isPrefix(fieldPath, "spec"), isPrefix(fieldPath, "status"):
			return Gateway, true
		}
	}
	return "", false
}

// isPrefix reports whether fieldPath equals prefix or starts with
// prefix followed by a dot. Used so "spec" matches both "spec" itself
// and "spec.scalePolicy.maxIdleSeconds".
func isPrefix(fieldPath, prefix string) bool {
	if fieldPath == prefix {
		return true
	}
	return strings.HasPrefix(fieldPath, prefix+".")
}

// OwnershipError captures a Validate violation. Use errors.As to
// retrieve the typed value.
type OwnershipError struct {
	Controller Controller
	CRD        CRD
	FieldPath  string
	Reason     string
}

func (e *OwnershipError) Error() string {
	return fmt.Sprintf("ownership: %s may not write %s.%s: %s", e.Controller, e.CRD, e.FieldPath, e.Reason)
}

// AllRules returns the canonical (CRD, fieldPath, Controller) rows for
// the §4.6.3 matrix. Useful for golden-file diff tests against the
// spec table and for documentation generation. The slice is fresh on
// every call so callers may sort or filter it.
func AllRules() []Rule {
	return []Rule{
		{SandboxTemplate, "spec", PoolScalingController},
		{SandboxTemplate, "status", WarmPoolController},

		{SandboxWarmPool, "spec.minWarm", PoolScalingController},
		{SandboxWarmPool, "spec.maxWarm", PoolScalingController},
		{SandboxWarmPool, "spec.scalePolicy", PoolScalingController},
		{SandboxWarmPool, "spec.sdkWarmDisabled", PoolScalingController},
		{SandboxWarmPool, "status.sdkWarmCircuitBreaker", PoolScalingController},
		{SandboxWarmPool, "status", WarmPoolController},

		{Sandbox, "spec", WarmPoolController},
		{Sandbox, "status", WarmPoolController},

		{SandboxClaim, "spec", Gateway},
		{SandboxClaim, "status", Gateway},
	}
}

// Rule is one row of the §4.6.3 matrix.
type Rule struct {
	CRD        CRD
	FieldPath  string
	Controller Controller
}
