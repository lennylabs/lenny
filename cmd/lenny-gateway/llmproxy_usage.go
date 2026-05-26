// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
)

// proxyUsageRecorder bridges the §4.9 LLM proxy's authoritative
// proxy-extracted token counts into the gateway's §11.2 / §15.1 usage
// store. spec: spec/04_system-components.md line 1468 — proxy-extracted
// counts are the authoritative record for quota accounting; pod-reported
// counts are ignored for sessions in proxy mode.
//
// The recorder is wired only when the gateway has a usagestore. A nil
// recorder, or a record call for a non-proxy-mode lease, leaves the
// usagestore untouched.
type proxyUsageRecorder struct {
	usage    usagestore.Store
	sessions sessionstore.Store
}

func newProxyUsageRecorder(usage usagestore.Store, sessions sessionstore.Store) *proxyUsageRecorder {
	if usage == nil {
		return nil
	}
	return &proxyUsageRecorder{usage: usage, sessions: sessions}
}

// RecordUsage implements llmproxy.UsageRecorder. It records the
// proxy-extracted token usage against the lease's owning tenant. A
// direct-mode lease never reaches the proxy hot path; this recorder
// defends against future direct-mode regressions by ignoring the call
// (the spec rule at line 1468 fires only in proxy mode).
//
// The runtime label is populated from a best-effort session lookup so
// the §15.1 per-runtime usage rollup keeps reporting. A lookup miss
// records the tokens against the tenant alone, leaving Runtime empty.
func (r *proxyUsageRecorder) RecordUsage(lease credential.Lease, u llmproxy.Usage) {
	if r == nil {
		return
	}
	if lease.DeliveryMode != credential.DeliveryProxy {
		// spec: §4.9 line 1468 — only proxy-mode counts are authoritative.
		return
	}
	if lease.TenantID == "" {
		// Without a tenant attribution the record is meaningless to the
		// §15.1 byTenant rollup; drop rather than emit an "unknown"
		// tenant series.
		return
	}
	rec := usagestore.Record{
		TenantID: lease.TenantID,
		Tokens: usagestore.Tokens{
			Input:  int64(u.InputTokens),
			Output: int64(u.OutputTokens),
		},
	}
	if r.sessions != nil && lease.SessionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), proxyUsageLookupTimeout)
		defer cancel()
		if sess, err := r.sessions.Get(ctx, lease.TenantID, lease.SessionID); err == nil {
			rec.Runtime = sess.RuntimeRef
		}
	}
	// Record is best-effort: the metering store is already a degradable
	// dependency in this gateway (memory fallback when Postgres is off),
	// so a transient failure must not fail the proxied LLM call.
	ctx, cancel := context.WithTimeout(context.Background(), proxyUsageRecordTimeout)
	defer cancel()
	_ = r.usage.Record(ctx, rec)
}

// proxyUsageLookupTimeout bounds the per-request session lookup that
// the recorder uses to attach a runtime label. The proxy call returns
// to the pod before this fires; the recorder runs synchronously on the
// response path so a stuck store cannot pin the pod's request thread.
const proxyUsageLookupTimeout = 250 * time.Millisecond

// proxyUsageRecordTimeout bounds the usagestore Record write itself.
const proxyUsageRecordTimeout = 250 * time.Millisecond
