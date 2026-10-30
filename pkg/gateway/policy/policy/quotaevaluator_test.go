// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// fakeLimits is a TenantLimitLookup test double.
type fakeLimits struct {
	limits map[string]TenantLimits
	err    error
}

func (f fakeLimits) LookupLimits(_ context.Context, tenantID string) (TenantLimits, error) {
	if f.err != nil {
		return TenantLimits{}, f.err
	}
	l, ok := f.limits[tenantID]
	if !ok {
		return TenantLimits{}, ErrTenantNotFound
	}
	return l, nil
}

// fakeUsage is a UsageReader test double. It models the three §11.2
// scope windows independently so a test can drive a reject at any single
// scope. Scope keys: "u/"+tenant+"/"+user (per-user), "t/"+tenant
// (per-tenant rollup), and "g" (global rollup). A scope absent from the
// map reads as 0.
type fakeUsage struct {
	scoped map[string]int64
	err    error
	// sliding records that the rolling-window read path was taken, so a
	// test can assert the ResetRolling branch runs.
	sliding bool
}

func (f *fakeUsage) scopedUsage(tenantID, userID string) quotastore.Scoped {
	return quotastore.Scoped{
		User:   f.scoped["u/"+tenantID+"/"+userID],
		Tenant: f.scoped["t/"+tenantID],
		Global: f.scoped["g"],
	}
}

func (f *fakeUsage) UsageHierarchical(_ context.Context, tenantID, userID string, _ quota.ResetPeriod, _ time.Time) (quotastore.Scoped, error) {
	if f.err != nil {
		return quotastore.Scoped{}, f.err
	}
	return f.scopedUsage(tenantID, userID), nil
}

func (f *fakeUsage) SlidingUsageHierarchical(_ context.Context, tenantID, userID string, _, _ time.Duration, _ time.Time) (quotastore.Scoped, error) {
	f.sliding = true
	if f.err != nil {
		return quotastore.Scoped{}, f.err
	}
	return f.scopedUsage(tenantID, userID), nil
}

func TestQuotaEvaluator_ContractFields(t *testing.T) {
	e := NewQuotaEvaluator(fakeLimits{}, &fakeUsage{}, nil)
	if e.Name() != QuotaEvaluatorName {
		t.Errorf("Name() = %q, want %q", e.Name(), QuotaEvaluatorName)
	}
	if e.Priority() != 200 {
		t.Errorf("Priority() = %d, want 200", e.Priority())
	}
	if !e.Builtin() {
		t.Error("Builtin() = false, want true")
	}
	if e.FailPolicy() != interceptor.FailClosed {
		t.Errorf("FailPolicy() = %q, want fail-closed", e.FailPolicy())
	}
}

func TestQuotaEvaluator_AdmitsUnderLimit(t *testing.T) {
	e := NewQuotaEvaluator(
		fakeLimits{limits: map[string]TenantLimits{"acme": {Tenant: 1000, Period: quota.ResetHourly}}},
		&fakeUsage{scoped: map[string]int64{"u/acme/alice": 100, "t/acme": 100}},
		nil,
	)
	res, err := e.Intercept(context.Background(), interceptor.Request{
		Phase:    interceptor.PhasePostAuth,
		Metadata: map[string]string{MetadataTenantID: "acme", MetadataUserID: "alice"},
	})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionAllow {
		t.Errorf("Action = %v, want ALLOW", res.Action)
	}
}

func TestQuotaEvaluator_RejectsHardExceeded(t *testing.T) {
	e := NewQuotaEvaluator(
		fakeLimits{limits: map[string]TenantLimits{"acme": {Tenant: 1000, Period: quota.ResetHourly}}},
		&fakeUsage{scoped: map[string]int64{"t/acme": 1000}},
		nil,
	)
	res, err := e.Intercept(context.Background(), interceptor.Request{
		Phase:    interceptor.PhasePostAuth,
		Metadata: map[string]string{MetadataTenantID: "acme", MetadataUserID: "alice"},
	})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionReject {
		t.Fatalf("Action = %v, want REJECT", res.Action)
	}
	if res.Code != CodeQuotaExceeded {
		t.Errorf("Code = %q, want %q", res.Code, CodeQuotaExceeded)
	}
	if res.Reason == "" {
		t.Error("REJECT must carry a reason")
	}
}

