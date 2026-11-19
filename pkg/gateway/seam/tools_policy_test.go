// SPDX-License-Identifier: MIT

package seam

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestCallerRefusalMatchesErrCallerRefused checks that a refusal of the
// caller check is recognized through errors.Is and carries its reason
// through errors.As, also when wrapped.
// spec: GWSEAM-CheckCreatingCaller
func TestCallerRefusalMatchesErrCallerRefused(t *testing.T) {
	err := fmt.Errorf("delegate: %w", &CallerRefusal{Reason: "subject_token_revoked"})
	var got *CallerRefusal
	if !errors.Is(err, ErrCallerRefused) || !errors.As(err, &got) || got.Reason != "subject_token_revoked" {
		t.Fatalf("errors.Is/As did not recover the refusal: %v", err)
	}
	if !strings.Contains(got.Error(), "subject_token_revoked") {
		t.Errorf("message %q does not name the reason", got.Error())
	}
}

// TestVCSTokenErrorReportsItsMessage checks that a VCS token error is
// recovered with its failure and host details and reports the core's
// message unchanged.
// spec: GWSEAM-IssueVCSToken
func TestVCSTokenErrorReportsItsMessage(t *testing.T) {
	err := fmt.Errorf("issue: %w", &VCSTokenError{Failure: VCSHostAmbiguous, Host: "github.com", MatchingPools: []string{"a", "b"}, Message: "two pools match"})
	var got *VCSTokenError
	if !errors.As(err, &got) || got.Failure != VCSHostAmbiguous || len(got.MatchingPools) != 2 || got.Error() != "two pools match" {
		t.Fatalf("errors.As did not recover the VCS token error: %#v", got)
	}
}

// TestPlatformToolsSentinelsAreDistinct checks that the sentinels of the
// platform tools seam's policy operations match no other sentinel.
// spec: GWSEAM-CheckCreatingCaller, GWSEAM-ResolveExportScan, GWSEAM-MaterializeChild
func TestPlatformToolsSentinelsAreDistinct(t *testing.T) {
	all := []error{
		ErrCallerRefused, ErrExportScanUnavailable, ErrTokenServiceUnavailable,
		ErrChildCredentialUnavailable, ErrChildUserCredentialNotFound, ErrChildCredentialAssignment,
		ErrSessionNotFound, ErrForbidden,
	}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("%v matches %v", a, b)
			}
		}
	}
}
