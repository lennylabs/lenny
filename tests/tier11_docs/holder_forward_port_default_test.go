// SPDX-License-Identifier: MIT

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// holderForwardPortDefault is the default port §13.2.4 states for the
// CH-HOLDERFORWARD inter-replica forward listener.
const holderForwardPortDefault = 50054

// holderForwardPortCell is the text the §13.2.4 Gateway row carries for the
// gateway-pods peer in both its egress and its ingress column.
const holderForwardPortCell = "gateway pods (TCP `{{ .Values.gateway.holderForwardPort }}` — default 50054, mTLS, `CH-HOLDERFORWARD`"

// spec: 13.2.4 (`lenny-system` Namespace NetworkPolicies, Gateway row),
// 10.1.1 (Stateless Replicas and Per-Session Coordination, Replica address)
// diagnosis: the chart's gateway.holderForwardPort default and the default
// the §13.2.4 Gateway row states for the gateway-pods peer disagree, or the
// row lost the peer from its egress or its ingress column. An operator
// reading the specification then opens a different port from the one the
// gateway listens on and the NetworkPolicies admit.
func TestHolderForwardPortDefaultMatchesGatewayRow_spec_13_2_4(t *testing.T) {
	root := repoRoot(t)

	raw, err := os.ReadFile(filepath.Join(root, "charts", "lenny", "values.yaml"))
	if err != nil {
		t.Fatalf("read values.yaml: %v", err)
	}
	var values struct {
		Gateway struct {
			HolderForwardPort *int `yaml:"holderForwardPort"`
		} `yaml:"gateway"`
	}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatalf("decode values.yaml: %v", err)
	}
	switch {
	case values.Gateway.HolderForwardPort == nil:
		t.Errorf("charts/lenny/values.yaml sets no gateway.holderForwardPort; §13.2.4 states default %d", holderForwardPortDefault)
	case *values.Gateway.HolderForwardPort != holderForwardPortDefault:
		t.Errorf("charts/lenny/values.yaml gateway.holderForwardPort = %d; §13.2.4 states default %d",
			*values.Gateway.HolderForwardPort, holderForwardPortDefault)
	}

	specRaw, err := os.ReadFile(filepath.Join(root, "spec", "13_security-model.md"))
	if err != nil {
		t.Fatalf("read spec/13_security-model.md: %v", err)
	}
	row := gatewayNetworkPolicyRow(string(specRaw))
	if row == "" {
		t.Fatalf("spec/13_security-model.md: no §13.2.4 Gateway row found")
	}
	// The egress column and the ingress column each name the peer once.
	if got := strings.Count(row, holderForwardPortCell); got != 2 {
		t.Errorf("§13.2.4 Gateway row names the gateway-pods peer on gateway.holderForwardPort %d time(s); want 2 (egress and ingress)", got)
	}
}

// gatewayNetworkPolicyRow returns the §13.2.4 table row for the gateway
// component, or "" when the specification carries none.
func gatewayNetworkPolicyRow(spec string) string {
	for _, line := range strings.Split(spec, "\n") {
		if strings.HasPrefix(line, "| **Gateway** (`lenny.dev/component: gateway`)") {
			return line
		}
	}
	return ""
}