func TestQuotaEvaluator_FailsClosedOnLookupError(t *testing.T) {
	// A backing-store fault surfaces as an interceptor error; the chain's
	// fail-closed handling then rejects the request.
	e := NewQuotaEvaluator(
		fakeLimits{err: errors.New("registry down")},
		&fakeUsage{},
		nil,
	)
	_, err := e.Intercept(context.Background(), interceptor.Request{
		Metadata: map[string]string{MetadataTenantID: "acme"},
	})
	if err == nil {
		t.Fatal("a limit-lookup fault must surface as an error so the chain fails closed")
	}
}

func TestQuotaEvaluator_RejectsUnknownTenant(t *testing.T) {
	e := NewQuotaEvaluator(fakeLimits{limits: map[string]TenantLimits{}}, &fakeUsage{}, nil)
	res, err := e.Intercept(context.Background(), interceptor.Request{
		Metadata: map[string]string{MetadataTenantID: "ghost"},
	})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionReject {
		t.Errorf("an unknown tenant must be rejected fail-closed; got %v", res.Action)
	}
}

func TestQuotaEvaluator_RejectsMissingTenant(t *testing.T) {
	e := NewQuotaEvaluator(fakeLimits{}, &fakeUsage{}, nil)
	res, err := e.Intercept(context.Background(), interceptor.Request{
		Metadata: map[string]string{},
	})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionReject {
		t.Errorf("a request with no tenant must be rejected; got %v", res.Action)
	}
}

func TestQuotaEvaluator_NoLimitAdmitsWithoutCounterRead(t *testing.T) {
	// A tenant with no configured limit at any scope is admitted without
	// touching the usage counter.
	e := NewQuotaEvaluator(
		fakeLimits{limits: map[string]TenantLimits{"acme": {}}},
		&fakeUsage{err: errors.New("counter must not be read")},
		nil,
	)
	res, err := e.Intercept(context.Background(), interceptor.Request{
		Metadata: map[string]string{MetadataTenantID: "acme"},
	})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionAllow {
		t.Errorf("Action = %v, want ALLOW", res.Action)
	}
}

func TestQuotaEvaluator_InChain(t *testing.T) {
	// The evaluator registers as a built-in on the PostAuth chain and a
	// hard-exceeded window short-circuits the chain with REJECT.
	e := NewQuotaEvaluator(
		fakeLimits{limits: map[string]TenantLimits{"acme": {Tenant: 500, Period: quota.ResetHourly}}},
		&fakeUsage{scoped: map[string]int64{"t/acme": 600}},
		nil,
	)
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePostAuth, e); err != nil {
		t.Fatalf("register built-in QuotaEvaluator: %v", err)
	}
	res := chain.Run(context.Background(), interceptor.Request{
		Phase:    interceptor.PhasePostAuth,
		Metadata: map[string]string{MetadataTenantID: "acme", MetadataUserID: "alice"},
	})
	if res.Action != interceptor.ActionReject {
		t.Errorf("chain Action = %v, want REJECT", res.Action)
	}
}

// TestQuotaEvaluator_GlobalScopeBindsIndependently proves F-11.2.7: the
// global scope rejects on its own rollup counter even when the per-user
// and per-tenant windows are well under their limits. Before the fix the
// three scopes collapsed to the per-user counter and the global check
// could never fire.
func TestQuotaEvaluator_GlobalScopeBindsIndependently(t *testing.T) {
	e := NewQuotaEvaluator(
		fakeLimits{limits: map[string]TenantLimits{"acme": {
			Global: 10_000, Tenant: 5_000, User: 1_000, Period: quota.ResetHourly,
		}}},
		&fakeUsage{scoped: map[string]int64{
			"u/acme/alice": 100,    // user well under 1_000
			"t/acme":       200,    // tenant well under 5_000
			"g":            10_000, // global at its limit
		}},
		nil,
	)
	res, err := e.Intercept(context.Background(), interceptor.Request{
		Phase:    interceptor.PhasePostAuth,
		Metadata: map[string]string{MetadataTenantID: "acme", MetadataUserID: "alice"},
	})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionReject {
		t.Fatalf("Action = %v, want REJECT on the global scope", res.Action)
	}
	if !strings.Contains(res.Reason, "global") {
		t.Errorf("reject reason %q must name the bound global scope", res.Reason)
	}
}

