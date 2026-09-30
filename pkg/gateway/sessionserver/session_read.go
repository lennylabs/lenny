// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// handleGet implements GET /v1/sessions/{id}.
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §8.8 (TaskRecord) — the single-session read materializes the
	// §8.8 TaskRecord envelope (projected from the row + transcript) so a
	// consumer expecting §8.8 semantics can read it off GET
	// /v1/sessions/{id}. F-8.8.1.
	resp := toResponse(row)
	resp.TaskRecord = s.buildTaskRecord(r.Context(), row)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// handleList implements GET /v1/sessions. Supports the §15.1 ?state=
// and ?runtime= filters in their basic form.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// spec: §15.1 — a platform-admin may scope the listing to a
	// specific tenant via `?tenant=<id>`. A non-admin's `?tenant=` is
	// ignored: their listing stays bound to their own tenant (and the
	// Postgres RLS context enforces that regardless). F-15.1.15.
	tenantID := s.resolveTenant(r)
	if want := q.Get("tenant"); want != "" {
		if p, ok := getPrincipal(r); ok && p.HasRole(auth.RolePlatformAdmin) {
			if err := authValidateTenantID(want); err == nil {
				tenantID = want
			}
		}
	}
	filter := sessionstore.ListFilter{
		State:        session.State(q.Get("state")),
		RuntimeRef:   q.Get("runtime"),
		FailureClass: session.FailureClass(q.Get("failureClass")),
		Labels:       parseLabelFilter(q["label"]),
	}
	// spec: §15.1 — derive_failure audit rows are included
	// by default; `?includeDeriveFailures=false` excludes them. Any other
	// value (absent, "true") preserves the default audit visibility.
	// F-15.1.14.
	if q.Get("includeDeriveFailures") == "false" {
		filter.ExcludeDeriveFailures = true
	}
	// spec: §15.1 — the canonical cursor-paginated list
	// envelope. `?cursor`/`?limit`/`?sort` are parsed and validated here;
	// the default sort is created_at:desc (line 1236) and the supported
	// fields are created_at and updated_at. F-15.1.6.
	params, ferr := pagination.ParseRequest(r,
		sessionListSortFields, sessionListDefaultSort, s.clock())
	if ferr != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", ferr.Message, ferr.Details())
		return
	}
	rows, err := s.store.List(r.Context(), tenantID, filter)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]SessionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toResponse(row))
	}
	keyOf := func(sr SessionResponse) (string, string) {
		if params.Sort.Field == "updated_at" {
			return sr.UpdatedAt, sr.ID
		}
		return sr.CreatedAt, sr.ID
	}
	pagination.SortSlice(out, params.Sort.Direction, keyOf)
	env := pagination.Page(out, params, s.clock(), keyOf)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(env)
}

// sessionListSortFields / sessionListDefaultSort pin the §15.1
// sort contract for GET /v1/sessions: created_at (default) and
// updated_at, descending by default.
var (
	sessionListSortFields  = []string{"created_at", "updated_at"}
	sessionListDefaultSort = pagination.Sort{Field: "created_at", Direction: pagination.DirectionDesc}
)

// parseLabelFilter turns the repeatable `?label=key=value` query values
// into the AND-containment map the store List honours. A value with no
// `=` is treated as a key match against an empty value; an empty key is
// skipped. spec: §15.1 — "filterable by ... labels". F-15.1.15.
func parseLabelFilter(raw []string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for _, item := range raw {
		key, value, _ := strings.Cut(item, "=")
		if key == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
