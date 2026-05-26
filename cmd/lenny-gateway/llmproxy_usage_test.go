// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
)

// TestProxyUsageRecorderRecordsProxyMode_Spec4_9_1468 confirms a
// proxy-mode lease's authoritative counts land in the usagestore with
// the lease's tenant and the session's runtime.
// spec: spec/04_system-components.md line 1468.
func TestProxyUsageRecorderRecordsProxyMode_Spec4_9_1468(t *testing.T) {
	var sessions sessionstore.Store = memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: "s_1", TenantID: "acme", RuntimeRef: "claude-prod", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	usage := usagestore.NewMemory()
	rec := newProxyUsageRecorder(usage, sessions)
	if rec == nil {
		t.Fatal("newProxyUsageRecorder returned nil with a usage store set")
	}

	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-1", SessionID: "s_1", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 100, OutputTokens: 30})

	report, err := usage.Aggregate(context.Background(), "acme")
	if err != nil {
		t.Fatalf("usage.Aggregate: %v", err)
	}
	if report.TotalTokens.Input != 100 || report.TotalTokens.Output != 30 {
		t.Errorf("tokens not recorded: got %+v want input=100 output=30", report.TotalTokens)
	}
	if len(report.ByRuntime) != 1 || report.ByRuntime[0].Runtime != "claude-prod" {
		t.Errorf("runtime rollup absent: %+v", report.ByRuntime)
	}
}

// TestProxyUsageRecorderIgnoresDirectMode_Spec4_9_1468 confirms a
// direct-mode lease's counts are not double-counted by the proxy
// recorder (the §4.9 LLM proxy never sees direct-mode traffic; the
// defensive check guards against future regressions).
// spec: spec/04_system-components.md line 1468.
func TestProxyUsageRecorderIgnoresDirectMode_Spec4_9_1468(t *testing.T) {
	usage := usagestore.NewMemory()
	rec := newProxyUsageRecorder(usage, memstore.New())
	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-2", SessionID: "s_2", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryDirect,
	}, llmproxy.Usage{InputTokens: 99, OutputTokens: 1})
	report, _ := usage.Aggregate(context.Background(), "acme")
	if report.TotalTokens.Input != 0 || report.TotalTokens.Output != 0 {
		t.Errorf("direct-mode counts leaked into usagestore: %+v", report.TotalTokens)
	}
}

// TestProxyUsageRecorderDropsTenantlessLease_Spec4_9_1468 confirms a
// lease without a tenant attribution is dropped rather than producing
// a tenant-empty usage series.
// spec: spec/04_system-components.md line 1468.
func TestProxyUsageRecorderDropsTenantlessLease_Spec4_9_1468(t *testing.T) {
	usage := usagestore.NewMemory()
	rec := newProxyUsageRecorder(usage, memstore.New())
	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-3", SessionID: "s_3", TenantID: "",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 5, OutputTokens: 5})
	report, _ := usage.Aggregate(context.Background(), "")
	if report.TotalSessions != 0 || report.TotalTokens.Input != 0 {
		t.Errorf("tenantless lease leaked into the usagestore: %+v", report)
	}
}

// TestProxyUsageRecorderSessionMissOmitsRuntime_Spec4_9_1468 confirms
// the recorder still records tenant-scoped counts when the session
// lookup misses (the byTenant rollup must keep reporting).
// spec: spec/04_system-components.md line 1468.
func TestProxyUsageRecorderSessionMissOmitsRuntime_Spec4_9_1468(t *testing.T) {
	usage := usagestore.NewMemory()
	rec := newProxyUsageRecorder(usage, memstore.New())
	rec.RecordUsage(credential.Lease{
		LeaseID: "cl-4", SessionID: "missing", TenantID: "acme",
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryProxy,
	}, llmproxy.Usage{InputTokens: 7, OutputTokens: 3})
	report, _ := usage.Aggregate(context.Background(), "acme")
	if report.TotalTokens.Input != 7 || report.TotalTokens.Output != 3 {
		t.Errorf("tokens not recorded on session miss: %+v", report.TotalTokens)
	}
	if len(report.ByRuntime) != 0 {
		t.Errorf("byRuntime should be empty on session miss: %+v", report.ByRuntime)
	}
}

// TestProxyUsageRecorderNilUsageReturnsNil confirms the recorder skips
// wiring when no usagestore is configured.
func TestProxyUsageRecorderNilUsageReturnsNil(t *testing.T) {
	if rec := newProxyUsageRecorder(nil, memstore.New()); rec != nil {
		t.Errorf("newProxyUsageRecorder(nil, _) = %v, want nil", rec)
	}
}
