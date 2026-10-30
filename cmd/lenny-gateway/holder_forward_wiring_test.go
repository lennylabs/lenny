// SPDX-License-Identifier: MIT

package main

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
)

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func TestCoordinationHolderCarriesTheAdvertiseAddress_spec_10_1_1(t *testing.T) {
	holder, err := coordinationHolder("gw-a", "10.0.0.7:50054", true)
	if err != nil {
		t.Fatalf("coordinationHolder err = %v", err)
	}
	if !strings.HasPrefix(holder, "gw-a.") {
		t.Errorf("holder %q does not start with the replica identity", holder)
	}
	if addr, ok := sessionserver.HolderAddress(holder); !ok || addr != "10.0.0.7:50054" {
		t.Errorf("HolderAddress(%q) = %q, %v; want 10.0.0.7:50054", holder, addr, ok)
	}
	if _, err := coordinationHolder("gw-a", "", true); err == nil || !strings.Contains(err.Error(), "--holder-forward-advertise-addr") {
		t.Errorf("lease store without an advertise address: err = %v, want the startup refusal naming the flag", err)
	}
	if _, err := coordinationHolder("gw-a", "10.0.0.7", true); err == nil {
		t.Error("an advertise address without a port was accepted")
	}
	if got, err := coordinationHolder("gw-a", "", false); err != nil || got != "gw-a" {
		t.Errorf("no lease store and no address = %q, %v; want the bare identity", got, err)
	}
}

// spec: 10.3 (mTLS PKI) NET-072
func TestHolderForwardMaterialRefusalOutsideDevMode_spec_10_3(t *testing.T) {
	complete := holderForwardMaterial{cert: "c", key: "k", ca: "ca"}
	for _, m := range []holderForwardMaterial{{}, {cert: "c", key: "k"}, {cert: "c", ca: "ca"}, {key: "k", ca: "ca"}} {
		if err := validateHolderForwardMaterial(m, false, true); err == nil || !strings.Contains(err.Error(), "NET-072") {
			t.Errorf("material %+v outside dev mode with a lease store: err = %v, want the NET-072 refusal", m, err)
		}
		if err := validateHolderForwardMaterial(m, true, true); err != nil {
			t.Errorf("material %+v in dev mode: err = %v, want none", m, err)
		}
		if err := validateHolderForwardMaterial(m, false, false); err != nil {
			t.Errorf("material %+v without a lease store: err = %v, want none", m, err)
		}
	}
	if err := validateHolderForwardMaterial(complete, false, true); err != nil {
		t.Errorf("complete material: err = %v, want none", err)
	}
}

// spec: 10.3 (mTLS PKI) NET-072, 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func TestResolveCoordinationHolderEnablesTheForwardOnlyWithMaterial_spec_10_3(t *testing.T) {
	cases := []struct {
		name       string
		devMode    bool
		leaseStore bool
		material   holderForwardMaterial
		want       bool
	}{
		{"lease store and material", false, true, holderForwardMaterial{"c", "k", "ca"}, true},
		{"dev mode without material", true, true, holderForwardMaterial{}, false},
		{"no lease store", false, false, holderForwardMaterial{"c", "k", "ca"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr := "10.0.0.7:50054"
			w := &gatewayWiring{f: &gatewayFlags{
				adapterTLSCert: &c.material.cert, adapterTLSKey: &c.material.key, adapterCA: &c.material.ca,
				devMode: &c.devMode, holderForwardAdvertiseAddr: &addr,
			}}
			holder := w.resolveCoordinationHolder("gw-a", c.leaseStore)
			if w.holderForwardEnabled != c.want {
				t.Errorf("holderForwardEnabled = %v, want %v", w.holderForwardEnabled, c.want)
			}
			if _, ok := sessionserver.HolderAddress(holder); !ok {
				t.Errorf("holder %q carries no address", holder)
			}
			// The Sweeper writes and compares the same composite value.
			if got := coordinationOptions(w, holder, time.Second, nil).ReplicaID; got != holder {
				t.Errorf("Sweeper ReplicaID = %q, want the holder value %q", got, holder)
			}
		})
	}
}

