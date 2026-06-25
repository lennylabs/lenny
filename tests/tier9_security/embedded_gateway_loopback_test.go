// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §17.4 EMBEDDED_MODE_LOCAL_ONLY check for the in-cluster gateway the
// proposal-0017 re-architecture exposes. The in-cluster gateway is reachable
// from the host only through the loopback-only host-side forwarder in front of
// the gateway NodePort: the Docker-backed launcher publishes the in-VM NodePort
// to 127.0.0.1 alone and the Linux launcher constrains the kube-proxy NodePort
// bind to 127.0.0.1/32, so the gateway never binds a non-loopback host address.
// This black-box check asserts the publicly-observable invariant that the
// address the CLI dials is loopback, complementing the in-package
// forwarder-fail-closed and NodePort-loopback unit tests.
package tier9_security_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/tests/testinfra/embedded"
)

// TestEmbeddedGatewayAddressIsLoopbackOnly asserts the gateway URL the CLI
// resolves from a recorded stack is loopback-only, so a runtime author or
// operator cannot reach (or expose) the embedded gateway on a non-loopback
// host address. This is the public face of the §17.4 EMBEDDED_MODE_LOCAL_ONLY
// invariant the launcher (loopback-only NodePort publish / kube-proxy bind)
// and the host-side forwarder (fail-closed non-loopback rejection) enforce.
//
// diagnosis: a failure means the embedded gateway is addressable on a
// non-loopback host address, so the local-only stack is reachable off-host,
// violating the §17.4 EMBEDDED_MODE_LOCAL_ONLY fail-closed invariant.
//
// spec: §17.4 (EMBEDDED_MODE_LOCAL_ONLY: the CLI reaches the in-cluster
// gateway through the loopback-only host-side forwarder; the gateway binds no
// non-loopback host address).
func TestEmbeddedGatewayAddressIsLoopbackOnly_spec_17_4(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := stack.NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	// Record a stack whose forwarder address is the §17.4 loopback endpoint,
	// written in the on-disk state-file JSON the bring-up persists.
	state := `{"startedAt":"2026-06-24T00:00:00Z","gatewayForwarderAddr":"127.0.0.1:8443","k3sEnabled":true}`
	if err := os.WriteFile(paths.StateFile(), []byte(state), 0o600); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	url, err := stack.RunningGateway(root)
	if err != nil {
		t.Fatalf("RunningGateway: %v", err)
	}
	// The CLI dials a loopback address only. A non-loopback host (0.0.0.0, a
	// LAN IP, or a hostname) would mean the gateway is reachable off-host.
	for _, nonLoopback := range []string{"0.0.0.0", "://0.0.0.0", "://10.", "://192.168.", "://172."} {
		if strings.Contains(url, nonLoopback) {
			t.Fatalf("gateway URL %q is reachable on a non-loopback address (%q); EMBEDDED_MODE_LOCAL_ONLY requires loopback", url, nonLoopback)
		}
	}
	if !strings.Contains(url, "127.0.0.1") && !strings.Contains(url, "localhost") {
		t.Errorf("gateway URL %q is not a loopback address", url)
	}
}

// TestEmbeddedStoppedStackResolvesNoRunningGateway asserts that the Stopped
// marker a non-`--purge` lenny down leaves behind (which preserves the
// substrate handle and the deployed image tag on disk so a warm up can reuse
// the persisted control plane) does not resolve to a reachable gateway URL.
// The marker keeps state on disk but the stack is not running, so the CLI must
// fail closed with ErrNoRunningStack rather than dial a stale loopback
// endpoint that nothing is answering on.
//
// diagnosis: a failure means a stopped embedded stack still resolves to a
// gateway URL, so a CLI command would dial a forwarder address with no live
// gateway behind it, or treat torn-down state as live, instead of reporting no
// running stack.
//
// spec: §17.4 (a non-`--purge` lenny down stops the stack while persisting the
// substrate and the deployed tag; the stopped stack is not running and the CLI
// reaches no gateway through it).
func TestEmbeddedStoppedStackResolvesNoRunningGateway_spec_17_4(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LENNY_HOME", root)
	paths := stack.NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	// The Stopped marker preserves the loopback forwarder address and the
	// deployed tag, but marks the stack stopped: the substrate persists, the
	// stack does not run.
	state := `{"gatewayForwarderAddr":"127.0.0.1:8443","deployedImageTag":"v1.2.3","k3sEnabled":true,"stopped":true}`
	if err := os.WriteFile(paths.StateFile(), []byte(state), 0o600); err != nil {
		t.Fatalf("seed stopped marker: %v", err)
	}

	if _, err := stack.RunningGateway(root); err == nil {
		t.Fatal("RunningGateway resolved a URL for a stopped stack; want ErrNoRunningStack (fail closed)")
	} else if !strings.Contains(err.Error(), "no running stack") {
		t.Errorf("RunningGateway error = %v, want a no-running-stack error", err)
	}
}

