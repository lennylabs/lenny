// SPDX-License-Identifier: MIT

package data_residency_validator_test

import (
	"strings"
	"testing"

	drv "github.com/lennylabs/lenny/pkg/admission/data_residency_validator"
)

// spec: §12.8 ("Data residency admission control") / §12.9 — the
// lenny-data-residency-validator webhook rejects a resource whose
// resolved dataResidencyRegion is not declared in storage.regions, and
// an environment-scoped resource whose region diverges from its
// tenant's region.

// regions builds a declared-region set from the given region names.
func regions(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func TestAdmitsResourceWithNoRegion(t *testing.T) {
	// A resource declaring no region and inheriting none carries no
	// residency constraint and is admitted unchanged.
	d := drv.Decide(drv.Request{
		Kind:            "SandboxClaim",
		DeclaredRegions: regions("eu-west-1"),
	})
	if !d.Allowed {
		t.Fatalf("unconstrained resource should be admitted: %+v", d)
	}
	if d.Code != 200 {
		t.Errorf("code = %d, want 200", d.Code)
	}
}

func TestAdmitsDeclaredRegion(t *testing.T) {
	d := drv.Decide(drv.Request{
		Kind:            "Tenant",
		Region:          "eu-west-1",
		DeclaredRegions: regions("eu-west-1", "us-east-1"),
	})
	if !d.Allowed {
		t.Fatalf("a declared region should be admitted: %+v", d)
	}
}

func TestRejectsUndeclaredRegion(t *testing.T) {
	d := drv.Decide(drv.Request{
		Kind:            "Tenant",
		Region:          "ap-south-1",
		DeclaredRegions: regions("eu-west-1", "us-east-1"),
	})
	if d.Allowed {
		t.Fatal("an undeclared region must be rejected (fail-closed)")
	}
	if d.Code != 403 {
		t.Errorf("code = %d, want 403", d.Code)
	}
	if !strings.Contains(d.Reason, drv.CodeRegionConstraintUnresolvable) {
		t.Errorf("reason %q does not carry the REGION_CONSTRAINT_UNRESOLVABLE code", d.Reason)
	}
	// The rejection message names the declared regions.
	if !strings.Contains(d.Reason, "eu-west-1") || !strings.Contains(d.Reason, "us-east-1") {
		t.Errorf("reason %q should list the declared regions", d.Reason)
	}
}

func TestRejectsUndeclaredRegionFailsClosedWithEmptyDeclaredSet(t *testing.T) {
	// A deployment with storage.regions unset must still reject a
	// resource that pins a region — the constraint is unresolvable.
	d := drv.Decide(drv.Request{
		Kind:            "Tenant",
		Region:          "eu-west-1",
		DeclaredRegions: nil,
	})
	if d.Allowed {
		t.Fatal("a region pin with no declared regions must be rejected (fail-closed)")
	}
	if !strings.Contains(d.Reason, "(none)") {
		t.Errorf("reason %q should report that no regions are declared", d.Reason)
	}
}

func TestEnvironmentInheritsTenantRegion(t *testing.T) {
	// An environment-scoped resource declaring no region of its own
	// inherits the tenant region; the inherited region is validated.
	d := drv.Decide(drv.Request{
		Kind:                "Environment",
		IsEnvironmentScoped: true,
		TenantRegion:        "eu-west-1",
		DeclaredRegions:     regions("eu-west-1"),
	})
	if !d.Allowed {
		t.Fatalf("an environment inheriting a declared tenant region should be admitted: %+v", d)
	}
}

func TestEnvironmentInheritsUndeclaredTenantRegionRejected(t *testing.T) {
	// The inherited region is still validated against storage.regions:
	// an environment inheriting an undeclared region is rejected.
	d := drv.Decide(drv.Request{
		Kind:                "Environment",
		IsEnvironmentScoped: true,
		TenantRegion:        "ap-south-1",
		DeclaredRegions:     regions("eu-west-1"),
	})
	if d.Allowed {
		t.Fatal("an environment inheriting an undeclared region must be rejected")
	}
	if !strings.Contains(d.Reason, drv.CodeRegionConstraintUnresolvable) {
		t.Errorf("reason %q does not carry the unresolvable code", d.Reason)
	}
}

func TestEnvironmentRestatingTenantRegionAdmitted(t *testing.T) {
	// §12.8 inheritance: an environment may restate its tenant's
	// region. Same value as the tenant is admitted.
	d := drv.Decide(drv.Request{
		Kind:                "Environment",
		IsEnvironmentScoped: true,
		Region:              "eu-west-1",
		TenantRegion:        "eu-west-1",
		DeclaredRegions:     regions("eu-west-1"),
	})
	if !d.Allowed {
		t.Fatalf("an environment restating its tenant region should be admitted: %+v", d)
	}
}

func TestEnvironmentDivergingFromTenantRegionRejected(t *testing.T) {
	// §12.8 inheritance: an environment must inherit or match the
	// tenant region; a divergent value is REGION_CONSTRAINT_VIOLATED.
	d := drv.Decide(drv.Request{
		Kind:                "Environment",
		IsEnvironmentScoped: true,
		Region:              "us-east-1",
		TenantRegion:        "eu-west-1",
		DeclaredRegions:     regions("eu-west-1", "us-east-1"),
	})
	if d.Allowed {
		t.Fatal("an environment region diverging from its tenant must be rejected")
	}
	if d.Code != 403 {
		t.Errorf("code = %d, want 403", d.Code)
	}
	// A divergence is reported as a constraint violation, not as an
	// unresolvable region — both regions are in fact declared.
	if !strings.Contains(d.Reason, drv.CodeRegionConstraintViolated) {
		t.Errorf("reason %q does not carry the REGION_CONSTRAINT_VIOLATED code", d.Reason)
	}
}

func TestRejectionNamesOffendingField(t *testing.T) {
	d := drv.Decide(drv.Request{
		Kind:            "SandboxClaim",
		Field:           "spec.dataResidencyRegion",
		Region:          "ap-south-1",
		DeclaredRegions: regions("eu-west-1"),
	})
	if d.Allowed {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(d.Reason, "spec.dataResidencyRegion") {
		t.Errorf("reason %q should name the offending field", d.Reason)
	}
}

func TestEffectiveRegionPrefersOwnValue(t *testing.T) {
	r := drv.Request{Region: "us-east-1", TenantRegion: "eu-west-1"}
	if got := r.EffectiveRegion(); got != "us-east-1" {
		t.Errorf("EffectiveRegion = %q, want the resource's own region", got)
	}
	inherit := drv.Request{TenantRegion: "eu-west-1"}
	if got := inherit.EffectiveRegion(); got != "eu-west-1" {
		t.Errorf("EffectiveRegion = %q, want the inherited tenant region", got)
	}
}
