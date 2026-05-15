// SPDX-License-Identifier: MIT

package mcptools

import (
	"crypto/rand"
	"encoding/hex"
)

// randomSessionID returns a fresh `sess_`-prefixed session id.
func randomSessionID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return "sess_" + hex.EncodeToString(buf[:])
}
