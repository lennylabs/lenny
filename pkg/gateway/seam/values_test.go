// SPDX-License-Identifier: MIT

package seam

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The package declares interfaces and values; these tests pin the behavior
// of the error values a caller branches on.

// TestSealedErrorMatchesErrSessionSealed checks that a partial delivery is
// recognized as a sealed session through errors.Is and carries its counts
// through errors.As, also when an implementation wraps it.
// spec: GWSEAM-SendMessages
func TestSealedErrorMatchesErrSessionSealed(t *testing.T) {
	sealed := &SealedError{Delivered: 2, Response: Response{Parts: []MessagePart{{Type: "text", Text: "ok"}}}}
	wrapped := fmt.Errorf("deliver to session s1: %w", sealed)
	if !errors.Is(wrapped, ErrSessionSealed) {
		t.Fatal("a wrapped SealedError does not match ErrSessionSealed")
	}
	var got *SealedError
	if !errors.As(wrapped, &got) || got.Delivered != 2 || len(got.Response.Parts) != 1 {
		t.Fatalf("errors.As did not recover the delivered count and response: %+v", got)
	}
	if !strings.Contains(sealed.Error(), ErrSessionSealed.Error()) {
		t.Errorf("SealedError message %q does not carry the sealed message", sealed.Error())
	}
	if errors.Is(wrapped, ErrNotBound) {
		t.Error("a SealedError matches ErrNotBound")
	}
}

// TestCapRefusalCarriesItsMessageAndDetails checks that a resume refusal
// is recovered with errors.As and names its message.
// spec: GWSEAM-AdmitResume
func TestCapRefusalCarriesItsMessageAndDetails(t *testing.T) {
	refusal := &CapRefusal{Message: "concurrent session limit reached", Details: map[string]any{"limit": 3}}
	var got *CapRefusal
	if !errors.As(fmt.Errorf("admit resume: %w", refusal), &got) {
		t.Fatal("errors.As did not recover the CapRefusal")
	}
	if got.Details["limit"] != 3 {
		t.Errorf("details = %v", got.Details)
	}
	if !strings.Contains(got.Error(), "concurrent session limit reached") {
		t.Errorf("message %q does not name the refusal", got.Error())
	}
}

// TestSentinelErrorsAreDistinct checks that no two sentinels match each
// other, so a caller's branch on one never catches another.
// spec: GWSEAM-AuthorizeSession, GWSEAM-ExportPaths, GWSEAM-MaterializeChild, GWSEAM-CheckChildCredentials
func TestSentinelErrorsAreDistinct(t *testing.T) {
	all := []error{
		ErrSessionNotFound, ErrUnauthenticated, ErrForbidden, ErrNoLiveBinding,
		ErrSessionSealed, ErrNotBound, ErrHeldDeliveryPreempted,
		ErrChildCredentialUnavailable, ErrChildUserCredentialNotFound,
		ErrChildCredentialAssignment, ErrPoolWarming,
	}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("%v matches %v", a, b)
			}
		}
	}
}
