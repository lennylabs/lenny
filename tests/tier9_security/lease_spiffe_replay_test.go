// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §4.9 SPIFFE-binding cross-pod lease-token
// replay defense. A proxy-mode lease is bound at mint time to the issuing
// pod's SPIFFE identity; on every LLM proxy request the gateway derives
// the peer SPIFFE URI from the authenticated mTLS connection and rejects
// a request whose identity does not match the lease's bound URI. This
// test exercises that derivation against real mTLS peer certificates
// carrying distinct spiffe:// URI SANs, which the existing unit test does
// not do: TestHandlerRejectsSpiffeMismatch drives a plaintext request
// whose peer identity is empty, so it never runs the peer-certificate
// extraction path the cross-pod replay defense depends on.
//
// The scenario mints a lease for pod A, then replays pod A's opaque lease
// token from a second client whose mTLS leaf carries pod B's SPIFFE URI,
// and asserts the gateway rejects the replay with LEASE_SPIFFE_MISMATCH.
// A matching-identity control (pod A presenting its own leaf) confirms the
// binding admits the legitimate holder, so the rejection is attributable
// to the identity mismatch and not to mTLS setup.

package tier9_security_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

const (
	podASpiffeURI = "spiffe://lenny-test/agent/claude-prod/pod-a"
	podBSpiffeURI = "spiffe://lenny-test/agent/claude-prod/pod-b"
)

// spec: 4.9 (SPIFFE-binding for proxy-mode lease tokens: "On every LLM
//
//	proxy request, the gateway extracts the peer SPIFFE URI from the
//	authenticated mTLS connection and verifies it matches the SPIFFE URI
//	stored in the lease record. A mismatch is rejected with
//	LEASE_SPIFFE_MISMATCH (category: SECURITY)."), 13.5 (per-pod mTLS
//	identity)
//
// diagnosis: the §4.9 cross-pod lease-token replay defense regressed. A
//
//	lease token captured from one agent pod was accepted when replayed
//	over mTLS from a second pod bearing a different SPIFFE identity, so a
//	memory-extracted lease token is no longer contained to its issuing
//	pod. Either the gateway stopped deriving the peer SPIFFE URI from the
//	authenticated mTLS connection (peerSPIFFE returning empty), or the
//	per-request SPIFFE-binding check stopped comparing it against the
//	lease's bound URI.
func TestLeaseSpiffeReplayRejectedOverMTLS(t *testing.T) {
	upstream := llmprovider.New(t)

	// Mint a pool-backed proxy lease bound to pod A's SPIFFE identity
	// through the production assignment service, exactly as the gateway
	// records the issuing pod's SPIFFE URI at AssignCredentials time.
	leases := credleasestore.New()
	creds := credcache.New()
	assign := credassign.New(leases, creds)
	const poolName = "claude-prod"
	assign.RegisterPool(credassign.Pool{
		Name:         poolName,
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryProxy,
		Strategy:     credential.StrategyLeastLoaded,
		ProxyURL:     "https://lenny-llm-proxy.internal/llm-proxy",
		ProxyDialect: string(credential.ProxyDialectAnthropic),
		Credentials: []credassign.PoolCredential{
			{ID: "cred-1", APIKey: "sk-upstream-real-secret-do-not-leak", Healthy: true},
		},
	})
	lease, err := assign.Assign(poolName, "s-spiffe-replay", podASpiffeURI, "acme")
	if err != nil {
		t.Fatalf("mint proxy lease: %v", err)
	}
	if lease.Proxy == nil || lease.Proxy.LeaseToken == "" {
		t.Fatalf("minted lease carries no proxy token: %+v", lease)
	}
	if lease.SpiffeURI != podASpiffeURI {
		t.Fatalf("lease not bound to issuing pod SPIFFE URI: got %q want %q", lease.SpiffeURI, podASpiffeURI)
	}
	leaseToken := lease.Proxy.LeaseToken

	// Serve the production proxy handler behind a real mTLS listener that
	// requires and verifies a client certificate, so the handler's peer
	// SPIFFE derivation runs against an authenticated connection. The
	// handler wiring mirrors cmd/lenny-gateway's newLLMProxyServer.
	caCert, caKey := spiffeReplayCA(t)
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(caCert)

	handler := &llmproxy.Handler{
		Leases: leases,
		Translators: llmproxy.NewTranslatorRegistry(
			&llmproxy.AnthropicDirectTranslator{
				BaseURL:                 upstream.URL(),
				DefaultAnthropicVersion: "2023-06-01",
			},
		),
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: creds,
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCAs,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	// Trust the httptest server's self-signed serving certificate.
	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(srv.Certificate())

	// The cross-pod replay: pod B presents a valid mTLS leaf carrying its
	// own SPIFFE identity and replays pod A's captured lease token. The
	// gateway must reject it — the peer identity does not match the lease.
	podBResp := postSpiffeProxy(t, srv.URL, serverRoots,
		issueSpiffeClientCert(t, caCert, caKey, podBSpiffeURI), leaseToken)
	defer podBResp.Body.Close()
	if podBResp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-pod replay: status %d, want 403", podBResp.StatusCode)
	}
	if code := spiffeProxyErrorCode(t, podBResp); code != "LEASE_SPIFFE_MISMATCH" {
		t.Fatalf("cross-pod replay: error code %q, want LEASE_SPIFFE_MISMATCH", code)
	}

	// Matching-identity control: pod A presents its own leaf and its own
	// lease token. The binding admits the legitimate holder, so the
	// request passes the SPIFFE check and reaches the upstream (200). This
	// proves the rejection above is attributable to the identity mismatch
	// rather than to the mTLS handshake or lease resolution.
	podAResp := postSpiffeProxy(t, srv.URL, serverRoots,
		issueSpiffeClientCert(t, caCert, caKey, podASpiffeURI), leaseToken)
	defer podAResp.Body.Close()
	if podAResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(podAResp.Body)
		t.Fatalf("matching-identity request: status %d, want 200; body %s", podAResp.StatusCode, body)
	}

	// The credential.lease_spiffe_mismatch audit event §4.9 names on this
	// rejection is not asserted here: the gateway does not yet emit the
	// §4.9.2 credential audit-event catalogue, so no EventStore row exists
	// to observe. Extend this test to assert the audit row once credential
	// audit-event emission lands in the product.
	t.Run("audit event recorded in EventStore", func(t *testing.T) {
		t.Skip("credential.lease_spiffe_mismatch audit-event emission is not yet implemented in the gateway; assert the hash-chained audit_log row once the credential audit-event catalogue is emitted")
	})
}

