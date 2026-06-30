// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §11.2 lines 87-88 — billing events auto-populate
// experiment_id/variant_id from the session's experimentContext. The
// accessor must be nil-safe so an unenrolled session yields empty
// strings rather than a panic. F-11.2.13.
func TestExperimentContextEnrollment_spec_11_2_87(t *testing.T) {
	t.Run("nil context yields empty ids", func(t *testing.T) {
		var c *sessionstore.ExperimentContext
		exp, variant := c.Enrollment()
		if exp != "" || variant != "" {
			t.Fatalf("nil context: got (%q, %q), want empty", exp, variant)
		}
	})

	t.Run("unenrolled session yields empty ids", func(t *testing.T) {
		s := sessionstore.Session{ID: "sess_a"}
		exp, variant := s.ExperimentContext.Enrollment()
		if exp != "" || variant != "" {
			t.Fatalf("unenrolled: got (%q, %q), want empty", exp, variant)
		}
	})

	t.Run("enrolled session yields experiment and variant", func(t *testing.T) {
		s := sessionstore.Session{
			ID: "sess_b",
			ExperimentContext: &sessionstore.ExperimentContext{
				ExperimentID: "exp-checkout",
				VariantID:    "treatment",
				Inherited:    true,
			},
		}
		exp, variant := s.ExperimentContext.Enrollment()
		if exp != "exp-checkout" || variant != "treatment" {
			t.Fatalf("enrolled: got (%q, %q), want (exp-checkout, treatment)", exp, variant)
		}
	})
}
