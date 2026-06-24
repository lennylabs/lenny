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
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
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