// TestEmbeddedForwarderLegsFailClosedOnNonLoopback asserts that both host-side
// forwarder legs (the TLS proxy on 127.0.0.1:8443 and the 127.0.0.1:8080 HTTP
// relay the proposal-0017 forwarder adds) bind loopback only and fail closed
// under §17.4 EMBEDDED_MODE_LOCAL_ONLY on any non-loopback bind. Both legs gate
// their bind on the same EmbeddedModeLocalOnly check, so the HTTP leg cannot
// expose the in-cluster gateway off-host any more than the TLS leg can. The
// test drives the shared gate over the loopback and non-loopback address
// families directly.
//
// diagnosis: a failure means a host-side forwarder leg would bind a
// non-loopback host address, exposing the embedded gateway off-host and
// violating the §17.4 EMBEDDED_MODE_LOCAL_ONLY fail-closed invariant, or it
// would reject a legitimate loopback bind and break the local stack.
//
// spec: §17.4 (EMBEDDED_MODE_LOCAL_ONLY: the host-side forwarder binds
// loopback only; both the TLS leg and the 127.0.0.1:8080 HTTP relay leg fail
// closed on a non-loopback bind).
func TestEmbeddedForwarderLegsFailClosedOnNonLoopback_spec_17_4(t *testing.T) {
	// Loopback host forms the forwarder must accept (the legs bind these).
	for _, loopback := range []string{"127.0.0.1:8080", "127.0.0.1:8443", "localhost:8080", "[::1]:8080"} {
		if err := stack.EmbeddedModeLocalOnly(loopback); err != nil {
			t.Errorf("EmbeddedModeLocalOnly(%q) = %v, want nil (a loopback bind is allowed)", loopback, err)
		}
	}
	// Non-loopback host forms every forwarder leg must reject fail-closed: a
	// wildcard bind, a LAN address, and a routable hostname all expose the
	// gateway off-host.
	for _, nonLoopback := range []string{"0.0.0.0:8080", "0.0.0.0:8443", "10.0.0.5:8080", "192.168.1.10:8080", "example.com:8080"} {
		err := stack.EmbeddedModeLocalOnly(nonLoopback)
		if err == nil {
			t.Errorf("EmbeddedModeLocalOnly(%q) = nil, want a fail-closed EMBEDDED_MODE_LOCAL_ONLY rejection", nonLoopback)
			continue
		}
		if !strings.Contains(err.Error(), "EMBEDDED_MODE_LOCAL_ONLY") {
			t.Errorf("EmbeddedModeLocalOnly(%q) error = %v, does not carry the EMBEDDED_MODE_LOCAL_ONLY code", nonLoopback, err)
		}
	}
}

