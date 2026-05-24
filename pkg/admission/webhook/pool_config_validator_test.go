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
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
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
		Kind:      metav1.GroupVersionKind{Group: "lenny.dev", Version: "v1", Kind: kind},
		Object:    runtime.RawExtension{Raw: raw},
		UserInfo:  authnv1.UserInfo{Username: username},
	}
}

func TestPoolConfigValidatorAdmitsValidWarmPool(t *testing.T) {
	pool := lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pool"},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: 2, MaxWarm: 10},
	}
	resp := webhook.PoolConfigValidator()(context.Background(), poolConfigReq(t, "SandboxWarmPool", pool))
	if !resp.Allowed {
		t.Fatalf("a SandboxWarmPool with minWarm <= maxWarm must be admitted: %+v", resp.Result)
	}
}

func TestPoolConfigValidatorRejectsWarmPoolBudgetViolation(t *testing.T) {
	pool := lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-pool"},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "t", MinWarm: 20, MaxWarm: 10},
	}
	resp := webhook.PoolConfigValidator()(context.Background(), poolConfigReq(t, "SandboxWarmPool", pool))
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
			RuntimeRef: "r", ExecutionMode: "task",
			TaskPolicy: &lennyv1.TaskPolicy{AcknowledgeBestEffortScrub: true, MaxTasksPerPod: 50},
		},
	}
	resp := webhook.PoolConfigValidator()(context.Background(), poolConfigReq(t, "SandboxTemplate", tpl))
	if !resp.Allowed {
		t.Fatalf("an acknowledged task-mode SandboxTemplate must be admitted: %+v", resp.Result)
	}
}

func TestPoolConfigValidatorRejectsTemplateInvariantViolation(t *testing.T) {
	tpl := lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-template"},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "r", ExecutionMode: "task"},
	}
	resp := webhook.PoolConfigValidator()(context.Background(), poolConfigReq(t, "SandboxTemplate", tpl))
	if resp.Allowed {
		t.Fatal("a task-mode SandboxTemplate without taskPolicy must be rejected")
	}
	if resp.Result == nil || resp.Result.Code != 422 {
		t.Fatalf("rejection code = %v, want 422", resp.Result)
	}
}

func TestPoolConfigValidatorRejectsUnknownKind(t *testing.T) {
	resp := webhook.PoolConfigValidator()(context.Background(),
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
	resp := webhook.PoolConfigValidator()(context.Background(), req)
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
			RuntimeRef: "r", ExecutionMode: "task",
			TaskPolicy: &lennyv1.TaskPolicy{AcknowledgeBestEffortScrub: true, MaxTasksPerPod: 50},
		},
	}
	req := poolConfigReqAs(t, "SandboxTemplate", tpl, "kubernetes-admin")
	resp := webhook.PoolConfigValidator()(context.Background(), req)
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
	resp := webhook.PoolConfigValidator()(context.Background(), req)
	if resp.Allowed {
		t.Fatal("a budget-violating write must be rejected regardless of principal")
	}
	if resp.Result == nil || resp.Result.Code != 422 {
		t.Fatalf("rejection code = %v, want 422 (rule set 1 precedes rule set 2)", resp.Result)
	}
}

func TestPoolConfigValidatorRejectsMalformedObject(t *testing.T) {
	req := &admissionv1.AdmissionRequest{
		UID:       "test-uid",
		Operation: admissionv1.Create,
		Kind:      metav1.GroupVersionKind{Group: "lenny.dev", Version: "v1", Kind: "SandboxWarmPool"},
		Object:    runtime.RawExtension{Raw: []byte("{not json")},
	}
	resp := webhook.PoolConfigValidator()(context.Background(), req)
	if resp.Allowed {
		t.Fatal("a SandboxWarmPool the webhook cannot decode must be rejected fail-closed")
	}
	if resp.Result == nil || resp.Result.Code != 400 {
		t.Fatalf("rejection code = %v, want 400", resp.Result)
	}
}
