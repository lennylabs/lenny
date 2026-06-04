// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/pagination"
)

// adminListDefaultSort pins the §15.1 line 1236 default ordering shared
// by the admin resource collections: created_at descending.
var adminListDefaultSort = pagination.Sort{Field: "created_at", Direction: pagination.DirectionDesc}

// adminTimestampSortFields is the §15.1 line 1236 sortable-field set for
// admin collections keyed by a timestamp plus a name/id tiebreaker. The
// `name` field is included because every admin resource exposes a stable
// name or id the caller can order by.
var adminTimestampSortFields = []string{"created_at", "updated_at", "name"}

// writePaginatedList sorts items by the request's `?sort`, slices them
// into the §15.1 canonical `{items, cursor, hasMore, total}` envelope
// honouring `?cursor` and `?limit`, and writes it. allowedSorts names
// the resource's sortable fields and keyOf yields the (sortKey,
// tiebreaker) pair for the active sort field. A malformed
// cursor/limit/sort writes a 400 VALIDATION_ERROR and returns.
//
// The collection is paginated in memory: the store returns the full
// post-filter set, len() is the cheaply-computable §15.1 line 1252
// total, and the comparison-based cursor in pagination.Page survives
// inserts and deletes between page requests.
//
// spec: §15.1 lines 1228-1253. F-15.1.6.
func writePaginatedList[T any](
	w http.ResponseWriter,
	req *http.Request,
	now time.Time,
	items []T,
	allowedSorts []string,
	defaultSort pagination.Sort,
	keyOf func(T, pagination.Sort) (key, tiebreak string),
) {
	params, ferr := pagination.ParseRequest(req, allowedSorts, defaultSort, now)
	if ferr != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", ferr.Message, ferr.Details())
		return
	}
	kf := func(it T) (string, string) { return keyOf(it, params.Sort) }
	pagination.SortSlice(items, params.Sort.Direction, kf)
	env := pagination.Page(items, params, now, kf)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(env)
}
