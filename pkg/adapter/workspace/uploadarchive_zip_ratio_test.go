// SPDX-License-Identifier: MIT

package workspace_test

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/upload"
)

// spec: §7.4 line 451; §13.4 line 659 — the 100:1 decompression-ratio
// bound is enforced for zip archives via the compressed totals walked
// from the central directory (not a streaming counter). A highly
// compressible zip entry whose uncompressed:compressed ratio exceeds
// 100:1 aborts extraction with details.reason = max_decompression_ratio.
// F-13.4.11.
func TestExtractionZipEnforcesRatioBound(t *testing.T) {
	// 2 MiB of zeros deflates to a few hundred bytes — a ratio in the
	// thousands, far past the 100:1 ceiling, while staying under the
	// 256 MiB decompressed-size cap so the ratio rule fires first.
	zip := buildZip(t, map[string]string{
		"bomb.bin": string(bytes.Repeat([]byte{0}, 2<<20)),
	})
	_, err := materializeArchive(t, "zip", "", 0, zip)
	mustExtractionError(t, err, upload.ReasonMaxDecompressionRatio)
}

// spec: §7.4 line 451 — the per-call 1 MiB decompressor cap the zip
// path now applies must not corrupt a multi-MiB entry. A ~1.2 MiB
// incompressible entry (so the ratio stays near 1:1 and the entry is
// admitted) round-trips byte-for-byte through the readCap wrapper.
// F-13.4.11.
func TestExtractionZipReadCapRoundTrip(t *testing.T) {
	// Deterministic pseudo-random bytes do not compress, so the ratio
	// check admits the entry; the size (1.2 MiB) crosses the 1 MiB
	// per-read cap, exercising multiple capped reads.
	rng := rand.New(rand.NewSource(1))
	payload := make([]byte, 1200*1024)
	if _, err := rng.Read(payload); err != nil {
		t.Fatalf("seed payload: %v", err)
	}
	zip := buildZip(t, map[string]string{"blob.bin": string(payload)})
	root, err := materializeArchive(t, "zip", "", 0, zip)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "blob.bin"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("extracted %d bytes do not match the %d-byte source", len(got), len(payload))
	}
}
