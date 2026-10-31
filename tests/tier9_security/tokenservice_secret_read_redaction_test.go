// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 credential-handling check for the Token Service keyed Secret read.
// A ProbeSecretAccess call that names a key returns a credential value, which
// makes it a credential-sensitive RPC: its payload stays out of access logs and
// span attributes, and only the RPC name, the Secret name, and the outcome are
// recorded. The test serves the production gRPC server behind the production
// redaction interceptor over an in-memory connection, with a captured slog
// handler and a span recorder, and asserts the recorded fields and the absence
// of the value bytes.
//
// spec: §16.4 (Credential-sensitive RPCs), §4.9 (Credential Lifecycle and
// Secret Management, Token Service secret resolution rule 5).
package tier9_security_test

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/pkg/tokenservice/secretprobe"
)

const redactionSecretValue = "ghp_tier9_redaction_canary_7f3a"

// TestTokenServiceKeyedReadRedaction_spec_16_4 drives a keyed read end to end
// and inspects every recorded log line and span attribute.
//
// spec: §16.4 (Credential-sensitive RPCs), §4.9 (Credential Lifecycle and
// Secret Management, Token Service secret resolution rule 5).
//
// diagnosis: the value in the log means CredentialRedactionInterceptor (or a
// logger added ahead of it) records the response; the value in a span means an
// attribute carries payload; a missing secret_name or outcome means the
// interceptor no longer classifies a keyed ProbeSecretAccess as
// credential-sensitive.
func TestTokenServiceKeyedReadRedaction_spec_16_4(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	// spanInterceptor stands in for the otelgrpc stats handler: it opens the
	// server span the redaction interceptor stamps.
	spanInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		ctx, span := tp.Tracer("tier9").Start(ctx, info.FullMethod)
		defer span.End()
		return h(ctx, req)
	}

	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lenny-credentials", Name: "github-token-1"},
		Data:       map[string][]byte{"token": []byte(redactionSecretValue)},
	})
	leases := credleasestore.New()
	ts := tokenservice.NewGRPCServer(credassign.New(leases, credcache.New()), leases)
	ts.SetSecretAccessProber(secretprobe.New(cs, "lenny-credentials"))
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(spanInterceptor, tokenservice.CredentialRedactionInterceptor(logger)))
	tokensv1.RegisterTokenServiceServer(srv, ts)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	resp, err := tokensv1.NewTokenServiceClient(conn).ProbeSecretAccess(context.Background(),
		&tokensv1.ProbeSecretAccessRequest{SecretName: "github-token-1", Key: "token"})
	if err != nil {
		t.Fatalf("ProbeSecretAccess: %v", err)
	}
	if string(resp.GetValue()) != redactionSecretValue {
		t.Fatalf("response value = %q; the read did not return the value the check guards", resp.GetValue())
	}

	logText := logs.String()
	for _, want := range []string{
		tokensv1.TokenService_ProbeSecretAccess_FullMethodName,
		`"secret_name":"github-token-1"`,
		`"outcome":"success"`,
	} {
		if !strings.Contains(logText, want) {
			t.Errorf("log missing %q: %s", want, logText)
		}
	}
	var spanText strings.Builder
	for _, s := range rec.Ended() {
		spanText.WriteString(s.Name() + "\n")
		for _, a := range s.Attributes() {
			spanText.WriteString(string(a.Key) + "=" + a.Value.Emit() + "\n")
		}
	}
	for _, want := range []string{"credential.secret_name=github-token-1", "credential.outcome=success"} {
		if !strings.Contains(spanText.String(), want) {
			t.Errorf("span missing %q: %s", want, spanText.String())
		}
	}
	if strings.Contains(logText, redactionSecretValue) || strings.Contains(spanText.String(), redactionSecretValue) {
		t.Fatalf("value bytes recorded: log=%s span=%s", logText, spanText.String())
	}
}
