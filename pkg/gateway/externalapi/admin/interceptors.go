// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor/interceptorstore"
	"github.com/lennylabs/lenny/pkg/observability/audit"
)

// InterceptorPayload is the §4.8 / §15.1 admin interceptor-registry wire
// shape. The §8.3 SEC-013 transition fields (failOpenTransitionAt,
// cooldownSecondsAtTransition) are response-only: the create and update
// handlers reject a request body that carries any of them (or a
// transition_ts / cooldownSeconds alias) with INTERCEPTOR_COOLDOWN_IMMUTABLE
// so a compromised admin credential cannot collapse the cooldown window.
type InterceptorPayload struct {
	Name       string   `json:"name"`
	Endpoint   string   `json:"endpoint"`
	Priority   int32    `json:"priority,omitempty"`
	FailPolicy string   `json:"failPolicy,omitempty"`
	TimeoutMs  int      `json:"timeoutMs,omitempty"`
	Phases     []string `json:"phases,omitempty"`

	// FailOpenTransitionAt / CooldownSecondsAtTransition are the §8.3
	// SEC-013 server-minted, admin-API-immutable transition fields,
	// rendered read-only on GET/LIST/PUT responses.
	FailOpenTransitionAt        string `json:"failOpenTransitionAt,omitempty"`
	CooldownSecondsAtTransition int    `json:"cooldownSecondsAtTransition,omitempty"`

	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	ETag      string `json:"etag,omitempty"`
}

// immutableInterceptorFields are the §8.3 SEC-013 request-body keys the
// admin write path rejects: the server-minted transition timestamp and
// the cluster-scoped cooldown duration (and their snake_case / alias
// spellings). A request carrying any of them is rejected as a whole with
// INTERCEPTOR_COOLDOWN_IMMUTABLE.
var immutableInterceptorFields = []string{
	"failOpenTransitionAt", "fail_open_transition_at",
	"transitionTs", "transition_ts",
	"cooldownSeconds", "cooldown_seconds",
	"cooldownSecondsAtTransition", "cooldown_seconds_at_transition",
}

// fromInterceptor maps a stored interceptor to the wire payload.
func fromInterceptor(ic interceptorstore.Interceptor) InterceptorPayload {
	phases := make([]string, 0, len(ic.Phases))
	for _, p := range ic.Phases {
		phases = append(phases, string(p))
	}
	return InterceptorPayload{
		Name:                        ic.Name,
		Endpoint:                    ic.Endpoint,
		Priority:                    ic.Priority,
		FailPolicy:                  string(ic.FailPolicy),
		TimeoutMs:                   ic.TimeoutMs,
		Phases:                      phases,
		FailOpenTransitionAt:        rfc3339Nano(ic.FailOpenTransitionAt),
		CooldownSecondsAtTransition: ic.CooldownSecondsAtTransition,
		CreatedAt:                   rfc3339Nano(ic.CreatedAt),
		UpdatedAt:                   rfc3339Nano(ic.UpdatedAt),
		ETag:                        formatETag(ic.Version),
	}
}

func toInterceptorPhases(in []string) []interceptor.Phase {
	out := make([]interceptor.Phase, 0, len(in))
	for _, p := range in {
		out = append(out, interceptor.Phase(p))
	}
	return out
}

// WithInterceptors wires the §4.8 / §15.1 interceptor-registry CRUD
// handlers onto the Router. cooldownSeconds is the cluster-scoped
// `gateway.interceptorWeakeningCooldownSeconds` value recorded on a
// `fail-closed → fail-open` transition (the §8.3 meta-cooldown rule pins
// a pending cooldown to the value recorded at the transition). A nil
// store leaves the routes unregistered. F-4.8.17.
func (r *Router) WithInterceptors(s interceptorstore.Store, cooldownSeconds int) *Router {
	r.interceptors = s
	r.interceptorCooldownSeconds = cooldownSeconds
	return r
}

