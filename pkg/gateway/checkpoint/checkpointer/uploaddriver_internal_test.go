// SPDX-License-Identifier: MIT

package checkpointer

import "testing"

// spec: §10.1 line 139 — the chunk object basename is
// chunk-{n}.{chunk_encoding} with {n} a zero-padded 5-digit monotonic index
// starting at 00000, so the signed key matches the reassembly layout the
// resume path enumerates and lexicographic list order tracks index order.
// diagnosis: a failure here (chunk-0.tar rather than chunk-00000.tar) means
// the driver signs PUT grants whose keys diverge from the spec chunk-object
// layout, so ListObjectsV2 order stops tracking index order (chunk-10 sorts
// before chunk-2) and the reassembly path cannot find the objects.
func TestChunkURIZeroPadsIndex_spec_10_1(t *testing.T) {
	d := &uploadDriver{
		c:            &Checkpointer{},
		tenantID:     "acme",
		sessionID:    "s1",
		checkpointID: "cp1",
	}
	cases := map[uint32]string{
		0:     "cp1/chunk-00000.tar",
		1:     "cp1/chunk-00001.tar",
		42:    "cp1/chunk-00042.tar",
		12345: "cp1/chunk-12345.tar",
	}
	for idx, want := range cases {
		if got := d.chunkURI(idx).PartID; got != want {
			t.Errorf("chunkURI(%d).PartID = %q, want %q", idx, got, want)
		}
	}
}
