// SPDX-License-Identifier: MIT

package billingretention

import (
	"context"
	"errors"
	"testing"
	"time"
)

// spec: §11.2.1 line 151 — the compliance-aware floors: hipaa 2190,
// soc2 365, fedramp 365; an empty/unregulated profile has no floor.
// F-11.2.15.
func TestComplianceFloorDays_spec_11_2_151(t *testing.T) {
	cases := map[string]int{
		"hipaa":   2190,
		"soc2":    365,
		"fedramp": 365,
		"":        0,
		"none":    0,
		"unknown": 0,
	}
	for profile, want := range cases {
		if got := ComplianceFloorDays(profile); got != want {
			t.Errorf("ComplianceFloorDays(%q) = %d, want %d", profile, got, want)
		}
	}
}

// spec: §11.2.1 line 151 — retentionDays below an active regulated
// profile's floor is rejected with the CONFIG_INVALID message naming the
// binding profile; at or above the floor is accepted. F-11.2.15.
func TestValidateRetentionDays_spec_11_2_151(t *testing.T) {
	t.Run("unregulated deployment accepts any positive value", func(t *testing.T) {
		if err := ValidateRetentionDays(30, nil); err != nil {
			t.Errorf("unregulated 30d: unexpected error %v", err)
		}
		if err := ValidateRetentionDays(395, []string{"", "none"}); err != nil {
			t.Errorf("unregulated profiles: unexpected error %v", err)
		}
	})

	t.Run("below floor is rejected naming the profile", func(t *testing.T) {
		err := ValidateRetentionDays(365, []string{"hipaa"})
		var floorErr *RetentionFloorError
		if !errors.As(err, &floorErr) {
			t.Fatalf("got %v, want *RetentionFloorError", err)
		}
		if floorErr.Profile != "hipaa" || floorErr.FloorDays != 2190 {
			t.Errorf("floor error = %+v, want hipaa/2190", floorErr)
		}
		if want := "CONFIG_INVALID: billing.retentionDays below compliance floor for complianceProfile 'hipaa'"; !contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err.Error(), want)
		}
	})

	t.Run("at the floor is accepted", func(t *testing.T) {
		if err := ValidateRetentionDays(2190, []string{"hipaa"}); err != nil {
			t.Errorf("at floor: unexpected error %v", err)
		}
		if err := ValidateRetentionDays(365, []string{"soc2"}); err != nil {
			t.Errorf("soc2 at floor: unexpected error %v", err)
		}
	})

	t.Run("binding profile is the highest floor among active profiles", func(t *testing.T) {
		err := ValidateRetentionDays(400, []string{"soc2", "hipaa", "fedramp"})
		var floorErr *RetentionFloorError
		if !errors.As(err, &floorErr) {
			t.Fatalf("got %v, want *RetentionFloorError", err)
		}
		if floorErr.Profile != "hipaa" {
			t.Errorf("binding profile = %q, want hipaa (the highest floor)", floorErr.Profile)
		}
	})

	t.Run("non-positive retentionDays falls back to the default before comparison", func(t *testing.T) {
		// 0 → DefaultRetentionDays (395) ≥ soc2 floor (365): accepted.
		if err := ValidateRetentionDays(0, []string{"soc2"}); err != nil {
			t.Errorf("default vs soc2: unexpected error %v", err)
		}
		// 0 → 395 < hipaa floor (2190): rejected (cannot bypass via unset).
		if err := ValidateRetentionDays(0, []string{"hipaa"}); err == nil {
			t.Error("unset retentionDays under hipaa must still be rejected")
		}
	})
}

func TestClampSweepInterval(t *testing.T) {
	if got := ClampSweepInterval(0); got != DefaultSweepInterval {
		t.Errorf("ClampSweepInterval(0) = %v, want %v", got, DefaultSweepInterval)
	}
	if got := ClampSweepInterval(time.Second); got != MinSweepInterval {
		t.Errorf("ClampSweepInterval(1s) = %v, want %v", got, MinSweepInterval)
	}
	if got := ClampSweepInterval(2 * time.Hour); got != 2*time.Hour {
		t.Errorf("ClampSweepInterval(2h) = %v, want 2h", got)
	}
}

// fakeDeleter records the cutoff per tenant and returns canned counts.
type fakeDeleter struct {
	counts map[string]int
	cutoff map[string]time.Time
	err    map[string]error
}

func (f *fakeDeleter) DeleteOlderThan(_ context.Context, tenantID string, cutoff time.Time) (int, error) {
	if f.cutoff == nil {
		f.cutoff = map[string]time.Time{}
	}
	f.cutoff[tenantID] = cutoff
	if e := f.err[tenantID]; e != nil {
		return 0, e
	}
	return f.counts[tenantID], nil
}

// spec: §11.2.1 line 151 — Tick computes cutoff = now − retentionDays
// and prunes every tenant, summing the counts. F-11.2.15.
func TestPrunerTickPrunesEveryTenant_spec_11_2_151(t *testing.T) {
	del := &fakeDeleter{counts: map[string]int{"acme": 3, "globex": 5}}
	p := New(del, StaticTenants{"acme", "globex"}, Options{RetentionDays: 395})
	now := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)

	pruned, err := p.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if pruned != 8 {
		t.Errorf("pruned = %d, want 8", pruned)
	}
	wantCutoff := now.AddDate(0, 0, -395)
	if got := del.cutoff["acme"]; !got.Equal(wantCutoff) {
		t.Errorf("acme cutoff = %v, want %v", got, wantCutoff)
	}
}

// A per-tenant delete failure does not abort the remaining tenants; the
// first error is returned with the partial count. F-11.2.15.
func TestPrunerTickContinuesPastTenantError_spec_11_2_151(t *testing.T) {
	boom := errors.New("delete failed")
	del := &fakeDeleter{
		counts: map[string]int{"acme": 2, "globex": 4},
		err:    map[string]error{"acme": boom},
	}
	p := New(del, StaticTenants{"acme", "globex"}, Options{RetentionDays: 10})

	pruned, err := p.Tick(context.Background(), time.Now())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if pruned != 4 {
		t.Errorf("pruned = %d, want 4 (globex survives acme's failure)", pruned)
	}
}

func TestNewDefaultsRetentionDays(t *testing.T) {
	p := New(&fakeDeleter{}, StaticTenants{}, Options{})
	if p.RetentionDays() != DefaultRetentionDays {
		t.Errorf("RetentionDays() = %d, want %d", p.RetentionDays(), DefaultRetentionDays)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
