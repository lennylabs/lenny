// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
)

// spec: 10.3 (mTLS PKI) NET-072
func TestHolderForwardListenerValidatesTheGatewayPeer_spec_10_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	addr := startTLSServer(t, serverTLSFor(t, m, gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !Forwarded(r.Context()) {
				w.WriteHeader(http.StatusTeapot)
			}
		}))
	dial := func(certs []tls.Certificate) error {
		conn, err := tls.Dial("tcp", addr, &tls.Config{
			RootCAs: m.CAPool, ServerName: gatewaycontrol.GatewayDNSName, Certificates: certs, MinVersion: tls.VersionTLS13,
		})
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		// TLS 1.3 reports a client-certificate rejection on the first read.
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write([]byte("GET /x HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
			return err
		}
		_, err = conn.Read(make([]byte, 1))
		return err
	}
	load := func(f gatewaypki.LeafFiles) []tls.Certificate {
		c, err := tls.LoadX509KeyPair(f.CertFile, f.KeyFile)
		if err != nil {
			t.Fatalf("load leaf: %v", err)
		}
		return []tls.Certificate{c}
	}
	if err := dial(load(gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile})); err != nil {
		t.Errorf("gateway-SAN client rejected: %v", err)
	}
	if err := dial(nil); err == nil {
		t.Error("a client with no certificate was accepted")
	}
	if err := dial(load(m.Leaf(t, []string{"lenny-other.lenny-system.svc"}, nil))); err == nil {
		t.Error("a cluster-CA certificate with another DNS SAN was accepted")
	}
	spiffe := m.Leaf(t, nil, []*url.URL{{Scheme: "spiffe", Host: "lenny.local", Path: "/ns/lenny-agents/sa/agent"}})
	if err := dial(load(spiffe)); err == nil {
		t.Error("an agent SPIFFE certificate was accepted")
	}
	plain, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = plain.Close() }()
	_, _ = plain.Write([]byte("GET /x HTTP/1.1\r\nHost: x\r\n\r\n"))
	_ = plain.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf, _ := io.ReadAll(plain)
	if strings.Contains(string(buf), "HTTP/1.1 200") || strings.Contains(string(buf), "418") {
		t.Errorf("a plaintext request reached the handler: %q", buf)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 3, 10.3 (mTLS PKI) NET-072
func TestHolderForwardHandlerAcceptsNoOtherAssertedPrincipal_spec_29_3(t *testing.T) {
	var got authmw.Principal
	var forwarded bool
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = authmw.FromContext(r.Context())
		forwarded = Forwarded(r.Context())
		if r.Header.Get(CallingSessionHeader) != "" {
			t.Errorf("the calling-session header reached the handler")
		}
	})
	sessions := callingSessions{"sess-parent": {ID: "sess-parent", TenantID: "acme", UserID: "alice"}}
	h := NewHolderForwardHandler(inner, authmw.Options{RequireAuth: true}, sessions)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set(CallingSessionHeader, "sess-parent")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got.SessionID != "sess-parent" || got.CallerType != "agent" || !forwarded {
		t.Errorf("calling-session principal = %+v forwarded=%v, want the session's agent principal on a forwarded request", got, forwarded)
	}

	got = authmw.Principal{}
	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set(CallingSessionHeader, "sess-parent")
	req.Header.Set("Authorization", "Bearer forged")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got.SessionID != "" || rec.Code == http.StatusOK {
		t.Errorf("a credential plus a calling session ran as %+v with %d, want the credential authenticated and the header ignored", got, rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set(CallingSessionHeader, "sess-unknown")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown calling session answered %d, want 401", rec.Code)
	}
}

// failingSessions fails every read with a transient error.
type failingSessions struct{}

func (failingSessions) GetByID(context.Context, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, errors.New("store unavailable")
}

// spec: 15.1 (SERVICE_UNAVAILABLE), 11.5 (Idempotency) Mechanism, 29.3 (Interactive message send) Forwarding rule 3
func TestHolderForwardListenerAnswersATransientCallingSessionReadAsServiceUnavailable_spec_29_3(t *testing.T) {
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the handler ran without a resolved calling session")
	})
	h := NewHolderForwardHandler(inner, authmw.Options{RequireAuth: true}, failingSessions{})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set(CallingSessionHeader, "sess-parent")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if rec.Code != http.StatusServiceUnavailable || env.Error.Code != "SERVICE_UNAVAILABLE" {
		t.Errorf("transient read answered %d %s, want 503 SERVICE_UNAVAILABLE", rec.Code, env.Error.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("the 503 carries no Retry-After")
	}
	if rec.Header().Get(forwardNoStoreHeader) == "" {
		t.Error("the refusal carries no no-store mark")
	}

	// End to end, the forwarding replica returns the catalog code to the
	// agent and keeps the refusal out of its §11.5 cache.
	m := gatewaypki.Mint(t)
	addr := startTLSServer(t, serverTLSFor(t, m, gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}), h)
	s := forwardServer(leasesHeldBy("gw-b.00@"+addr), "gw-a.00@10.0.0.1:1", testForwardClient(t, m, time.Second))
	ctx := idemmw.WithGate(authmw.WithPrincipal(context.Background(), authmw.Principal{SessionID: "sess-parent", TenantID: "acme", CallerType: "agent"}))
	_, out, err := s.ForwardToolCall(ctx, forwardRow(), ForwardKindTerminate, "lenny/ok", nil)
	assertToolError(t, out, ForwardDelivered, err, "SERVICE_UNAVAILABLE")
	if !idemmw.NotStored(ctx) {
		t.Error("the transient refusal was left storable (§11.5)")
	}
}

