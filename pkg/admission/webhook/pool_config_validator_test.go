// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	pcv "github.com/lennylabs/lenny/pkg/admission/pool_config_validator"
	"github.com/lennylabs/lenny/pkg/admission/webhook"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// spec: §4.6.3 (spec/04_system-components.md) — the
// lenny-pool-config-validator webhook transport dispatches on the
// admitted resource Kind, decodes a SandboxWarmPool or SandboxTemplate,
// and surfaces a rule-set-1 rejection as a 422 with the
// INVALID_POOL_CONFIGURATION reason code.

// poolConfigReq builds an AdmissionRequest for the given Kind carrying
// obj as the raw object payload, with userInfo.username set to the
// PoolScalingController SA so the rule-set-2 authorization backstop
// (§4.6.3) admits the write. Tests exercising rule set 2 set a different
// principal via poolConfigReqAs.
func poolConfigReq(t *testing.T, kind string, obj any) *admissionv1.AdmissionRequest {
	return poolConfigReqAs(t, kind, obj, pcv.PoolScalingControllerSA)
}

// poolConfigReqAs builds an AdmissionRequest as a specific principal.
func poolConfigReqAs(t *testing.T, kind string, obj any, username string) *admissionv1.AdmissionRequest {
	t.Helper()
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal %s: %v", kind, err)
	}
	return &admissionv1.AdmissionRequest{
		UID:       "test-uid",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Group: "lenny.dev", Version: "v1alpha1", Kind: kind},
		Object:    runtime.RawExtension{Raw: raw},
		UserInfo:  authnv1.UserInfo{Username: username},
	}
}

func TestPoolConfigValidatorAdmitsValidWarmPool(t *testing.T) {
	pool := lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pool"},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: 2, MaxWarm: 10},
	}
	resp := webhook.PoolConfigValidator(nil)(context.Background(), poolConfigReq(t, "SandboxWarmPool", pool))
	if !resp.Allowed {
		t.Fatalf("a SandboxWarmPool with minWarm <= maxWarm must be admitted: %+v", resp.Result)
	}
}

func TestPoolConfigValidatorRejectsWarmPoolBudgetViolation(t *testing.T) {
	pool := lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pool"},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: 20, MaxWarm: 10},
	}
	resp := webhook.PoolConfigValidator(nil)(context.Background(), poolConfigReq(t, "SandboxWarmPool", pool))
	if resp.Allowed {
		t.Fatal("a SandboxWarmPool with minWarm above maxWarm must be rejected")
	}
	if resp.Result == nil || resp.Result.Code != 422 {
		t.Fatalf("rejection code = %v, want 422", resp.Result)
	}
	if !strings.Contains(resp.Result.Message, pcv.ReasonInvalidPoolConfiguration) {
		t.Errorf("message = %q, want %s", resp.Result.Message, pcv.ReasonInvalidPoolConfiguration)
	}
}

func TestPoolConfigValidatorAdmitsValidTemplate(t *testing.T) {
	tpl := lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-template"},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef: "r", ExecutionMode: "session", IsolationProfile: "microvm",
			SessionPolicy: &lennyv1.SessionPolicy{
				Recycle: &lennyv1.RecyclePolicy{
					ScrubProfile:                    "in-place",
					AcknowledgeMicrovmResidualState: true,
				},
			},
		},
	}
	resp := webhook.PoolConfigValidator(nil)(context.Background(), poolConfigReq(t, "SandboxTemplate", tpl))
	if !resp.Allowed {
		t.Fatalf("an acknowledged in-place recycle SandboxTemplate must be admitted: %+v", resp.Result)
	}
}

func TestPoolConfigValidatorRejectsTemplateInvariantViolation(t *testing.T) {
	tpl := lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-template"},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef: "r", ExecutionMode: "session", IsolationProfile: "microvm",
			SessionPolicy: &lennyv1.SessionPolicy{
				Recycle: &lennyv1.RecyclePolicy{ScrubProfile: "in-place"},
			},
		},
	}
	resp := webhook.PoolConfigValidator(nil)(context.Background(), poolConfigReq(t, "SandboxTemplate", tpl))
	if resp.Allowed {
		t.Fatal("an in-place recycle SandboxTemplate without the residual-state acknowledgment must be rejected")
	}
	if resp.Result == nil || resp.Result.Code != 422 {
		t.Fatalf("rejection code = %v, want 422", resp.Result)
	}
}

