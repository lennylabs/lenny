// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the operator security guide's statement
// of gateway-to-gateway forwarding: the forward port, the symmetric gateway
// certificate validation, the receiving replica's credential re-check, the
// calling-session identity of a forwarded platform tool call, and the
// lenny-system policies that admit only gateway pods on the forward port.
// No external infrastructure.

package tier11_docs_test

import "testing"

// TestOperatorSecurityGuideStatesHolderForwardIdentity_spec_10_3 asserts the
// Gateway mTLS Identity section of the operator security guide states how
// replicas authenticate each other and the forwarded request on the
// inter-replica forward port.
//
// diagnosis: A failure means the operator security guide omits the
// inter-replica forward bullet, names a port other than the
// gateway.holderForwardPort default 50054, drops the gateway-certificate
// peer validation, drops the receiver's re-check of the client credential,
// omits that a forwarded agent tool call runs as the calling session the
// forwarding replica names, omits a lenny-system policy that admits gateway
// pods on the forward port, or cites a specification section number.
//
// spec: 10.3 (mTLS PKI, Gateway ↔ Gateway mTLS peer validation),
// 13.2.4 (Gateway row of the lenny-system NetworkPolicy table),
// 29.3 (Interactive message send, Forwarding.)
func TestOperatorSecurityGuideStatesHolderForwardIdentity_spec_10_3(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page: "operator-guide/security.md",
			row:  "Replicas forward session requests to each other over mutual TLS",
			contains: []string{
				"port 50054",
				"`gateway.holderForwardPort`",
				"presents its gateway certificate",
				"accepts only a peer that presents the gateway certificate",
				"re-checks the client's credential on the forwarded request",
				"runs it as the calling session that the forwarding replica names",
				"`lenny-system`",
				"`allow-gateway-ingress`",
				"`allow-gateway-egress`",
				"admit only gateway pods on port 50054",
			},
			notContain: []string{"§", "LNK-INTERREPLICA", "50051"},
		},
	})
}
