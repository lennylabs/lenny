// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// TestSeamForwardKindsNeverForwardASuspendedSession guards ForwardRequest's
// rebuilt session row, which carries no pod assignment because the seam's
// session record has none. The forwarding rules read the pod assignment only
// for a suspended session, so every kind the seam maps must forward in no
// suspended state. A kind that does, such as a held-pod resume or a
// terminate, needs the pod assignment to cross the seam, or ForwardRequest
// to re-read the row, before it is added to forwardKinds.
//
// spec: GWSEAM-ForwardRequest; §29.3 (Interactive message send) Forwarding
// rule 1.
func TestSeamForwardKindsNeverForwardASuspendedSession(t *testing.T) {
	for seamKind, kind := range forwardKinds {
		if forwardStates[kind][session.StateSuspended] {
			t.Errorf("seam forward kind %q maps to a forwarding row that forwards a suspended session", seamKind)
		}
	}
}
