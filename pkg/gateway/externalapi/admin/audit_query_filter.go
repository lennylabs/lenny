// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"
)

// auditQueryFilter is the parsed §25.9 list-endpoint query filter. The
// payload-derived fields (actorID, resourceType, resourceID,
// operationID) and the row fields (eventType, since, until) are matched
// in-process per row; severity is matched against the translated OCSF
// record's severity_id; the translation/publish states are resolved via
// the backend's optional state-tracking interfaces. Combining filters
// is AND.
//
// spec: §25.9 line 3659 (the query-parameter set), line 3703
// (?operationId= diagnostics correlation).
type auditQueryFilter struct {
	since            time.Time
	until            time.Time
	eventType        string   // raw ?eventType= value, echoed in diagnostics
	eventTypes       []string // eventType split on commas; OR-matched per row
	actorID          string
	resourceType     string
	resourceID       string
	severity         string // OCSF severity word (case-insensitive) or numeric id
	operationID      string
	translationState audit.OCSFTranslationState
	publishState     eventbus.PublishState
}

// auditWindow is the longest unfiltered time window §25.9 line 3707
// permits before AUDIT_QUERY_TOO_BROAD: 90 days.
const auditWindow = 90 * 24 * time.Hour

// auditDefaultWindow is the §25.9 line 3708 default look-back applied
// when a query supplies neither `since` nor `until`.
const auditDefaultWindow = 24 * time.Hour

// parseAuditFilter parses the §25.9 list query parameters off req. now is
// the gateway clock used to default the time window. It returns
// ok=false with an error already written when a parameter is malformed
// or the query is too broad (§25.9 line 3707).
func parseAuditFilter(w http.ResponseWriter, req *http.Request, now time.Time) (auditQueryFilter, bool) {
	q := req.URL.Query()
	var f auditQueryFilter

	since, until, ok := parseAuditTimeWindow(w, req, now)
	if !ok {
		return auditQueryFilter{}, false
	}
	f.since, f.until = since, until

	// spec: §25.9 line 3659 / §12.8 line 986 — ?eventType= accepts a
	// comma-separated list. The §12.8 DSAR template's category-(b)
	// invocation passes
	// `eventType=admin.impersonation_started,admin.impersonation_ended`;
	// a row matches when its event type is any list member.
	f.eventType = q.Get("eventType")
	f.eventTypes = splitCSV(f.eventType)
	f.actorID = q.Get("actorId")
	f.resourceType = q.Get("resourceType")
	f.resourceID = q.Get("resourceId")
	f.severity = q.Get("severity")
	f.operationID = q.Get("operationId")

	// spec: §25.9 line 3659 — ?ocsf_translation_state filters by the
	// §11.7 translator state; an unparseable value is rejected.
	if v := q.Get("ocsf_translation_state"); v != "" {
		f.translationState = audit.OCSFTranslationState(v)
		if !f.translationState.IsValid() {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
				"ocsf_translation_state must be one of pending, retry_pending, succeeded, dead_lettered", nil)
			return auditQueryFilter{}, false
		}
	}
	// spec: §25.9 line 3659 — ?eventbus_publish_state filters by the
	// §12.3.7 publish state; an unparseable value is rejected.
	if v := q.Get("eventbus_publish_state"); v != "" {
		f.publishState = eventbus.PublishState(v)
		if !f.publishState.IsValid() {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
				"eventbus_publish_state must be one of pending, retry_pending, published, failed", nil)
			return auditQueryFilter{}, false
		}
	}

	// spec: §25.9 line 3707 — a query spanning more than 90 days without
	// a narrowing filter would scan too broadly; reject it.
	if f.until.Sub(f.since) > auditWindow && !f.hasNarrowingFilter() {
		writeError(w, http.StatusBadRequest, "AUDIT_QUERY_TOO_BROAD",
			"query spans more than 90 days without a narrowing filter; add since/until or an eventType/actorId/resource filter", nil)
		return auditQueryFilter{}, false
	}

	return f, true
}

// parseAuditTimeWindow parses the §25.9 since/until bounds with the line
// 3708 default look-back. A query without `since` or `until` defaults to
// the last 24 hours; when only one bound is supplied the other anchors
// the 24h window to it. A malformed timestamp or an inverted range is
// rejected with 400 INVALID_ARGUMENT.
func parseAuditTimeWindow(w http.ResponseWriter, req *http.Request, now time.Time) (time.Time, time.Time, bool) {
	q := req.URL.Query()
	until := now
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "until must be an RFC 3339 timestamp", nil)
			return time.Time{}, time.Time{}, false
		}
		until = t.UTC()
	}
	since := until.Add(-auditDefaultWindow)
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "since must be an RFC 3339 timestamp", nil)
			return time.Time{}, time.Time{}, false
		}
		since = t.UTC()
	}
	if until.Before(since) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "until must not precede since", nil)
		return time.Time{}, time.Time{}, false
	}
	return since, until, true
}

