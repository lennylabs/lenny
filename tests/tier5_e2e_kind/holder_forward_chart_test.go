// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the chart wiring of the CH-HOLDERFORWARD
// inter-replica forward listener. The applied gateway Deployment advertises
// <pod IP>:<gateway.holderForwardPort> and binds the listener on that port,
// and the applied allow-gateway-egress and allow-gateway-ingress policies
// admit gateway pods on it. The test reads what the API server accepted,
// so a field the API server drops or rejects fails here rather than in
// helm-unittest.

package tier5_e2e_kind_test

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// holderForwardDefaultPort is the chart default for gateway.holderForwardPort.
const holderForwardDefaultPort = 50054

// appliedGatewayContainer is the subset of the applied gateway container
// this test reads.
type appliedGatewayContainer struct {
	Name string `json:"name"`
	Env  []struct {
		Name      string `json:"name"`
		Value     string `json:"value"`
		ValueFrom *struct {
			FieldRef *struct {
				FieldPath string `json:"fieldPath"`
			} `json:"fieldRef"`
		} `json:"valueFrom"`
	} `json:"env"`
	Ports []struct {
		Name          string `json:"name"`
		ContainerPort int    `json:"containerPort"`
	} `json:"ports"`
}

// appliedSelfPeerPolicy is the subset of an applied NetworkPolicy this test
// reads: the pod-selector peers and ports of each egress and ingress rule.
type appliedSelfPeerPolicy struct {
	Spec struct {
		Egress  []appliedSelfPeerRule `json:"egress"`
		Ingress []appliedSelfPeerRule `json:"ingress"`
	} `json:"spec"`
}

type appliedSelfPeerRule struct {
	To    []appliedSelfPeer `json:"to"`
	From  []appliedSelfPeer `json:"from"`
	Ports []struct {
		Port     any    `json:"port"`
		Protocol string `json:"protocol"`
	} `json:"ports"`
}