// decodeInterceptorBody reads the request body, rejects any §8.3 SEC-013
// immutable field, and unmarshals the remainder into an
// InterceptorPayload. It writes the error envelope and returns ok=false
// on a malformed body or an immutable-field violation.
func decodeInterceptorBody(w http.ResponseWriter, req *http.Request) (InterceptorPayload, bool) {
	var body InterceptorPayload
	raw, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not readable", nil)
		return body, false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return body, false
	}
	for _, f := range immutableInterceptorFields {
		if _, present := probe[f]; present {
			// spec: §8.3 SEC-013 — the transition_ts field and the cooldown
			// duration are not admin-API-writable; the request is rejected
			// as a whole (not silently stripped) so a compromised client
			// cannot probe for acceptance.
			writeError(w, http.StatusBadRequest, "INTERCEPTOR_COOLDOWN_IMMUTABLE",
				"the interceptor cooldown transition timestamp and duration are server-minted and cannot be set via the admin API",
				map[string]any{"field": f})
			return body, false
		}
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return body, false
	}
	return body, true
}

// writeInterceptorValidationError maps an interceptorstore validation
// error to the §15.1 envelope, surfacing the §4.8 INVALID_INTERCEPTOR_*
// codes for the priority and phase sentinels.
func writeInterceptorValidationError(w http.ResponseWriter, err error) {
	if code, status, ok := interceptor.RegistrationErrorCode(err); ok {
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
}

func (r *Router) handleCreateInterceptor(w http.ResponseWriter, req *http.Request) {
	body, ok := decodeInterceptorBody(w, req)
	if !ok {
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	ic := interceptorstore.Interceptor{
		Name:       body.Name,
		Endpoint:   body.Endpoint,
		Priority:   body.Priority,
		FailPolicy: interceptor.FailPolicy(body.FailPolicy),
		TimeoutMs:  body.TimeoutMs,
		Phases:     toInterceptorPhases(body.Phases),
		CreatedAt:  r.clock(),
	}
	ic.UpdatedAt = ic.CreatedAt
	interceptorstore.ApplyDefaults(&ic)
	if err := interceptorstore.Validate(ic); err != nil {
		writeInterceptorValidationError(w, err)
		return
	}
	// spec: §15.1 line 1140 — ?dryRun=true validates without persisting.
	if req.URL.Query().Get("dryRun") == "true" {
		writeDryRun(w, http.StatusCreated, fromInterceptor(ic))
		return
	}
	if err := r.interceptors.Create(req.Context(), ic); err != nil {
		if errors.Is(err, interceptorstore.ErrAlreadyExists) {
			// spec: §15.1 line 983 — duplicate identifier is RESOURCE_ALREADY_EXISTS.
			writeError(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS",
				"interceptor with this name already exists", nil)
			return
		}
		writeInterceptorValidationError(w, err)
		return
	}
	stored, _ := r.interceptors.Get(req.Context(), ic.Name)
	r.emit(req.Context(), principal, "admin.interceptor.created", ic.Name, map[string]any{
		"endpoint":   stored.Endpoint,
		"priority":   stored.Priority,
		"failPolicy": string(stored.FailPolicy),
	})
	w.Header().Set("ETag", formatETag(stored.Version))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromInterceptor(stored))
}

func (r *Router) handleListInterceptors(w http.ResponseWriter, req *http.Request) {
	rows, err := r.interceptors.List(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]InterceptorPayload, 0, len(rows))
	for _, ic := range rows {
		out = append(out, fromInterceptor(ic))
	}
	writePaginatedList(w, req, r.clock(), out, adminTimestampSortFields, adminListDefaultSort,
		func(x InterceptorPayload, s pagination.Sort) (string, string) {
			switch s.Field {
			case "name":
				return x.Name, x.Name
			case "updated_at":
				return x.UpdatedAt, x.Name
			default:
				return x.CreatedAt, x.Name
			}
		})
}

func (r *Router) handleGetInterceptor(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	row, err := r.interceptors.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, interceptorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "interceptor not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("ETag", formatETag(row.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromInterceptor(row))
}

