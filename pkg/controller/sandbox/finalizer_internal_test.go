// SPDX-License-Identifier: MIT

package sandbox

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

// spec: §4.6.1 "Sandbox finalizers" — the controller removes the
// session-cleanup finalizer only after confirming no active
// SandboxClaim references the pod; released/failed claims (§4.6.3
// terminal) do not block removal.
func TestActiveClaimReferences(t *testing.T) {
	claim := func(ref, phase string) lennyv1.SandboxClaim {
		return lennyv1.SandboxClaim{
			Spec:   lennyv1.SandboxClaimSpec{SandboxRef: ref},
			Status: lennyv1.SandboxClaimStatus{Phase: phase},
		}
	}
	cases := []struct {
		name    string
		claims  []lennyv1.SandboxClaim
		sandbox string
		want    bool
	}{
		{"no claims", nil, "sb-a", false},
		{"bound claim references", []lennyv1.SandboxClaim{claim("sb-a", "bound")}, "sb-a", true},
		{"active claim references", []lennyv1.SandboxClaim{claim("sb-a", "active")}, "sb-a", true},
		{"empty phase counts active", []lennyv1.SandboxClaim{claim("sb-a", "")}, "sb-a", true},
		{"released is terminal", []lennyv1.SandboxClaim{claim("sb-a", "released")}, "sb-a", false},
		{"failed is terminal", []lennyv1.SandboxClaim{claim("sb-a", "failed")}, "sb-a", false},
		{"claim for a different sandbox", []lennyv1.SandboxClaim{claim("sb-b", "active")}, "sb-a", false},
		{
			"mixed: one terminal one active",
			[]lennyv1.SandboxClaim{claim("sb-a", "released"), claim("sb-a", "active")},
			"sb-a", true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := activeClaimReferences(tc.claims, tc.sandbox); got != tc.want {
				t.Errorf("activeClaimReferences = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasAndRemoveFinalizer(t *testing.T) {
	other := "example.com/other"
	with := []string{other, lennyv1.FinalizerSessionCleanup}
	if !hasFinalizer(with) {
		t.Errorf("hasFinalizer(%v) = false, want true", with)
	}
	if hasFinalizer([]string{other}) {
		t.Errorf("hasFinalizer without session-cleanup = true, want false")
	}
	got := removeFinalizer(with)
	if len(got) != 1 || got[0] != other {
		t.Errorf("removeFinalizer(%v) = %v, want [%q]", with, got, other)
	}
	if hasFinalizer(got) {
		t.Errorf("removeFinalizer left the session-cleanup finalizer in place")
	}
}

// spec: §6.2 lines 305-313 — a freshly-created Sandbox (unset phase,
// treated as warming) has no coarse operational value, so the label is
// omitted; once idle, the coarse value is "idle". An attached pod maps to
// the coarse "active" value rather than carrying the raw §6.2 phase.
func TestPodStateLabel(t *testing.T) {
	sb := &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-a"}}
	if got, ok := podStateLabel(sb); ok {
		t.Errorf("podStateLabel(empty phase) = (%q, true), want no coarse value (warming is pre-ready)", got)
	}
	sb.Status.Phase = "idle"
	if got, ok := podStateLabel(sb); !ok || got != "idle" {
		t.Errorf("podStateLabel(idle) = (%q, %v), want (idle, true)", got, ok)
	}
	sb.Status.Phase = "attached"
	if got, ok := podStateLabel(sb); !ok || got != "active" {
		t.Errorf("podStateLabel(attached) = (%q, %v), want (active, true) per coarse mapping", got, ok)
	}
}
