// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// RequireSATokenAudienceInterceptor returns a unary server interceptor
// that validates the projected ServiceAccount token's audience claim on
// every pod→gateway GatewayControl call. The §10.3 "Projected SA token"
// clause requires the gateway to validate the audience claim on every
// pod→gateway request; the audience is deployment-specific
// (`lenny-gateway-<cluster-name>`, the global.saTokenAudience value) so a
// token minted for another Lenny deployment's gateway is rejected even
// when the cluster CA would otherwise trust the peer's certificate. This
// is the SA-token layer of the §10.3 defense-in-depth chain that sits
// alongside the mTLS SPIFFE check (RequireVerifiedPeerInterceptor) and
// the NetworkPolicy isolation.
//
// The token is carried as an `authorization: Bearer <jwt>` gRPC metadata
// header by the pod adapter (the gatewaycontrol client's per-RPC
// credential). The interceptor decodes the JWT payload and checks the
// `aud` claim without verifying the signature: the mTLS handshake has
// already authenticated the peer, so this layer exists only to bind the
// token to this deployment's audience and block cross-deployment replay.
// A missing token, an unparseable token, or an audience that does not
// include expectedAudience is rejected with Unauthenticated.
//
// When expectedAudience is empty — the local-development path where
// global.saTokenAudience is unset — the interceptor passes every call
// through unchanged, mirroring RequireVerifiedPeerInterceptor's
// dev-mode behaviour.
// spec: §10.3 line 334 (Projected SA token).
func RequireSATokenAudienceInterceptor(expectedAudience string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if expectedAudience == "" {
			return handler(ctx, req)
		}
		token, ok := bearerTokenFromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated,
				"leasecontrol: GatewayControl requires a projected SA token (reason=token_missing) (§10.3)")
		}
		auds, err := jwtAudiences(token)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated,
				"leasecontrol: SA token is not a parseable JWT (reason=token_malformed): %v (§10.3)", err)
		}
		for _, a := range auds {
			if a == expectedAudience {
				return handler(ctx, req)
			}
		}
		return nil, status.Error(codes.Unauthenticated,
			"leasecontrol: SA token audience does not match this deployment (reason=audience_mismatch) (§10.3)")
	}
}

// bearerTokenFromContext extracts the bearer token from the incoming
// gRPC `authorization` metadata header. It tolerates the case-insensitive
// "Bearer " scheme prefix and a bare token. It returns ok=false when the
// header is absent or empty.
func bearerTokenFromContext(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return "", false
	}
	tok := strings.TrimSpace(vals[0])
	if len(tok) >= 7 && strings.EqualFold(tok[:7], "bearer ") {
		tok = strings.TrimSpace(tok[7:])
	}
	if tok == "" {
		return "", false
	}
	return tok, true
}

// jwtAudiences decodes a JWT's payload and returns its `aud` claim as a
// slice. RFC 7519 permits `aud` to be either a single string or an array
// of strings; both forms are handled. The signature is deliberately not
// verified — see RequireSATokenAudienceInterceptor for why. An absent
// `aud` claim yields an empty slice (no audience matches).
func jwtAudiences(token string) ([]string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errNotAJWT
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims struct {
		Aud json.RawMessage `json:"aud"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	if len(claims.Aud) == 0 {
		return nil, nil
	}
	// aud may be a JSON string or a JSON array of strings.
	var single string
	if err := json.Unmarshal(claims.Aud, &single); err == nil {
		return []string{single}, nil
	}
	var many []string
	if err := json.Unmarshal(claims.Aud, &many); err == nil {
		return many, nil
	}
	return nil, errAudClaimShape
}

type satokenError string

func (e satokenError) Error() string { return string(e) }

const (
	errNotAJWT       = satokenError("token is not a three-segment JWT")
	errAudClaimShape = satokenError("aud claim is neither a string nor an array of strings")
)