// handleUpdateInterceptor implements PUT — a full replace of the mutable
// registration fields. A `fail-closed → fail-open` transition
// server-mints the §8.3 SEC-013 transition timestamp and records the
// cluster cooldown then in force, emits interceptor.fail_policy_weakened
// plus interceptor.weakening_cooldown_active, and arms the weakening
// cooldown. The reverse transition emits interceptor.fail_policy_strengthened
// and clears any pending cooldown immediately.
func (r *Router) handleUpdateInterceptor(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	body, ok := decodeInterceptorBody(w, req)
	if !ok {
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	current, gerr := r.interceptors.Get(req.Context(), name)
	if gerr != nil {
		if errors.Is(gerr, interceptorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "interceptor not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	// spec: §15.1 lines 1207-1211 — every admin PUT requires If-Match.
	if !enforceIfMatch(w, req, current.Version) {
		return
	}
	newFailPolicy := interceptor.FailPolicy(body.FailPolicy)
	if newFailPolicy == "" {
		newFailPolicy = interceptor.FailClosed
	}
	// Validate the merged candidate up-front so the §4.8 priority/phase
	// codes surface before any state change.
	candidate := current
	r.applyInterceptorUpdate(&candidate, body, newFailPolicy)
	if err := interceptorstore.Validate(candidate); err != nil {
		writeInterceptorValidationError(w, err)
		return
	}
	if req.URL.Query().Get("dryRun") == "true" {
		writeDryRun(w, http.StatusOK, fromInterceptor(candidate))
		return
	}
	oldFailPolicy := current.FailPolicy
	updated, err := r.interceptors.Update(req.Context(), name, func(ic *interceptorstore.Interceptor) error {
		r.applyInterceptorUpdate(ic, body, newFailPolicy)
		return nil
	})
	if err != nil {
		if errors.Is(err, interceptorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "interceptor not found", nil)
			return
		}
		writeInterceptorValidationError(w, err)
		return
	}
	r.emit(req.Context(), principal, "admin.interceptor.updated", name, map[string]any{
		"endpoint":   updated.Endpoint,
		"failPolicy": string(updated.FailPolicy),
	})
	r.emitInterceptorFailPolicyTransition(req.Context(), principal, name, oldFailPolicy, updated)
	w.Header().Set("ETag", formatETag(updated.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromInterceptor(updated))
}

// applyInterceptorUpdate merges the wire payload onto an interceptor in
// place and server-mints the §8.3 SEC-013 transition state from the
// interceptor's prior fail policy. It is shared by the dry-run preview
// and the persisted update so the preview reflects exactly what a write
// produces. The transition timestamp is minted from the gateway clock —
// never read from the request body (decodeInterceptorBody rejects it).
func (r *Router) applyInterceptorUpdate(ic *interceptorstore.Interceptor, body InterceptorPayload, newFailPolicy interceptor.FailPolicy) {
	oldFailPolicy := ic.FailPolicy
	ic.Endpoint = body.Endpoint
	ic.Priority = body.Priority
	ic.FailPolicy = newFailPolicy
	ic.TimeoutMs = body.TimeoutMs
	ic.Phases = toInterceptorPhases(body.Phases)
	interceptorstore.ApplyDefaults(ic)
	switch {
	case oldFailPolicy == interceptor.FailClosed && ic.FailPolicy == interceptor.FailOpen:
		// spec: §8.3 SEC-013 — server-mint the transition timestamp and
		// pin the cooldown duration in force so a later cluster-config
		// reduction cannot shorten this pending window.
		ic.FailOpenTransitionAt = r.clock()
		ic.CooldownSecondsAtTransition = r.interceptorCooldownSeconds
	case oldFailPolicy == interceptor.FailOpen && ic.FailPolicy == interceptor.FailClosed:
		// Strengthening clears any pending cooldown so subsequent
		// delegations admit immediately.
		ic.FailOpenTransitionAt = time.Time{}
		ic.CooldownSecondsAtTransition = 0
	}
}

// emitInterceptorFailPolicyTransition emits the §4.8 line 1034 / §8.3
// line 218 audit events for a failPolicy change. A weakening
// (fail-closed → fail-open) emits interceptor.fail_policy_weakened plus a
// single interceptor.weakening_cooldown_active for the window entry; a
// strengthening (fail-open → fail-closed) emits
// interceptor.fail_policy_strengthened. No event fires when the value is
// unchanged.
func (r *Router) emitInterceptorFailPolicyTransition(ctx context.Context, p authmw.Principal, name string, oldFailPolicy interceptor.FailPolicy, updated interceptorstore.Interceptor) {
	if oldFailPolicy == updated.FailPolicy {
		return
	}
	count, names := r.interceptorAffectedPolicies(ctx, name)
	detail := map[string]any{
		"interceptor_ref":       name,
		"old_fail_policy":       string(oldFailPolicy),
		"new_fail_policy":       string(updated.FailPolicy),
		"affected_policy_count": count,
		"affected_policy_names": names,
	}
	if oldFailPolicy == interceptor.FailClosed && updated.FailPolicy == interceptor.FailOpen {
		transitionTs := rfc3339Nano(updated.FailOpenTransitionAt)
		cooldown := updated.CooldownSecondsAtTransition
		detail["transition_ts"] = transitionTs
		detail["cooldown_seconds"] = cooldown
		r.emit(ctx, p, string(audit.EventInterceptorFailPolicyWeakened), name, detail)
		// spec: §11.2.1 — the failPolicy transition is also a billing-stream
		// cost-attribution / compliance event under the operator's tenant.
		r.appendBilling(ctx, billingfanout.InterceptorFailPolicy(
			billingstore.EventInterceptorFailPolicyWeakened, p.TenantID, name,
			string(oldFailPolicy), string(updated.FailPolicy), uint32(count), names, transitionTs, uint32(cooldown),
		))
		// spec: §8.3 line 218 — one weakening_cooldown_active per window
		// entry (not per rejected request).
		r.emit(ctx, p, string(audit.EventInterceptorWeakeningCooldownActive), name, map[string]any{
			"interceptor_ref":       name,
			"transition_ts":         transitionTs,
			"cooldown_seconds":      cooldown,
			"affected_policy_count": count,
			"affected_policy_names": names,
		})
		r.appendBilling(ctx, billingfanout.InterceptorFailPolicy(
			billingstore.EventInterceptorWeakeningCooldownActive, p.TenantID, name,
			string(oldFailPolicy), string(updated.FailPolicy), uint32(count), names, transitionTs, uint32(cooldown),
		))
		return
	}
	r.emit(ctx, p, string(audit.EventInterceptorFailPolicyStrengthened), name, detail)
	r.appendBilling(ctx, billingfanout.InterceptorFailPolicy(
		billingstore.EventInterceptorFailPolicyStrengthened, p.TenantID, name,
		string(oldFailPolicy), string(updated.FailPolicy), uint32(count), names, "", 0,
	))
}

// interceptorAffectedPolicies counts the active DelegationPolicy
// resources (across all tenants) whose contentPolicy.interceptorRef
// names the interceptor, returning the count and the policy names (the
// §4.8 line 1034 affected_policy_count / affected_policy_names). It is a
// no-op returning (0, nil) when the delegation-policy registry is not
// wired.
func (r *Router) interceptorAffectedPolicies(ctx context.Context, name string) (int, []string) {
	if r.delegationPolicies == nil {
		return 0, nil
	}
	rows, err := r.delegationPolicies.List(ctx, delegationpolicystore.AllTenantsSentinel, delegationpolicystore.ListFilter{})
	if err != nil {
		return 0, nil
	}
	var names []string
	for _, pol := range rows {
		if pol.IsActive() && pol.ContentPolicy.InterceptorRef == name {
			names = append(names, pol.Name)
		}
	}
	sort.Strings(names)
	return len(names), names
}

// handleDeleteInterceptor implements DELETE per §8.3 rule 6: an
// interceptor referenced by any active DelegationPolicy's
// contentPolicy.interceptorRef cannot be deleted; the request is rejected
// with RESOURCE_HAS_DEPENDENTS.
func (r *Router) handleDeleteInterceptor(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	current, gerr := r.interceptors.Get(req.Context(), name)
	if gerr != nil {
		if errors.Is(gerr, interceptorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "interceptor not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	// spec: §8.3 rule 6 — the deletion guard runs before the If-Match
	// check so a referenced interceptor is rejected regardless of the tag.
	if count, names := r.interceptorAffectedPolicies(req.Context(), name); count > 0 {
		entry := map[string]any{"type": "delegation_policy", "count": count}
		if len(names) > 20 {
			entry["names"] = names[:20]
			entry["truncated"] = true
		} else {
			entry["names"] = names
		}
		writeError(w, http.StatusConflict, "RESOURCE_HAS_DEPENDENTS",
			"interceptor is referenced by one or more delegation policies",
			map[string]any{"dependents": []map[string]any{entry}})
		return
	}
	// spec: §15.1 line 1213 — DELETE honours If-Match only when present.
	if !enforceIfMatchIfPresent(w, req, current.Version) {
		return
	}
	if err := r.interceptors.Delete(req.Context(), name); err != nil {
		if errors.Is(err, interceptorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "interceptor not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	r.emit(req.Context(), principal, "admin.interceptor.deleted", name, nil)
	w.WriteHeader(http.StatusNoContent)
}