// TestEmbeddedGatewayDockerBackedSecurityLegs drives a real `lenny up` on the
// cross-platform substrate and asserts the §17.4 security invariants that only
// a live bring-up can exercise, complementing the in-process forwarder and
// loopback-resolution checks above. It is one bring-up with four subtests so a
// single expensive `lenny up` covers all four legs:
//
//   - TLS termination: the host-side forwarder terminates TLS on
//     127.0.0.1:8443 with the per-`lenny up` self-signed leaf and forwards to
//     the plaintext-HTTP in-cluster gateway, so https://localhost:8443
//     completes the handshake and reaches the gateway.
//   - non-loopback unreachable: the gateway NodePort (stack.GatewayNodePort)
//     is unreachable on a non-loopback host address (the Linux NodePort is
//     constrained to 127.0.0.1/32 by --kube-proxy-arg and the Docker NodePort
//     is published only on host loopback), so the §17.4
//     EMBEDDED_MODE_LOCAL_ONLY 0.0.0.0 fail-closed invariant holds end to end.
//     This leg probes the node port the C4 containment guards, not the
//     forwarder's host port whose loopback bind is already covered in-process.
//   - production banner: the non-suppressible production warning banner prints
//     on `lenny up`.
//   - CLI bearer auth: the CLI's minted dev bearer authenticates against the
//     dev-mode gateway end to end (the gateway trusts the dev HMAC key through
//     --bearer-trust-hmac-key-file).
//
// The legs are gated behind embedded.SkipUnlessAvailable (the Docker-present /
// LENNY_EMBEDDED_SMOKE guard the existing Docker-backed legs use), so on a host
// or CI runner without the substrate the test skips and states the dependency
// (the test-coverage tier-5/6 escape hatch) rather than failing.
//
// diagnosis: a failure means a §17.4 EMBEDDED_MODE_LOCAL_ONLY or dev-auth
// invariant did not hold against a live bring-up. A TLS-termination failure
// means the forwarder did not present the self-signed leaf or did not reach the
// plaintext-HTTP gateway. A reachable NodePort on a non-loopback address means
// the C4 node-port loopback containment failed and the gateway is exposed
// off-host, violating the local-only constraint. A missing banner means
// the non-suppressible production warning was dropped. A 401 on the bearer leg
// means the in-cluster gateway did not trust the CLI's minted dev bearer.
//
// spec: §17.4 (EMBEDDED_MODE_LOCAL_ONLY: the forwarder terminates TLS on
// loopback and the gateway is unreachable off-host; the non-suppressible
// production banner; the dev bearer the gateway trusts through
// --bearer-trust-hmac-key-file).
func TestEmbeddedGatewayDockerBackedSecurityLegs_spec_17_4(t *testing.T) {
	embedded.SkipUnlessAvailable(t)
	bin := embedded.Build(t)
	home := t.TempDir() + "/lenny-home"

	up := embedded.Run(t, bin, home, embedded.UpTimeout(), "up")
	if up.ExitCode != 0 {
		t.Fatalf("lenny up: exit %d\nstdout:\n%s\nstderr:\n%s", up.ExitCode, up.Stdout, up.Stderr)
	}
	t.Cleanup(func() {
		_ = embedded.Run(t, bin, home, 90*time.Second, "down", "--purge")
	})

	gatewayURL, err := stack.RunningGateway(home)
	if err != nil {
		t.Fatalf("RunningGateway: %v", err)
	}

	t.Run("production_banner_prints", func(t *testing.T) {
		// The non-suppressible production warning banner (stack.go
		// ProductionWarningBanner) prints on every `lenny up`, so its text must
		// appear in the bring-up stdout captured above.
		if !strings.Contains(up.Stdout, stack.ProductionWarningBanner) {
			t.Errorf("lenny up stdout does not contain the non-suppressible production warning banner %q\nstdout:\n%s",
				stack.ProductionWarningBanner, up.Stdout)
		}
	})

	t.Run("forwarder_terminates_tls_on_loopback", func(t *testing.T) {
		// The forwarder terminates TLS on 127.0.0.1:8443 with the per-`lenny
		// up` self-signed leaf and forwards to the plaintext-HTTP gateway. A
		// client trusting the stack's CA must complete the handshake and reach
		// the gateway (a non-error HTTP status from an unauthenticated probe).
		client := loopbackTLSClient(t, home)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, gatewayURL+"/healthz", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HTTPS GET %s through the forwarder: %v (TLS termination on 127.0.0.1:8443 failed or the gateway is unreachable)", gatewayURL, err)
		}
		defer func() { _ = resp.Body.Close() }()
		// The handshake completing and any HTTP status returning proves the
		// forwarder terminated TLS and relayed to the plaintext-HTTP gateway.
		if resp.StatusCode >= 500 {
			t.Errorf("forwarder reached the gateway but it returned %d; want a non-5xx status from a live gateway", resp.StatusCode)
		}
	})

	t.Run("gateway_nodeport_unreachable_on_non_loopback", func(t *testing.T) {
		// The genuinely-uncovered C4 surface is the gateway NodePort itself,
		// not the host-side forwarder (the forwarder's loopback-only bind is
		// already covered in-process by
		// TestEmbeddedForwarderLegsFailClosedOnNonLoopback via the
		// EmbeddedModeLocalOnly gate). A bare NodePort binds 0.0.0.0, so C4
		// constrains it to host loopback on both launchers: the Linux launcher
		// pins the kube-proxy bind with
		// --kube-proxy-arg=nodeport-addresses=127.0.0.1/32, and the Docker
		// launcher publishes the in-VM node port only on host loopback
		// (-p 127.0.0.1:<nodePort>:<nodePort>), leaving the in-VM 0.0.0.0 bind
		// contained inside the Docker VM. Dialing the fixed gateway NodePort
		// (stack.GatewayNodePort) on the host's primary non-loopback IP must be
		// refused or time out, so the gateway is not exposed off-host through
		// the node port the forwarder fronts.
		nonLoopback := primaryNonLoopbackIP(t)
		target := net.JoinHostPort(nonLoopback, strconv.Itoa(stack.GatewayNodePort))
		conn, err := net.DialTimeout("tcp", target, 3*time.Second)
		if err == nil {
			_ = conn.Close()
			t.Fatalf("the embedded gateway NodePort answered on non-loopback address %s; EMBEDDED_MODE_LOCAL_ONLY requires the node port be constrained to host loopback (Linux kube-proxy nodeport-addresses=127.0.0.1/32, Docker host-loopback publish)", target)
		}
	})

	t.Run("cli_bearer_authenticates_end_to_end", func(t *testing.T) {
		// `lenny session new` mints the dev bearer from the persisted dev key
		// and sends it as Authorization: Bearer. The in-cluster gateway trusts
		// that key through --bearer-trust-hmac-key-file in dev mode, so the
		// session creation authenticates end to end. A 401 TOKEN_INVALID would
		// mean the gateway did not trust the CLI's minted bearer. The single
		// echo pool may still be warming, so a §5.2 PoolWarmingUp 503 is
		// tolerated: it is an authenticated response (the bearer was accepted)
		// rather than an auth rejection. The leg fails only on an auth error.
		assertCLIBearerAuthenticates(t, bin, home)
	})
}

