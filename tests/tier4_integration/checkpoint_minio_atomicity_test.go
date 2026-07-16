// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §4.4 checkpoint-atomicity contract
// and the checkpoint-to-object-store-then-resume round-trip.
//
// The checkpoint upload no longer flows through an in-adapter
// CheckpointSink that PUTs a single object: the adapter now serves the
// gateway-driven bidirectional Checkpoint stream (§10.1), the gateway
// mints one presigned PUT capability per chunk, and the adapter uploads
// each chunk directly against object storage. The atomicity round-trip is
// re-expressed against that grant/confirm driver once the gateway-side
// checkpointer loop lands; this file is a compiling placeholder until then
// so the tier-0 build gate stays green.
//
// spec: §4.4 (Event / Checkpoint Store), §10.1 (gateway-driven chunked
// checkpoint upload and reassembly on resume).

package tier4_integration_test

import "testing"

func TestCheckpointWritesMetadataOnlyAfterMinIOUpload(t *testing.T) {
	t.Skip("checkpoint upload moved to the gateway-driven chunked Checkpoint " +
		"stream (§10.1); the atomicity round-trip is re-expressed against the " +
		"gateway grant/confirm driver with the gateway-side checkpointer loop")
}

func TestCheckpointDiscardedWhenMinIOUploadFails(t *testing.T) {
	t.Skip("checkpoint upload moved to the gateway-driven chunked Checkpoint " +
		"stream (§10.1); the fail-closed discard is re-expressed against the " +
		"gateway grant/confirm driver with the gateway-side checkpointer loop")
}
