// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §13.2.4 KMS endpoint egress on the gateway
// and lenny-ops. The e2e install runs the local KMS provider, which makes
// no KMS call, so the applied allow-gateway-egress and lenny-ops-egress
// policies must carry no KMS peer. In particular the 0.0.0.0/0
// kms.endpointCIDR default, which the Token Service policy carries, must
// not reach either policy as an unrestricted ipBlock peer, because such a
// peer would cancel the private-range and IMDS excepts of the external
// HTTPS rules on the live cluster.

package tier5_e2e_kind_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// appliedEgressPolicy is the subset of an applied NetworkPolicy this test
// reads.
type appliedEgressPolicy struct {
	Spec struct {
		Egress []struct {
			To []struct {
				IPBlock *struct {
					CIDR   string   `json:"cidr"`
					Except []string `json:"except"`
				} `json:"ipBlock"`
			} `json:"to"`
			Ports []struct {
				Port any `json:"port"`
			} `json:"ports"`
		} `json:"egress"`
	} `json:"spec"`
}

// spec: §13.2.4 (`lenny-system` Namespace NetworkPolicies, Gateway row and
// lenny-ops row), §25.4 (lenny-ops-egress)
// diagnosis: on a local-provider install the applied allow-gateway-egress
// or lenny-ops-egress carries an unrestricted ipBlock peer (0.0.0.0/0 or
// ::/0 with no except list). The KMS peer renders only when kms.provider
// is not local; a peer here means the chart copied the always-rendered
// Token Service KMS peer into the gateway or lenny-ops policy, which
// re-admits every private range and IMDS address the external HTTPS
// rules except.
func TestKMSEgressAbsentOnLocalProviderInstall_spec_13_2_4(t *testing.T) {
	c := kind.InstallLenny(t)

	args, err := c.KubectlOut(t, "-n", "lenny-system", "get", "deployment", "lenny-gateway",
		"-o", "jsonpath={.spec.template.spec.containers[?(@.name==\"gateway\")].args}")
	if err != nil {
		t.Fatalf("read lenny-gateway args: %v\n%s", err, args)
	}
	if !strings.Contains(args, "--kms-provider=local") {
		t.Skipf("not-yet-applicable: the e2e install does not run the local KMS provider (args %s); this case covers the local install only", args)
	}

	for _, name := range []string{"allow-gateway-egress", "lenny-ops-egress"} {
		out, err := c.KubectlOut(t, "-n", "lenny-system", "get", "networkpolicy", name, "-o", "json")
		if err != nil {
			t.Fatalf("read NetworkPolicy %s: %v\n%s", name, err, out)
		}
		var pol appliedEgressPolicy
		if err := json.Unmarshal([]byte(out), &pol); err != nil {
			t.Fatalf("decode NetworkPolicy %s: %v", name, err)
		}
		for i, rule := range pol.Spec.Egress {
			for _, peer := range rule.To {
				if peer.IPBlock == nil {
					continue
				}
				cidr := peer.IPBlock.CIDR
				if (cidr == "0.0.0.0/0" || cidr == "::/0") && len(peer.IPBlock.Except) == 0 {
					t.Errorf("%s egress[%d] carries an unrestricted ipBlock peer %s with no except list on a local-provider install",
						name, i, cidr)
				}
			}
		}
	}
}
