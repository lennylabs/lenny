// SPDX-License-Identifier: MIT

package pagegate

import (
	"strings"
	"testing"
)

// These cases pin the area rule for review comments. They cover
// specification tooling rather than a platform behavior, so they carry no
// specification annotation.

const reviewBody = "# Pool\n\n<!-- src: a numbered section -->\nText.\n"

func TestReviewCommentsOnACapabilityPageFollowItsArea(t *testing.T) {
	t.Parallel()
	area := "spec/capabilities/pools/"

	// The area has landed: no page under its folder is a placeholder, so
	// even a partial page refuses the comment.
	landed := Check(loadPages(t, map[string]string{
		area + "warm-pools.md": page("POOL", "capability", "area: pools\nstatus: partial\n", reviewBody),
		area + "claims.md":     page("CLAIM", "capability", "area: pools\n", "# Claims\n"),
	}))
	assertProblem(t, landed, 10, `"src" review comment stands on a page of the area pools, which has landed`)

	// The area is migrating: one page is still a placeholder, so a page
	// written in full may still carry the comment.
	migrating := Check(loadPages(t, map[string]string{
		area + "warm-pools.md": page("POOL", "capability", "area: pools\n", reviewBody),
		area + "claims.md":     page("CLAIM", "capability", "area: pools\nstatus: placeholder\n", "# Claims\n"),
	}))
	for _, p := range migrating {
		if p.Path == area+"warm-pools.md" {
			t.Errorf("a review comment was refused while its area migrates: %s", p)
		}
	}
}

func TestReviewCommentsOnAPageInNoAreaFollowItsOwnStatus(t *testing.T) {
	t.Parallel()
	const comp = "spec/components/gateway.md"
	for status, refused := range map[string]bool{"": true, "partial": false, "placeholder": false} {
		extra := ""
		if status != "" {
			extra = "status: " + status + "\n"
		}
		problems := Check(loadPages(t, map[string]string{comp: page("GW", "component", extra, reviewBody)}))
		got := false
		for _, p := range problems {
			if p.Path == comp && strings.Contains(p.Message, "review comment") {
				got = true
			}
		}
		if got != refused {
			t.Errorf("status %q: refused %v, want %v", status, got, refused)
		}
	}
}
