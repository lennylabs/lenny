// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"math"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
)

// routeTaskSpec is the §4.8 PreRoute / PostRoute content payload: the
// serialized TaskSpec the interceptor chain inspects around runtime
// selection. PreRoute carries the requested runtime and input before
// selection; PostRoute carries the resolved runtime metadata after
// selection (spec: §4.8). The JSON field names match
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
// PreRoute chain (spec: §4.8) fires after authentication and
// before runtime selection; the PostRoute chain (spec: §4.8)
// fires after runtime selection with the resolved runtime metadata. The
// same chain the spec names for top-level session creation runs here.
//
// On a deliberate REJECT it emits the §16.7 `interceptor.rejected`
// audit row (synchronously, per §11.7) and writes a 403
// INTERCEPTOR_REJECTED response; on a fail-closed timeout/error it
// writes a 503 INTERCEPTOR_TIMEOUT (TRANSIENT, retryable) carrying the
// `interceptor_ref`, `phase`, and `timeout_ms` details (spec: §4.8, §15.1). When the chain admits the request it
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
	return s.runRouteChainRange(w, r, phase, spec, math.MinInt32, math.MaxInt32)
}

// runRouteChainRange is runRouteChain restricted to the interceptors
// whose priority falls in the half-open window [minPriority,
// maxPriority). The PreRoute chain runs in two segments around the §4.8
// ExperimentRouter built-in (priority 300, spec: §4.8): the
// segment below 300 runs before experiment routing so a priority 101–299
// MODIFY of the runtime hint affects which variant the router assigns
// (spec: §4.8), and the segment at or above 300 runs after it
// so a priority ≥ 300 external interceptor orders after the built-in per
// the §4.8 line-12 ascending-priority rule. PostRoute and the other
// single-segment phases pass the full window via runRouteChain.
func (s *Server) runRouteChainRange(w http.ResponseWriter, r *http.Request, phase interceptor.Phase, spec routeTaskSpec, minPriority, maxPriority int32) (routeTaskSpec, bool) {
	if s.interceptors == nil || s.interceptors.LenRange(phase, minPriority, maxPriority) == 0 {
		return spec, true
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"route task spec could not be serialized for the policy chain: "+err.Error(), nil)
		return routeTaskSpec{}, false
	}
	res := s.interceptors.RunRange(r.Context(), interceptor.Request{
		Phase:    phase,
		TenantID: spec.TenantID,
		Content:  payload,
		Metadata: map[string]string{
			policy.MetadataTenantID: spec.TenantID,
			policy.MetadataUserID:   spec.UserID,
		},
	}, minPriority, maxPriority)
	switch res.Action {
	case interceptor.ActionReject:
		auditErr := s.auditRouteRejection(r.Context(), phase, spec.TenantID, spec.UserID, res)
		s.writeRouteRejection(w, phase, res, auditErr)
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

// auditRouteRejection writes the §16.7 `interceptor.rejected` audit row for
// a chain REJECT and returns the audit sink's error. No sink records nothing
// and returns nil. spec: §4.8; §16.7.
func (s *Server) auditRouteRejection(ctx context.Context, phase interceptor.Phase, tenantID, userID string, res interceptor.Result) error {
	if s.policyAuditSink == nil {
		return nil
	}
	return s.policyAuditSink.RecordRejection(ctx, policy.RejectionContext{
		TenantID:  tenantID,
		CallerSub: userID,
		Phase:     phase,
	}, res)
}

// writeRouteRejection writes the HTTP error envelope for a chain REJECT whose
// audit half returned auditErr. A failed audit write maps to 500; a
// fail-closed timeout/error (CodeInterceptorTimeout) maps to 503; an
// immutable-field violation (CodeInterceptorImmutableFieldViolation) maps to
// 400 INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION carrying interceptor_ref, phase,
// and violated_fields; a deliberate REJECT maps to 403 INTERCEPTOR_REJECTED.
// spec: §4.8; §15.1.
func (s *Server) writeRouteRejection(w http.ResponseWriter, phase interceptor.Phase, res interceptor.Result, auditErr error) {
	if auditErr != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"policy rejection could not be recorded to the audit chain: "+auditErr.Error(), nil)
		return
	}
	if res.Code == interceptor.CodeInterceptorTimeout {
		s.writeError(w, http.StatusServiceUnavailable, interceptor.CodeInterceptorTimeout, res.Reason,
			map[string]any{
				"interceptor_ref": res.RejectedBy,
				"phase":           string(phase),
				"timeout_ms":      res.TimeoutMs,
			})
		return
	}
	// An external MODIFY that altered an immutable field surfaces as the
	// §15.1 400 INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION (POLICY, non-retryable
	// via errorclassify) carrying the offending interceptor, phase, and the
	// violated field paths, so a caller or SIEM can distinguish identity or
	// routing tampering from an ordinary policy REJECT.
	// spec: §4.8 (immutable field enforcement), §15.1
	// (INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION POLICY/400 with violated_fields).
	if res.Code == interceptor.CodeInterceptorImmutableFieldViolation {
		s.writeError(w, http.StatusBadRequest, interceptor.CodeInterceptorImmutableFieldViolation, res.Reason,
			map[string]any{
				"interceptor_ref": res.RejectedBy,
				"phase":           string(phase),
				"violated_fields": res.ViolatedFields,
			})
		return
	}
	details := map[string]any{"reason": res.Reason, "phase": string(phase)}
	if res.Code != "" {
		details["interceptorCode"] = res.Code
	}
	s.writeError(w, http.StatusForbidden, "INTERCEPTOR_REJECTED", res.Reason, details)
}

// runPostAgentOutput runs the §4.8 PostAgentOutput chain over the agent's
// output parts before the gateway delivers them. It returns the possibly
// modified parts, the chain's result when it REJECTs (nil otherwise), and the
// audit-record error. On REJECT it writes the §16.7 audit row and returns the
// result, so the caller delivers nothing and writes its own answer; on
// MODIFY it returns the rewritten parts. A malformed MODIFY payload leaves the
// parts unchanged. It writes no response. spec: §4.8.
func (s *Server) runPostAgentOutput(ctx context.Context, tenantID, sessionID string, out []executor.MessagePart) ([]executor.MessagePart, *interceptor.Result, error) {
	if s.interceptors == nil || s.interceptors.Len(interceptor.PhasePostAgentOutput) == 0 {
		return out, nil, nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, nil, nil
	}
	res := s.interceptors.Run(ctx, interceptor.Request{
		Phase:     interceptor.PhasePostAgentOutput,
		SessionID: sessionID,
		TenantID:  tenantID,
		Content:   raw,
	})
	switch res.Action {
	case interceptor.ActionReject:
		auditErr := s.auditRouteRejection(ctx, interceptor.PhasePostAgentOutput, tenantID, "", res)
		return out, &res, auditErr
	case interceptor.ActionModify:
		var modified []executor.MessagePart
		if err := json.Unmarshal(res.ModifiedContent, &modified); err != nil {
			return out, nil, nil
		}
		return modified, nil, nil
	default:
		return out, nil, nil
	}
}
