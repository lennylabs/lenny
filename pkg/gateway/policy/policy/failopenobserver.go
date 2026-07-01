// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
)

// EventTypeInterceptorFailOpenEscalated and ...Restored are the §4.8
// line 1030 audit event types for cumulative fail-open escalation.
const (
	EventTypeInterceptorFailOpenEscalated = "interceptor.fail_open_escalated"
	EventTypeInterceptorFailOpenRestored  = "interceptor.fail_open_restored"
)

// FailOpenObserver writes the §4.8 line 1030 fail-open escalation audit
// events to the per-tenant §11.7 hash chain. It satisfies
// interceptor.FailOpenObserver so the gateway hands it to
// Chain.SetFailOpenEscalation. The append is gateway-originated and
// best-effort: an escalation must take effect even if the audit write
// transiently fails, so a write error is dropped rather than blocking
// the request path. The hash-chain integrity check (§11.7) surfaces a
// persistent backend fault.
type FailOpenObserver struct {
	appender AuditAppender
	clock    func() time.Time
}

// NewFailOpenObserver returns an observer backed by appender. clock
// overrides time.Now; pass nil in production.
func NewFailOpenObserver(appender AuditAppender, clock func() time.Time) *FailOpenObserver {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &FailOpenObserver{appender: appender, clock: clock}
}

// FailOpenEscalated writes `interceptor.fail_open_escalated`.
func (o *FailOpenObserver) FailOpenEscalated(ctx context.Context, ev interceptor.FailOpenEvent) {
	o.emit(ctx, EventTypeInterceptorFailOpenEscalated, ev)
}

// FailOpenRestored writes `interceptor.fail_open_restored`.
func (o *FailOpenObserver) FailOpenRestored(ctx context.Context, ev interceptor.FailOpenEvent) {
	o.emit(ctx, EventTypeInterceptorFailOpenRestored, ev)
}

func (o *FailOpenObserver) emit(ctx context.Context, eventType string, ev interceptor.FailOpenEvent) {
	if o.appender == nil {
		return
	}
	// spec: §4.8 line 1030 — payload fields are interceptor_ref,
	// failure_count, and window_seconds. The phase is carried as an
	// operator aid for which chain the interceptor runs on.
	payload, _ := json.Marshal(map[string]any{
		"interceptor_ref": ev.InterceptorName,
		"failure_count":   ev.Consecutive,
		"window_seconds":  ev.WindowSeconds,
		"phase":           string(ev.Phase),
	})
	_, _ = o.appender.Append(ctx, ev.TenantID, eventType, payload, o.clock())
}

// Ensure FailOpenObserver satisfies the interceptor observer at compile
// time.
var _ interceptor.FailOpenObserver = (*FailOpenObserver)(nil)