// spec: 10.3 (mTLS PKI) NET-072
func TestHolderForwardTransportLoadsTheGatewayCertificate_spec_10_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	client, serverTLS, err := holderForwardTransport(holderForwardMaterial{cert: m.CertFile, key: m.KeyFile, ca: m.CAFile})
	if err != nil || client == nil || serverTLS == nil {
		t.Fatalf("holderForwardTransport = %v, %v, %v; want a client and a listener config", client, serverTLS, err)
	}
	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("no certificates"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := holderForwardTransport(holderForwardMaterial{cert: m.CertFile, key: m.KeyFile, ca: empty}); err == nil {
		t.Error("a CA bundle with no certificates was accepted")
	}
	if _, _, err := holderForwardTransport(holderForwardMaterial{cert: m.CAFile, key: m.KeyFile, ca: m.CAFile}); err == nil {
		t.Error("a mismatched certificate and key were accepted")
	}
}

// spec: 29.3 (Interactive message send) Forwarding rules 3 and 4
func TestHolderForwardServerServesOnlyTheSessionAndMCPSurfaces_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	_, serverTLS, err := holderForwardTransport(holderForwardMaterial{cert: m.CertFile, key: m.KeyFile, ca: m.CAFile})
	if err != nil {
		t.Fatal(err)
	}
	mark := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !sessionserver.Forwarded(r.Context()) {
				t.Errorf("%s reached without the forwarded mark", name)
			}
			w.Header().Set("X-Surface", name)
		})
	}
	srv := newHolderForwardServer(":0", serverTLS, holderForwardInner(mark("sessions"), mark("mcp")), devAuthOptionsForTest(), memstore.New())
	if srv.TLSConfig != serverTLS || srv.ReadHeaderTimeout == 0 {
		t.Fatalf("server TLS or header timeout not set: %+v", srv)
	}
	for path, want := range map[string]string{"/v1/sessions/s/messages": "sessions", "/mcp": "mcp", "/v1/admin/tenants": ""} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		srv.Handler.ServeHTTP(rr, req)
		if got := rr.Header().Get("X-Surface"); got != want {
			t.Errorf("%s served by %q, want %q", path, got, want)
		}
	}
}

// devAuthOptionsForTest is the dev-header auth posture of a dev gateway.
func devAuthOptionsForTest() authmw.Options {
	return authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}
}

// spec: 10.3 (mTLS PKI) NET-072, 10.1.7 (Terminal pipelines at process exit.)
func TestHolderForwardListenerBuildsStartsAndStopsWithTheRESTListener_spec_10_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	listen := freeLoopbackAddrForTest(t)
	devMode := false
	w := &gatewayWiring{f: &gatewayFlags{
		adapterTLSCert: &m.CertFile, adapterTLSKey: &m.KeyFile, adapterCA: &m.CAFile,
		devMode: &devMode, holderForwardListenAddr: &listen,
	}}
	w.sessions = memstore.New()
	w.holderForwardEnabled = true
	w.restAuthOpts = devAuthOptionsForTest()
	w.buildHolderForwardTransport()
	if w.holderForwardClient == nil || w.holderForwardTLS == nil {
		t.Fatal("the forward client or listener TLS was not built with the material present")
	}
	w.buildHolderForwardServer(sessionserver.New(memstore.New(), sessionserver.Options{}), mcp.NewServer())
	if w.holderForwardSrv == nil || w.holderForwardSrv.Addr != listen {
		t.Fatalf("forward server = %+v, want one bound to %s", w.holderForwardSrv, listen)
	}
	w.startHolderForwardListener()
	conn, err := tls.Dial("tcp", listen, &tls.Config{RootCAs: m.CAPool, ServerName: "lenny-gateway.lenny-system.svc", MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatalf("dial the forward listener: %v", err)
	}
	_ = conn.Close()
	w.httpSrv = &http.Server{}
	q := w.shutdownSequence(5 * time.Second)
	if q.forward == nil {
		t.Fatal("the shutdown sequence does not stop the forward listener")
	}
	q.stopListener()
	if _, err := net.DialTimeout("tcp", listen, time.Second); err == nil {
		t.Error("the forward listener still accepts after the shutdown stop")
	}

	off := &gatewayWiring{f: w.f}
	off.buildHolderForwardTransport()
	off.buildHolderForwardServer(nil, nil)
	off.startHolderForwardListener()
	if off.holderForwardClient != nil || off.holderForwardSrv != nil {
		t.Error("a gateway without the forward enabled built a client or a listener")
	}
	off.httpSrv = &http.Server{}
	if off.shutdownSequence(time.Second).forward != nil {
		t.Error("the shutdown sequence names a forward listener that does not exist")
	}
}

// freeLoopbackAddrForTest returns a loopback address nothing listens on.
func freeLoopbackAddrForTest(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}
