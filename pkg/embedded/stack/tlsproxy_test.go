// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
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
