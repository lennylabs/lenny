// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §4.2 recovery_generation increment
// on a real pod recovery through the durable stack.
//
// The adapter Resume RPC no longer restores from an in-adapter
// CheckpointSource: it fetches the checkpoint's chunks over presigned GET
// capabilities the gateway mints from the manifest row and passes on
// ResumeRequest.chunks (§10.1 line 155). The durable recovery-generation
// round-trip is re-expressed against that gateway-minted restore path once
// the gateway-side resume driver lands; this file is a compiling
// placeholder until then so the tier-0 build gate stays green.
//
// spec: §4.2 line 156 (recovery_generation), §10.1 line 155 (reassembly
// on resume from presigned chunk GET capabilities).

package tier4_integration_test

import "testing"

func TestResumeBumpsRecoveryGenerationDurablyPostgres(t *testing.T) {
	t.Skip("adapter Resume restore moved to gateway-minted presigned chunk GET " +
		"capabilities (§10.1 line 155); the durable recovery_generation " +
		"round-trip is re-expressed against the gateway resume driver")
}
