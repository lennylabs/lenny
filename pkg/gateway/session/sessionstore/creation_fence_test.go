// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// fenceOracle is the creation fence's rule list written out independently
// of CreationMarkers.Refusal: the code of the first matching rule, or "".
func fenceOracle(m sessionstore.CreationMarkers) string {
	switch {
	case m.UserFound && m.UserDeleted:
		return sessionstore.RefusalUserInvalidated
	case m.UserFound && m.UserDisabled && !m.IsDelegatedChild:
		return sessionstore.RefusalUserInvalidated
	case m.UserFound && m.ProcessingRestricted:
		return sessionstore.RefusalErasureInProgress
	case m.TenantFound && m.TenantSuspended:
		return sessionstore.RefusalTenantSuspended
	case m.TenantFound && !(tenantstore.Tenant{State: m.TenantState}).AcceptsNewWork():
		return sessionstore.RefusalTenantNotActive
	}
	return ""
}

// fenceDetails is the detail map each refusal code carries.
func fenceDetails(code, tenantID, userID string, m sessionstore.CreationMarkers) map[string]any {
	switch code {
	case sessionstore.RefusalErasureInProgress:
		return map[string]any{"userId": userID, "jobId": m.ErasureJobID}
	case sessionstore.RefusalTenantSuspended:
		return map[string]any{"tenantId": tenantID}
	case sessionstore.RefusalTenantNotActive:
		return map[string]any{"tenantId": tenantID, "state": m.TenantState}
	}
	return nil
}

// TestCreationRefusalCrossProduct_spec_12_8 walks the full cross-product of
// the marker fields, root and delegated-child inserts included, and asserts
// Refusal returns the code and details of the first matching rule in the
// order USER_INVALIDATED, ERASURE_IN_PROGRESS, TENANT_SUSPENDED,
// TENANT_NOT_ACTIVE.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 11.4 (User Invalidation)
func TestCreationRefusalCrossProduct_spec_12_8(t *testing.T) {
	states := []string{
		"", tenantstore.TenantStateActive, tenantstore.TenantStateDisabling,
		tenantstore.TenantStateDeleting, tenantstore.TenantStateDeleted,
	}
	const tenantID, userID = "acme", "alice"
	cases := 0
	for bits := 0; bits < 1<<7; bits++ {
		for _, st := range states {
			m := sessionstore.CreationMarkers{
				TenantFound:          bits&1 != 0,
				TenantSuspended:      bits&2 != 0,
				UserFound:            bits&4 != 0,
				UserDisabled:         bits&8 != 0,
				UserDeleted:          bits&16 != 0,
				ProcessingRestricted: bits&32 != 0,
				IsDelegatedChild:     bits&64 != 0,
				TenantState:          st,
				ErasureJobID:         "job-1",
			}
			cases++
			want := fenceOracle(m)
			got := m.Refusal(tenantID, userID)
			name := fmt.Sprintf("%+v", m)
			if want == "" {
				if got != nil {
					t.Errorf("%s: Refusal = %+v, want admit", name, got)
				}
				continue
			}
			if got == nil {
				t.Errorf("%s: Refusal = nil, want %s", name, want)
				continue
			}
			if got.Code != want {
				t.Errorf("%s: code = %s, want %s", name, got.Code, want)
			}
			if wd := fenceDetails(want, tenantID, userID, m); !reflect.DeepEqual(got.Details, wd) {
				t.Errorf("%s: details = %v, want %v", name, got.Details, wd)
			}
			if got.Message == "" {
				t.Errorf("%s: empty message", name)
			}
		}
	}
	if cases != 640 {
		t.Fatalf("walked %d cases, want the full 640-case cross-product", cases)
	}
}

