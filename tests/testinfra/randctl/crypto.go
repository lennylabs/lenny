// SPDX-License-Identifier: MIT

package randctl

import (
	cryptorand "crypto/rand"
	"encoding/binary"
)

// cryptoReader and cryptoUint64 isolate the crypto/rand import to one
// file so the test-facing API in rand.go stays focused on the Rand
// interface and the PCG-backed deterministic implementation.

func cryptoReader(b []byte) (int, error) { return cryptorand.Read(b) }

func cryptoUint64() uint64 {
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		panic("randctl: crypto/rand read failed: " + err.Error())
	}
	return binary.LittleEndian.Uint64(b[:])
}
