// SPDX-License-Identifier: MIT

package translator

import "testing"

// TestResponseIDCodec_spec_15 pins the response id format: the session id, a
// ".", and the 1-based ordinal, split on the last "." so a session id that
// itself carries a "." still parses, and rejected before any store read when
// the separator, the prefix, or a decimal ordinal of at least 1 is missing.
// The prefix is not validated as a UUID; the tenant-scoped store decides.
//
// spec: §15 (Open Responses conversation sessions)
func TestResponseIDCodec_spec_15(t *testing.T) {
	const uuid = "0190a1b2-c3d4-8e5f-9a0b-1c2d3e4f5a6b"
	for _, tc := range []struct {
		id      string
		session string
		n       int
		ok      bool
	}{
		{id: uuid + ".1", session: uuid, n: 1, ok: true},
		{id: uuid + ".42", session: uuid, n: 42, ok: true},
		{id: "S.1.2", session: "S.1", n: 2, ok: true},
		{id: "resp_1.1", session: "resp_1", n: 1, ok: true},
		{id: uuid},
		{id: ".1"},
		{id: "S."},
		{id: "S.0"},
		{id: "S.-1"},
		{id: "S.+1"},
		{id: "S.x"},
		{id: "S.1x"},
		{id: "S.99999999999999999999999"},
		{id: ""},
	} {
		t.Run(tc.id, func(t *testing.T) {
			session, n, ok := parseResponseID(tc.id)
			if ok != tc.ok || session != tc.session || n != tc.n {
				t.Fatalf("parseResponseID(%q) = (%q, %d, %v), want (%q, %d, %v)",
					tc.id, session, n, ok, tc.session, tc.n, tc.ok)
			}
			if ok {
				if got := formatResponseID(session, n); got != tc.id {
					t.Errorf("formatResponseID(%q, %d) = %q, want %q", session, n, got, tc.id)
				}
			}
		})
	}
}
