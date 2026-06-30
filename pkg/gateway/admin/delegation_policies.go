// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// DelegationPolicyPayload is the §8.3 / §15.1 admin DelegationPolicy
// wire shape.
type DelegationPolicyPayload struct {
	Name string `json:"name"`
	// TenantID is read-only on the wire — the admin handler resolves
	// it from the principal (or, for platform-admin, the optional
	// body.tenantId / ?tenant_id= override).
	TenantID           string                  `json:"tenantId,omitempty"`
	Rules              []DelegationRulePayload `json:"rules,omitempty"`
	ContentPolicy      ContentPolicyPayload    `json:"contentPolicy"`
	AllowSelfRecursion bool                    `json:"allowSelfRecursion,omitempty"`
	CreatedAt          string                  `json:"createdAt,omitempty"`
	UpdatedAt          string                  `json:"updatedAt,omitempty"`
	DeletedAt          string                  `json:"deletedAt,omitempty"`

	// ETag is the §15.1 optimistic-concurrency entity tag — the quoted
	// decimal version. List and GET responses carry it so a client can
	// supply it as the If-Match header on a later PUT.
	// spec: §15.1 lines 1207-1209.
	ETag string `json:"etag,omitempty"`
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
		TenantID:           p.TenantID,
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
		// spec: §15.1 line 1207 — the ETag is the quoted decimal version,
		// carried per-item on list responses and in the GET header.
		ETag: formatETag(p.Version),
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
// and arms the §8.3 interceptor-weakening cooldown; a strengthening
// (false to true) emits `delegation_policy.export_scan_strengthened`
// and takes effect immediately. No event fires when the value is
// unchanged.
//
// The cooldown enforcement at `delegate_task` time (rejecting with
// INTERCEPTOR_WEAKENING_COOLDOWN during the window) lives in the
// delegation service (F-8.7.12 / F-13.5.7) — it reads
// `ScanExportedFilesWeakenedAt` off the policy row written by the
// Update path above.
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
		// spec: §11.2.1 — the scanExportedFiles weakening is a billing-stream
		// cost-attribution / compliance event under the operator's tenant.
		r.appendBilling(ctx, billingfanout.DelegationPolicyExportScan(
			billingstore.EventDelegationPolicyExportScanWeakened, p.TenantID, name,
			oldScan, newScan, transitionTs, uint32(interceptorWeakeningCooldownSeconds),
		))
		return
	}
	r.emit(ctx, p, "delegation_policy.export_scan_strengthened", name, detail)
	r.appendBilling(ctx, billingfanout.DelegationPolicyExportScan(
		billingstore.EventDelegationPolicyExportScanStrengthened, p.TenantID, name,
		oldScan, newScan, transitionTs, 0,
	))
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
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID, err := resolveTargetTenant(principal, req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	p := delegationpolicystore.DelegationPolicy{
		TenantID:           tenantID,
		Name:               body.Name,
		Rules:              toDelegationRules(body.Rules),
		ContentPolicy:      toContentPolicy(body.ContentPolicy),
		AllowSelfRecursion: body.AllowSelfRecursion,
		CreatedAt:          r.clock(),
	}
	p.UpdatedAt = p.CreatedAt
	delegationpolicystore.ApplyDefaults(&p)
	// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or auditing.
	if req.URL.Query().Get("dryRun") == "true" {
		if err := delegationpolicystore.Validate(p); err != nil {
			writeDelegationPolicyStoreError(w, err)
			return
		}
		writeDryRun(w, http.StatusCreated, fromDelegationPolicy(p))
		return
	}
	if err := r.delegationPolicies.Create(req.Context(), p); err != nil {
		if errors.Is(err, delegationpolicystore.ErrAlreadyExists) {
			// spec: §15.1 line 983 — duplicate identifier is RESOURCE_ALREADY_EXISTS.
			writeError(w, http.StatusConflict, "RESOURCE_ALREADY_EXISTS",
				"delegation policy with this name already exists", nil)
			return
		}
		writeDelegationPolicyStoreError(w, err)
		return
	}
	stored, _ := r.delegationPolicies.Get(req.Context(), tenantID, body.Name)
	r.emit(req.Context(), principal, "admin.delegation_policy.created", body.Name, map[string]any{
		"tenant_id":          tenantID,
		"ruleCount":          len(stored.Rules),
		"scanExportedFiles":  stored.ContentPolicy.ScanExportedFiles,
		"allowSelfRecursion": stored.AllowSelfRecursion,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(fromDelegationPolicy(stored))
}

func (r *Router) handleListDelegationPolicies(w http.ResponseWriter, req *http.Request) {
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID := delegationPolicyListScope(principal, req)
	rows, err := r.delegationPolicies.List(req.Context(), tenantID, delegationpolicystore.ListFilter{
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
	// spec: §15.1 lines 1228-1253 — canonical cursor-paginated envelope. F-15.1.6.
	writePaginatedList(w, req, r.clock(), out, adminTimestampSortFields, adminListDefaultSort,
		func(x DelegationPolicyPayload, s pagination.Sort) (string, string) {
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

func (r *Router) handleGetDelegationPolicy(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID := delegationPolicyListScope(principal, req)
	row, err := r.delegationPolicies.Get(req.Context(), tenantID, name)
	if err != nil {
		if errors.Is(err, delegationpolicystore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "delegation policy not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §15.1 line 1209 — GET responses for an admin resource carry the
	// ETag header so the client can use it as the next PUT's If-Match.
	w.Header().Set("ETag", formatETag(row.Version))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fromDelegationPolicy(row))
}

// delegationPolicyListScope mirrors listTenantScope for delegation
// policies. tenant-admins read their own tenant; platform-admins read
// across tenants by default and may narrow with ?tenant_id=.
//
// spec: §4.2 line 172 / §10.6 — tenant-admins see only their own
// tenant; platform-admins see all tenants with optional filter.
func delegationPolicyListScope(p authmw.Principal, req *http.Request) string {
	if !p.HasRole(auth.RolePlatformAdmin) {
		return p.TenantID
	}
	if t := req.URL.Query().Get("tenant_id"); t != "" {
		return t
	}
	return delegationpolicystore.AllTenantsSentinel
}

// applyDelegationPolicyUpdate merges a §15.1 DelegationPolicyPayload onto
// a policy in place — a full replace of the mutable fields (rules,
// contentPolicy, allowSelfRecursion) — and server-mints the §8.3
// scanExportedFiles weakening transition timestamp from the policy's
// prior scan state. It is the single merge implementation shared by the
// real store Update closure and the dry-run preview, so the preview
// reflects exactly what a persisted update would produce.
func (r *Router) applyDelegationPolicyUpdate(p *delegationpolicystore.DelegationPolicy, body DelegationPolicyPayload) {
	oldScan := p.ContentPolicy.ScanExportedFiles
	p.Rules = toDelegationRules(body.Rules)
	p.ContentPolicy = toContentPolicy(body.ContentPolicy)
	p.AllowSelfRecursion = body.AllowSelfRecursion
	delegationpolicystore.ApplyDefaults(p)
	// spec: §8.3 line 181 (F-8.7.12 / F-13.5.7) — server-mint the
	// scanExportedFiles weakening transition timestamp so the
	// gateway can enforce INTERCEPTOR_WEAKENING_COOLDOWN at
	// `delegate_task` time. A `true → false` flip stamps the
	// row with the gateway clock; a `false → true` strengthen
	// clears any prior stamp so subsequent delegations admit
	// immediately. The field is admin-API-immutable per
	// §8.3 SEC-013 — the wire payload does not expose it.
	switch {
	case oldScan && !p.ContentPolicy.ScanExportedFiles:
		p.ScanExportedFilesWeakenedAt = r.clock()
	case !oldScan && p.ContentPolicy.ScanExportedFiles:
		p.ScanExportedFilesWeakenedAt = time.Time{}
	}
}

// handleUpdateDelegationPolicy implements PUT — a full replace of the
// mutable fields (rules, contentPolicy, allowSelfRecursion).
func (r *Router) handleUpdateDelegationPolicy(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var body DelegationPolicyPayload
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID, err := resolveTargetTenant(principal, req, body.TenantID)
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	// Resolve the current policy first so the §15.1 If-Match precondition
	// and the dry-run preview both read the stored record; a missing
	// policy 404s ahead of the precondition and the dry-run branch.
	current, gerr := r.delegationPolicies.Get(req.Context(), tenantID, name)
	if gerr != nil {
		if errors.Is(gerr, delegationpolicystore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "delegation policy not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	// spec: §15.1 lines 1207-1211 — every admin PUT requires If-Match. The
	// policy's entity tag is its version; enforce the optimistic-
	// concurrency precondition before applying the mutation. This runs
	// before the dry-run branch so a dry-run with a stale If-Match still
	// returns 412 and a dry-run with no If-Match still returns 428.
	if !enforceIfMatch(w, req, current.Version) {
		return
	}
	// spec: §15.1 line 1140 — ?dryRun=true validates without persisting or auditing.
	if req.URL.Query().Get("dryRun") == "true" {
		preview := current
		r.applyDelegationPolicyUpdate(&preview, body)
		preview.TenantID = tenantID
		if err := delegationpolicystore.Validate(preview); err != nil {
			writeDelegationPolicyStoreError(w, err)
			return
		}
		writeDryRun(w, http.StatusOK, fromDelegationPolicy(preview))
		return
	}
	var oldScan bool
	updated, err := r.delegationPolicies.Update(req.Context(), tenantID, name, func(p *delegationpolicystore.DelegationPolicy) error {
		oldScan = p.ContentPolicy.ScanExportedFiles
		r.applyDelegationPolicyUpdate(p, body)
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
	r.emit(req.Context(), principal, "admin.delegation_policy.updated", name, map[string]any{
		"tenant_id": tenantID,
	})
	r.emitScanExportedFilesTransition(req.Context(), principal, name,
		oldScan, updated.ContentPolicy.ScanExportedFiles, rfc3339Nano(updated.UpdatedAt))
	// spec: §15.1 line 1210 — a successful PUT carries the bumped ETag so
	// the client can chain a subsequent write without a refresh GET.
	w.Header().Set("ETag", formatETag(updated.Version))
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
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID, err := resolveTargetTenant(principal, req, "")
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	if dep, ok := r.delegationPolicyRuntimeDependents(req.Context(), name); ok {
		writeError(w, http.StatusConflict, "RESOURCE_HAS_DEPENDENTS",
			"delegation policy is referenced by one or more runtimes",
			map[string]any{"dependents": []map[string]any{dep}})
		return
	}
	// Resolve the current policy so the §15.1 DELETE If-Match precondition
	// can compare against its version; a missing policy 404s.
	current, gerr := r.delegationPolicies.Get(req.Context(), tenantID, name)
	if gerr != nil {
		if errors.Is(gerr, delegationpolicystore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "delegation policy not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", gerr.Error(), nil)
		return
	}
	// spec: §15.1 line 1213 — DELETE honours If-Match only when present: a
	// stale tag returns 412 ETAG_MISMATCH, an absent header proceeds. This
	// runs before the actual delete.
	if !enforceIfMatchIfPresent(w, req, current.Version) {
		return
	}
	if err := r.delegationPolicies.SoftDelete(req.Context(), tenantID, name, r.clock()); err != nil {
		if errors.Is(err, delegationpolicystore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "delegation policy not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	r.emit(req.Context(), principal, "admin.delegation_policy.soft_deleted", name, map[string]any{
		"tenant_id": tenantID,
	})
	w.WriteHeader(http.StatusNoContent)
}
