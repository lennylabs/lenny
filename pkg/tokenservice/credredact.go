// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// IsCredentialSensitiveCall reports whether a Token Service gRPC call is a
// §16.4 credential-sensitive RPC: ProbeSecretAccess with a non-empty key,
// whose response carries a credential value. It is the seam any logging or
// tracing on the Token Service gRPC surface consults before recording a
// request or response.
//
// spec: §16.4 (Credential-sensitive RPCs); §4.9 Token Service secret
// resolution rule 5.
func IsCredentialSensitiveCall(fullMethod string, req any) bool {
	if fullMethod != tokensv1.TokenService_ProbeSecretAccess_FullMethodName {
		return false
	}
	r, ok := req.(*tokensv1.ProbeSecretAccessRequest)
	return ok && r.GetKey() != ""
}

// CredentialRedactionInterceptor is the server-side enforcement of §16.4 on
// the Token Service gRPC surface. For a credential-sensitive call it emits
// one access log line and stamps the active span with only the RPC name,
// the Secret name, and the outcome, and never the key's value. Other calls
// pass through with no logging added.
//
// spec: §16.4 (Credential-sensitive RPCs).
func CredentialRedactionInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !IsCredentialSensitiveCall(info.FullMethod, req) {
			return handler(ctx, req)
		}
		// Read the Secret name before dispatch so a handler that mutates
		// the request cannot change what is recorded.
		secretName := req.(*tokensv1.ProbeSecretAccessRequest).GetSecretName()
		resp, err := handler(ctx, req)
		outcome := secretReadCallOutcome(resp, err)
		if span := trace.SpanFromContext(ctx); span.IsRecording() {
			span.SetAttributes(
				attribute.String("rpc.method", info.FullMethod),
				attribute.String("credential.secret_name", secretName),
				attribute.String("credential.outcome", outcome),
			)
		}
		logger.LogAttrs(
			ctx, slog.LevelInfo, "credential_rpc",
			slog.String("rpc", info.FullMethod),
			slog.String("secret_name", secretName),
			slog.String("outcome", outcome),
		)
		return resp, err
	}
}

// secretReadCallOutcome derives the recorded outcome of a ProbeSecretAccess
// call from its response or error, using the §16.1 outcome vocabulary. An
// error other than Unavailable records "error".
func secretReadCallOutcome(resp any, err error) string {
	if err != nil {
		if status.Code(err) == codes.Unavailable {
			return SecretReadOutcomeUnavailable
		}
		return "error"
	}
	r, ok := resp.(*tokensv1.ProbeSecretAccessResponse)
	if !ok {
		return "error"
	}
	switch r.GetVerdict() {
	case tokensv1.Verdict_VERDICT_ALLOWED:
		return SecretReadOutcomeSuccess
	case tokensv1.Verdict_VERDICT_DENIED:
		return SecretReadOutcomeDenied
	case tokensv1.Verdict_VERDICT_NOT_FOUND:
		return SecretReadOutcomeNotFound
	default:
		return "error"
	}
}
