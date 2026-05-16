// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// DelegationPolicyPayload is the §8.3 / §15.1 admin DelegationPolicy
// wire shape.
type DelegationPolicyPayload struct {
	Name               string                  `json:"name"`
	Rules              []DelegationRulePayload `json:"rules,omitempty"`
	ContentPolicy      ContentPolicyPayload    `json:"contentPolicy"`
	AllowSelfRecursion bool                    `json:"allowSelfRecursion,omitempty"`
	CreatedAt          string                  `json:"createdAt,omitempty"`
	UpdatedAt          string                  `json:"updatedAt,omitempty"`
	DeletedAt          string                  `json:"deletedAt,omitempty"`
}

// DelegationRulePayload is one §8.3 tag-matched allow/deny rule.
type DelegationRulePayload struct {
	Target DelegationTargetPayload `json:"target"`
	Allow  bool                    `json:"allow"`
}

// DelegationTargetPayload is the §8.3 tag-based match target.
type DelegationTargetPayload struct {
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
	IDs         []string          `json:"ids,omitempty"`
	Types       []string          `json:"types,omitempty"`
}

// ContentPolicyPayload is the §8.3 delegation content policy.
type ContentPolicyPayload struct {
	MaxInputSize        int    `json:"maxInputSize,omitempty"`
	InterceptorRef      string `json:"interceptorRef,omitempty"`
	ScanExportedFiles   bool   `json:"scanExportedFiles,omitempty"`
	MaxExportedFileSize int64  `json:"maxExportedFileSize,omitempty"`
}

// fromDelegationPolicy maps a stored policy to the wire payload.
func fromDelegationPolicy(p delegationpolicystore.DelegationPolicy) DelegationPolicyPayload {
	out := DelegationPolicyPayload{
		Name:               p.Name,
		AllowSelfRecursion: p.AllowSelfRecursion,
		ContentPolicy: ContentPolicyPayload{
			MaxInputSize:        p.ContentPolicy.MaxInputSize,
			InterceptorRef:      p.ContentPolicy.InterceptorRef,
			ScanExportedFiles:   p.ContentPolicy.ScanExportedFiles,
			MaxExportedFileSize: p.ContentPolicy.MaxExportedFileSize,
		},
		CreatedAt: rfc3339Nano(p.CreatedAt),
		UpdatedAt: rfc3339Nano(p.UpdatedAt),
		DeletedAt: rfc3339Nano(p.DeletedAt),
	}
	for _, r := range p.Rules {
		out.Rules = append(out.Rules, DelegationRulePayload{
			Target: DelegationTargetPayload{
				MatchLabels: r.Target.MatchLabels,
				IDs:         r.Target.IDs,
				Types:       r.Target.Types,
			},
			Allow: r.Allow,
		})
	}
	return out
}

// toDelegationRules maps the wire rules to the store representation.
func toDelegationRules(in []DelegationRulePayload) []delegationpolicystore.Rule {
	if in == nil {
		return nil
	}
	out := make([]delegationpolicystore.Rule, 0, len(in))
	for _, r := range in {
		out = append(out, delegationpolicystore.Rule{
			Target: delegationpolicystore.Target{
				MatchLabels: r.Target.MatchLabels,
				IDs:         r.Target.IDs,
				Types:       r.Target.Types,
			},
			Allow: r.Allow,
		})
	}
	return out
}

// toContentPolicy maps the wire content policy to the store
// representation.
func toContentPolicy(in ContentPolicyPayload) delegationpolicystore.ContentPolicy {
	return delegationpolicystore.ContentPolicy{
		MaxInputSize:        in.MaxInputSize,
		InterceptorRef:      in.InterceptorRef,
		ScanExportedFiles:   in.ScanExportedFiles,
		MaxExportedFileSize: in.MaxExportedFileSize,
	}
}

// WithDelegationPolicies wires the §15.1 delegation-policy CRUD
// handlers onto the Router.
func (r *Router) WithDelegationPolicies(s delegationpolicystore.Store) *Router {
	r.delegationPolicies = s
	return r
}

// interceptorWeakeningCooldownSeconds is the §8.3 cluster-scoped,
// admin-immutable cooldown applied after a delegation-policy
// content-scan weakening (`gateway.interceptorWeakeningCooldownSeconds`).
const interceptorWeakeningCooldownSeconds = 60

// emitScanExportedFilesTransition emits the §8.3 operational event for
// a `scanExportedFiles` change on a DelegationPolicy update: a
// weakening (true to false) emits `delegation_policy.export_scan_weakened`
// and starts the §8.3 interceptor-weakening cooldown; a strengthening
// (false to true) emits `delegation_policy.export_scan_strengthened`
// and takes effect immediately. No event fires when the value is
// unchanged.
//
// The cooldown enforcement at `delegate_task` time (rejecting with
// INTERCEPTOR_WEAKENING_COOLDOWN during the window) is deferred — it
// belongs to the delegation request path.
func (r *Router) emitScanExportedFilesTransition(ctx context.Context, p authmw.Principal, name string, oldScan, newScan bool, transitionTs string) {
	if oldScan == newScan {
		return
	}
	detail := map[string]any{
		"policy_name":           name,
		"old_scanExportedFiles": oldScan,
		"new_scanExportedFiles": newScan,
		"transition_ts":         transitionTs,
	}
	if oldScan && !newScan {
		detail["cooldown_seconds"] = interceptorWeakeningCooldownSeconds
		r.emit(ctx, p, "delegation_policy.export_scan_weakened", name, detail)
		return
	}
	r.emit(ctx, p, "delegation_policy.export_scan_strengthened", name, detail)
}

