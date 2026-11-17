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

// spec: §10.5 — the lenny-podset-deletion-guard
// webhook blocks a PodSet DELETE while a RuntimeUpgrade of the PodSet's
// pool is still active.

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

func deletePodSetReq(name string) *admissionv1.AdmissionRequest {
	return &admissionv1.AdmissionRequest{
		UID:       "d1",
		Operation: admissionv1.Delete,
		Namespace: guardNS,
		Name:      name,
		Resource:  metav1.GroupVersionResource{Group: "lenny.dev", Version: "v1alpha1", Resource: "podsets"},
	}
}

func TestDeletionBlockedWhenReferencingPoolUpgradeActive_spec_10_5_508(t *testing.T) {
	probe := fakeUpgradeProbe{active: map[string]bool{"coding-agents": true}}
	resp := webhook.PodSetDeletionGuard(probe)(context.Background(), deletePodSetReq("coding-agents"))
	if resp.Allowed {
		t.Fatalf("deleting a PodSet whose pool has an active upgrade must be denied")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusConflict {
		t.Errorf("deny code = %v, want 409", resp.Result)
	}
}

func TestDeletionAllowedWhenNoActiveUpgrade_spec_10_5_508(t *testing.T) {
	probe := fakeUpgradeProbe{active: map[string]bool{"coding-agents": false}}
	resp := webhook.PodSetDeletionGuard(probe)(context.Background(), deletePodSetReq("coding-agents"))
	if !resp.Allowed {
		t.Errorf("deleting a PodSet with no active upgrade should be allowed: %+v", resp.Result)
	}
}

func TestDeletionGuardIgnoresNonDelete_spec_10_5_508(t *testing.T) {
	probe := fakeUpgradeProbe{active: map[string]bool{"coding-agents": true}}
	req := deletePodSetReq("coding-agents")
	req.Operation = admissionv1.Update
	resp := webhook.PodSetDeletionGuard(probe)(context.Background(), req)
	if !resp.Allowed {
		t.Errorf("the guard governs DELETE only; an UPDATE must pass: %+v", resp.Result)
	}
}

func TestDeletionFailsClosedOnProbeError_spec_10_5_508(t *testing.T) {
	probe := fakeUpgradeProbe{err: errors.New("gateway unreachable")}
	resp := webhook.PodSetDeletionGuard(probe)(context.Background(), deletePodSetReq("coding-agents"))
	if resp.Allowed {
		t.Fatalf("a gateway probe failure must deny the delete fail-closed")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusServiceUnavailable {
		t.Errorf("deny code = %v, want 503", resp.Result)
	}
}