func TestPoolConfigValidatorRejectsUnknownKind(t *testing.T) {
	resp := webhook.PoolConfigValidator(nil)(context.Background(),
		poolConfigReq(t, "Sandbox", lennyv1.Sandbox{}))
	if resp.Allowed {
		t.Fatal("an unexpected resource kind must be rejected fail-closed")
	}
	if resp.Result == nil || resp.Result.Code != 400 {
		t.Fatalf("rejection code = %v, want 400", resp.Result)
	}
}

// spec: §4.6.3 line 601 (spec/04_system-components.md) — rule set 2:
// manual writes to a SandboxWarmPool/SandboxTemplate spec from any
// principal other than the PoolScalingController SA are rejected with
// HTTP 403 and the UNAUTHORIZED_POOL_CONFIG_WRITE reason code, even when
// the write satisfies every rule-set-1 budget invariant.
func TestPoolConfigValidatorRejectsManualWarmPoolWrite(t *testing.T) {
	pool := lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pool"},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: 2, MaxWarm: 10},
	}
	req := poolConfigReqAs(t, "SandboxWarmPool", pool, "system:serviceaccount:acme:platform-admin")
	resp := webhook.PoolConfigValidator(nil)(context.Background(), req)
	if resp.Allowed {
		t.Fatal("a budget-valid SandboxWarmPool written by a non-PSC principal must be rejected by rule set 2")
	}
	if resp.Result == nil || resp.Result.Code != 403 {
		t.Fatalf("rejection code = %v, want 403", resp.Result)
	}
	if !strings.Contains(resp.Result.Message, pcv.ReasonUnauthorizedPoolConfigWrite) {
		t.Errorf("message = %q, want %s", resp.Result.Message, pcv.ReasonUnauthorizedPoolConfigWrite)
	}
}

func TestPoolConfigValidatorRejectsManualTemplateWrite(t *testing.T) {
	tpl := lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-template"},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef: "r", ExecutionMode: "session",
		},
	}
	req := poolConfigReqAs(t, "SandboxTemplate", tpl, "kubernetes-admin")
	resp := webhook.PoolConfigValidator(nil)(context.Background(), req)
	if resp.Allowed {
		t.Fatal("a budget-valid SandboxTemplate written by a non-PSC principal must be rejected by rule set 2")
	}
	if resp.Result == nil || resp.Result.Code != 403 {
		t.Fatalf("rejection code = %v, want 403", resp.Result)
	}
}

// A rule-set-1 budget violation is reported as a 422 even when the
// writer is not the PSC SA; rule set 1 runs first so the more specific
// budget message reaches the manual editor.
func TestPoolConfigValidatorBudgetViolationPrecedesAuthz(t *testing.T) {
	pool := lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pool"},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: 20, MaxWarm: 10},
	}
	req := poolConfigReqAs(t, "SandboxWarmPool", pool, "system:serviceaccount:acme:platform-admin")
	resp := webhook.PoolConfigValidator(nil)(context.Background(), req)
	if resp.Allowed {
		t.Fatal("a budget-violating write must be rejected regardless of principal")
	}
	if resp.Result == nil || resp.Result.Code != 422 {
		t.Fatalf("rejection code = %v, want 422 (rule set 1 precedes rule set 2)", resp.Result)
	}
}

// spec: §5.2 line 516 (spec/05_runtime-registry-and-pool-model.md) — a
// concurrent-workspace pool whose computed terminationGracePeriodSeconds
// floor exceeds 600s is admitted with an advisory warning on the
// AdmissionResponse, not rejected.
func TestPoolConfigValidatorPropagatesTerminationGraceWarning_spec_5_2_516(t *testing.T) {
	tpl := lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-template"},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:    "r",
			ExecutionMode: "service",
			MaxConcurrent: 8, // 8*90 + 90 + 30 = 840s > 600s
		},
	}
	resp := webhook.PoolConfigValidator(nil)(context.Background(), poolConfigReq(t, "SandboxTemplate", tpl))
	if !resp.Allowed {
		t.Fatalf("an above-600s floor must be admitted with a warning: %+v", resp.Result)
	}
	if len(resp.Warnings) != 1 {
		t.Fatalf("want one warning, got %v", resp.Warnings)
	}
	if !strings.Contains(resp.Warnings[0], "840s") {
		t.Errorf("warning %q does not name the floor", resp.Warnings[0])
	}
}