// writeDelegationPolicyStoreError maps a delegationpolicystore error to
// the §15.1 error envelope shared by the create and update handlers.
func writeDelegationPolicyStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, delegationpolicystore.ErrScanRequiresInterceptor):
		// §8.3 rule 1 / §15.1 error catalog: HTTP 400.
		writeError(w, http.StatusBadRequest, "EXPORT_SCAN_REQUIRES_INTERCEPTOR", err.Error(), nil)
	default:
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
	}
}

func (r *Router) handleCreateDelegationPolicy(w http.ResponseWriter, req *http.Request) {
	var body DelegationPolicyPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	p := delegationpolicystore.DelegationPolicy{
		Name:               body.Name,
		Rules:              toDelegationRules(body.Rules),
		ContentPolicy:      toContentPolicy(body.ContentPolicy),
		AllowSelfRecursion: body.AllowSelfRecursion,
		CreatedAt:          r.clock(),
	}
	p.UpdatedAt = p.CreatedAt
	delegationpolicystore.ApplyDefaults(&p)
	if err := r.delegationPolicies.Create(req.Context(), p); err != nil {
		if errors.Is(err, delegationpolicystore.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"delegation policy with this name already exists", nil)
			return
		}
		writeDelegationPolicyStoreError(w, err)
		return
	}
	stored, _ := r.delegationPolicies.Get(req.Context(), body.Name)
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.delegation_policy.created", body.Name, map[string]any{
		"ruleCount":          len(stored.Rules),
		"scanExportedFiles":  stored.ContentPolicy.ScanExportedFiles,
		"allowSelfRecursion": stored.AllowSelfRecursion,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromDelegationPolicy(stored))
}

func (r *Router) handleListDelegationPolicies(w http.ResponseWriter, req *http.Request) {
	rows, err := r.delegationPolicies.List(req.Context(), delegationpolicystore.ListFilter{
		IncludeDeleted: req.URL.Query().Get("includeDeleted") == "true",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]DelegationPolicyPayload, 0, len(rows))
	for _, p := range rows {
		out = append(out, fromDelegationPolicy(p))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"delegationPolicies": out})
}

func (r *Router) handleGetDelegationPolicy(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	row, err := r.delegationPolicies.Get(req.Context(), name)
	if err != nil {
		if errors.Is(err, delegationpolicystore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "delegation policy not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromDelegationPolicy(row))
}

// handleUpdateDelegationPolicy implements PUT — a full replace of the
// mutable fields (rules, contentPolicy, allowSelfRecursion).
func (r *Router) handleUpdateDelegationPolicy(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body DelegationPolicyPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	var oldScan bool
	updated, err := r.delegationPolicies.Update(req.Context(), name, func(p *delegationpolicystore.DelegationPolicy) error {
		oldScan = p.ContentPolicy.ScanExportedFiles
		p.Rules = toDelegationRules(body.Rules)
		p.ContentPolicy = toContentPolicy(body.ContentPolicy)
		p.AllowSelfRecursion = body.AllowSelfRecursion
		delegationpolicystore.ApplyDefaults(p)
		return nil
	})
	if err != nil {
		if errors.Is(err, delegationpolicystore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "delegation policy not found", nil)
			return
		}
		writeDelegationPolicyStoreError(w, err)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.delegation_policy.updated", name, nil)
	r.emitScanExportedFilesTransition(req.Context(), principal, name,
		oldScan, updated.ContentPolicy.ScanExportedFiles, rfc3339Nano(updated.UpdatedAt))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromDelegationPolicy(updated))
}

// delegationPolicyRuntimeDependents builds the §8.3 / §15.1
// `details.dependents` entry for active runtimes whose
// `delegationPolicyRef` names the policy, returning true when at least
// one such runtime exists. It is a no-op when the runtime store is not
// wired.
func (r *Router) delegationPolicyRuntimeDependents(ctx context.Context, name string) (map[string]any, bool) {
	if r.runtimes == nil {
		return nil, false
	}
	rows, err := r.runtimes.List(ctx, runtimestore.ListFilter{})
	if err != nil {
		return nil, false
	}
	var ids []string
	for _, rt := range rows {
		if rt.DelegationPolicyRef == name {
			ids = append(ids, rt.Name)
		}
	}
	if len(ids) == 0 {
		return nil, false
	}
	entry := map[string]any{"type": "runtime", "count": len(ids)}
	// §15.1: the `ids` array is capped at 20; past that it is truncated.
	if len(ids) > 20 {
		entry["ids"] = ids[:20]
		entry["truncated"] = true
	} else {
		entry["ids"] = ids
	}
	return entry, true
}

// handleDeleteDelegationPolicy implements DELETE per §8.3: the policy
// is soft-deleted unless an active runtime still references it, in
// which case the §8.3 deletion guard rejects the request. The
// active-lease half of the §8.3 guard is deferred — delegation leases
// are not enumerable from the admin Router.
func (r *Router) handleDeleteDelegationPolicy(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if dep, ok := r.delegationPolicyRuntimeDependents(req.Context(), name); ok {
		writeError(w, http.StatusConflict, "RESOURCE_HAS_DEPENDENTS",
			"delegation policy is referenced by one or more runtimes",
			map[string]any{"dependents": []map[string]any{dep}})
		return
	}
	if err := r.delegationPolicies.SoftDelete(req.Context(), name, r.clock()); err != nil {
		if errors.Is(err, delegationpolicystore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "delegation policy not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	r.emit(req.Context(), principal, "admin.delegation_policy.soft_deleted", name, nil)
	w.WriteHeader(http.StatusNoContent)
}
