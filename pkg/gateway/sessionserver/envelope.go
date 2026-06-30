// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// maxRuntimeOptionsBytes is the §14 line 155 runtimeOptions size ceiling.
// spec: §14 line 155 — "Maximum size: 64 KB". F-14.1.14.
const maxRuntimeOptionsBytes = 64 * 1024

// validateRequestEnvelope validates the §14 CreateSessionRequest envelope
// fields (env, pool, timeouts, credentialPolicy, delegationLease,
// runtimeOptions) and copies the accepted values onto row. It returns the
// §14 warnings that admission produced (currently the
// RuntimeOptionsUnschematized warning) and ok=false after writing the
// §15.1 error envelope when any field is rejected. spec: §14 lines 47-79,
// 154-155. F-14.1.12 / F-14.1.14 / F-14.1.15.
func (s *Server) validateRequestEnvelope(w http.ResponseWriter, r *http.Request, req CreateSessionRequest, tenantID string, row *sessionstore.Session) ([]workspaceplan.Warning, bool) {
	// spec: §14 lines 47-50, 105 — every env key is matched against the
	// deployer blocklist (exact names and `*` globs). A blocked key is
	// rejected with 400 ENV_VAR_BLOCKLISTED identifying the offending key
	// and the matching pattern. F-14.1.12.
	if len(req.Env) > 0 {
		for key := range req.Env {
			if key == "" {
				s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
					"env keys must be non-empty", map[string]any{"field": "env"})
				return nil, false
			}
			if pattern, blocked := s.envBlocklist.Match(key); blocked {
				s.writeError(w, http.StatusBadRequest, "ENV_VAR_BLOCKLISTED",
					fmt.Sprintf("env var %q is blocked by the deployer env blocklist (pattern %q)", key, pattern),
					map[string]any{"field": "env", "key": key, "pattern": pattern})
				return nil, false
			}
		}
		row.Env = cloneMetadata(req.Env)
	}

	// spec: §14 line 311 / §15.1 line 598 — copy the client labels onto
	// the row so the list endpoint can filter on them. Keys must be
	// non-empty so the on-row selector set stays well-formed. F-15.1.15.
	if len(req.Labels) > 0 {
		for key := range req.Labels {
			if key == "" {
				s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
					"label keys must be non-empty", map[string]any{"field": "labels"})
				return nil, false
			}
		}
		row.Labels = cloneMetadata(req.Labels)
	}

	// spec: §7.1 line 6 — reject an empty metadata key so the on-row
	// annotation map stays well-formed, mirroring the §14 env and label
	// key checks above. The metadata round-trips to the §15.1 GET
	// envelope, so an empty key would echo back an unkeyed annotation.
	// F-CS4 (0018).
	for key := range req.Metadata {
		if key == "" {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"metadata keys must be non-empty", map[string]any{"field": "metadata"})
			return nil, false
		}
	}

	// spec: §7.1 step 1, §7.1 step 4, §14.1 — `pool` is a first-class
	// client scheduling selector, so a pin the gateway cannot satisfy is
	// rejected (400/403/503) rather than accepted, echoed, and dropped from
	// scheduling. validateRequestEnvelope runs before the §7.1 claim, so the
	// gate fails fast before any pod is claimed. F-CS2 (0018).
	//
	// spec: §7.1 line 18 / line 75 — when a pool is pinned, the named pool's
	// own profile governs the session's isolation (the pin overrides the
	// default pool selection and the resolved level is populated from the
	// assigned pool's configuration). Pass the client's raw request profile
	// (empty when the client omitted isolationProfile), not the defaulted
	// row.IsolationProfile, so ResolvePool's `isolationProfile != ""`
	// short-circuit lets the pool's profile govern and rejects only an
	// explicitly-requested profile the pool does not satisfy. A defaulted
	// profile would reject a pin whose pool differs from the deployment
	// default even though the client deferred to the pool. F-CS2 (0018).
	if !s.requirePoolSelectable(w, r, tenantID, req.RuntimeRef, string(req.IsolationProfile), req.Pool) {
		return nil, false
	}
	row.Pool = req.Pool

	// spec: §14 line 154 — per-session timeouts are non-negative and
	// cannot exceed the runtime's limits.maxSessionAge. F-14.1.14.
	if req.Timeouts != nil {
		if req.Timeouts.MaxSessionAgeSeconds < 0 || req.Timeouts.MaxIdleSeconds < 0 {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"timeouts.maxSessionAgeSeconds and timeouts.maxIdleSeconds must be non-negative",
				map[string]any{"field": "timeouts"})
			return nil, false
		}
		if cap := s.runtimeMaxSessionAge(r.Context(), req.RuntimeRef); cap > 0 &&
			req.Timeouts.MaxSessionAgeSeconds > int64(cap) {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				fmt.Sprintf("timeouts.maxSessionAgeSeconds %d exceeds the runtime limits.maxSessionAge cap of %d",
					req.Timeouts.MaxSessionAgeSeconds, cap),
				map[string]any{"field": "timeouts.maxSessionAgeSeconds", "reason": "exceeds_runtime_cap", "cap": cap})
			return nil, false
		}
		t := *req.Timeouts
		row.Timeouts = &t
	}

	// spec: §14 credentialPolicy; §4.9 lines 1310, 1336 — the per-session
	// preferredSource must be a valid enum value and may only restrict,
	// not expand, the tenant credentialPolicy. F-14.1.14.
	if req.CredentialPolicy != nil {
		ps := credential.PreferredSource(req.CredentialPolicy.PreferredSource)
		if ps != "" && !ps.IsValid() {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				fmt.Sprintf("credentialPolicy.preferredSource %q must be one of %v",
					req.CredentialPolicy.PreferredSource, credential.AllPreferredSources()),
				map[string]any{"field": "credentialPolicy.preferredSource"})
			return nil, false
		}
		if ps != "" && s.overrideExpandsTenantPolicy(r.Context(), tenantID, ps) {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				fmt.Sprintf("credentialPolicy.preferredSource %q expands the tenant credentialPolicy; a per-session override may only restrict it", ps),
				map[string]any{"field": "credentialPolicy.preferredSource", "reason": "expands_tenant_policy"})
			return nil, false
		}
		c := *req.CredentialPolicy
		row.CredentialPolicyOverride = &c
	}

	// spec: §14 lines 75-79 — delegation lease bounds are non-negative.
	// F-14.1.14.
	if req.DelegationLease != nil {
		if req.DelegationLease.MaxDepth != nil && *req.DelegationLease.MaxDepth < 0 {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"delegationLease.maxDepth must be non-negative",
				map[string]any{"field": "delegationLease.maxDepth"})
			return nil, false
		}
		if req.DelegationLease.MaxChildrenTotal != nil && *req.DelegationLease.MaxChildrenTotal < 0 {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"delegationLease.maxChildrenTotal must be non-negative",
				map[string]any{"field": "delegationLease.maxChildrenTotal"})
			return nil, false
		}
		row.DelegationLeaseRequest = cloneDelegationLeaseRequest(req.DelegationLease)
	}

	// spec: §14 line 155 — runtimeOptions is ≤64 KB and validated against
	// the runtime's runtimeOptionsSchema when one is registered; when no
	// schema is registered the options pass through and a
	// RuntimeOptionsUnschematized warning is emitted. F-14.1.14 / F-14.1.15.
	warnings, ok := s.validateRuntimeOptions(w, r, req, row)
	if !ok {
		return nil, false
	}

	// spec: §14 lines 108-139 — validate the callbackUrl against the SSRF
	// mitigations and KMS-seal the callbackSecret. F-14.1.11.
	if !s.validateCallback(w, r, req.CallbackURL, req.CallbackSecret, tenantID, row) {
		return nil, false
	}
	return warnings, true
}

