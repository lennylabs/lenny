// SPDX-License-Identifier: MIT

package sessionserver

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"slices"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
)

// errPeerNotGateway rejects a client certificate that chains to the cluster
// CA but does not carry the gateway DNS SAN.
var errPeerNotGateway = errors.New("holder forward: peer certificate does not carry the gateway DNS SAN")

// HolderForwardServerTLS is the TLS configuration of the CH-HOLDERFORWARD
// listener. It presents the gateway replica certificate reloader serves,
// requires a TLS 1.3 client certificate that chains to clientCAs, and rejects
// at handshake a peer leaf whose DNS SANs do not include the gateway DNS SAN,
// so plaintext, one-way TLS, and any other cluster-CA certificate, including
// an agent pod's SPIFFE certificate, never reach a handler. A successful
// handshake establishes only that the peer is a gateway replica.
//
// spec: §10.3 (mTLS PKI) NET-072
func HolderForwardServerTLS(reloader *certreload.Reloader, clientCAs *x509.CertPool) *tls.Config {
	return &tls.Config{
		GetCertificate:   reloader.GetCertificate,
		ClientCAs:        clientCAs,
		ClientAuth:       tls.RequireAndVerifyClientCert,
		MinVersion:       tls.VersionTLS13,
		VerifyConnection: verifyGatewayPeer,
	}
}

// verifyGatewayPeer requires the verified peer leaf to carry the gateway DNS
// SAN.
func verifyGatewayPeer(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errPeerNotGateway
	}
	if !slices.Contains(cs.PeerCertificates[0].DNSNames, gatewaycontrol.GatewayDNSName) {
		return errPeerNotGateway
	}
	return nil
}

// NewHolderForwardHandler builds the handler chain of the CH-HOLDERFORWARD
// listener over inner, the session handler mux and the /mcp handler the REST
// listener serves.
//
// A request that carries a client credential passes the auth middleware
// built from authOpts, the options of the REST listener, so the coordinator
// authenticates the relayed credential as it authenticates a credential
// presented to it directly, including the §13.3 exp check. A request that
// carries CallingSessionHeader and no credential is a forwarded platform tool
// call: it runs under the principal platformtools.WithSessionPrincipal
// installs for that session, the one CallPlatformTool installs. The listener
// accepts no other principal the forwarding replica asserts.
//
// The chain then marks the context as forwarded and installs a §11.5 no-store
// gate. It omits the §11.1 rate-limit and §11.5 idempotency middleware,
// which the serving replica applied. A response whose gate a handler set
// carries the gateway-internal no-store mark back to the forwarding replica.
//
// spec: §29.3 (Interactive message send) Forwarding rules 3, 4, and 5, §10.3
// (mTLS PKI) NET-072
func NewHolderForwardHandler(inner http.Handler, authOpts authmw.Options, sessions platformtools.SessionResolver) http.Handler {
	marked := forwardedMarker{inner: inner}
	authed := authmw.Wrap(marked, authOpts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.Header.Get(CallingSessionHeader)
		r.Header.Del(CallingSessionHeader)
		if sessionID == "" || authmw.PresentsCredential(r.Header) {
			authed.ServeHTTP(w, r)
			return
		}
		serveCallingSession(w, r, marked, sessions, sessionID)
	})
}

// serveCallingSession runs a forwarded platform tool call under the calling
// session's principal. An unknown session is refused as unauthenticated.
func serveCallingSession(w http.ResponseWriter, r *http.Request, next http.Handler, sessions platformtools.SessionResolver, sessionID string) {
	ctx, err := platformtools.WithSessionPrincipal(r.Context(), sessions, sessionID)
	if err != nil {
		writeListenerError(w, err)
		return
	}
	p, _ := authmw.FromContext(ctx)
	r = r.WithContext(ctx)
	r.Header.Set("X-Lenny-Tenant-ID", p.TenantID)
	next.ServeHTTP(w, r)
}

// writeListenerError answers a calling-session resolution failure.
func writeListenerError(w http.ResponseWriter, err error) {
	if errors.Is(err, leasecontrol.ErrSessionNotFound) {
		writeErrorEnvelope(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"the calling session of the forwarded tool call does not exist", nil)
		return
	}
	writeErrorEnvelope(w, http.StatusServiceUnavailable, "TARGET_NOT_READY",
		"the calling session of the forwarded tool call could not be read", nil)
}

// forwardedMarker marks each request as forwarded, installs the §11.5
// no-store gate, and reports a set gate on the response.
type forwardedMarker struct{ inner http.Handler }

func (m forwardedMarker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(idemmw.WithGate(WithForwarded(r.Context())))
	m.inner.ServeHTTP(&noStoreMarkWriter{ResponseWriter: w, r: r}, r)
}

// noStoreMarkWriter adds the no-store mark to a response whose request gate
// is set when the response header is written.
type noStoreMarkWriter struct {
	http.ResponseWriter
	r           *http.Request
	wroteHeader bool
}

func (w *noStoreMarkWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		if idemmw.NotStored(w.r.Context()) {
			w.Header().Set(forwardNoStoreHeader, "1")
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *noStoreMarkWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

// Flush forwards a flush so a streamed response reaches the forwarding
// replica as it is written.
func (w *noStoreMarkWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *noStoreMarkWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
