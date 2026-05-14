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
		{PoolScalingController, SandboxTemplate, "spec"},
		{PoolScalingController, SandboxTemplate, "spec.runtimeRef"},
		{WarmPoolController, SandboxTemplate, "status.conditions"},
		{PoolScalingController, SandboxWarmPool, "spec.minWarm"},
		{PoolScalingController, SandboxWarmPool, "spec.maxWarm"},
		{PoolScalingController, SandboxWarmPool, "spec.scalePolicy"},
		{PoolScalingController, SandboxWarmPool, "spec.scalePolicy.maxIdleSeconds"},
		{PoolScalingController, SandboxWarmPool, "spec.sdkWarmDisabled"},
		{PoolScalingController, SandboxWarmPool, "status.sdkWarmCircuitBreaker"},
		{PoolScalingController, SandboxWarmPool, "status.sdkWarmCircuitBreaker.openedAt"},
		{WarmPoolController, SandboxWarmPool, "status.warmCount"},
		{WarmPoolController, SandboxWarmPool, "status.readyCount"},
		{WarmPoolController, Sandbox, "spec.runtimeImage"},
		{WarmPoolController, Sandbox, "status.phase"},
		{Gateway, SandboxClaim, "spec.sandboxRef"},
		{Gateway, SandboxClaim, "status.phase"},
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
		{WarmPoolController, SandboxTemplate, "spec", PoolScalingController},
		{PoolScalingController, SandboxTemplate, "status", WarmPoolController},
		{WarmPoolController, SandboxWarmPool, "spec.minWarm", PoolScalingController},
		{WarmPoolController, SandboxWarmPool, "status.sdkWarmCircuitBreaker.openedAt", PoolScalingController},
		{PoolScalingController, SandboxWarmPool, "status.warmCount", WarmPoolController},
		{PoolScalingController, Sandbox, "spec", WarmPoolController},
		{Gateway, Sandbox, "status.phase", WarmPoolController},
		{WarmPoolController, SandboxClaim, "status.phase", Gateway},
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
	err := Validate(PoolScalingController, SandboxTemplate, "metadata.labels")
	if err == nil {
		t.Errorf("metadata.* paths should be outside the §4.6.3 matrix")
	}
}

func TestValidateRejectsEmptyFieldPath(t *testing.T) {
	err := Validate(PoolScalingController, SandboxTemplate, "")
	if err == nil {
		t.Errorf("empty fieldPath should error")
	}
}

func TestOwnerSdkWarmCircuitBreakerOverridesDefault(t *testing.T) {
	// The carve-out: status.sdkWarmCircuitBreaker.* belongs to PSC
	// even though status.* by default is WarmPoolController.
	owner, ok := Owner(SandboxWarmPool, "status.sdkWarmCircuitBreaker.openedAt")
	if !ok || owner != PoolScalingController {
		t.Errorf("status.sdkWarmCircuitBreaker.openedAt: want PoolScalingController, got (%q, %v)", owner, ok)
	}
	owner, ok = Owner(SandboxWarmPool, "status.warmCount")
	if !ok || owner != WarmPoolController {
		t.Errorf("status.warmCount: want WarmPoolController, got (%q, %v)", owner, ok)
	}
}

func TestAllRulesEnumerateMatrix(t *testing.T) {
	rules := AllRules()
	if len(rules) < 12 {
		t.Errorf("AllRules() should enumerate the §4.6.3 matrix, got %d", len(rules))
	}
	// Every rule must validate against its declared owner.
	for _, r := range rules {
		if err := Validate(r.Controller, r.CRD, r.FieldPath); err != nil {
			t.Errorf("AllRules row %+v fails Validate: %v", r, err)
		}
	}
}
