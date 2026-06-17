// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// spec: §10.5 line 508 — the lenny-sandboxtemplate-deletion-guard
// webhook blocks a SandboxTemplate DELETE while a RuntimeUpgrade
// referencing the template's pool is still active.

// pool builds a SandboxWarmPool referencing templateRef in guardNS.
func pool(name, templateRef string) *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: guardNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: templateRef},
	}
}

// fakeUpgradeProbe reports a fixed active set keyed by pool name, or an
// error so the fail-closed branch can be exercised.
type fakeUpgradeProbe struct {
	active map[string]bool
	err    error
}

func (p fakeUpgradeProbe) ActiveForPool(_ context.Context, poolName string) (bool, error) {
	if p.err != nil {
		return false, p.err
	}
	return p.active[poolName], nil
}

func deleteTemplateReq(name string) *admissionv1.AdmissionRequest {
	return &admissionv1.AdmissionRequest{
		UID:       "d1",
		Operation: admissionv1.Delete,
		Namespace: guardNS,
		Name:      name,
		Resource:  metav1.GroupVersionResource{Group: "lenny.dev", Version: "v1alpha1", Resource: "sandboxtemplates"},
	}
}

func TestDeletionBlockedWhenReferencingPoolUpgradeActive_spec_10_5_508(t *testing.T) {
	c := guardClient(t, pool("coding-agents", "coding-tmpl-v1"))
	probe := fakeUpgradeProbe{active: map[string]bool{"coding-agents": true}}
	resp := webhook.SandboxTemplateDeletionGuard(c, probe)(context.Background(), deleteTemplateReq("coding-tmpl-v1"))
	if resp.Allowed {
		t.Fatalf("deleting a template whose pool has an active upgrade must be denied")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusConflict {
		t.Errorf("deny code = %v, want 409", resp.Result)
	}
}

func TestDeletionAllowedWhenNoActiveUpgrade_spec_10_5_508(t *testing.T) {
	c := guardClient(t, pool("coding-agents", "coding-tmpl-v1"))
	probe := fakeUpgradeProbe{active: map[string]bool{"coding-agents": false}}
	resp := webhook.SandboxTemplateDeletionGuard(c, probe)(context.Background(), deleteTemplateReq("coding-tmpl-v1"))
	if !resp.Allowed {
		t.Errorf("deleting a template with no active upgrade should be allowed: %+v", resp.Result)
	}
}

func TestDeletionAllowedWhenTemplateUnreferenced_spec_10_5_508(t *testing.T) {
	// A pool references a different template; the deleted template has no
	// referencing pool, so the guard has nothing to protect.
	c := guardClient(t, pool("coding-agents", "other-tmpl"))
	probe := fakeUpgradeProbe{active: map[string]bool{"coding-agents": true}}
	resp := webhook.SandboxTemplateDeletionGuard(c, probe)(context.Background(), deleteTemplateReq("coding-tmpl-v1"))
	if !resp.Allowed {
		t.Errorf("deleting an unreferenced template should be allowed: %+v", resp.Result)
	}
}

func TestDeletionGuardIgnoresNonDelete_spec_10_5_508(t *testing.T) {
	c := guardClient(t, pool("coding-agents", "coding-tmpl-v1"))
	probe := fakeUpgradeProbe{active: map[string]bool{"coding-agents": true}}
	req := deleteTemplateReq("coding-tmpl-v1")
	req.Operation = admissionv1.Update
	resp := webhook.SandboxTemplateDeletionGuard(c, probe)(context.Background(), req)
	if !resp.Allowed {
		t.Errorf("the guard governs DELETE only; an UPDATE must pass: %+v", resp.Result)
	}
}

func TestDeletionFailsClosedOnProbeError_spec_10_5_508(t *testing.T) {
	c := guardClient(t, pool("coding-agents", "coding-tmpl-v1"))
	probe := fakeUpgradeProbe{err: errors.New("gateway unreachable")}
	resp := webhook.SandboxTemplateDeletionGuard(c, probe)(context.Background(), deleteTemplateReq("coding-tmpl-v1"))
	if resp.Allowed {
		t.Fatalf("a gateway probe failure must deny the delete fail-closed")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusServiceUnavailable {
		t.Errorf("deny code = %v, want 503", resp.Result)
	}
}

func TestDeletionFailsClosedWhenReaderUnavailable_spec_10_5_508(t *testing.T) {
	probe := fakeUpgradeProbe{active: map[string]bool{}}
	resp := webhook.SandboxTemplateDeletionGuard(nil, probe)(context.Background(), deleteTemplateReq("coding-tmpl-v1"))
	if resp.Allowed {
		t.Fatalf("a nil reader cannot confirm the safety invariant; delete must be denied")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusServiceUnavailable {
		t.Errorf("deny code = %v, want 503", resp.Result)
	}
}

func TestDeletionChecksEveryReferencingPool_spec_10_5_508(t *testing.T) {
	// Two pools share the template; only the second has an active
	// upgrade. The guard must still deny.
	c := guardClient(
		t,
		pool("pool-a", "shared-tmpl"),
		pool("pool-b", "shared-tmpl"),
	)
	probe := fakeUpgradeProbe{active: map[string]bool{"pool-b": true}}
	resp := webhook.SandboxTemplateDeletionGuard(c, probe)(context.Background(), deleteTemplateReq("shared-tmpl"))
	if resp.Allowed {
		t.Fatalf("an active upgrade on any referencing pool must block the delete")
	}
}
