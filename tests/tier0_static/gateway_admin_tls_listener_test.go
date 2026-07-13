// SPDX-License-Identifier: MIT

package tier0_static

import (
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// TestGatewayAdminTLSListenerDistinctFromLLMProxy asserts the chart wires
// a gateway admin-API-over-TLS listener that lenny-ops can actually reach
// over TLS on gateway.internalTLSPort, served by a listener distinct from
// the LLM reverse proxy.
//
// The §25.4 NET-070 admin-API hop is TLS by default (ops.tls.internalEnabled).
// The §17 install-time ops-admin-tls preflight check requires that lenny-ops
// "open a TCP connection to the gateway's internal TLS port
// (gateway.internalTLSPort ... default 8443) via the lenny-gateway ClusterIP
// Service, perform a TLS 1.2+ handshake ... and verify the server
// certificate's SAN covers the ClusterIP hostname." The §13.2 counterparty
// table separately requires the gateway to serve the LLM reverse proxy on
// gateway.llmProxyPort for proxy-mode agent pods. Both listeners run on the
// same gateway pod, so a single TCP port cannot serve both: the port
// lenny-ops dials over TLS must resolve to the admin-API-over-TLS listener,
// not the LLM proxy.
//
// spec: §25.4 (NET-070 admin-API TLS hop), §17 (ops-admin-tls preflight),
// §13.2 (gateway counterparty ports: internalTLSPort admin-API-over-TLS,
// llmProxyPort LLM reverse proxy).
func TestGatewayAdminTLSListenerDistinctFromLLMProxy(t *testing.T) {
	// The gateway does not yet bind an admin-API-over-TLS listener on
	// internalTLSPort, and the default chart sets internalTLSPort ==
	// llmProxyPort (both 8443), so the lenny-gateway Service maps the port
	// lenny-ops dials over TLS onto the plaintext LLM-proxy listener and
	// every admin-API handshake fails. Closing this requires a gateway
	// admin-TLS listener plus a distinct internalTLSPort, a product and
	// chart change pending a spec proposal.
	// The gateway does not yet bind an admin-API-over-TLS listener on
	// internalTLSPort, and the default chart sets internalTLSPort ==
	// llmProxyPort (both 8443), so the port lenny-ops dials over TLS lands
	// on the plaintext LLM-proxy listener (or has no listener at all when
	// the proxy is disabled) and every admin-API handshake fails. Closing
	// this requires a gateway admin-TLS listener plus a distinct
	// internalTLSPort, a product and chart change pending a spec proposal.
	t.Skip("gateway admin-API-over-TLS listener on internalTLSPort is unimplemented and collides with llmProxyPort; TEST-GAPS finding open pending a spec proposal")

	helm.SkipUnlessAvailable(t)

	// Render with the LLM reverse proxy enabled so the gateway binds the
	// llmProxyPort listener and the internalTLSPort collision is exercised.
	// When the proxy is disabled the gateway still binds no admin-API-over-TLS
	// listener, so lenny-ops's TLS dial to internalTLSPort has no listener at
	// all; either way the admin-API hop cannot complete a handshake.
	manifests := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set:       []string{"coredns.clusterIP=10.96.0.10", "features.llmProxy=true"},
	})

	// The transport lenny-ops negotiates is the admin-API-over-TLS hop only
	// when ops.tls.internalEnabled (the non-dev default). Read it and the
	// dialed port off the rendered lenny-ops Deployment args.
	opsArgs := containerArgs(t, manifests, "lenny-ops")
	if !hasArg(opsArgs, "--gateway-internal-tls=true") {
		t.Fatalf("lenny-ops is not configured for the TLS admin-API hop with stock values; "+
			"expected --gateway-internal-tls=true, got args:\n%s", strings.Join(opsArgs, "\n"))
	}
	internalTLSPort := argIntValue(t, opsArgs, "--gateway-internal-tls-port=")

	// The gateway ClusterIP Service is the front door for the admin-API hop.
	svc := manifests.MustFind(t, "Service", "lenny-gateway")
	ports := servicePorts(t, svc)

	// The LLM reverse proxy listener (§13.2, proxy-mode pods).
	llmProxyPort, ok := portTargetingListener(ports, "llm-proxy")
	if !ok {
		t.Fatalf("lenny-gateway Service exposes no port targeting the llm-proxy listener; "+
			"ports:\n%v", ports)
	}

	// A single gateway pod cannot serve the admin-API-over-TLS listener and
	// the LLM proxy on the same TCP port.
	if internalTLSPort == llmProxyPort {
		t.Fatalf("§25.4 NET-070 port collision: gateway.internalTLSPort (%d, the port lenny-ops dials "+
			"over TLS) equals gateway.llmProxyPort (%d, the LLM reverse proxy). A single gateway pod "+
			"cannot bind one TCP port for both the admin-API-over-TLS listener and the LLM proxy; "+
			"lenny-ops's TLS handshake lands on the plaintext LLM-proxy listener and fails.",
			internalTLSPort, llmProxyPort)
	}

	// The port lenny-ops dials over TLS must resolve to an admin-API-over-TLS
	// listener on the gateway, not the LLM proxy or the plaintext REST port.
	target, exposed := ports[internalTLSPort]
	if !exposed {
		t.Fatalf("§25.4 NET-070: lenny-gateway Service exposes no port %d for the admin-API-over-TLS hop "+
			"lenny-ops dials; ports:\n%v", internalTLSPort, ports)
	}
	if target == "llm-proxy" {
		t.Fatalf("§25.4 NET-070: the admin-API-over-TLS port %d lenny-ops dials targets the llm-proxy "+
			"listener; it must reach a dedicated admin-API-over-TLS listener on the gateway.", internalTLSPort)
	}
}