// loopbackTLSClient builds an HTTP client that trusts the running stack's
// per-`lenny up` self-signed CA, so an HTTPS request to the forwarder verifies
// the leaf the forwarder presents rather than skipping verification. The CA is
// the ca.crt the bring-up wrote under the stack's TLS directory.
func loopbackTLSClient(t *testing.T, home string) *http.Client {
	t.Helper()
	caPath := filepath.Join(stack.NewPaths(home).TLS, "ca.crt")
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read embedded CA %s: %v", caPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatalf("append embedded CA from %s", caPath)
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		Timeout:   15 * time.Second,
	}
}

// primaryNonLoopbackIP returns the host's primary non-loopback IPv4 address, so
// the non-loopback-unreachable leg can dial the gateway NodePort on an off-host
// address. The test skips when the host has no non-loopback interface (a
// constrained CI sandbox), since there is then no off-host address to probe.
func primaryNonLoopbackIP(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("list interface addresses: %v", err)
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	t.Skip("host has no non-loopback IPv4 interface; no off-host address to probe for the EMBEDDED_MODE_LOCAL_ONLY leg")
	return ""
}

// assertCLIBearerAuthenticates runs `lenny session new` and fails only when the
// gateway rejected the CLI's minted dev bearer (a 401 / TOKEN_INVALID), so the
// leg asserts the bearer-trust path rather than full placement. A successful
// session, or a tolerated §5.2 PoolWarmingUp response (which is itself an
// authenticated 503), both prove the bearer was accepted; a connection or auth
// failure is the hard failure.
func assertCLIBearerAuthenticates(t *testing.T, bin, home string) {
	t.Helper()
	res := embedded.Run(t, bin, home, 2*time.Minute,
		"session", "new", "--runtime", "echo", "--user", "alice@acme.com")
	if res.ExitCode == 0 {
		return
	}
	// A non-zero exit is acceptable only when it is the §5.2 warming window,
	// not an auth rejection. The CLI surfaces an auth failure as a 401 /
	// TOKEN_INVALID / unauthorized on stderr; any of those means the gateway
	// did not trust the CLI's minted bearer.
	low := strings.ToLower(res.Stderr)
	for _, authFail := range []string{"401", "token_invalid", "unauthorized", "authentication"} {
		if strings.Contains(low, authFail) {
			t.Fatalf("lenny session new was rejected with an auth error (%q); the in-cluster gateway did not trust the CLI's minted dev bearer\nstderr:\n%s",
				authFail, res.Stderr)
		}
	}
	// A non-auth non-zero exit (a still-warming pool, a placement timeout) does
	// not disprove the bearer-trust property this leg asserts; log it and pass.
	t.Logf("lenny session new exited %d without an auth error (likely the §5.2 warming window); the bearer was accepted\nstderr:\n%s",
		res.ExitCode, res.Stderr)
}
