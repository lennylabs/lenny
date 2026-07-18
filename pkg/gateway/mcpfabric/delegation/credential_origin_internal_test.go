// SPDX-License-Identifier: MIT

package delegation

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: 8.3 lines 472, 488, 474 — credentialOriginID resolves the origin
// credential pool stamped on a delegated child. A `credentialPropagation:
// inherit` hop forwards the parent's origin so contiguous inherit hops share
// one origin pool, traced back to the last `independent` break or the root.
// Every other mode establishes a new origin equal to the child itself.
func TestCredentialOriginID(t *testing.T) {
	const (
		childID  = "11111111-1111-1111-1111-111111111111"
		parentID = "22222222-2222-2222-2222-222222222222"
		originID = "33333333-3333-3333-3333-333333333333"
	)

	// A parent that already carries an origin (a mid-tree inherit hop):
	// its origin is forwarded so the whole contiguous inherit chain shares
	// one origin pool.
	inheritingParent := sessionstore.Session{ID: parentID, CredentialOriginSessionID: originID}
	// A parent that is itself an origin (a root, top-level, or independent
	// hop, whose read-path origin collapses to empty): the parent id becomes
	// the child's origin.
	rootParent := sessionstore.Session{ID: parentID}

	tests := []struct {
		name   string
		mode   lease.CredentialPropagation
		parent sessionstore.Session
		want   string
	}{
		{
			name:   "inherit forwards parent's existing origin",
			mode:   lease.CredentialPropagationInherit,
			parent: inheritingParent,
			want:   originID,
		},
		{
			name:   "inherit from origin parent adopts the parent id",
			mode:   lease.CredentialPropagationInherit,
			parent: rootParent,
			want:   parentID,
		},
		{
			name:   "independent establishes a new origin at the child",
			mode:   lease.CredentialPropagationIndependent,
			parent: inheritingParent,
			want:   childID,
		},
		{
			name:   "deny establishes a new origin at the child",
			mode:   lease.CredentialPropagationDeny,
			parent: inheritingParent,
			want:   childID,
		},
		{
			// spec: 8.3 — an omitted mode defaults to independent, so an
			// omitted-mode child must NOT inherit the parent's origin. This
			// guards the origin-pool break for the common omitted case.
			name:   "omitted mode defaults to a new origin at the child",
			mode:   "",
			parent: inheritingParent,
			want:   childID,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := credentialOriginID(tc.mode, tc.parent, childID)
			if got != tc.want {
				t.Errorf("credentialOriginID(%q) = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}
