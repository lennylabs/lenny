// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 render checks for the §13.2.4 KMS endpoint egress on the gateway
// and lenny-ops. Under a cloud KMS provider both processes call the
// provider over TCP 443: the gateway signs every Lenny-issued JWT and both
// load the verifier key set through GetPublicKey (§10.2). The security
// property is the SSRF boundary: NetworkPolicy allows are additive, so a
// KMS peer of 0.0.0.0/0 or ::/0 would re-admit every private range and
// IMDS address the external HTTPS rules except (NET-057). The chart must
// therefore refuse that render and carry the KMS peer only at a narrower
// CIDR, and only when the provider is not local.
//
// These are chart-render assertions through the helm CLI, so they need no
// live cluster.
package tier9_security_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// kmsEgressChart is the chart path relative to this package.
const kmsEgressChart = "../../charts/lenny"

// kmsEgressPolicies names the two policies that carry the KMS peer under a
// cloud provider.
var kmsEgressPolicies = []string{"allow-gateway-egress", "lenny-ops-egress"}

// kmsEgressBaseSet is the minimum value set every render in this file
// needs: the SPIFFE trust domain and the CoreDNS clusterIP the chart
// requires.
func kmsEgressBaseSet(extra ...string) []string {
	return append([]string{
		"global.spiffeTrustDomain=lenny-test",
		"coredns.clusterIP=10.96.0.10",
	}, extra...)
}

// renderKMSEgress renders the chart with the given --set overrides.
func renderKMSEgress(t *testing.T, set ...string) helm.Manifests {
	t.Helper()
	helm.SkipUnlessAvailable(t)
	return helm.Render(t, helm.Options{Chart: kmsEgressChart, Set: kmsEgressBaseSet(set...)})
}

// renderKMSEgressErr renders the chart and returns the helm stderr and
// whether the render failed. helm.Render fails the test on a render error,
// so a refusal case runs the CLI directly.
func renderKMSEgressErr(t *testing.T, set ...string) (stderr string, failed bool) {
	t.Helper()
	helm.SkipUnlessAvailable(t)
	bin := os.Getenv("LENNY_HELM_BIN")
	if bin == "" {
		bin = "helm"
	}
	args := []string{"template", "lenny", kmsEgressChart, "--namespace", "lenny-system"}
	for _, kv := range kmsEgressBaseSet(set...) {
		args = append(args, "--set", kv)
	}
	var errBuf bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &errBuf
	return errBuf.String(), cmd.Run() != nil
}

// ipBlockPeer is one ipBlock peer of an egress rule with the TCP ports the
// rule admits.
type ipBlockPeer struct {
	cidr      string
	hasExcept bool
	ports     []int
}

// policyIPBlockPeers returns every ipBlock peer of the named
// NetworkPolicy's egress rules.
func policyIPBlockPeers(t *testing.T, m helm.Manifests, name string) []ipBlockPeer {
	t.Helper()
	pol := m.MustFind(t, "NetworkPolicy", name)
	spec, _ := pol.Raw["spec"].(map[string]any)
	rules, _ := spec["egress"].([]any)
	var peers []ipBlockPeer
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		var ports []int
		ps, _ := rule["ports"].([]any)
		for _, p := range ps {
			pm, _ := p.(map[string]any)
			if n, ok := pm["port"].(int); ok {
				ports = append(ports, n)
			}
		}
		to, _ := rule["to"].([]any)
		for _, peer := range to {
			pm, _ := peer.(map[string]any)
			ib, ok := pm["ipBlock"].(map[string]any)
			if !ok {
				continue
			}
			cidr, _ := ib["cidr"].(string)
			except, _ := ib["except"].([]any)
			peers = append(peers, ipBlockPeer{cidr: cidr, hasExcept: len(except) > 0, ports: ports})
		}
	}
	return peers
}

// countPeersAt counts the ipBlock peers at cidr that admit TCP 443.
func countPeersAt(peers []ipBlockPeer, cidr string) int {
	n := 0
	for _, p := range peers {
		if p.cidr != cidr {
			continue
		}
		for _, port := range p.ports {
			if port == 443 {
				n++
				break
			}
		}
	}
	return n
}

// unrestrictedPeers returns the peers whose CIDR spans a whole address
// family with no except list: the peers that would cancel the external
// HTTPS excepts.
func unrestrictedPeers(peers []ipBlockPeer) []ipBlockPeer {
	var out []ipBlockPeer
	for _, p := range peers {
		if (p.cidr == "0.0.0.0/0" || p.cidr == "::/0") && !p.hasExcept {
			out = append(out, p)
		}
	}
	return out
}

