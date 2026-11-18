// SPDX-License-Identifier: MIT

package ownership

import (
	"errors"
	"testing"
)

func TestValidateAllowsOwnedWrites(t *testing.T) {
	cases := []struct {
		controller Controller
		crd        CRD
		field      string
	}{
		{PoolScalingController, PodSet, "spec"},
		{PoolScalingController, PodSet, "spec.runtimeRef"},
		{WarmPoolController, PodSet, "status.conditions"},
		{PoolScalingController, PodSet, "spec.minWarm"},
		{PoolScalingController, PodSet, "spec.maxWarm"},
		{PoolScalingController, PodSet, "spec.scalePolicy"},
		{PoolScalingController, PodSet, "spec.scalePolicy.maxIdleSeconds"},
		{PoolScalingController, PodSet, "spec.sdkWarmDisabled"},
		{PoolScalingController, PodSet, "status.sdkWarmCircuitBreaker"},
		{PoolScalingController, PodSet, "status.sdkWarmCircuitBreaker.openedAt"},
		{WarmPoolController, PodSet, "status.warmCount"},
		{WarmPoolController, PodSet, "status.readyCount"},
		{WarmPoolController, AgentPod, "spec.runtimeImage"},
		{WarmPoolController, AgentPod, "status.phase"},
		{Gateway, PodClaim, "spec.agentPodRef"},
		{Gateway, PodClaim, "status.phase"},
	}
	for _, c := range cases {
		if err := Validate(c.controller, c.crd, c.field); err != nil {
			t.Errorf("Validate(%s, %s, %s) = %v, want nil", c.controller, c.crd, c.field, err)
		}
	}
}

func TestValidateRejectsCrossControllerWrites(t *testing.T) {
	cases := []struct {
		controller Controller
		crd        CRD
		field      string
		want       Controller
	}{
		{WarmPoolController, PodSet, "spec", PoolScalingController},
		{PoolScalingController, PodSet, "status", WarmPoolController},
		{WarmPoolController, PodSet, "spec.minWarm", PoolScalingController},
		{WarmPoolController, PodSet, "status.sdkWarmCircuitBreaker.openedAt", PoolScalingController},
		{PoolScalingController, PodSet, "status.warmCount", WarmPoolController},
		{PoolScalingController, AgentPod, "spec", WarmPoolController},
		{Gateway, AgentPod, "status.phase", WarmPoolController},
		{WarmPoolController, PodClaim, "status.phase", Gateway},
	}
	for _, c := range cases {
		err := Validate(c.controller, c.crd, c.field)
		if err == nil {
			t.Errorf("Validate(%s, %s, %s) returned nil, expected error", c.controller, c.crd, c.field)
			continue
		}
		var oe *OwnershipError
		if !errors.As(err, &oe) {
			t.Errorf("expected *OwnershipError, got %T", err)
			continue
		}
		if got, _ := Owner(c.crd, c.field); got != c.want {
			t.Errorf("Owner(%s, %s) = %s, want %s", c.crd, c.field, got, c.want)
		}
	}
}

func TestValidateRejectsUnknownField(t *testing.T) {
	err := Validate(PoolScalingController, PodSet, "metadata.labels")
	if err == nil {
		t.Errorf("metadata.* paths should be outside the §4.6.3 matrix")
	}
}

func TestValidateRejectsEmptyFieldPath(t *testing.T) {
	err := Validate(PoolScalingController, PodSet, "")
	if err == nil {
		t.Errorf("empty fieldPath should error")
	}
}

func TestOwnerSdkWarmCircuitBreakerOverridesDefault(t *testing.T) {
	// The carve-out: status.sdkWarmCircuitBreaker.* belongs to PSC
	// even though status.* by default is WarmPoolController.
	owner, ok := Owner(PodSet, "status.sdkWarmCircuitBreaker.openedAt")
	if !ok || owner != PoolScalingController {
		t.Errorf("status.sdkWarmCircuitBreaker.openedAt: want PoolScalingController, got (%q, %v)", owner, ok)
	}
	owner, ok = Owner(PodSet, "status.warmCount")
	if !ok || owner != WarmPoolController {
		t.Errorf("status.warmCount: want WarmPoolController, got (%q, %v)", owner, ok)
	}
}

func TestAllRulesEnumerateMatrix(t *testing.T) {
	rules := AllRules()
	if len(rules) < 7 {
		t.Errorf("AllRules() should enumerate the §4.6.3 matrix, got %d", len(rules))
	}
	// Every rule must validate against its declared owner.
	for _, r := range rules {
		if err := Validate(r.Controller, r.CRD, r.FieldPath); err != nil {
			t.Errorf("AllRules row %+v fails Validate: %v", r, err)
		}
	}
}
