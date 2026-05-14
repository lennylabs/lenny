// SPDX-License-Identifier: MIT

// Package circuitbreaker implements the §11.6 operator-managed
// circuit-breaker admission logic. The §11.6 design separates this
// surface from the in-memory per-subsystem automatic breakers (§4.1,
// §4.3, §10.2) — operator-managed breakers are Redis-backed,
// toggled via the admin API, and consulted by the AdmissionController
// before quota and policy evaluation.
//
// This package is pure: no Redis, no admin-API handlers, no HTTP. The
// gateway binary wires the Redis cache + pub/sub + admin API on top
// of the State, LimitTier, OperationType, and Match primitives below.
package circuitbreaker

import (
	"fmt"
	"time"
)

// State is the §11.6 breaker state.
type State string

const (
	StateClosed State = "closed"
	StateOpen   State = "open"
)

// AllStates returns the closed enum.
func AllStates() []State { return []State{StateClosed, StateOpen} }

// IsValid reports whether s is one of the two §11.6 states.
func (s State) IsValid() bool { return s == StateClosed || s == StateOpen }

// LimitTier is the §11.6 scope-discrimination enum. The tier
// determines which fields the Scope object carries and what request
// fields the AdmissionController compares against.
type LimitTier string

const (
	TierRuntime       LimitTier = "runtime"
	TierPool          LimitTier = "pool"
	TierConnector     LimitTier = "connector"
	TierOperationType LimitTier = "operation_type"
)

// AllTiers returns the closed §11.6 enum.
func AllTiers() []LimitTier {
	return []LimitTier{TierRuntime, TierPool, TierConnector, TierOperationType}
}

// IsValid reports whether t is one of the four §11.6 tiers.
func (t LimitTier) IsValid() bool {
	for _, v := range AllTiers() {
		if t == v {
			return true
		}
	}
	return false
}

// OperationType is the §11.6 operation_type closed value set: the
// admission classes a tier-operation_type breaker can match against.
type OperationType string

const (
	OpUploads          OperationType = "uploads"
	OpDelegationDepth  OperationType = "delegation_depth"
	OpSessionCreation  OperationType = "session_creation"
	OpMessageInjection OperationType = "message_injection"
)

// AllOperationTypes returns the closed enum from §11.6.
func AllOperationTypes() []OperationType {
	return []OperationType{OpUploads, OpDelegationDepth, OpSessionCreation, OpMessageInjection}
}

// IsValid reports whether o is one of the §11.6 operation types.
func (o OperationType) IsValid() bool {
	for _, v := range AllOperationTypes() {
		if o == v {
			return true
		}
	}
	return false
}

// Scope is the tier-specific matcher payload on a Breaker. Exactly
// one of Runtime / Pool / Connector / OperationType is populated;
// the populated field MUST correspond to the parent Breaker's Tier.
type Scope struct {
	Runtime       string
	Pool          string
	Connector     string
	OperationType OperationType
}

// Breaker is one operator-managed circuit breaker record. Mirrors the
// Redis cb:{name} payload from §11.6 Storage and propagation, minus
// the audit-attribution fields (which the gateway tracks separately
// at Open time).
type Breaker struct {
	Name             string
	State            State
	Reason           string
	OpenedAt         time.Time
	OpenedBySub      string
	OpenedByTenantID string
	LimitTier        LimitTier
	Scope            Scope
}

// Validate reports the §11.6 INVALID_BREAKER_SCOPE conditions on a
// Breaker. Returns nil when the Breaker is admissible. Used both at
// the admin POST /open boundary and at startup-time cache rehydration.
func (b Breaker) Validate() error {
	if b.Name == "" {
		return &ScopeError{Field: "name", Reason: "required"}
	}
	if !b.State.IsValid() {
		return &ScopeError{Field: "state", Reason: fmt.Sprintf("%q is not in {closed, open}", b.State)}
	}
	if !b.LimitTier.IsValid() {
		return &ScopeError{Field: "limit_tier", Reason: fmt.Sprintf("%q is not in the §11.6 vocabulary", b.LimitTier)}
	}
	return b.Scope.ValidateForTier(b.LimitTier)
}

