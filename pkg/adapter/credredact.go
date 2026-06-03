// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"log/slog"
	"sort"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/observability/tracing"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// credentialSensitiveMethods is the §16.4 line 376 set of gRPC methods whose
// request and response payloads MUST be excluded from access logs, gRPC
// access logs, and OTel trace span attributes. The CredentialLease.payload
// carries the live secret material, so only the RPC name, lease ID, provider
// type, and outcome may be recorded.
//
// spec: §16.4 line 376.
var credentialSensitiveMethods = map[string]bool{
	adapterv1.Adapter_AssignCredentials_FullMethodName: true,
	adapterv1.Adapter_RotateCredentials_FullMethodName: true,
}

// IsCredentialSensitiveMethod reports whether fullMethod is a §16.4 line 376
// credential-sensitive RPC. It is the seam any future logging or tracing
// added to the gateway↔adapter gRPC link must consult before recording a
// request or response: a credential-sensitive method's payload must never
// reach a log line or a span attribute. Pair it with SafeCredentialFields to
// recover the fields the spec does permit.
//
// spec: §16.4 line 376.
func IsCredentialSensitiveMethod(fullMethod string) bool {
	return credentialSensitiveMethods[fullMethod]
}

// SafeCredentialFields returns the only request-derived values §16.4 line 376
// permits a credential-sensitive RPC to record: the lease IDs and the
// provider types. The CredentialLease.Payload secret bytes are never read or
// returned. Results are sorted and deduplicated so a log line or span is
// stable across protobuf map iteration order. A request type that carries no
// leases (or an unexpected type) yields two empty slices.
//
// spec: §16.4 line 376.
func SafeCredentialFields(req any) (leaseIDs, providers []string) {
	var leases map[string]*adapterv1.CredentialLease
	switch r := req.(type) {
	case *adapterv1.AssignCredentialsRequest:
		leases = r.GetLeases()
	case *adapterv1.RotateCredentialsRequest:
		leases = r.GetLeases()
	default:
		return nil, nil
	}
	idSet := make(map[string]struct{}, len(leases))
	provSet := make(map[string]struct{}, len(leases))
	for _, lease := range leases {
		if lease == nil {
			continue
		}
		if id := lease.GetLeaseId(); id != "" {
			idSet[id] = struct{}{}
		}
		if p := lease.GetProvider(); p != "" {
			provSet[p] = struct{}{}
		}
	}
	return sortedKeys(idSet), sortedKeys(provSet)
}

func sortedKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// credentialRedactionInterceptor is the gRPC server-side enforcement of
// §16.4 line 376. For a credential-sensitive RPC it emits exactly one access
// log line carrying the RPC name, the §16.3 reserved credential-operation
// span name, the lease IDs, the provider types, and the outcome — never the
// secret payload — and stamps the same safe field set onto the active OTel
// span (the one the otelgrpc StatsHandler opened). Non-sensitive methods pass
// through untouched: this interceptor introduces no per-RPC logging for them.
//
// Installing this as the credential access-log surface (rather than letting a
// future generic gRPC logger attach first) is the point: the redaction is
// built into the seam, so AssignCredentials / RotateCredentials cannot leak a
// payload into a log or span even as observability is extended.
//
// spec: §16.4 line 376; §16.3 lines 59-60 (SpanCredentialAssign /
// SpanCredentialRotate). F-16.4.8.
func credentialRedactionInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !IsCredentialSensitiveMethod(info.FullMethod) {
			return handler(ctx, req)
		}

		// Read the safe fields before dispatch so a handler that mutates the
		// request in place cannot change what is recorded.
		leaseIDs, providers := SafeCredentialFields(req)

		resp, err := handler(ctx, req)

		operation := string(tracing.SpanCredentialAssign)
		if info.FullMethod == adapterv1.Adapter_RotateCredentials_FullMethodName {
			operation = string(tracing.SpanCredentialRotate)
		}
		outcome := "ok"
		if err != nil {
			outcome = "error"
		}

		if span := trace.SpanFromContext(ctx); span.IsRecording() {
			span.SetAttributes(
				attribute.String("credential.operation", operation),
				attribute.StringSlice("credential.lease_ids", leaseIDs),
				attribute.StringSlice("credential.providers", providers),
				attribute.String("credential.outcome", outcome),
			)
		}

		attrs := []slog.Attr{
			slog.String("rpc", info.FullMethod),
			slog.String("operation", operation),
			slog.Any("lease_ids", leaseIDs),
			slog.Any("providers", providers),
			slog.String("outcome", outcome),
		}
		if err != nil {
			attrs = append(attrs, slog.String("grpc_code", status.Code(err).String()))
		}
		logger.LogAttrs(ctx, slog.LevelInfo, "credential_rpc", attrs...)

		return resp, err
	}
}
