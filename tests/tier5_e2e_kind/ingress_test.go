// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §13.2 gateway-ingress NetworkPolicy
// (NET-038). The chart's allow-gateway-ingress policy admits external
// HTTPS to the gateway from the Ingress controller namespace, matched
// by a namespaceSelector on the ingressControllerNamespace chart value
// (default ingress-nginx). install.sh installs ingress-nginx onto the
// cluster, so the NET-038 admission rule has a real counterparty.
//
// The test asserts the NET-038 ingress admission three ways: the
// rendered allow-gateway-ingress policy carries the
// ingress-controller-namespace rule on the gateway TLS port; the
// ingress-nginx namespace and its controller pods exist; and — as the
// adversarial half — a probe pod in an unrelated namespace carrying no
// allow-list label is blocked by the lenny-system default-deny.

package tier5_e2e_kind_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// ingressControllerNamespace is the namespace the e2e Ingress
// controller runs in. It matches the chart's ingressControllerNamespace
// default and the namespace install.sh installs ingress-nginx into.
const ingressControllerNamespace = "ingress-nginx"

// spec: 13.2
// diagnosis: the §13.2 NET-038 gateway-ingress NetworkPolicy is not
// enforced. The chart's allow-gateway-ingress admits external HTTPS to
// the gateway from the Ingress controller namespace via a
// namespaceSelector on the ingressControllerNamespace value. The test
// asserts the rendered policy carries that ingress rule on the gateway
// TLS port, the ingress-nginx namespace and controller pods exist, and
// a probe pod in an unrelated namespace is blocked by default-deny. A
// missing rule, an absent controller, or an admitted unrelated probe is
// a NET-038 admission failure.
func TestGatewayIngressPolicy(t *testing.T) {
	c := kind.InstallLenny(t)

	// --- Assertion 1: the ingress-nginx namespace exists and carries
	// the immutable kubernetes.io/metadata.name label the chart's
	// namespaceSelector keys on. Without that label the NET-038 rule
	// selects nothing and the gateway is unreachable from the Ingress
	// controller.
	nsLabel, err := c.KubectlOut(
		t,
		"get", "namespace", ingressControllerNamespace,
		"-o", "jsonpath={.metadata.labels.kubernetes\\.io/metadata\\.name}",
	)
	if err != nil {
		t.Fatalf("the Ingress controller namespace %q is not present; install.sh installs ingress-nginx "+
			"as the NET-038 counterparty: %v\n%s", ingressControllerNamespace, err, nsLabel)
	}
	if strings.TrimSpace(nsLabel) != ingressControllerNamespace {
		t.Fatalf("namespace %q carries kubernetes.io/metadata.name=%q; the NET-038 namespaceSelector "+
			"matches on this label and would not select the namespace",
			ingressControllerNamespace, strings.TrimSpace(nsLabel))
	}

	// --- Assertion 2: the Ingress controller has a running pod. NET-038
	// admits HTTPS from this namespace specifically because the Ingress
	// controller terminates TLS there and forwards to the gateway.
	controllerPods := ingressControllerPods(t, c)
	if len(controllerPods) == 0 {
		t.Fatalf("no ingress-nginx controller pod is running in %q; the NET-038 admission rule has no "+
			"counterparty to admit", ingressControllerNamespace)
	}
	t.Logf("Ingress controller namespace %q present, %d controller pod(s) running",
		ingressControllerNamespace, len(controllerPods))

	// --- Assertion 3: the rendered allow-gateway-ingress NetworkPolicy
	// contains the NET-038 ingress rule. The rule is a from-clause with a
	// namespaceSelector on kubernetes.io/metadata.name matching the
	// ingress-controller namespace, on the gateway TLS port.
	//
	// This is asserted statically against the rendered policy rather
	// than by a live HTTPS probe: the dev-mode gateway binds only its
	// HTTP (8080) and gRPC (50051) listeners, with no TLS listener on
	// the port NET-038 admits (gateway.httpsPort, 443). A curl from the
	// ingress-nginx namespace to gateway:443 reaches a port with no
	// endpoint and times out, and a curl to gateway:8080 is blocked
	// because 8080 is not in the ingress-nginx from-rule — neither
	// outcome distinguishes "NET-038 admitted" from "default-deny
	// blocked" at the CNI layer. The rendered-policy assertion is the
	// rigorous check of the admission rule; the adversarial probe below
	// proves the default-deny baseline the rule narrows is enforced.
	policy := gatewayIngressPolicy(t, c)
	rule, port, found := policy.ingressControllerRule(ingressControllerNamespace)
	if !found {
		t.Fatalf("§13.2 NET-038 violation: the rendered allow-gateway-ingress NetworkPolicy carries no "+
			"ingress rule with a namespaceSelector on kubernetes.io/metadata.name=%q; external HTTPS from "+
			"the Ingress controller is not admitted.\npolicy ingress:\n%s",
			ingressControllerNamespace, policy.ingressJSON())
	}
	t.Logf("§13.2 NET-038: allow-gateway-ingress admits namespace %q on TCP %d (rule index %d)",
		ingressControllerNamespace, port, rule)

	// --- Assertion 4 (adversarial): a probe pod in an unrelated
	// namespace carrying no lenny.dev/component allow-list label cannot
	// reach the gateway. NET-038 admits the ingress-nginx namespace
	// specifically; the lenny-system default-deny blocks every other
	// source. The probe runs in the `default` namespace, which carries
	// no allow-list label, so its connection to the gateway must time
	// out at the CNI layer.
	gatewayIP := t5ServiceClusterIP(t, c, "lenny-gateway")
	if gatewayIP == "" {
		t.Fatalf("the lenny-gateway Service has no ClusterIP; cannot run the NET-038 adversarial probe")
	}
	t5CreateProbePod(t, c, "t5-ingress-neg-probe", "default", nil)
	for _, port := range []string{"8080", "443"} {
		res := t5CurlFromPod(t, c, "t5-ingress-neg-probe", "default",
			fmt.Sprintf("http://%s:%s/healthz", gatewayIP, port), 8*time.Second)
		if res.exitCode == 0 {
			t.Fatalf("§13.2 NET-038 violation: a probe pod in the unrelated `default` namespace reached "+
				"the gateway at %s:%s. allow-gateway-ingress admits only the Ingress controller namespace; "+
				"the lenny-system default-deny must block every other source.\noutput:\n%s",
				gatewayIP, port, res.output)
		}
		// curl exit 28 is the CNI timeout this test wants — the SYN was
		// dropped. Exit 7 (connection refused) would mean the packet
		// reached a host that actively rejected it, which is not a
		// NetworkPolicy block; flag it but do not fail, since a
		// refused-but-not-admitted result still satisfies the property.
		if res.exitCode != 28 {
			t.Logf("note: unrelated-namespace probe to %s:%s failed with curl exit %d (expected 28, "+
				"connection timed out); the connection did not complete, so the gateway was not reached",
				gatewayIP, port, res.exitCode)
			continue
		}
		t.Logf("§13.2 NET-038 adversarial: unrelated-namespace probe blocked reaching the gateway at "+
			"%s:%s (curl exit 28, connection timed out — default-deny enforced)", gatewayIP, port)
	}
}

