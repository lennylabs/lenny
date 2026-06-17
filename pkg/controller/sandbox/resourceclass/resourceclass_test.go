// SPDX-License-Identifier: MIT

package resourceclass_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/resourceclass"
)

// TestDefaultRegistryHasSpec52Classes asserts the §5.2 line 369
// small/medium/large classes are present with non-zero CPU and memory.
func TestDefaultRegistryHasSpec52Classes_spec_5_2_369(t *testing.T) {
	reg := resourceclass.DefaultRegistry()
	for _, class := range []string{"small", "medium", "large"} {
		req, ok := reg.Resolve(class)
		if !ok {
			t.Fatalf("class %q missing from default registry", class)
		}
		if req.Requests.Cpu().IsZero() || req.Requests.Memory().IsZero() {
			t.Errorf("class %q has a zero request: %+v", class, req.Requests)
		}
		if req.Limits.Cpu().IsZero() || req.Limits.Memory().IsZero() {
			t.Errorf("class %q has a zero limit: %+v", class, req.Limits)
		}
	}
}

// TestDefaultRegistryAccountsForTmpfs verifies every default class's memory
// limit clears the §6.4 line 413 tmpfs reservation, so the registry's own
// Validate accepts it.
func TestDefaultRegistryAccountsForTmpfs_spec_6_4_413(t *testing.T) {
	if err := resourceclass.DefaultRegistry().Validate(); err != nil {
		t.Fatalf("default registry should account for tmpfs: %v", err)
	}
	floor := resource.MustParse("576Mi") // 256+256+64
	for _, class := range []string{"small", "medium", "large"} {
		req, _ := resourceclass.DefaultRegistry().Resolve(class)
		lim := req.Limits[corev1.ResourceMemory]
		if lim.Cmp(floor) <= 0 {
			t.Errorf("class %q memory limit %s does not exceed the %s tmpfs reservation", class, lim.String(), floor.String())
		}
	}
}

// TestResolveDeepCopies ensures two resolutions of the same class do not
// share the underlying ResourceList map, so mutating one pod's resources
// cannot bleed into another.
func TestResolveDeepCopies(t *testing.T) {
	reg := resourceclass.DefaultRegistry()
	a, _ := reg.Resolve("medium")
	b, _ := reg.Resolve("medium")
	a.Limits[corev1.ResourceCPU] = resource.MustParse("99")
	if b.Limits.Cpu().Value() == 99 {
		t.Fatal("Resolve returned an aliased ResourceList; mutation bled across copies")
	}
	// The registry's stored value must also be untouched.
	c, _ := reg.Resolve("medium")
	if c.Limits.Cpu().Value() == 99 {
		t.Fatal("Resolve aliased the registry's stored value")
	}
}

func TestResolveUnknownClass(t *testing.T) {
	if _, ok := resourceclass.DefaultRegistry().Resolve("xlarge"); ok {
		t.Fatal("Resolve returned ok for an unregistered class")
	}
}

// TestValidateRejectsTmpfsUndersizedLimit asserts an override whose memory
// limit does not clear the tmpfs reservation is rejected (§6.4 line 413).
func TestValidateRejectsTmpfsUndersizedLimit_spec_6_4_413(t *testing.T) {
	reg := resourceclass.DefaultRegistry()
	name, req, err := resourceclass.ParseOverride("tiny=requests.cpu:50m,requests.memory:128Mi,limits.cpu:200m,limits.memory:256Mi")
	if err != nil {
		t.Fatalf("ParseOverride: %v", err)
	}
	reg.Set(name, req)
	if err := reg.Validate(); err == nil {
		t.Fatal("Validate accepted a class whose memory limit (256Mi) is below the 576Mi tmpfs reservation")
	}
}

// TestValidateRejectsMissingMemoryLimit covers the unset-limit branch.
func TestValidateRejectsMissingMemoryLimit_spec_6_4_413(t *testing.T) {
	reg := resourceclass.Registry{
		"nolimit": corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
		},
	}
	if err := reg.Validate(); err == nil {
		t.Fatal("Validate accepted a class with no memory limit")
	}
}

func TestParseOverrideRoundTrips(t *testing.T) {
	name, req, err := resourceclass.ParseOverride("big=requests.cpu:2,requests.memory:4Gi,limits.cpu:8,limits.memory:8Gi")
	if err != nil {
		t.Fatalf("ParseOverride: %v", err)
	}
	if name != "big" {
		t.Errorf("name = %q, want big", name)
	}
	if got := req.Requests.Cpu().String(); got != "2" {
		t.Errorf("requests.cpu = %q, want 2", got)
	}
	if got := req.Limits.Memory().String(); got != "8Gi" {
		t.Errorf("limits.memory = %q, want 8Gi", got)
	}
}

func TestParseOverrideRejectsMalformed(t *testing.T) {
	cases := []string{
		"",                 // empty
		"noequals",         // no name=
		"x=requests.cpu:1", // missing fields
		"x=bogus.field:1,requests.cpu:1,requests.memory:1Gi,limits.cpu:1,limits.memory:1Gi",  // unknown field
		"x=requests.cpu:notaqty,requests.memory:1Gi,limits.cpu:1,limits.memory:1Gi",          // bad quantity
		"x=requests.cpu:1,requests.cpu:2,requests.memory:1Gi,limits.cpu:1,limits.memory:1Gi", // duplicate
	}
	for _, c := range cases {
		if _, _, err := resourceclass.ParseOverride(c); err == nil {
			t.Errorf("ParseOverride(%q) accepted a malformed override", c)
		}
	}
}