// spec: §5.2 line 516 — a deployer who sets
// maxTerminationGracePeriodSeconds gets a hard rejection when the floor
// breaches the ceiling.
func TestPoolConfigValidatorRejectsTerminationGraceCeilingBreach_spec_5_2_516(t *testing.T) {
	ceiling := int64(600)
	tpl := lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-template"},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:                       "r",
			ExecutionMode:                    "service",
			MaxConcurrent:                    8,
			MaxTerminationGracePeriodSeconds: &ceiling,
		},
	}
	resp := webhook.PoolConfigValidator(nil)(context.Background(), poolConfigReq(t, "SandboxTemplate", tpl))
	if resp.Allowed {
		t.Fatal("a floor above maxTerminationGracePeriodSeconds must be rejected")
	}
	if resp.Result == nil || resp.Result.Code != 422 {
		t.Fatalf("rejection code = %v, want 422", resp.Result)
	}
	if !strings.Contains(resp.Result.Message, "spec.maxTerminationGracePeriodSeconds (600s)") {
		t.Errorf("message = %q, want ceiling reference", resp.Result.Message)
	}
}

// spec: §16.1 line 129 / §10.1 line 119 — every SandboxTemplate write
// the webhook rejects on the termination-budget inequality increments
// lenny_pool_termination_budget_exceeded_total, labeled by pool. The
// BarrierAck-floor and execution-mode rejections must NOT increment it.
type budgetCounterSpy struct{ pools []string }

func (s *budgetCounterSpy) IncPoolTerminationBudgetExceeded(pool string) {
	s.pools = append(s.pools, pool)
}

func TestPoolConfigValidatorEmitsBudgetCounter_spec_16_1_129(t *testing.T) {
	int64p := func(v int64) *int64 { return &v }

	t.Run("budget rejection increments the counter with the pool label", func(t *testing.T) {
		spy := &budgetCounterSpy{}
		// session-mode pool, default 90s tier, grace 1s → floor 210s > 1s.
		tpl := lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "agent-template"},
			Spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef:                    "r",
				TerminationGracePeriodSeconds: int64p(1),
			},
		}
		resp := webhook.PoolConfigValidator(spy)(context.Background(), poolConfigReq(t, "SandboxTemplate", tpl))
		if resp.Allowed {
			t.Fatal("a below-floor template must be rejected")
		}
		if len(spy.pools) != 1 || spy.pools[0] != "agent-template" {
			t.Fatalf("counter pools = %v, want one increment labeled agent-template", spy.pools)
		}
	})

	t.Run("BarrierAck-floor rejection does not increment the budget counter", func(t *testing.T) {
		spy := &budgetCounterSpy{}
		tpl := lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "agent-template"},
			Spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef:                         "r",
				WorkspaceSizeLimitBytes:            int64p(300 * 1024 * 1024),
				CheckpointBarrierAckTimeoutSeconds: int64p(30),
			},
		}
		resp := webhook.PoolConfigValidator(spy)(context.Background(), poolConfigReq(t, "SandboxTemplate", tpl))
		if resp.Allowed {
			t.Fatal("a BarrierAck-floor violation must be rejected")
		}
		if len(spy.pools) != 0 {
			t.Fatalf("counter pools = %v, want no increment for a non-budget rejection", spy.pools)
		}
	})

	t.Run("admitted template does not increment the counter", func(t *testing.T) {
		spy := &budgetCounterSpy{}
		tpl := lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "agent-template"},
			Spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef:                    "r",
				TerminationGracePeriodSeconds: int64p(210),
			},
		}
		resp := webhook.PoolConfigValidator(spy)(context.Background(), poolConfigReq(t, "SandboxTemplate", tpl))
		if !resp.Allowed {
			t.Fatalf("an at-floor template must be admitted: %+v", resp.Result)
		}
		if len(spy.pools) != 0 {
			t.Fatalf("counter pools = %v, want no increment on admit", spy.pools)
		}
	})

	t.Run("nil sink is safe on a budget rejection", func(t *testing.T) {
		tpl := lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "agent-template"},
			Spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef:                    "r",
				TerminationGracePeriodSeconds: int64p(1),
			},
		}
		resp := webhook.PoolConfigValidator(nil)(context.Background(), poolConfigReq(t, "SandboxTemplate", tpl))
		if resp.Allowed {
			t.Fatal("a below-floor template must be rejected even with a nil metrics sink")
		}
	})
}

func TestPoolConfigValidatorRejectsMalformedObject(t *testing.T) {
	req := &admissionv1.AdmissionRequest{
		UID:       "test-uid",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Group: "lenny.dev", Version: "v1alpha1", Kind: "SandboxWarmPool"},
		Object:    runtime.RawExtension{Raw: []byte("{not json")},
	}
	resp := webhook.PoolConfigValidator(nil)(context.Background(), req)
	if resp.Allowed {
		t.Fatal("a SandboxWarmPool the webhook cannot decode must be rejected fail-closed")
	}
	if resp.Result == nil || resp.Result.Code != 400 {
		t.Fatalf("rejection code = %v, want 400", resp.Result)
	}
}
