// SPDX-License-Identifier: MIT

package delegation

import (
	"crypto/rand"
	"encoding/hex"
)

// randomHex returns n random bytes hex-encoded (2n characters).
func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