// containerArgs returns the args of the first container in the named
// Deployment. Fatal on a missing Deployment or no container with args.
func containerArgs(t *testing.T, m helm.Manifests, deployment string) []string {
	t.Helper()
	dep := m.MustFind(t, "Deployment", deployment)
	spec, _ := dep.Raw["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	for _, c := range containers {
		cm, _ := c.(map[string]any)
		raw, ok := cm["args"].([]any)
		if !ok {
			continue
		}
		out := make([]string, 0, len(raw))
		for _, a := range raw {
			if s, ok := a.(string); ok {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	t.Fatalf("Deployment/%s has no container with args", deployment)
	return nil
}

// argIntValue extracts the integer value from an arg of the form
// "<prefix><int>". Fatal when absent or non-numeric.
func argIntValue(t *testing.T, args []string, prefix string) int {
	t.Helper()
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			v, err := strconv.Atoi(strings.TrimPrefix(a, prefix))
			if err != nil {
				t.Fatalf("arg %q value is not an int: %v", a, err)
			}
			return v
		}
	}
	t.Fatalf("no arg with prefix %q in %v", prefix, args)
	return 0
}

// servicePorts maps each Service port number to its targetPort (string
// name or numeric string).
func servicePorts(t *testing.T, svc helm.Manifest) map[int]string {
	t.Helper()
	spec, _ := svc.Raw["spec"].(map[string]any)
	raw, _ := spec["ports"].([]any)
	out := map[int]string{}
	for _, p := range raw {
		pm, _ := p.(map[string]any)
		port, ok := asInt(pm["port"])
		if !ok {
			continue
		}
		out[port] = targetPortString(pm["targetPort"])
	}
	return out
}

func portTargetingListener(ports map[int]string, target string) (int, bool) {
	for port, tp := range ports {
		if tp == target {
			return port, true
		}
	}
	return 0, false
}

func targetPortString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.Itoa(int(t))
	default:
		return ""
	}
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		n, err := strconv.Atoi(t)
		return n, err == nil
	default:
		return 0, false
	}
}
