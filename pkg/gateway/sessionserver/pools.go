// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// poolListSortFields / poolListDefaultSort pin the §15.1 line 1228 sort
// contract for GET /v1/pools: created_at (default) and updated_at,
// descending by default — matching the other §15.1 list surfaces.
var (
	poolListSortFields  = []string{"created_at", "updated_at"}
	poolListDefaultSort = pagination.Sort{Field: "created_at", Direction: pagination.DirectionDesc}
)

// PoolDiscoveryEntry is one item in the GET /v1/pools discovery list. It
// carries the session-facing warm-pool fields a client needs to choose a
// pool: the pool name, the runtime it warms, its execution mode, and the
// configured warm replica count. Security-internal fields (isolation
// profile, egress profile, drain state, generation) are not surfaced on
// the public discovery list — those live on the platform-admin
// GET /v1/admin/pools surface.
//
// spec: §15.1 line 703 ("List pools and warm pod counts").
type PoolDiscoveryEntry struct {
	Name             string `json:"name"`
	RuntimeRef       string `json:"runtimeRef"`
	ExecutionMode    string `json:"executionMode"`
	ConcurrencyStyle string `json:"concurrencyStyle,omitempty"`
	ResourceClass    string `json:"resourceClass,omitempty"`
	WarmCount        int    `json:"warmCount"`
	MaxConcurrent    int    `json:"maxConcurrent,omitempty"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

// handleListPools implements GET /v1/pools — the §15.1 line 703
// session-facing pool-discovery surface. It lists warm pools and their
// configured warm replica counts, filtered to the pools that warm a
// runtime the caller can already discover through GET /v1/runtimes (the
// §10.6 transparent environment filter). A pool whose runtimeRef the
// caller cannot see is absent, so the endpoint never widens runtime
// visibility, and a runtime store read failure fails closed to an empty
// list. The response uses the canonical §15.1 cursor-paginated envelope.
//
// spec: §15.1 line 703; line 1228 (cursor pagination).
func (s *Server) handleListPools(w http.ResponseWriter, r *http.Request) {
	params, ferr := pagination.ParseRequest(r, poolListSortFields, poolListDefaultSort, s.clock())
	if ferr != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", ferr.Message, ferr.Details())
		return
	}
	visible := s.visibleRuntimeNames(r)
	out := make([]PoolDiscoveryEntry, 0)
	if s.pools != nil {
		rows, err := s.pools.List(r.Context(), poolstore.ListFilter{})
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		for _, p := range rows {
			if !visible[p.RuntimeRef] {
				continue
			}
			out = append(out, poolDiscoveryEntry(p))
		}
	}
	keyOf := func(e PoolDiscoveryEntry) (string, string) {
		if params.Sort.Field == "updated_at" {
			return e.UpdatedAt, e.Name
		}
		return e.CreatedAt, e.Name
	}
	pagination.SortSlice(out, params.Sort.Direction, keyOf)
	env := pagination.Page(out, params, s.clock(), keyOf)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(env)
}

// visibleRuntimeNames returns the set of runtime names the request
// principal may discover, applying the same §5.1 derived-runtime
// resolution and §10.6 transparent environment filter that
// GET /v1/runtimes applies. It fails closed: a nil runtime registry or a
// store read error yields an empty set so the pool list it scopes is
// empty rather than unfiltered.
//
// spec: §10.6 environment resolver; §15.1 line 703.
func (s *Server) visibleRuntimeNames(r *http.Request) map[string]bool {
	names := map[string]bool{}
	if s.runtimes == nil {
		return names
	}
	rows, err := s.runtimes.List(r.Context(), runtimestore.ListFilter{})
	if err != nil {
		return names
	}
	rows = s.resolveRuntimes(r.Context(), rows)
	rows = s.filterRuntimesByEnvironment(r, rows)
	for _, rt := range rows {
		names[rt.Name] = true
	}
	return names
}

// poolDiscoveryEntry projects a stored pool onto the public discovery
// entry. The concurrency sub-fields are stamped only for a concurrent
// pool, where §5.2 makes ConcurrencyStyle and MaxConcurrent meaningful.
func poolDiscoveryEntry(p poolstore.Pool) PoolDiscoveryEntry {
	// An unset execution mode resolves to the §5.2 default (session), so
	// the discovery list never reports an empty mode.
	mode := p.ExecutionMode
	if mode == "" {
		mode = runtimestore.ExecutionModeSession
	}
	e := PoolDiscoveryEntry{
		Name:          p.Name,
		RuntimeRef:    p.RuntimeRef,
		ExecutionMode: string(mode),
		ResourceClass: p.ResourceClass,
		WarmCount:     p.WarmCount,
		CreatedAt:     p.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     p.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if p.ExecutionMode == runtimestore.ExecutionModeConcurrent {
		e.ConcurrencyStyle = string(p.ConcurrencyStyle)
		e.MaxConcurrent = p.MaxConcurrent
	}
	return e
}
