// SPDX-License-Identifier: MIT

package main

import "testing"

// spec: §25.8 Release Channel Service Details, Signing: "The Lenny
// release-channel public key is compiled into `lenny-ops`. Operators
// running a mirror that re-signs with their own key can override via
// platform.releaseChannel.publicKeyPath."
//
// The stock chart ships platform.releaseChannel.url set to the canonical
// endpoint and platform.releaseChannel.publicKeyPath empty, so a default
// install supplies no operator key. Per the spec the compiled-in
// canonical public key is the trust anchor in that case, so
// buildReleaseChannelVerifier called with the stock-chart (empty)
// operator key must still return a working verifier that trusts the
// canonical release-channel signing key. Without the compiled-in key the
// upgrade-check consumer has no trust anchor on a stock install and fails
// closed, contradicting the spec.
func TestStockChartReleaseChannelHasCompiledInTrustAnchor_spec_25_8(t *testing.T) {
	t.Skip("compiled-in canonical release-channel public key is not yet shipped in lenny-ops; " +
		"generating it and defining the private-key custody process for releases.lenny.dev is an " +
		"unresolved build/release decision")
	// Stock chart: platform.releaseChannel.publicKeyPath and publicKeyID
	// are both empty (values.yaml), so no operator key is supplied.
	verifier, err := buildReleaseChannelVerifier("", "")
	if err != nil {
		t.Fatalf("buildReleaseChannelVerifier with stock-chart (empty) operator key: %v", err)
	}
	if verifier == nil {
		t.Fatal("stock install has no compiled-in release-channel trust anchor: " +
			"buildReleaseChannelVerifier returned nil for the default (empty) operator key; " +
			"§25.8 requires the Lenny release-channel public key to be compiled into lenny-ops " +
			"so a default install can verify signed release-channel responses")
	}
}
