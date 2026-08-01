// SPDX-License-Identifier: MIT

package skipreason_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/skipreason"
)

// The skip reason categories are owned by TESTING.md §17.9. These cases
// carry no spec annotation: the harness attributes an annotated failure
// to a numbered section under spec/, and this package implements a test
// convention rather than a platform behavior.

func TestOpenedWithCategoryAcceptsEveryCategoryAndRejectsFreeText(t *testing.T) {
	t.Parallel()
	if len(skipreason.Categories) == 0 {
		t.Fatalf("the category list is empty, so every skip reason would be rejected")
	}
	for _, category := range skipreason.Categories {
		if !strings.HasSuffix(category, ":") {
			t.Errorf("the category %q does not end in a colon, so it does not separate the label from the reason",
				category)
		}
		if !skipreason.OpenedWithCategory(category + " the reason continues here") {
			t.Errorf("a reason opening with %q was rejected", category)
		}
	}
	for _, reason := range []string{
		"docker is not running",
		"",
		"the reason states not implemented: later in the line",
		"NOT IMPLEMENTED: shouted",
	} {
		if skipreason.OpenedWithCategory(reason) {
			t.Errorf("the reason %q was accepted, and it opens with no category", reason)
		}
	}
}
