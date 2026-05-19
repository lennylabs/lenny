// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
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

// fakeUsage is a UsageReader test double.
type fakeUsage struct {
	used map[string]int64
	err  error
}

func (f fakeUsage) Usage(_ context.Context, tenantID, userID string, _ quota.ResetPeriod, _ time.Time) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.used[tenantID+"/"+userID], nil
}

func TestQuotaEvaluator_ContractFields(t *testing.T) {
	e := NewQuotaEvaluator(fakeLimits{}, fakeUsage{}, nil)
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
		fakeUsage{used: map[string]int64{"acme/alice": 100}},
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
		fakeUsage{used: map[string]int64{"acme/alice": 1000}},
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
		fakeUsage{},
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
	e := NewQuotaEvaluator(fakeLimits{limits: map[string]TenantLimits{}}, fakeUsage{}, nil)
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
	e := NewQuotaEvaluator(fakeLimits{}, fakeUsage{}, nil)
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
		fakeUsage{err: errors.New("counter must not be read")},
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
		fakeUsage{used: map[string]int64{"acme/alice": 600}},
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