type appliedSelfPeer struct {
	NamespaceSelector *json.RawMessage `json:"namespaceSelector"`
	PodSelector       *struct {
		MatchLabels map[string]string `json:"matchLabels"`
	} `json:"podSelector"`
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination, Replica
// address), 10.3 (mTLS PKI, Gateway ↔ Gateway mTLS peer validation
// (NET-072))
// diagnosis: the applied lenny-gateway Deployment lacks the downward-API
// LENNY_POD_IP, declares the advertise address before it (so $(LENNY_POD_IP)
// does not expand), advertises or binds a port other than
// gateway.holderForwardPort, or lacks the holder-forward container port. A
// replica then writes a holder value with no reachable address, and every
// forward to it takes the not-delivered outcome.
func TestGatewayDeploymentAdvertisesHolderForwardAddress_spec_10_1_1(t *testing.T) {
	c := kind.InstallLenny(t)

	out, err := c.KubectlOut(t, "-n", "lenny-system", "get", "deployment", "lenny-gateway",
		"-o", "jsonpath={.spec.template.spec.containers[?(@.name==\"gateway\")]}")
	if err != nil {
		t.Fatalf("read lenny-gateway container: %v\n%s", err, out)
	}
	var ctr appliedGatewayContainer
	if err := json.Unmarshal([]byte(out), &ctr); err != nil {
		t.Fatalf("decode lenny-gateway container: %v\n%s", err, out)
	}

	podIPAt, advertiseAt := -1, -1
	for i, e := range ctr.Env {
		switch e.Name {
		case "LENNY_POD_IP":
			podIPAt = i
			if e.ValueFrom == nil || e.ValueFrom.FieldRef == nil || e.ValueFrom.FieldRef.FieldPath != "status.podIP" {
				t.Errorf("LENNY_POD_IP is not sourced from fieldRef status.podIP")
			}
		case "LENNY_HOLDER_FORWARD_ADVERTISE_ADDR":
			advertiseAt = i
			if want := "$(LENNY_POD_IP):50054"; e.Value != want {
				t.Errorf("LENNY_HOLDER_FORWARD_ADVERTISE_ADDR = %q; want %q", e.Value, want)
			}
		case "LENNY_HOLDER_FORWARD_LISTEN_ADDR":
			if want := ":50054"; e.Value != want {
				t.Errorf("LENNY_HOLDER_FORWARD_LISTEN_ADDR = %q; want %q", e.Value, want)
			}
		}
	}
	switch {
	case podIPAt < 0:
		t.Errorf("applied gateway container carries no LENNY_POD_IP")
	case advertiseAt < 0:
		t.Errorf("applied gateway container carries no LENNY_HOLDER_FORWARD_ADVERTISE_ADDR")
	case advertiseAt < podIPAt:
		t.Errorf("LENNY_HOLDER_FORWARD_ADVERTISE_ADDR (env[%d]) precedes LENNY_POD_IP (env[%d]); $(LENNY_POD_IP) does not expand",
			advertiseAt, podIPAt)
	}

	found := false
	for _, p := range ctr.Ports {
		if p.Name == "holder-forward" {
			found = true
			if p.ContainerPort != holderForwardDefaultPort {
				t.Errorf("holder-forward containerPort = %d; want %d", p.ContainerPort, holderForwardDefaultPort)
			}
		}
	}
	if !found {
		t.Errorf("applied gateway container exposes no holder-forward port")
	}
}

// spec: 13.2.4 (`lenny-system` Namespace NetworkPolicies, Gateway row)
// diagnosis: the applied allow-gateway-egress lacks a gateway-pod peer on
// TCP 50054, or the applied allow-gateway-ingress lacks the mirror rule or
// admits a source other than gateway pods on that port. Under the
// lenny-system default-deny a forward between replicas then times out, or
// a non-gateway pod reaches the forward listener.
func TestGatewaySelfPeerPoliciesAdmitHolderForwardPort_spec_13_2_4(t *testing.T) {
	c := kind.InstallLenny(t)

	egress := readSelfPeerPolicy(t, c, "allow-gateway-egress").Spec.Egress
	if n := countGatewaySelfPeerRules(egress, true); n != 1 {
		t.Errorf("allow-gateway-egress carries %d gateway-pod peer rule(s) on TCP %d; want 1", n, holderForwardDefaultPort)
	}

	ingress := readSelfPeerPolicy(t, c, "allow-gateway-ingress").Spec.Ingress
	if n := countGatewaySelfPeerRules(ingress, false); n != 1 {
		t.Errorf("allow-gateway-ingress carries %d gateway-pod source rule(s) on TCP %d; want 1", n, holderForwardDefaultPort)
	}
	for i, rule := range ingress {
		if !rulePortsInclude(rule, holderForwardDefaultPort) {
			continue
		}
		if !isGatewayOnlyPeerSet(rule.From) {
			t.Errorf("allow-gateway-ingress ingress[%d] admits a source other than gateway pods on TCP %d", i, holderForwardDefaultPort)
		}
	}
}

// readSelfPeerPolicy reads and decodes one applied lenny-system NetworkPolicy.
func readSelfPeerPolicy(t *testing.T, c *kind.Cluster, name string) appliedSelfPeerPolicy {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", "lenny-system", "get", "networkpolicy", name, "-o", "json")
	if err != nil {
		t.Fatalf("read NetworkPolicy %s: %v\n%s", name, err, out)
	}
	var pol appliedSelfPeerPolicy
	if err := json.Unmarshal([]byte(out), &pol); err != nil {
		t.Fatalf("decode NetworkPolicy %s: %v", name, err)
	}
	return pol
}

// countGatewaySelfPeerRules counts the rules whose peers are gateway pods
// only and whose ports include the forward port. egress selects the To
// peers; otherwise the From peers.
func countGatewaySelfPeerRules(rules []appliedSelfPeerRule, egress bool) int {
	n := 0
	for _, r := range rules {
		peers := r.From
		if egress {
			peers = r.To
		}
		if isGatewayOnlyPeerSet(peers) && rulePortsInclude(r, holderForwardDefaultPort) {
			n++
		}
	}
	return n
}

// isGatewayOnlyPeerSet reports whether every peer is a same-namespace pod
// selector matching lenny.dev/component: gateway.
func isGatewayOnlyPeerSet(peers []appliedSelfPeer) bool {
	if len(peers) == 0 {
		return false
	}
	for _, p := range peers {
		if p.NamespaceSelector != nil || p.PodSelector == nil {
			return false
		}
		if p.PodSelector.MatchLabels["lenny.dev/component"] != "gateway" {
			return false
		}
	}
	return true
}

// rulePortsInclude reports whether the rule names port on TCP.
func rulePortsInclude(r appliedSelfPeerRule, port int) bool {
	for _, p := range r.Ports {
		if p.Protocol != "" && p.Protocol != "TCP" {
			continue
		}
		if f, ok := p.Port.(float64); ok && int(f) == port {
			return true
		}
	}
	return false
}