// validateCallback applies the §14 callbackUrl SSRF mitigations and, when
// a callbackSecret is supplied, KMS-envelope-seals it onto the row. An
// empty callbackURL is a no-op (a bare callbackSecret is rejected). It
// returns ok=false after writing the §15.1 INVALID_CALLBACK_URL (or
// VALIDATION_ERROR) envelope on rejection. spec: §14 lines 108-139; §15.1
// line 1097. F-14.1.11 / F-15.1.11.
func (s *Server) validateCallback(w http.ResponseWriter, r *http.Request, callbackURL, callbackSecret, tenantID string, row *sessionstore.Session) bool {
	if callbackURL == "" {
		if callbackSecret != "" {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"callbackSecret requires callbackUrl", map[string]any{"field": "callbackSecret"})
			return false
		}
		return true
	}
	res, err := s.callbackValidator.Validate(r.Context(), callbackURL)
	if err != nil {
		reason := sessioncallback.ReasonInvalidURL
		var ve *sessioncallback.ValidationError
		if errors.As(err, &ve) {
			reason = ve.Reason
		}
		s.writeError(w, http.StatusBadRequest, "INVALID_CALLBACK_URL", err.Error(),
			map[string]any{"field": "callbackUrl", "reason": reason})
		return false
	}
	row.CallbackURL = res.URL
	row.CallbackPinnedIP = res.PinnedIP.String()
	// spec: §14 line 139 — the callbackSecret is KMS-envelope-encrypted at
	// admission; the gateway never persists or returns the plaintext.
	if callbackSecret != "" {
		if s.callbackSeal == nil {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"callbackSecret is not accepted: no KMS backend is configured for session callbacks",
				map[string]any{"field": "callbackSecret"})
			return false
		}
		sealed, serr := s.callbackSeal(r.Context(), tenantID, []byte(callbackSecret))
		if serr != nil {
			s.writeError(w, http.StatusServiceUnavailable, "INTERNAL",
				"could not secure callbackSecret", nil)
			return false
		}
		row.CallbackSecret = sealed
	}
	return true
}

