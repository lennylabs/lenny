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
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessioncallback"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
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
