// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// RequireVerifiedPeerInterceptor returns a unary server interceptor that
// rejects any §8.6 GatewayControl call whose peer did not present an
// mTLS-verified client certificate. §4.7 line 616 declares the
// adapter↔gateway channel as "internal gRPC/HTTP+mTLS API" and §15.3
// requires "gRPC + mTLS" for gateway↔pod traffic; the GatewayControl
// listener that the pod adapter dials for ExtendLease is the inverse
// direction of that same secured channel.
//
// The transport layer already rejects peers without a chain when the
// listener is built with tls.RequireAndVerifyClientCert (the
// clientCAFile branch of adapter.TLSServerOption). This interceptor is
// the in-process auth gate that fails closed if a request ever reaches
// the handler without a verified peer: the ExtendLease handler resolves
// the caller's tenant from the session_id in the request body and has
// no other proof of identity, so an unauthenticated peer must never
// reach it. Without this gate any process able to reach the
// GatewayControl port could call ExtendLease for any session id.
//
// When enabled is false — the local-development plaintext path where no
// mTLS material is configured — the interceptor passes every call
// through unchanged, so in-process tests and dev runs are unaffected.
// spec: §4.7 line 616; §15.3
func RequireVerifiedPeerInterceptor(enabled bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !enabled {
			return handler(ctx, req)
		}
		if !peerHasVerifiedCert(ctx) {
			return nil, status.Error(codes.Unauthenticated,
				"leasecontrol: GatewayControl requires an mTLS-verified client certificate (§4.7/§15.3)")
		}
		return handler(ctx, req)
	}
}

// peerHasVerifiedCert reports whether the gRPC peer attached to ctx
// presented a client certificate that the server's mTLS configuration
// verified against its client CA. A peer with no TLS auth info, with
// non-TLS auth info, or with an unverified chain (empty VerifiedChains)
// fails the check.
func peerHasVerifiedCert(ctx context.Context) bool {
	pr, ok := peer.FromContext(ctx)
	if !ok || pr == nil {
		return false
	}
	tlsInfo, ok := pr.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return false
	}
	return len(tlsInfo.State.VerifiedChains) > 0
}
