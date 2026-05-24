// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
)

// routeTaskSpec is the §4.8 PreRoute / PostRoute content payload: the
// serialized TaskSpec the interceptor chain inspects around runtime
// selection. PreRoute carries the requested runtime and input before
// selection; PostRoute carries the resolved runtime metadata after
// selection (spec: §4.8 lines 1048, 1052). The JSON field names match
// the immutable-field paths the chain enforces on MODIFY
// (pkg/gateway/interceptor/immutability.go): `tenant_id` / `user_id`
// at PreRoute, `resolved_runtime_name` / `credential_pool_id` at
// PostRoute.
type routeTaskSpec struct {
	TenantID            string `json:"tenant_id"`
	UserID              string `json:"user_id,omitempty"`
	RequestedRuntime    string `json:"requested_runtime,omitempty"`
	Input               string `json:"input,omitempty"`
	ResolvedRuntimeName string `json:"resolved_runtime_name,omitempty"`
	CredentialPoolID    string `json:"credential_pool_id,omitempty"`
}

// runRouteChain runs the §4.8 chain for phase over the serialized
// routeTaskSpec and returns the (possibly MODIFY-rewritten) spec. The
// PreRoute chain (spec: §4.8 line 1048) fires after authentication and
// before runtime selection; the PostRoute chain (spec: §4.8 line 1052)
// fires after runtime selection with the resolved runtime metadata. The
// same chain the spec names for top-level session creation runs here.
//
// On a deliberate REJECT it emits the §16.7 `interceptor.rejected`
// audit row (synchronously, per §11.7) and writes a 403
// INTERCEPTOR_REJECTED response; on a fail-closed timeout/error it
// writes a 503 INTERCEPTOR_TIMEOUT (TRANSIENT, retryable) carrying the
// `interceptor_ref`, `phase`, and `timeout_ms` details (spec: §4.8
// line 1032, §15.1 line 1008). When the chain admits the request it
// returns ok=true; when it returns ok=false it has already written the
// response.
//
// A MODIFY rewrites the routeTaskSpec for the caller. The chain has
// already rejected any MODIFY that altered an immutable field
// (tenant_id/user_id at PreRoute, resolved_runtime_name/credential_pool_id
// at PostRoute) with INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION before this
// helper sees the result, so a returned MODIFY only carries permitted
// changes (runtime hints, input content at PreRoute; runtime-specific
// parameters at PostRoute).
func (s *Server) runRouteChain(w http.ResponseWriter, r *http.Request, phase interceptor.Phase, spec routeTaskSpec) (routeTaskSpec, bool) {
	if s.interceptors == nil || s.interceptors.Len(phase) == 0 {
		return spec, true
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"route task spec could not be serialized for the policy chain: "+err.Error(), nil)
		return routeTaskSpec{}, false
	}
	res := s.interceptors.Run(r.Context(), interceptor.Request{
		Phase:    phase,
		TenantID: spec.TenantID,
		Content:  payload,
		Metadata: map[string]string{
			policy.MetadataTenantID: spec.TenantID,
			policy.MetadataUserID:   spec.UserID,
		},
	})
	switch res.Action {
	case interceptor.ActionReject:
		if !s.recordRouteRejection(r.Context(), w, phase, spec.TenantID, spec.UserID, res) {
			return routeTaskSpec{}, false
		}
		return routeTaskSpec{}, false
	case interceptor.ActionModify:
		var modified routeTaskSpec
		if err := json.Unmarshal(res.ModifiedContent, &modified); err != nil {
			s.writeError(w, http.StatusBadGateway, "INTERCEPTOR_REJECTED",
				"a policy interceptor returned a MODIFY that is not a valid route task spec: "+err.Error(), nil)
			return routeTaskSpec{}, false
		}
		return modified, true
	default:
		return spec, true
	}
}

// recordRouteRejection writes the §16.7 audit row and the HTTP error
// envelope for a PreRoute/PostRoute chain REJECT. It returns false in
// every case (the caller always aborts); the bool exists so the audit
// failure path can short-circuit the response code distinctly. A
// fail-closed timeout/error (CodeInterceptorTimeout) maps to 503; a
// deliberate REJECT maps to 403 INTERCEPTOR_REJECTED.
func (s *Server) recordRouteRejection(ctx context.Context, w http.ResponseWriter, phase interceptor.Phase, tenantID, userID string, res interceptor.Result) bool {
	if s.policyAuditSink != nil {
		if err := s.policyAuditSink.RecordRejection(ctx, policy.RejectionContext{
			TenantID:  tenantID,
			CallerSub: userID,
			Phase:     phase,
		}, res); err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"policy rejection could not be recorded to the audit chain: "+err.Error(), nil)
			return false
		}
	}
	if res.Code == interceptor.CodeInterceptorTimeout {
		s.writeError(w, http.StatusServiceUnavailable, interceptor.CodeInterceptorTimeout, res.Reason,
			map[string]any{
				"interceptor_ref": res.RejectedBy,
				"phase":           string(phase),
				"timeout_ms":      res.TimeoutMs,
			})
		return false
	}
	details := map[string]any{"reason": res.Reason, "phase": string(phase)}
	if res.Code != "" {
		details["interceptorCode"] = res.Code
	}
	s.writeError(w, http.StatusForbidden, "INTERCEPTOR_REJECTED", res.Reason, details)
	return false
}
