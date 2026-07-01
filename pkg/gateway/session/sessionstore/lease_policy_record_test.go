// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §8.10 lines 1044-1049 — the persisted delegation lease record
// carries the lease-scoped policy reference alongside the resource
// slice. IsZero must treat a record carrying only policy fields as
// non-empty so the store does not drop it to NULL, otherwise the
// no-re-evaluation recovery guarantee loses its backing. F-8.10.5.
func TestDelegationLeaseIsZeroAccountsForPolicyRecord_spec_8_10_1044(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		l    *sessionstore.DelegationLease
		want bool
	}{
		{"nil", nil, true},
		{"all unset", &sessionstore.DelegationLease{}, true},
		{"resource axis set", &sessionstore.DelegationLease{MaxTokenBudget: 1}, false},
		{"delegation policy ref only", &sessionstore.DelegationLease{DelegationPolicyRef: "tight"}, false},
		{"max delegation policy only", &sessionstore.DelegationLease{MaxDelegationPolicy: "tight"}, false},
		{"content policy ref only", &sessionstore.DelegationLease{ContentPolicyRef: "scrub-pii"}, false},
		{"snapshotted pool ids only", &sessionstore.DelegationLease{SnapshottedPoolIDs: []string{"pool-a"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.l.IsZero(); got != tc.want {
				t.Errorf("IsZero() = %v, want %v", got, tc.want)
			}
		})
	}
}
