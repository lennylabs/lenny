// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/tlsgen"
)

func TestStartTLSProxyForwardsToUpstream(t *testing.T) {
	// A plain-HTTP backend stands in for the gateway.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "from-upstream:"+r.URL.Path)
	}))
	defer backend.Close()

	mat, err := tlsgen.Generate(t.TempDir())
	if err != nil {
		t.Fatalf("tlsgen.Generate: %v", err)
	}
	listen := "127.0.0.1:" + freePort(t)
	proxy, err := startTLSProxy(listen, backend.URL, mat.CertPath, mat.KeyPath)
	if err != nil {
		t.Fatalf("startTLSProxy: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = proxy.Shutdown(ctx)
	}()

	caPEM, err := os.ReadFile(mat.CACertPath)
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("AppendCertsFromPEM failed")
	}
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		Timeout:   5 * time.Second,
	}
	// The HTTPS request must terminate at the proxy and forward to the
	// plain-HTTP backend.
	resp, err := client.Get("https://localhost:" + portOf(listen) + "/probe")
	if err != nil {
		t.Fatalf("HTTPS GET through proxy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "from-upstream:/probe" {
		t.Errorf("proxy returned %q, want the upstream response", string(body))
	}
}

func TestStartTLSProxyRejectsNonLoopback(t *testing.T) {
	mat, err := tlsgen.Generate(t.TempDir())
	if err != nil {
		t.Fatalf("tlsgen.Generate: %v", err)
	}
	// §17.4 EMBEDDED_MODE_LOCAL_ONLY: a non-loopback bind must fail
	// closed.
	_, err = startTLSProxy("0.0.0.0:9443", "http://127.0.0.1:8080", mat.CertPath, mat.KeyPath)
	if err == nil {
		t.Fatal("expected startTLSProxy to reject a 0.0.0.0 bind")
	}
	if got := err.Error(); !contains(got, "EMBEDDED_MODE_LOCAL_ONLY") {
		t.Errorf("error %q does not carry the EMBEDDED_MODE_LOCAL_ONLY code", got)
	}
}

// freePort returns a currently-free loopback TCP port as a string.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := listenLoopback()
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return portOf(ln.Addr().String())
}

// TestStartTCPRelayForwardsToNodePort asserts the §17.4 127.0.0.1:8080 HTTP
// leg is a plain TCP relay that forwards bytes to the gateway node port. A
// plain-HTTP backend stands in for the in-cluster gateway behind the node
// port; an HTTP request over the relay must reach it and return its response,
// proving the raw byte relay carries the plaintext-HTTP traffic without
// terminating TLS or parsing HTTP.
//
// spec: §17.4 (the 127.0.0.1:8080 HTTP leg is a plain TCP relay to the same
// node port).
func TestStartTCPRelayForwardsToNodePort(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "from-nodeport:"+r.URL.Path)
	}))
	defer backend.Close()
	upstream := backend.Listener.Addr().String()

	listen := "127.0.0.1:" + freePort(t)
	relay, err := startTCPRelay(listen, upstream)
	if err != nil {
		t.Fatalf("startTCPRelay: %v", err)
	}
	defer func() { _ = relay.Shutdown(context.Background()) }()

	client := &http.Client{Timeout: 5 * time.Second}
	// A plain-HTTP request to the relay (the 8080 leg) must reach the backend
	// standing in for the gateway node port and return its response verbatim.
	resp, err := client.Get("http://" + listen + "/relayed")
	if err != nil {
		t.Fatalf("HTTP GET through relay: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "from-nodeport:/relayed" {
		t.Errorf("relay returned %q, want the node-port response", string(body))
	}
}

// TestStartTCPRelayDropsClientWhenUpstreamUnreachable asserts the relay closes
// an accepted client connection rather than hanging when the gateway node port
// is unreachable. The CLI then observes a connection failure, the honest result
// when the in-cluster gateway is not answering, rather than a stalled dial.
//
// spec: §17.4 (the HTTP relay forwards to the gateway node port; an
// unreachable node port surfaces a connection failure).
func TestStartTCPRelayDropsClientWhenUpstreamUnreachable(t *testing.T) {
	// Reserve a loopback port and close it so the upstream dial is refused.
	deadUpstream := "127.0.0.1:" + freePort(t)

	listen := "127.0.0.1:" + freePort(t)
	relay, err := startTCPRelay(listen, deadUpstream)
	if err != nil {
		t.Fatalf("startTCPRelay: %v", err)
	}
	defer func() { _ = relay.Shutdown(context.Background()) }()

	conn, err := net.DialTimeout("tcp", listen, 2*time.Second)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	defer func() { _ = conn.Close() }()
	// Writing to the relay then reading must observe EOF once the relay drops
	// the connection on the failed upstream dial, rather than blocking forever.
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
	buf := make([]byte, 1)
	if _, rerr := conn.Read(buf); rerr == nil {
		t.Error("relay returned data with an unreachable upstream; want the connection dropped")
	}
}

// TestStartTCPRelayRejectsNonLoopback asserts the HTTP leg carries the same
// §17.4 EMBEDDED_MODE_LOCAL_ONLY fail-closed bind check the TLS leg holds: a
// non-loopback bind must fail closed so the HTTP leg cannot expose the gateway
// outside localhost.
//
// spec: §17.4 (EMBEDDED_MODE_LOCAL_ONLY: the host-side forwarder binds
// loopback only; both legs fail closed on a non-loopback bind).
func TestStartTCPRelayRejectsNonLoopback(t *testing.T) {
	_, err := startTCPRelay("0.0.0.0:9080", "127.0.0.1:30080")
	if err == nil {
		t.Fatal("expected startTCPRelay to reject a 0.0.0.0 bind")
	}
	if got := err.Error(); !contains(got, "EMBEDDED_MODE_LOCAL_ONLY") {
		t.Errorf("error %q does not carry the EMBEDDED_MODE_LOCAL_ONLY code", got)
	}
}

// TestTCPRelayShutdownClosesConnections asserts Shutdown stops the relay so a
// subsequent dial to the host port fails. Stack.Stop calls this to tear the
// HTTP leg down alongside the TLS proxy.
//
// spec: §17.4 (the host-side forwarder is torn down with the stack).
func TestTCPRelayShutdownClosesConnections(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	listen := "127.0.0.1:" + freePort(t)
	relay, err := startTCPRelay(listen, backend.Listener.Addr().String())
	if err != nil {
		t.Fatalf("startTCPRelay: %v", err)
	}
	if err := relay.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// After Shutdown the listener is closed, so a dial to the host port is
	// refused rather than answered.
	conn, err := net.DialTimeout("tcp", listen, 500*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatal("dial to the relay host port succeeded after Shutdown; want connection refused")
	}
	// A double Shutdown is a no-op, matching the proxy leg.
	if err := relay.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
}
