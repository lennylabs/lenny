// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
)

// TestValidateNonceOnlyAcknowledgment_spec_4_7 covers the §4.7 / §5.3
// nonce-only acknowledgment gate: a pool bound to a runtime whose registry
// definition keeps SO_PEERCRED disabled (requireSoPeercred: false) is
// admitted only when the pool carries acknowledgeNonceOnlyAuth: true. The
// gate is unconditional and a no-op for runtimes that keep SO_PEERCRED
// enforcement, so it must not reject the common case.
//
// spec: 4.7 (acknowledgment gate), 5.3 (acknowledgeNonceOnlyAuth)
func TestValidateNonceOnlyAcknowledgment_spec_4_7(t *testing.T) {
	cases := []struct {
		name              string
		requireSoPeercred bool
		ack               bool
		ok                bool
	}{
		{
			name:              "nonce-only runtime without ack is rejected",
			requireSoPeercred: false,
			ack:               false,
			ok:                false,
		},
		{
			name:              "nonce-only runtime with ack is admitted",
			requireSoPeercred: false,
			ack:               true,
			ok:                true,
		},
		{
			// The common case: a runtime that keeps SO_PEERCRED enforcement is
			// admitted regardless of the acknowledgment, so the gate never
			// fires for the vast majority of pools.
			name:              "SO_PEERCRED-enforcing runtime without ack is admitted",
			requireSoPeercred: true,
			ack:               false,
			ok:                true,
		},
		{
			// A set acknowledgment on an enforcing runtime is harmless.
			name:              "SO_PEERCRED-enforcing runtime with ack is admitted",
			requireSoPeercred: true,
			ack:               true,
			ok:                true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := poolstore.Pool{Name: "p", AcknowledgeNonceOnlyAuth: tc.ack}
			err := poolstore.ValidateNonceOnlyAcknowledgment(tc.requireSoPeercred, p)
			if tc.ok && err != nil {
				t.Fatalf("want nil, got %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("want rejection, got nil")
				}
				// The rejection must name the acknowledgment flag and §4.7 so
				// the operator can correct the pool.
				if !strings.Contains(err.Error(), "acknowledgeNonceOnlyAuth") {
					t.Errorf("rejection does not mention acknowledgeNonceOnlyAuth: %q", err.Error())
				}
				if !strings.Contains(err.Error(), "§4.7") {
					t.Errorf("rejection does not cite §4.7: %q", err.Error())
				}
			}
		})
	}
}
