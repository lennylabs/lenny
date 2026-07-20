// SPDX-License-Identifier: MIT

package events

import (
	"context"

	gwevents "github.com/lennylabs/lenny/pkg/events"
)

// tenantLabelExtension is the CloudEvents extension attribute that labels an
// operational event with the tenant it concerns. An event carrying no such
// label is platform-scoped and is observable only by a platform-admin read
// caller. spec: 25.5 (tenant isolation on the read surface).
const tenantLabelExtension = "lennytenantid"

// readerScope is the tenant authorization scope of a §25.5 read caller,
// resolved at the opsserver HTTP boundary and carried on the request context.
// A platform-admin reads every event; a tenant-admin reads only events labeled
// with its own tenant and never a platform-scoped (no-label) event. It is the
// read-side twin of the delivery-time tenantFilter the webhook worker applies,
// so SSE and polling enforce the same tenant isolation as delivery. spec: 25.5
// ("SSE and polling endpoints apply the same filter: tenant-scoped callers only
// see events matching their tenant or carrying no tenant label if the caller
// has permission for platform-scoped events, typically platform-admin only").
type readerScope struct {
	tenantID      string
	platformAdmin bool
}

// readerScopeKey is the private context key the resolved read scope is stored
// under. A private zero-size key type keeps the value un-collidable with any
// other package's context values.
type readerScopeKey struct{}

// WithReaderScope returns a context carrying the resolved §25.5 read-caller
// tenant scope. The opsserver route boundary sets it from the authenticated
// caller (subject, tenant, platform-admin) so HandlePoll and HandleStream can
// intersect the served events with the caller's tenant without a signature
// change. spec: 25.5 (read-endpoint tenant filter).
func WithReaderScope(ctx context.Context, tenantID string, platformAdmin bool) context.Context {
	return context.WithValue(ctx, readerScopeKey{}, readerScope{tenantID: tenantID, platformAdmin: platformAdmin})
}

// readerScopeFrom extracts the read-caller tenant scope from ctx and reports
// whether one was set. When no scope is present the caller did not pass through
// the opsserver authorization boundary (an in-process or test caller), so the
// read path applies no tenant filter. The scope is always set for a real HTTP
// request, so the filter is enforced on every externally reachable read. spec:
// 25.5.
func readerScopeFrom(ctx context.Context) (readerScope, bool) {
	sc, ok := ctx.Value(readerScopeKey{}).(readerScope)
	return sc, ok
}

// admits reports whether the read caller may observe ev under the §25.5
// tenant-isolation rule. A platform-admin observes every event. A tenant-admin
// observes only events labeled with its own tenant; a platform-scoped event
// (no tenant label) is dropped for a tenant-admin. The check fails closed: a
// caller that is not a platform-admin and whose tenant does not equal the
// event's label is denied, so an empty-tenant non-admin caller sees nothing.
// spec: 25.5 (platform-scoped events reach only platform-admin read callers).
func (sc readerScope) admits(ev gwevents.OperationalEvent) bool {
	if sc.platformAdmin {
		return true
	}
	label := ev.Extensions[tenantLabelExtension]
	if label == "" {
		return false
	}
	return label == sc.tenantID
}

// filterForReader returns the subset of in the read caller resolved from ctx
// may observe. It is the poll-page choke point: the tenant predicate narrows
// the items shown while the pagination cursor still advances over the raw
// source position, mirroring how the event-type/severity EventFilter narrows a
// page. When no scope is set, or the caller is a platform-admin, the input is
// returned unchanged so no allocation is spent on the common path. spec: 25.5
// (read-endpoint tenant filter, post-query drop).
func (s *Service) filterForReader(ctx context.Context, in []gwevents.BufferedEvent) []gwevents.BufferedEvent {
	sc, ok := readerScopeFrom(ctx)
	if !ok || sc.platformAdmin {
		return in
	}
	out := make([]gwevents.BufferedEvent, 0, len(in))
	for _, ev := range in {
		if sc.admits(ev.Event) {
			out = append(out, ev)
		}
	}
	return out
}
