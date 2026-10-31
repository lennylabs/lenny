// SPDX-License-Identifier: MIT

package tokenservice

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// runRedacted runs the redaction interceptor around the ProbeSecretAccess
// handler of srv for req, inside a recording span, and returns the captured
// log text and the span attributes rendered as text.
func runRedacted(t *testing.T, srv *GRPCServer, req *tokensv1.ProbeSecretAccessRequest) (logText, spanText string, resp any, err error) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	ctx, span := tp.Tracer("test").Start(context.Background(), "rpc")
	info := &grpc.UnaryServerInfo{FullMethod: tokensv1.TokenService_ProbeSecretAccess_FullMethodName}
	handler := func(ctx context.Context, r any) (any, error) {
		return srv.ProbeSecretAccess(ctx, r.(*tokensv1.ProbeSecretAccessRequest))
	}
	resp, err = CredentialRedactionInterceptor(logger)(ctx, req, info, handler)
	span.End()
	var sb strings.Builder
	for _, s := range rec.Ended() {
		for _, a := range s.Attributes() {
			sb.WriteString(string(a.Key) + "=" + a.Value.Emit() + "\n")
		}
	}
	return logs.String(), sb.String(), resp, err
}

// spec: §16.4 (Credential-sensitive RPCs) — a keyed ProbeSecretAccess call
// records the RPC name, the Secret name, and the outcome in one log line and
// on the span, and the key's value appears in neither.
func TestCredentialRedaction_KeyedCallRecordsSafeFieldsOnly(t *testing.T) {
	srv, _ := newProbeServer()
	srv.SetSecretAccessProber(&stubProber{verdict: SecretAccessAllowed, value: []byte(testSecretValue)})
	logText, spanText, resp, err := runRedacted(t, srv,
		&tokensv1.ProbeSecretAccessRequest{SecretName: "github-token-1", Key: "token"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := resp.(*tokensv1.ProbeSecretAccessResponse).GetValue(); string(got) != testSecretValue {
		t.Fatalf("interceptor altered the response value: %q", got)
	}
	for _, want := range []string{
		tokensv1.TokenService_ProbeSecretAccess_FullMethodName, `"secret_name":"github-token-1"`, `"outcome":"success"`,
	} {
		if !strings.Contains(logText, want) {
			t.Errorf("log line missing %q: %s", want, logText)
		}
	}
	for _, want := range []string{"credential.secret_name=github-token-1", "credential.outcome=success"} {
		if !strings.Contains(spanText, want) {
			t.Errorf("span missing %q: %s", want, spanText)
		}
	}
	if strings.Contains(logText, testSecretValue) || strings.Contains(spanText, testSecretValue) {
		t.Fatalf("value leaked: log=%s span=%s", logText, spanText)
	}
}

// spec: §16.4 — the outcome recorded for a failed keyed read is unavailable,
// and other verdicts record their own outcome.
func TestCredentialRedaction_Outcomes(t *testing.T) {
	cases := []struct {
		name string
		pr   *stubProber
		want string
	}{
		{"denied", &stubProber{verdict: SecretAccessDenied}, `"outcome":"denied"`},
		{"not found", &stubProber{verdict: SecretAccessNotFound}, `"outcome":"not_found"`},
		{"failed", &stubProber{err: errors.New("timeout")}, `"outcome":"unavailable"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newProbeServer()
			srv.SetSecretAccessProber(tc.pr)
			logText, _, _, _ := runRedacted(t, srv, &tokensv1.ProbeSecretAccessRequest{SecretName: "s", Key: "token"})
			if !strings.Contains(logText, tc.want) {
				t.Fatalf("log line missing %s: %s", tc.want, logText)
			}
		})
	}
}

// spec: §16.4 — an unkeyed probe returns no credential value, so it is not
// credential-sensitive and the interceptor adds no log line; other methods
// are not credential-sensitive either.
func TestCredentialRedaction_UnkeyedAndOtherMethodsPassThrough(t *testing.T) {
	srv, _ := newProbeServer()
	srv.SetSecretAccessProber(&stubProber{verdict: SecretAccessAllowed})
	logText, spanText, _, err := runRedacted(t, srv, &tokensv1.ProbeSecretAccessRequest{SecretName: "s"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if logText != "" || spanText != "" {
		t.Fatalf("unkeyed probe recorded: log=%q span=%q", logText, spanText)
	}
	if IsCredentialSensitiveCall(tokensv1.TokenService_RevokeCredentials_FullMethodName,
		&tokensv1.ProbeSecretAccessRequest{Key: "token"}) {
		t.Fatal("a non-ProbeSecretAccess method classified as credential-sensitive")
	}
}

// secretReadCallOutcome records "error" for a non-Unavailable failure and
// for an unexpected response type.
func TestSecretReadCallOutcome_Errors(t *testing.T) {
	if got := secretReadCallOutcome(nil, status.Error(codes.InvalidArgument, "x")); got != "error" {
		t.Fatalf("InvalidArgument outcome = %q, want error", got)
	}
	if got := secretReadCallOutcome("unexpected", nil); got != "error" {
		t.Fatalf("unexpected response outcome = %q, want error", got)
	}
	if got := secretReadCallOutcome(&tokensv1.ProbeSecretAccessResponse{}, nil); got != "error" {
		t.Fatalf("unspecified verdict outcome = %q, want error", got)
	}
}