// postSpiffeProxy issues one Anthropic-dialect proxy request over mTLS,
// presenting clientCert as the connection identity and leaseToken as the
// pod's x-api-key. The caller closes the returned response body.
func postSpiffeProxy(t *testing.T, baseURL string, serverRoots *x509.CertPool, clientCert tls.Certificate, leaseToken string) *http.Response {
	t.Helper()
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      serverRoots,
				Certificates: []tls.Certificate{clientCert},
			},
		},
	}
	body := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build proxy request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", leaseToken)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("issue proxy request: %v", err)
	}
	return resp
}

// spiffeProxyErrorCode decodes the proxy's {"error":{"code":...}} envelope.
func spiffeProxyErrorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode proxy error envelope: %v", err)
	}
	return env.Error.Code
}

// spiffeReplayCA mints a short-lived CA that signs the agent-pod mTLS
// client leaves this test presents to the proxy listener.
func spiffeReplayCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "lenny-agent-mtls-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return cert, key
}

// issueSpiffeClientCert signs an mTLS client leaf carrying spiffeURI as a
// URI SAN, the form the gateway's peer-SPIFFE derivation reads from the
// authenticated connection.
func issueSpiffeClientCert(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, spiffeURI string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	u, err := url.Parse(spiffeURI)
	if err != nil {
		t.Fatalf("parse SPIFFE URI %q: %v", spiffeURI, err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: spiffeURI},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{u},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create client certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
