// SPDX-License-Identifier: MIT

package llmproxy

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
)

type countingStamper struct{ n int }

func (c *countingStamper) Stamp(string, string) { c.n++ }

// TestStampingFlushGuards_spec_6_2 pins that only a proxy-mode lease naming
// a session stamps, and that the wrapped flush runs first in every case.
//
// spec: 6.2 (maxClientIdleSeconds clock behavior across states)
func TestStampingFlushGuards_spec_6_2(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lease     credential.Lease
		stamper   bool
		wantStamp int
	}{
		{"proxy_with_session", credential.Lease{DeliveryMode: credential.DeliveryProxy, SessionID: "s1"}, true, 1},
		{"direct_mode", credential.Lease{DeliveryMode: credential.DeliveryDirect, SessionID: "s1"}, true, 0},
		{"no_session", credential.Lease{DeliveryMode: credential.DeliveryProxy}, true, 0},
		{"no_stamper", credential.Lease{DeliveryMode: credential.DeliveryProxy, SessionID: "s1"}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &countingStamper{}
			h := &Handler{}
			if tc.stamper {
				h.Activity = st
			}
			flushed := 0
			h.stampingFlush(tc.lease, func() { flushed++ })()
			if flushed != 1 || st.n != tc.wantStamp {
				t.Fatalf("flushed %d stamped %d, want 1 and %d", flushed, st.n, tc.wantStamp)
			}
		})
	}
}