// TestQuotaEvaluator_TenantScopeBindsIndependently proves the tenant
// rollup binds even when the per-user window is far under its limit.
func TestQuotaEvaluator_TenantScopeBindsIndependently(t *testing.T) {
	e := NewQuotaEvaluator(
		fakeLimits{limits: map[string]TenantLimits{"acme": {
			Tenant: 5_000, User: 1_000, Period: quota.ResetHourly,
		}}},
		&fakeUsage{scoped: map[string]int64{
			"u/acme/alice": 100,   // user well under 1_000
			"t/acme":       5_000, // tenant at its limit
		}},
		nil,
	)
	res, err := e.Intercept(context.Background(), interceptor.Request{
		Phase:    interceptor.PhasePostAuth,
		Metadata: map[string]string{MetadataTenantID: "acme", MetadataUserID: "alice"},
	})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionReject {
		t.Fatalf("Action = %v, want REJECT on the tenant scope", res.Action)
	}
	if !strings.Contains(res.Reason, "tenant") {
		t.Errorf("reject reason %q must name the bound tenant scope", res.Reason)
	}
}

// TestQuotaEvaluator_RollingWindowUsesSlidingRead proves F-11.2.3: a
// tenant configured with the rolling reset period is read via the
// sliding-window counter, not the fixed-window store (which errors for
// ResetRolling). The window is admitted when under the limit.
func TestQuotaEvaluator_RollingWindowUsesSlidingRead(t *testing.T) {
	usage := &fakeUsage{scoped: map[string]int64{"u/acme/alice": 100, "t/acme": 100}}
	e := NewQuotaEvaluator(
		fakeLimits{limits: map[string]TenantLimits{"acme": {
			Tenant: 1_000, Period: quota.ResetRolling, RollingWindow: time.Hour,
		}}},
		usage,
		nil,
	)
	res, err := e.Intercept(context.Background(), interceptor.Request{
		Phase:    interceptor.PhasePostAuth,
		Metadata: map[string]string{MetadataTenantID: "acme", MetadataUserID: "alice"},
	})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionAllow {
		t.Errorf("Action = %v, want ALLOW", res.Action)
	}
	if !usage.sliding {
		t.Error("a rolling reset period must read through the sliding-window counter")
	}
}

func TestAuditSink_RecordsRejection(t *testing.T) {
	chains := audit.NewChainSet()
	sink := NewAuditSink(NewChainSetAppender(chains, nil), nil)
	err := sink.RecordRejection(
		context.Background(),
		RejectionContext{TenantID: "acme", CallerSub: "alice", Phase: interceptor.PhasePostAuth},
		interceptor.Result{Action: interceptor.ActionReject, Code: CodeQuotaExceeded, Reason: "quota exhausted"},
	)
	if err != nil {
		t.Fatalf("RecordRejection: %v", err)
	}
	chain := chains.Chain("acme")
	if chain == nil || chain.Len() != 1 {
		t.Fatalf("audit chain for acme has %d rows, want 1", chainLen(chain))
	}
	row := chain.Rows()[0]
	if row.EventType != EventTypeInterceptorRejected {
		t.Errorf("event_type = %q, want %q", row.EventType, EventTypeInterceptorRejected)
	}
	var payload map[string]any
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload["caller_sub"] != "alice" {
		t.Errorf("payload caller_sub = %v, want alice", payload["caller_sub"])
	}
	if payload["reason"] != "quota exhausted" {
		t.Errorf("payload reason = %v, want %q", payload["reason"], "quota exhausted")
	}
}

func chainLen(c *audit.Chain) int {
	if c == nil {
		return 0
	}
	return c.Len()
}
