// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// RequireSATokenInterceptor returns a unary server interceptor that
// validates the projected ServiceAccount token on every pod→gateway
// GatewayControl call. The §10.2 line 227 contract — "Pods cannot forge
// or extend this token. The gateway validates the signature on every
// pod→gateway request" — and the §10.3 line 334 audience binding are both
// enforced here, the SA-token layer of the defense-in-depth chain that
// sits alongside the mTLS SPIFFE check (RequireVerifiedPeerInterceptor)
// and the NetworkPolicy isolation.
//
// The token is carried as an `authorization: Bearer <jwt>` gRPC metadata
// header by the pod adapter (the gatewaycontrol client's per-RPC
// credential). When a verifier is configured (the production path, a
// TokenReviewVerifier backed by the in-cluster authentication client), the
// interceptor submits the token to the kube-apiserver, which validates the
// signature and expiry against the cluster's service-account issuer and
// confirms the deployment-specific audience (`lenny-gateway-<cluster>`, the
// global.saTokenAudience value). A forged, expired, or cross-deployment
// token is rejected even when the cluster CA would otherwise trust the
// peer's certificate. Any failure fails closed.
//
// When no verifier is configured but an audience is set (an in-cluster
// client could not be built, e.g. single-process dev with an audience),
// the interceptor falls back to decoding the JWT payload and matching the
// `aud` claim without verifying the signature; the mTLS handshake still
// authenticates the peer in that degraded path.
//
// When expectedAudience is empty — the local-development path where
// global.saTokenAudience is unset — the interceptor passes every call
// through unchanged, mirroring RequireVerifiedPeerInterceptor's dev-mode
// behaviour.
// spec: §10.2 line 227 (signature validation); §10.3 line 334 (audience).
func RequireSATokenInterceptor(expectedAudience string, verifier TokenVerifier) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if expectedAudience == "" {
			return handler(ctx, req)
		}
		token, ok := bearerTokenFromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated,
				"leasecontrol: GatewayControl requires a projected SA token (reason=token_missing) (§10.2)")
		}
		if verifier != nil {
			// spec: §10.2 line 227 — validate the signature (and audience)
			// on every pod→gateway request. Fail closed on any error.
			if err := verifier.Verify(ctx, token, expectedAudience); err != nil {
				reason := "signature_invalid"
				switch {
				case errors.Is(err, ErrSATokenAudienceMismatch):
					reason = "audience_mismatch"
				case errors.Is(err, ErrSATokenReviewFailed):
					reason = "tokenreview_unavailable"
				}
				return nil, status.Errorf(codes.Unauthenticated,
					"leasecontrol: SA token rejected (reason=%s): %v (§10.2)", reason, err)
			}
			return handler(ctx, req)
		}
		// Degraded dev path: no cluster TokenReview client. Decode the
		// payload and match the audience without a signature check.
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
// of strings; both forms are handled. This decode path is the degraded
// dev fallback used by RequireSATokenInterceptor when no TokenReview
// verifier is configured; it does not verify the signature. An absent
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