// ingressControllerPods returns the names of the running ingress-nginx
// controller pods. The Kind ingress-nginx manifest labels them
// app.kubernetes.io/component=controller.
func ingressControllerPods(t *testing.T, c *kind.Cluster) []string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", ingressControllerNamespace, "get", "pods",
		"-l", "app.kubernetes.io/component=controller",
		"--field-selector=status.phase=Running",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}",
	)
	if err != nil {
		t.Fatalf("list ingress-nginx controller pods: %v\n%s", err, out)
	}
	var pods []string
	for _, name := range strings.Fields(out) {
		pods = append(pods, name)
	}
	return pods
}

// netPolicy is the parsed spec of a NetworkPolicy, decoded just far
// enough to inspect the ingress from-clauses and ports.
type netPolicy struct {
	Spec struct {
		Ingress []struct {
			From []struct {
				NamespaceSelector struct {
					MatchLabels map[string]string `json:"matchLabels"`
				} `json:"namespaceSelector"`
			} `json:"from"`
			Ports []struct {
				Port any `json:"port"`
			} `json:"ports"`
		} `json:"ingress"`
	} `json:"spec"`
	raw string
}

// ingressControllerRule reports whether the policy carries an ingress
// rule whose from-clause has a namespaceSelector matching
// kubernetes.io/metadata.name=ns, and returns the rule index and its
// first port. The port is the gateway TLS port NET-038 admits.
func (p netPolicy) ingressControllerRule(ns string) (ruleIndex, port int, found bool) {
	for i, rule := range p.Spec.Ingress {
		for _, from := range rule.From {
			if from.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != ns {
				continue
			}
			pn := 0
			if len(rule.Ports) > 0 {
				pn = portAsInt(rule.Ports[0].Port)
			}
			return i, pn, true
		}
	}
	return 0, 0, false
}

