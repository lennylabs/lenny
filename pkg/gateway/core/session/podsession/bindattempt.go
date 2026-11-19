// SPDX-License-Identifier: MIT

package podsession

import (
	"crypto/rand"
	"encoding/hex"
)

// newBindAttempt mints the opaque per-attempt token §4.7.1 fences the slot
// registry entry with. It is called once per bind attempt, before that
// attempt's first RPC, and the value is carried on the requests §4.7.1's
// carriage table marks it carried on and named again on the compensating
// Shutdown.
//
// The token is minted by the caller rather than reported by the adapter,
// because a value the adapter mints reaches the gateway only on a response:
// an attempt that fails inside its first entry-creating RPC would never hold
// it, and that attempt is exactly the one whose compensation has to be
// fenced.
//
// crypto/rand.Read cannot return an error on the Go version this module pins
// (go.mod declares go 1.25.0); it panics if the system source fails. There is
// deliberately no error branch here, and a later reader should not add one.
//
// spec: §4.7.1 (role and gateway RPC contract)
func newBindAttempt() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