// runtimeMaxSessionAge returns the resolved runtime's
// limits.maxSessionAge in seconds, or 0 when no runtime / limit is
// registered (no cap). spec: §14 line 154; §5.1 limits. F-14.1.14.
func (s *Server) runtimeMaxSessionAge(ctx context.Context, runtimeRef string) int {
	if s.runtimes == nil || runtimeRef == "" {
		return 0
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, runtimeRef)
	if err != nil || rt.Limits == nil {
		return 0
	}
	return rt.Limits.MaxSessionAgeSeconds
}

// runtimeMaxClientIdle returns the resolved runtime's effective
// sessionPolicy.maxClientIdleSeconds in seconds, or 0 when no runtime is
// registered (no per-runtime idle bound). The bound is
// sessionPolicy.maxClientIdleSeconds when the runtime declares one; absent
// that it defaults to the runtime's effective maxSessionAgeSeconds, the §6.2
// idle-clock default that supersedes the removed limits.maxIdleTimeSeconds
// knob. The §27.6 playground idle override is resolved min-wins against this
// value at create time. spec: §5.2 (sessionPolicy.maxClientIdleSeconds); §6.2
// (idle clock defaults to the effective maxSessionAgeSeconds); §27.6 line 201.
func (s *Server) runtimeMaxClientIdle(ctx context.Context, runtimeRef string) int {
	if s.runtimes == nil || runtimeRef == "" {
		return 0
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, runtimeRef)
	if err != nil {
		return 0
	}
	if rt.SessionPolicy != nil && rt.SessionPolicy.MaxClientIdleSeconds > 0 {
		return rt.SessionPolicy.MaxClientIdleSeconds
	}
	// spec: §6.2 / §3.1 — maxClientIdleSeconds defaults to the pool's
	// effective maxSessionAgeSeconds when no idle bound is declared.
	return s.runtimeMaxSessionAge(ctx, runtimeRef)
}

// runtimeSDKWarm returns the resolved runtime's §6.1 SDK-warm inputs: the
// capabilities.preConnect flag and the sdkWarmBlockingPaths glob list the
// binder uses to decide demotion. A pod-warm or unresolvable runtime
// returns (false, nil) so the binder takes the pod-warm path. spec: §5.1
// capabilities.preConnect / sdkWarmBlockingPaths; §6.1 lines 30-40.
func (s *Server) runtimeSDKWarm(ctx context.Context, tenantID, runtimeRef string) (preConnect bool, blockingPaths []string) {
	if s.runtimes == nil || runtimeRef == "" {
		return false, nil
	}
	// §5.1 line 49: a tenant may toggle preConnect or add demotion blockers
	// to sdkWarmBlockingPaths for this runtime; overlay the override before
	// the binder reads the SDK-warm inputs. F-5.1.20.
	rt, err := runtimecapoverride.ResolveForTenant(ctx, s.runtimes, s.capOverrides, tenantID, runtimeRef)
	if err != nil || rt.Capabilities == nil || !rt.Capabilities.PreConnect {
		return false, nil
	}
	return true, rt.SDKWarmBlockingPaths
}