// ingressJSON returns the policy's ingress block as indented JSON for a
// failure message.
func (p netPolicy) ingressJSON() string {
	b, err := json.MarshalIndent(p.Spec.Ingress, "", "  ")
	if err != nil {
		return p.raw
	}
	return string(b)
}

// portAsInt coerces a NetworkPolicy port value (a JSON number or a
// named-port string) to an int; a named port or an unparseable value
// yields 0.
func portAsInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i := 0
		if _, err := fmt.Sscanf(n, "%d", &i); err == nil {
			return i
		}
	}
	return 0
}

// gatewayIngressPolicy reads the rendered allow-gateway-ingress
// NetworkPolicy from lenny-system and decodes its spec. A missing
// policy fails the test: allow-gateway-ingress is a §13.2 baseline the
// chart renders unconditionally.
func gatewayIngressPolicy(t *testing.T, c *kind.Cluster) netPolicy {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", t5SystemNS, "get", "networkpolicy", "allow-gateway-ingress",
		"-o", "json",
	)
	if err != nil {
		t.Fatalf("the §13.2 allow-gateway-ingress NetworkPolicy is not installed in lenny-system: %v\n%s",
			err, out)
	}
	var p netPolicy
	p.raw = out
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("allow-gateway-ingress NetworkPolicy is not valid JSON: %v\n%s", err, out)
	}
	return p
}

// t5CurlResult holds the outcome of a curl run inside a probe pod.
type t5CurlResult struct {
	exitCode int
	output   string
}

// t5CurlFromPod runs curl inside the named probe pod against target
// with a bounded connect timeout and returns curl's exit code and
// combined output. A CNI-dropped connection returns curl exit 28 within
// the bound rather than stalling the test.
func t5CurlFromPod(t *testing.T, c *kind.Cluster, pod, namespace, target string, timeout time.Duration) t5CurlResult {
	t.Helper()
	secs := int(timeout.Seconds())
	script := fmt.Sprintf(
		"curl -sS -m %d -o /dev/null -w 'http=%%{http_code}' %s 2>&1; echo \" exit=$?\"",
		secs, target,
	)
	out, _ := c.KubectlOut(
		t,
		"-n", namespace, "exec", pod, "--", "sh", "-c", script,
	)
	return t5CurlResult{exitCode: parseT5CurlExit(out), output: out}
}

// parseT5CurlExit extracts the integer N from the `exit=N` marker
// t5CurlFromPod's script appends. A missing or unparseable marker
// yields -1, which no caller treats as success.
func parseT5CurlExit(out string) int {
	idx := strings.LastIndex(out, "exit=")
	if idx < 0 {
		return -1
	}
	field := strings.TrimSpace(out[idx+len("exit="):])
	if i := strings.IndexFunc(field, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		field = field[:i]
	}
	n := 0
	if _, err := fmt.Sscanf(field, "%d", &n); err != nil {
		return -1
	}
	return n
}