// ValidateForTier reports whether the Scope has exactly the fields
// expected for the given LimitTier per the §11.6 storage table.
func (s Scope) ValidateForTier(t LimitTier) error {
	switch t {
	case TierRuntime:
		if s.Runtime == "" {
			return &ScopeError{Field: "scope.runtime", Reason: "required for limit_tier=runtime"}
		}
		if s.Pool != "" || s.Connector != "" || s.OperationType != "" {
			return &ScopeError{Field: "scope", Reason: "limit_tier=runtime scope must contain only the runtime field"}
		}
	case TierPool:
		if s.Pool == "" {
			return &ScopeError{Field: "scope.pool", Reason: "required for limit_tier=pool"}
		}
		if s.Runtime != "" || s.Connector != "" || s.OperationType != "" {
			return &ScopeError{Field: "scope", Reason: "limit_tier=pool scope must contain only the pool field"}
		}
	case TierConnector:
		if s.Connector == "" {
			return &ScopeError{Field: "scope.connector", Reason: "required for limit_tier=connector"}
		}
		if s.Runtime != "" || s.Pool != "" || s.OperationType != "" {
			return &ScopeError{Field: "scope", Reason: "limit_tier=connector scope must contain only the connector field"}
		}
	case TierOperationType:
		if s.OperationType == "" {
			return &ScopeError{Field: "scope.operation_type", Reason: "required for limit_tier=operation_type"}
		}
		if !s.OperationType.IsValid() {
			return &ScopeError{Field: "scope.operation_type", Reason: fmt.Sprintf("%q is not in the §11.6 operation_type vocabulary", s.OperationType)}
		}
		if s.Runtime != "" || s.Pool != "" || s.Connector != "" {
			return &ScopeError{Field: "scope", Reason: "limit_tier=operation_type scope must contain only the operation_type field"}
		}
	default:
		return &ScopeError{Field: "limit_tier", Reason: fmt.Sprintf("%q is not in the §11.6 vocabulary", t)}
	}
	return nil
}

// ScopeError is the typed error the admin API surfaces as
// INVALID_BREAKER_SCOPE (HTTP 422) per §11.6.
type ScopeError struct {
	Field  string
	Reason string
}

func (e *ScopeError) Error() string {
	return fmt.Sprintf("circuitbreaker: %s: %s", e.Field, e.Reason)
}

// Request is the admission-time request snapshot the
// AdmissionController matches against open breakers. Each field is
// the value the gateway resolved at admission time; empty strings
// mean "not applicable for this request kind" (e.g., a delegation
// admission has no Connector value; a connector invocation has no
// resolved Pool).
type Request struct {
	Runtime       string
	Pool          string
	Connector     string
	OperationType OperationType
}

// Match reports whether the request is rejected by the breaker. A
// closed breaker never rejects. An open breaker rejects when the
// request's tier-relevant field equals the breaker's Scope.
//
// §11.6 specifies that the breaker {name} is operator-facing only
// and is NOT used for matching — Match keys exclusively on the
// (LimitTier, Scope) pair.
func (b Breaker) Match(r Request) bool {
	if b.State != StateOpen {
		return false
	}
	switch b.LimitTier {
	case TierRuntime:
		return b.Scope.Runtime != "" && b.Scope.Runtime == r.Runtime
	case TierPool:
		return b.Scope.Pool != "" && b.Scope.Pool == r.Pool
	case TierConnector:
		return b.Scope.Connector != "" && b.Scope.Connector == r.Connector
	case TierOperationType:
		return b.Scope.OperationType != "" && b.Scope.OperationType == r.OperationType
	}
	return false
}

// FirstMatch returns the first open breaker that matches r, or nil
// when no open breaker matches. The AdmissionController scans the
// gateway's in-process breaker cache and surfaces the first match as
// the §11.6 CIRCUIT_BREAKER_OPEN rejection.
func FirstMatch(breakers []Breaker, r Request) *Breaker {
	for i := range breakers {
		if breakers[i].Match(r) {
			return &breakers[i]
		}
	}
	return nil
}

// ImmutableScopeError is returned when an admin caller attempts to
// reopen an existing breaker with a different LimitTier or Scope.
// §11.6: scope is immutable across the breaker's lifecycle; close
// and open a new one with a distinct name to change scope.
type ImmutableScopeError struct {
	Name string
}

func (e *ImmutableScopeError) Error() string {
	return fmt.Sprintf("circuitbreaker: breaker %q has a persisted scope; close it and open a new one with a distinct name to change scope", e.Name)
}

// ScopeMatches reports whether two breakers carry the same
// (LimitTier, Scope) pair. Used by the admin POST /open handler to
// enforce the §11.6 immutable-scope invariant.
func ScopeMatches(a, b Breaker) bool {
	return a.LimitTier == b.LimitTier &&
		a.Scope.Runtime == b.Scope.Runtime &&
		a.Scope.Pool == b.Scope.Pool &&
		a.Scope.Connector == b.Scope.Connector &&
		a.Scope.OperationType == b.Scope.OperationType
}
