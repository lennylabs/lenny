// SPDX-License-Identifier: MIT

package pgstore

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// spec: §11.2, §10.1.1
//
// absentIdentifiers keeps the caller's spelling, treats a differently cased
// spelling of a present row as present, and reports an identifier that is
// not a UUID as absent.
func TestAbsentIdentifiersComparesCanonicalUUIDs_spec_11_2(t *testing.T) {
	present := uuid.NewString()
	missing := uuid.NewString()
	upper := strings.ToUpper(present)
	got := absentIdentifiers([]string{upper, missing, "not-a-uuid", ""}, []string{present})
	want := []string{missing, "not-a-uuid", ""}
	if len(got) != len(want) {
		t.Fatalf("absentIdentifiers = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("absentIdentifiers = %q, want %q", got, want)
		}
	}
	if got := absentIdentifiers(nil, nil); len(got) != 0 {
		t.Fatalf("absentIdentifiers(nil) = %q, want none", got)
	}
}