// TestCreationRefusalSoftDisabledAdmitsOnlyDelegatedChild_spec_11_4 pins the
// one marker-specific rule: a soft-disabled owner is refused for a root
// session and admitted for a delegated child, and a hard-disabled owner is
// refused for both.
//
// spec: 11.4 (User Invalidation), 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestCreationRefusalSoftDisabledAdmitsOnlyDelegatedChild_spec_11_4(t *testing.T) {
	soft := sessionstore.CreationMarkers{UserFound: true, UserDisabled: true}
	if r := soft.Refusal("acme", "alice"); r == nil || r.Code != sessionstore.RefusalUserInvalidated {
		t.Fatalf("soft-disabled root = %+v, want USER_INVALIDATED", r)
	}
	soft.IsDelegatedChild = true
	if r := soft.Refusal("acme", "alice"); r != nil {
		t.Fatalf("soft-disabled delegated child = %+v, want admit", r)
	}
	hard := sessionstore.CreationMarkers{UserFound: true, UserDeleted: true, IsDelegatedChild: true}
	if r := hard.Refusal("acme", "alice"); r == nil || r.Code != sessionstore.RefusalUserInvalidated {
		t.Fatalf("hard-disabled delegated child = %+v, want USER_INVALIDATED", r)
	}
	restricted := sessionstore.CreationMarkers{UserFound: true, ProcessingRestricted: true, IsDelegatedChild: true, ErasureJobID: "j"}
	if r := restricted.Refusal("acme", "alice"); r == nil || r.Code != sessionstore.RefusalErasureInProgress {
		t.Fatalf("restricted delegated child = %+v, want ERASURE_IN_PROGRESS", r)
	}
}

// TestCreationRefusalTenantStateMatchesAcceptsNewWork_spec_12_8 asserts, for
// every tenant state constant, the empty value, and an unknown value, that
// Refusal admits exactly the states tenantstore.Tenant.AcceptsNewWork
// admits.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (Tenant deletion lifecycle)
func TestCreationRefusalTenantStateMatchesAcceptsNewWork_spec_12_8(t *testing.T) {
	states := append([]string{"", "bogus"}, tenantstore.AllTenantStates()...)
	for _, st := range states {
		m := sessionstore.CreationMarkers{TenantFound: true, TenantState: st}
		admits := m.Refusal("acme", "") == nil
		if want := (tenantstore.Tenant{State: st}).AcceptsNewWork(); admits != want {
			t.Errorf("state %q: fence admits=%v, AcceptsNewWork=%v", st, admits, want)
		}
	}
}

// TestCreationRefusalMissingRowsCarryNoMarker_spec_12_8 asserts that marker
// fields read from a missing row never refuse: a session with no owner, an
// owner with no registry row, and a missing tenant row are admitted.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestCreationRefusalMissingRowsCarryNoMarker_spec_12_8(t *testing.T) {
	m := sessionstore.CreationMarkers{
		TenantSuspended: true, TenantState: tenantstore.TenantStateDeleting,
		UserDisabled: true, UserDeleted: true, ProcessingRestricted: true,
	}
	if r := m.Refusal("acme", "alice"); r != nil {
		t.Fatalf("missing rows = %+v, want admit", r)
	}
}

// TestCreationRefusalIsTypedError_spec_12_8 asserts the refusal satisfies
// error and is recoverable with errors.As through wrapping, which every
// create site's refusal mapping relies on, and that the reservation-ended
// refusal names the owner without a job.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestCreationRefusalIsTypedError_spec_12_8(t *testing.T) {
	err := fmt.Errorf("wrap: %w", sessionstore.ReservationEndedRefusal("alice"))
	var ref *sessionstore.CreationRefusal
	if !errors.As(err, &ref) {
		t.Fatalf("errors.As failed on %v", err)
	}
	if ref.Code != sessionstore.RefusalErasureInProgress {
		t.Fatalf("code = %s", ref.Code)
	}
	if !reflect.DeepEqual(ref.Details, map[string]any{"userId": "alice"}) {
		t.Fatalf("details = %v, want userId only", ref.Details)
	}
	if ref.Error() == "" {
		t.Fatal("empty Error()")
	}
}