// spec: §13.2.4 (`lenny-system` Namespace NetworkPolicies, Gateway row)
// diagnosis: the chart renders a cloud-provider install whose KMS egress
// peer is 0.0.0.0/0 or ::/0. That peer would sit on allow-gateway-egress
// and lenny-ops-egress beside the external HTTPS rules and, because
// NetworkPolicy allows are additive, re-admit the RFC1918, link-local, and
// IMDS ranges those rules except, opening the SSRF boundary.
func TestKMSEgressRefusesUnrestrictedCIDROnCloudProvider_spec_13_2_4(t *testing.T) {
	cases := []struct {
		provider string
		cidr     string
	}{
		{"aws", ""}, // the shipped 0.0.0.0/0 default
		{"aws", "::/0"},
		{"gcp", "0.0.0.0/0"},
		{"azure", "::/0"},
	}
	for _, tc := range cases {
		name := tc.provider + "/" + tc.cidr
		if tc.cidr == "" {
			name = tc.provider + "/default"
		}
		t.Run(name, func(t *testing.T) {
			set := []string{"kms.provider=" + tc.provider, "kms.signQuotaPerSecond=50"}
			if tc.cidr != "" {
				set = append(set, "kms.endpointCIDR="+tc.cidr)
			}
			stderr, failed := renderKMSEgressErr(t, set...)
			if !failed {
				t.Fatalf("render with kms.provider=%s and an unrestricted kms.endpointCIDR succeeded; §13.2.4 requires the chart to refuse it", tc.provider)
			}
			if !strings.Contains(stderr, "kms.endpointCIDR is") {
				t.Fatalf("render failed for a reason other than the kms.endpointCIDR refusal:\n%s", stderr)
			}
		})
	}
}

// spec: §13.2.4 (`lenny-system` Namespace NetworkPolicies, Gateway row and
// lenny-ops row), §25.4 (lenny-ops-egress)
// diagnosis: a cloud-provider render with a private KMS endpoint subnet
// does not give the gateway or lenny-ops exactly one TCP 443 peer at that
// subnet, or it carries an unrestricted ipBlock peer with no except list.
// A missing peer leaves the gateway unable to sign and both processes
// unable to load the verifier key set under an enforcing CNI; an
// unrestricted peer opens the SSRF boundary.
func TestKMSEgressRendersOneNarrowPeerPerPolicy_spec_13_2_4(t *testing.T) {
	const cidr = "10.20.0.0/24"
	for _, provider := range []string{"aws", "gcp", "azure"} {
		t.Run(provider, func(t *testing.T) {
			m := renderKMSEgress(t, "kms.provider="+provider, "kms.signQuotaPerSecond=50", "kms.endpointCIDR="+cidr)
			for _, name := range kmsEgressPolicies {
				peers := policyIPBlockPeers(t, m, name)
				if got := countPeersAt(peers, cidr); got != 1 {
					t.Errorf("%s: got %d TCP 443 peers at %s, want 1", name, got, cidr)
				}
				if bad := unrestrictedPeers(peers); len(bad) > 0 {
					t.Errorf("%s: carries unrestricted ipBlock peers with no except list: %+v", name, bad)
				}
			}
		})
	}
}

// spec: §13.2.4 (`lenny-system` Namespace NetworkPolicies, Gateway row)
// diagnosis: under the local KMS provider, which makes no KMS call,
// allow-gateway-egress or lenny-ops-egress carries a KMS peer, or the
// 0.0.0.0/0 kms.endpointCIDR default reaches either policy as an
// unrestricted peer.
func TestKMSEgressAbsentUnderLocalProvider_spec_13_2_4(t *testing.T) {
	m := renderKMSEgress(t, "kms.endpointCIDR=10.20.0.0/24")
	for _, name := range kmsEgressPolicies {
		peers := policyIPBlockPeers(t, m, name)
		if got := countPeersAt(peers, "10.20.0.0/24"); got != 0 {
			t.Errorf("%s: got %d KMS peers under the local provider, want 0", name, got)
		}
		if bad := unrestrictedPeers(peers); len(bad) > 0 {
			t.Errorf("%s: carries unrestricted ipBlock peers with no except list: %+v", name, bad)
		}
	}
}