// copyLeaf copies a leaf's certificate and key to path stems under dir, so a
// later rotation can overwrite them in place as cert-manager does.
func copyLeaf(t *testing.T, src gatewaypki.LeafFiles, dir, stem string) gatewaypki.LeafFiles {
	t.Helper()
	dst := gatewaypki.LeafFiles{CertFile: filepath.Join(dir, stem+".crt"), KeyFile: filepath.Join(dir, stem+".key")}
	overwriteLeaf(t, src, dst, time.Now())
	return dst
}

// overwriteLeaf replaces dst's certificate and key with src's and stamps both
// files with mod, so a reloader sees the modification time advance.
func overwriteLeaf(t *testing.T, src, dst gatewaypki.LeafFiles, mod time.Time) {
	t.Helper()
	for from, to := range map[string]string{src.CertFile: dst.CertFile, src.KeyFile: dst.KeyFile} {
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatalf("read %s: %v", from, err)
		}
		if err := os.WriteFile(to, b, 0o600); err != nil {
			t.Fatalf("write %s: %v", to, err)
		}
		if err := os.Chtimes(to, mod, mod); err != nil {
			t.Fatalf("chtimes %s: %v", to, err)
		}
	}
}

// leafSerial reads the serial number of a leaf certificate file.
func leafSerial(t *testing.T, f gatewaypki.LeafFiles) *big.Int {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(f.CertFile, f.KeyFile)
	if err != nil {
		t.Fatalf("load leaf: %v", err)
	}
	return pair.Leaf.SerialNumber
}

// spec: 10.3 (mTLS PKI) NET-072, Certificate authority
func TestHolderForwardCertificateRotationReachesListenerAndClient_spec_10_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	dir := t.TempDir()
	original := gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}
	serverFiles := copyLeaf(t, original, dir, "server")
	clientFiles := copyLeaf(t, original, dir, "client")

	var clientSerial atomic.Value
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientSerial.Store(r.TLS.PeerCertificates[0].SerialNumber.String())
		w.WriteHeader(http.StatusNoContent)
	})
	addr := startTLSServer(t, serverTLSFor(t, m, serverFiles), handler)
	clientReloader, err := certreload.New(clientFiles.CertFile, clientFiles.KeyFile)
	if err != nil {
		t.Fatalf("certreload: %v", err)
	}
	var d net.Dialer
	client := newHolderForwardClient(holderForwardClientTLS(clientReloader, m.CAPool), HolderForwardDialBound, d.DialContext)

	handshake := func() (serverLeaf, clientLeaf string) {
		t.Helper()
		resp, err := client.Get("https://" + addr + "/rotate")
		if err != nil {
			t.Fatalf("forward request: %v", err)
		}
		_ = resp.Body.Close()
		client.CloseIdleConnections()
		got, _ := clientSerial.Load().(string)
		return resp.TLS.PeerCertificates[0].SerialNumber.String(), got
	}
	want := leafSerial(t, original).String()
	if s, c := handshake(); s != want || c != want {
		t.Fatalf("before rotation the listener presented %s and the client %s, want %s for both", s, c, want)
	}

	rotated := m.Leaf(t, []string{gatewaycontrol.GatewayDNSName}, nil)
	later := time.Now().Add(time.Minute)
	overwriteLeaf(t, rotated, serverFiles, later)
	overwriteLeaf(t, rotated, clientFiles, later)
	want = leafSerial(t, rotated).String()
	if s, c := handshake(); s != want || c != want {
		t.Errorf("after rotation the listener presented %s and the client %s, want the new leaf %s for both", s, c, want)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 4, 11.5 (Idempotency), 11.1 (Rate Limiting)
func TestHolderForwardListenerAppliesNoIdempotencyOrRateLimit_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	var calls atomic.Int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Idempotency-Key") != "k-1" {
			t.Errorf("Idempotency-Key = %q, want the header passed through untouched", r.Header.Get("Idempotency-Key"))
		}
		w.WriteHeader(http.StatusCreated)
	})
	h := NewHolderForwardHandler(inner, authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}, callingSessions{})
	addr := startTLSServer(t, serverTLSFor(t, m, gatewaypki.LeafFiles{CertFile: m.CertFile, KeyFile: m.KeyFile}), h)
	client := testForwardClient(t, m, time.Second)

	const attempts = 25
	for i := 0; i < attempts; i++ {
		req, err := http.NewRequest(http.MethodPost, "https://"+addr+"/v1/sessions/sess-1/messages", strings.NewReader(`{"messages":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Idempotency-Key", "k-1")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("attempt %d answered %d, want the handler's 201 on every attempt", i, resp.StatusCode)
		}
		if resp.Header.Get("Idempotent-Replayed") != "" {
			t.Errorf("attempt %d was answered from an idempotency cache", i)
		}
	}
	if n := calls.Load(); n != attempts {
		t.Errorf("handler reached %d times, want %d: a forwarded request is neither idempotency-cached nor rate-limited", n, attempts)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 4, 11.5 (Idempotency) Mechanism
func TestHolderForwardListenerMarksOnlyAGatedRefusal_spec_11_5(t *testing.T) {
	cases := []struct {
		name     string
		refuse   bool
		wantMark bool
	}{
		{"a handler that refuses under the gate is marked", true, true},
		{"a delivered response is not marked", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.refuse {
					idemmw.DoNotStore(r.Context())
				}
				w.WriteHeader(http.StatusConflict)
			})
			h := NewHolderForwardHandler(inner, authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}, callingSessions{})
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-1/resume", nil)
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if got := rec.Header().Get(forwardNoStoreHeader) != ""; got != c.wantMark {
				t.Errorf("no-store mark present = %v, want %v", got, c.wantMark)
			}
		})
	}
}
