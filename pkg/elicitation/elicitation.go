// SPDX-License-Identifier: MIT

// Package elicitation encodes the §9.2 elicitation-chain primitives:
// the EnforcementMode strict ordering plus platform-floor resolver,
// the DepthPolicy enum, the InitiatorType enum, and the
// gateway-origin-binding canonicalisation + SHA-256 digest the
// content-integrity verifier uses to detect tamper.
//
// The hop-by-hop elicitation chain, the URL-mode allowlist evaluator,
// the maxElicitationWait timer, and the respond_to_elicitation
// authorization triple are all wired by the gateway binary; this
// package keeps the primitives pure so the integrity contract can be
// unit-tested against the spec text.
package elicitation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// EnforcementMode is the §9.2 elicitation content integrity
// enforcement mode. Each tenant carries one; the platform floor
// clamps the effective value upward.
type EnforcementMode string

const (
	// ModeOff: no canonicalization or SHA-256 comparison runs. No
	// audit event, no counter increment. The detector does not run.
	ModeOff EnforcementMode = "off"
	// ModeDetectOnly: divergence is recorded (audit event + counter)
	// but the divergent payload is forwarded as received. Pre-
	// enforcement canary mode.
	ModeDetectOnly EnforcementMode = "detect-only"
	// ModeEnforce: divergence rejects the forward with
	// ELICITATION_CONTENT_TAMPERED (PERMANENT/409). Default tenant
	// value.
	ModeEnforce EnforcementMode = "enforce"
)

// AllModes returns the closed §9.2 enum in the spec's strict order:
// off < detect-only < enforce. Order matters — ResolveEffective uses
// it to clamp the tenant value upward against the platform floor.
func AllModes() []EnforcementMode { return []EnforcementMode{ModeOff, ModeDetectOnly, ModeEnforce} }

// IsValid reports whether m is one of the three §9.2 modes.
func (m EnforcementMode) IsValid() bool {
	for _, v := range AllModes() {
		if m == v {
			return true
		}
	}
	return false
}

// Rank returns the §9.2 strict-ordering rank: 0 for off, 1 for
// detect-only, 2 for enforce. Used by ResolveEffective to take the
// max of platform floor and tenant stored mode.
func (m EnforcementMode) Rank() int {
	switch m {
	case ModeOff:
		return 0
	case ModeDetectOnly:
		return 1
	case ModeEnforce:
		return 2
	}
	return -1
}

// AtLeast reports whether m is at least as strict as other under the
// §9.2 ordering.
func (m EnforcementMode) AtLeast(other EnforcementMode) bool {
	return m.Rank() >= other.Rank()
}

// ResolveEffective implements the §9.2 platform-floor clamp:
//
//	effective = max(platformFloor, tenantStored)
//
// The stricter of the two wins. Returns *InvalidModeError when either
// argument is not a valid §9.2 mode.
func ResolveEffective(platformFloor, tenantStored EnforcementMode) (EnforcementMode, error) {
	if !platformFloor.IsValid() {
		return "", &InvalidModeError{Field: "platformFloor", Value: string(platformFloor)}
	}
	if !tenantStored.IsValid() {
		return "", &InvalidModeError{Field: "tenantStored", Value: string(tenantStored)}
	}
	if platformFloor.Rank() > tenantStored.Rank() {
		return platformFloor, nil
	}
	return tenantStored, nil
}

// InvalidModeError captures the §9.2 mode-validation failure surfaced
// by the gateway admin API as 400 ELICITATION_INTEGRITY_INVALID_MODE.
type InvalidModeError struct {
	Field string
	Value string
}

func (e *InvalidModeError) Error() string {
	return fmt.Sprintf("elicitation: %s %q is not one of {off, detect-only, enforce}", e.Field, e.Value)
}

// DepthPolicy is the §9.2 pool-level elicitationDepthPolicy enum that
// governs auto-suppression of agent-initiated elicitations at deep
// delegation levels.
type DepthPolicy string

const (
	// DepthAllowAll: no suppression at any depth.
	DepthAllowAll DepthPolicy = "allow_all"
	// DepthSuppressAtDepth: suppress agent-initiated elicitations at
	// depth >= N (the N value is carried separately on the pool
	// config; this enum value identifies the policy mode).
	DepthSuppressAtDepth DepthPolicy = "suppress_at_depth"
	// DepthBlockAll: no elicitations from delegated sessions
	// (depth > 0).
	DepthBlockAll DepthPolicy = "block_all"
)

// AllDepthPolicies returns the closed enum.
func AllDepthPolicies() []DepthPolicy {
	return []DepthPolicy{DepthAllowAll, DepthSuppressAtDepth, DepthBlockAll}
}

// IsValid reports whether p is one of the three §9.2 depth policies.
func (p DepthPolicy) IsValid() bool {
	for _, v := range AllDepthPolicies() {
		if p == v {
			return true
		}
	}
	return false
}

// ShouldSuppress applies the §9.2 depth policy to a candidate hop.
// initiator is `connector` (always exempt — gateway-initiated OAuth
// flows are not suppressed) or `agent`. suppressAtDepth carries the
// threshold N for the DepthSuppressAtDepth policy; ignored otherwise.
// Returns true when the elicitation should be auto-suppressed at the
// gateway boundary.
func (p DepthPolicy) ShouldSuppress(initiator InitiatorType, depth, suppressAtDepth int) bool {
	if initiator == InitiatorConnector {
		return false
	}
	switch p {
	case DepthAllowAll:
		return false
	case DepthBlockAll:
		return depth > 0
	case DepthSuppressAtDepth:
		return depth >= suppressAtDepth
	}
	return false
}

