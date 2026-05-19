// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
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
// obj as the raw object payload.
func poolConfigReq(t *testing.T, kind string, obj any) *admissionv1.AdmissionRequest {
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