// overrideExpandsTenantPolicy reports whether a per-session
// preferredSource override would enable a credential source the tenant
// policy disallows. The expandable axis is user-scoped credentials: an
// override that resolves to user credentials when the tenant policy does
// not use them (and does not enable them) is an expansion the §14
// "restrict, not expand" rule forbids. A narrowing override (e.g. tenant
// allows user-then-pool, session pins to pool) is permitted. spec: §14
// credentialPolicy; §4.9 line 1362. F-14.1.14.
func (s *Server) overrideExpandsTenantPolicy(ctx context.Context, tenantID string, override credential.PreferredSource) bool {
	if s.tenants == nil {
		return false
	}
	tenant, err := s.tenants.Get(ctx, tenantID)
	if err != nil {
		return false
	}
	policy := tenant.CredentialPolicy
	if override.UsesUserCredentials() {
		return !policy.PreferredSource.UsesUserCredentials() && !policy.UserCredentialsEnabled
	}
	return false
}

// validateRuntimeOptions enforces the §14 line 155 runtimeOptions size
// ceiling and the per-runtime discriminated-union schema. When the
// runtime registered a runtimeOptionsSchema the blob is validated against
// it (400 RUNTIME_OPTIONS_INVALID on failure). When no schema is
// registered the blob passes through and a RuntimeOptionsUnschematized
// warning is returned for the caller to publish. F-14.1.14 / F-14.1.15.
func (s *Server) validateRuntimeOptions(w http.ResponseWriter, r *http.Request, req CreateSessionRequest, row *sessionstore.Session) ([]workspaceplan.Warning, bool) {
	if len(req.RuntimeOptions) == 0 {
		return nil, true
	}
	if len(req.RuntimeOptions) > maxRuntimeOptionsBytes {
		s.writeError(w, http.StatusBadRequest, "RUNTIME_OPTIONS_INVALID",
			fmt.Sprintf("runtimeOptions exceeds the %d-byte limit", maxRuntimeOptionsBytes),
			map[string]any{"field": "runtimeOptions", "reason": "size_limit", "maxBytes": maxRuntimeOptionsBytes})
		return nil, false
	}
	var opts any
	if err := json.Unmarshal(req.RuntimeOptions, &opts); err != nil {
		s.writeError(w, http.StatusBadRequest, "RUNTIME_OPTIONS_INVALID",
			"runtimeOptions is not valid JSON",
			map[string]any{"field": "runtimeOptions", "reason": "invalid_json"})
		return nil, false
	}

	var schema json.RawMessage
	if s.runtimes != nil && req.RuntimeRef != "" {
		if rt, err := runtimestore.Resolve(r.Context(), s.runtimes, req.RuntimeRef); err == nil {
			schema = rt.RuntimeOptionsSchema
		}
	}

	row.RuntimeOptions = append(json.RawMessage(nil), req.RuntimeOptions...)

	if len(schema) == 0 {
		// spec: §14 line 155 — no schema registered: pass through and
		// emit the RuntimeOptionsUnschematized warning. F-14.1.15.
		return []workspaceplan.Warning{{
			Code:    workspaceplan.WarnRuntimeOptionsUnschematized,
			Field:   "runtimeOptions",
			Message: fmt.Sprintf("runtime %q registered no runtimeOptionsSchema; runtimeOptions passed through unvalidated", req.RuntimeRef),
		}}, true
	}

	if report := validateAgainstSchema(schema, opts); report != "" {
		s.writeError(w, http.StatusBadRequest, "RUNTIME_OPTIONS_INVALID",
			"runtimeOptions failed schema validation",
			map[string]any{"field": "runtimeOptions", "reason": "schema_validation", "report": report})
		return nil, false
	}
	return nil, true
}

// validateAgainstSchema compiles the runtime's runtimeOptionsSchema and
// validates value against it, returning a human-readable validation
// report on failure or "" on success. A schema that fails to compile is
// treated as "no constraint" (the runtime author owns the schema; a
// malformed one must not wedge session creation). spec: §14 line 155.
func validateAgainstSchema(schema json.RawMessage, value any) string {
	c := jsonschema.NewCompiler()
	if err := c.AddResource("runtimeOptions.json", bytes.NewReader(schema)); err != nil {
		return ""
	}
	compiled, err := c.Compile("runtimeOptions.json")
	if err != nil {
		return ""
	}
	if err := compiled.Validate(value); err != nil {
		return err.Error()
	}
	return ""
}