// hasNarrowingFilter reports whether the filter carries a non-time
// predicate that bounds the scan, per the §25.9 line 3707
// "without sufficient filters" exemption from the 90-day cap.
func (f auditQueryFilter) hasNarrowingFilter() bool {
	return f.eventType != "" || f.actorID != "" || f.resourceType != "" ||
		f.resourceID != "" || f.operationID != "" || f.severity != ""
}

// matchesRow reports whether row satisfies the row- and payload-level
// predicates (everything except severity, which needs the translated
// OCSF record, and the translation/publish states, which need a backend
// lookup). Combining filters is AND.
func (f auditQueryFilter) matchesRow(row audit.Row) bool {
	if row.Timestamp.Before(f.since) || row.Timestamp.After(f.until) {
		return false
	}
	if len(f.eventTypes) > 0 && !containsString(f.eventTypes, row.EventType) {
		return false
	}
	if f.actorID == "" && f.resourceType == "" && f.resourceID == "" && f.operationID == "" {
		return true
	}
	payload := decodePayload(row.Payload)
	if f.actorID != "" && !matchesActor(payload, f.actorID) {
		return false
	}
	if f.resourceType != "" && resourceTypeOf(payload) != f.resourceType {
		return false
	}
	if f.resourceID != "" && resourceIDOf(payload) != f.resourceID {
		return false
	}
	if f.operationID != "" && stringFrom(payload, "operation_id") != f.operationID {
		return false
	}
	return true
}

// matchesSeverity reports whether the translated record's severity_id
// satisfies the ?severity= filter. An empty filter matches anything.
// The filter value may be the OCSF severity word (case-insensitive) or
// the numeric severity_id.
func (f auditQueryFilter) matchesSeverity(severityID int) bool {
	if f.severity == "" {
		return true
	}
	if n, err := strconv.Atoi(f.severity); err == nil {
		return n == severityID
	}
	return strings.EqualFold(f.severity, ocsf.SeverityName(severityID))
}

// decodePayload unmarshals an audit payload to a map for filter
// introspection. A non-object payload yields an empty map so the filter
// simply fails to match rather than erroring the whole query.
func decodePayload(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if len(raw) == 0 {
		return m
	}
	_ = json.Unmarshal(raw, &m)
	return m
}

// splitCSV splits a comma-separated query value into its trimmed,
// non-empty members. An empty input yields a nil slice so callers can
// treat "no filter" as len == 0.
func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// containsString reports whether s is a member of list.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// matchesActor matches an actorId filter against either the OCSF-mapped
// `user_id` or the admin-event `actor_subject` payload field.
func matchesActor(payload map[string]any, actorID string) bool {
	return stringFrom(payload, "user_id") == actorID || stringFrom(payload, "actor_subject") == actorID
}

// resourceTypeOf resolves a row's resource type from the OCSF-mapped
// `resource_type` field, falling back to the type half of the
// admin-event `target_resource` ("type/id").
func resourceTypeOf(payload map[string]any) string {
	if v := stringFrom(payload, "resource_type"); v != "" {
		return v
	}
	t, _ := splitTargetResource(stringFrom(payload, "target_resource"))
	return t
}

// resourceIDOf resolves a row's resource id from the OCSF-mapped
// `resource_id` field, falling back to the id half of the admin-event
// `target_resource` ("type/id").
func resourceIDOf(payload map[string]any) string {
	if v := stringFrom(payload, "resource_id"); v != "" {
		return v
	}
	_, id := splitTargetResource(stringFrom(payload, "target_resource"))
	return id
}

// splitTargetResource splits an admin-event target_resource of the form
// "type/id" (e.g. "tenant/acme", "audit-event/42") into its halves. A
// value without a slash is treated as a bare type.
func splitTargetResource(tr string) (string, string) {
	if tr == "" {
		return "", ""
	}
	if i := strings.Index(tr, "/"); i >= 0 {
		return tr[:i], tr[i+1:]
	}
	return tr, ""
}

// stringFrom returns payload[key] as a string when present and a string,
// else "".
func stringFrom(payload map[string]any, key string) string {
	if v, ok := payload[key].(string); ok {
		return v
	}
	return ""
}