// InitiatorType is the §9.2 provenance metadata initiator_type field.
type InitiatorType string

const (
	InitiatorConnector InitiatorType = "connector"
	InitiatorAgent     InitiatorType = "agent"
)

// AllInitiatorTypes returns the closed enum.
func AllInitiatorTypes() []InitiatorType {
	return []InitiatorType{InitiatorConnector, InitiatorAgent}
}

// IsValid reports whether t is one of the two §9.2 initiator types.
func (t InitiatorType) IsValid() bool {
	return t == InitiatorConnector || t == InitiatorAgent
}

// Content captures the §9.2 `{message, schema}` pair that the
// gateway records at origination time and re-validates on every
// forward hop. The schema is opaque to this package; the canonical-
// form digest is what the integrity verifier compares against.
type Content struct {
	Message string
	// Schema is the JSON Schema (or null) the elicitation declares.
	// May be a map[string]any, nil, or any other JSON value.
	Schema any
}

// Digest returns the §9.2 canonicalised SHA-256 hex digest of c. The
// canonical form is the JSON encoding of `{message, schema}` with
// stable key ordering (alphabetical on every object level) and UTF-8
// normalisation. Two Content values produce the same digest iff
// they are semantically equal.
func (c Content) Digest() (string, error) {
	canonical, err := canonicalize(map[string]any{
		"message": c.Message,
		"schema":  c.Schema,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// Equal reports whether c and other have identical digests. The
// caller is responsible for handling the Digest error; this helper
// returns false on either side erroring (defensive — a non-encodable
// schema cannot match anything).
func (c Content) Equal(other Content) bool {
	a, err := c.Digest()
	if err != nil {
		return false
	}
	b, err := other.Digest()
	if err != nil {
		return false
	}
	return a == b
}

// VerifyContent implements the §9.2 forward-hop content-integrity
// check. Given the gateway-recorded original digest and the inbound
// forward-hop content, returns:
//
//   - nil          — digests match; the forward is admitted.
//   - *TamperError — digests diverge. Caller emits the audit event
//     and counter; the effective mode determines whether to reject
//     the forward (ModeEnforce → ELICITATION_CONTENT_TAMPERED) or to
//     forward as-is with the divergence recorded (ModeDetectOnly).
//     Under ModeOff this function should NOT be called.
func VerifyContent(originalDigest string, inbound Content) error {
	if originalDigest == "" {
		return errors.New("elicitation: originalDigest is required")
	}
	got, err := inbound.Digest()
	if err != nil {
		return fmt.Errorf("elicitation: inbound digest: %w", err)
	}
	if got == originalDigest {
		return nil
	}
	return &TamperError{
		ExpectedDigest: originalDigest,
		ObservedDigest: got,
	}
}

// TamperError captures the §9.2 content-integrity divergence. Carries
// the two digests so the gateway audit event can record what was
// observed alongside what was expected.
type TamperError struct {
	ExpectedDigest string
	ObservedDigest string
}

func (e *TamperError) Error() string {
	return fmt.Sprintf("elicitation: content tampered: expected digest %s, observed %s (§9.2 gateway-origin-binding)", e.ExpectedDigest, e.ObservedDigest)
}

// Provenance is the §9.2 provenance metadata the gateway stamps on
// every elicitation before forwarding it. Client UIs render the
// fields prominently so users can distinguish platform OAuth flows
// from agent-initiated prompts.
type Provenance struct {
	OriginPod        string
	DelegationDepth  int
	OriginRuntime    string
	Purpose          string
	ConnectorID      string
	ExpectedDomain   string
	InitiatorType    InitiatorType
}

// Validate reports the §9.2 provenance-shape errors.
func (p Provenance) Validate() error {
	if p.OriginPod == "" {
		return errors.New("elicitation: provenance.origin_pod is required")
	}
	if !p.InitiatorType.IsValid() {
		return fmt.Errorf("elicitation: provenance.initiator_type %q is not in {connector, agent}", p.InitiatorType)
	}
	if p.InitiatorType == InitiatorConnector && p.ConnectorID == "" {
		return errors.New("elicitation: connector-initiated provenance requires connector_id")
	}
	if p.DelegationDepth < 0 {
		return errors.New("elicitation: provenance.delegation_depth must be >= 0")
	}
	return nil
}

// canonicalize returns the §9.2 stable-key-ordered JSON encoding of
// v. The recursion sorts every map's keys alphabetically; arrays and
// scalars are encoded verbatim. Used to produce the digest input.
func canonicalize(v any) ([]byte, error) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []byte{'{'}
		for i, k := range keys {
			if i > 0 {
				out = append(out, ',')
			}
			keyJSON, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			out = append(out, keyJSON...)
			out = append(out, ':')
			child, err := canonicalize(t[k])
			if err != nil {
				return nil, err
			}
			out = append(out, child...)
		}
		out = append(out, '}')
		return out, nil
	case []any:
		out := []byte{'['}
		for i, item := range t {
			if i > 0 {
				out = append(out, ',')
			}
			child, err := canonicalize(item)
			if err != nil {
				return nil, err
			}
			out = append(out, child...)
		}
		out = append(out, ']')
		return out, nil
	default:
		return json.Marshal(t)
	}
}
