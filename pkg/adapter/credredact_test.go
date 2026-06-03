// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

const secretPayload = "sk-super-secret-token-material"

func assignReq() *adapterv1.AssignCredentialsRequest {
	return &adapterv1.AssignCredentialsRequest{
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: "lease-b", Provider: "anthropic", Payload: []byte(secretPayload)},
			"openai":    {LeaseId: "lease-a", Provider: "openai", Payload: []byte(secretPayload)},
		},
	}
}

// spec: §16.4 line 376 — AssignCredentials and RotateCredentials are the
// credential-sensitive methods; SendMessage is not. F-16.4.8.
func TestIsCredentialSensitiveMethod_spec_16_4_376(t *testing.T) {
	sensitive := []string{
		adapterv1.Adapter_AssignCredentials_FullMethodName,
		adapterv1.Adapter_RotateCredentials_FullMethodName,
	}
	for _, m := range sensitive {
		if !IsCredentialSensitiveMethod(m) {
			t.Errorf("IsCredentialSensitiveMethod(%q) = false, want true", m)
		}
	}
	if IsCredentialSensitiveMethod(adapterv1.Adapter_SendMessage_FullMethodName) {
		t.Errorf("SendMessage classified as credential-sensitive")
	}
}

// SafeCredentialFields returns the sorted, deduplicated lease IDs and
// provider types and never the secret payload. spec: §16.4 line 376.
func TestSafeCredentialFields_spec_16_4_376(t *testing.T) {
	leaseIDs, providers := SafeCredentialFields(assignReq())
	if got, want := strings.Join(leaseIDs, ","), "lease-a,lease-b"; got != want {
		t.Errorf("lease IDs = %q, want %q (sorted)", got, want)
	}
	if got, want := strings.Join(providers, ","), "anthropic,openai"; got != want {
		t.Errorf("providers = %q, want %q (sorted)", got, want)
	}
	// RotateCredentials carries the same lease map shape.
	rotIDs, _ := SafeCredentialFields(&adapterv1.RotateCredentialsRequest{
		Leases: map[string]*adapterv1.CredentialLease{"p": {LeaseId: "lease-r", Provider: "p"}},
	})
	if got, want := strings.Join(rotIDs, ","), "lease-r"; got != want {
		t.Errorf("rotate lease IDs = %q, want %q", got, want)
	}
	// A non-credential request type yields nothing.
	if ids, provs := SafeCredentialFields(&adapterv1.SendMessageRequest{}); ids != nil || provs != nil {
		t.Errorf("SafeCredentialFields(SendMessageRequest) = %v/%v, want nil/nil", ids, provs)
	}
}

func jsonLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

// The interceptor emits a redacted access line for a credential RPC: it
// carries the safe fields and never the secret payload bytes. spec: §16.4
// line 376. F-16.4.8.
func TestCredentialRedactionInterceptorRedactsPayload_spec_16_4_376(t *testing.T) {
	var buf bytes.Buffer
	interceptor := credentialRedactionInterceptor(jsonLogger(&buf))

	handler := func(_ context.Context, _ any) (any, error) {
		return &adapterv1.AssignCredentialsResponse{}, nil
	}
	_, err := interceptor(context.Background(), assignReq(),
		&grpc.UnaryServerInfo{FullMethod: adapterv1.Adapter_AssignCredentials_FullMethodName}, handler)
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, secretPayload) {
		t.Fatalf("credential access log leaked the secret payload:\n%s", out)
	}
	var rec map[string]any
	if e := json.Unmarshal([]byte(strings.TrimSpace(out)), &rec); e != nil {
		t.Fatalf("log line is not JSON: %v\n%s", e, out)
	}
	if rec["msg"] != "credential_rpc" {
		t.Errorf("msg = %v, want credential_rpc", rec["msg"])
	}
	if rec["operation"] != "credential.assign" {
		t.Errorf("operation = %v, want credential.assign", rec["operation"])
	}
	if rec["outcome"] != "ok" {
		t.Errorf("outcome = %v, want ok", rec["outcome"])
	}
	if rec["rpc"] != adapterv1.Adapter_AssignCredentials_FullMethodName {
		t.Errorf("rpc = %v, want the AssignCredentials full method", rec["rpc"])
	}
	// Lease IDs and providers are present (the only request-derived fields).
	if ids, _ := json.Marshal(rec["lease_ids"]); !strings.Contains(string(ids), "lease-a") {
		t.Errorf("lease_ids = %v, want to contain lease-a", rec["lease_ids"])
	}
	if provs, _ := json.Marshal(rec["providers"]); !strings.Contains(string(provs), "openai") {
		t.Errorf("providers = %v, want to contain openai", rec["providers"])
	}
}

// On a handler error the line records outcome=error and the gRPC code,
// still without the payload. spec: §16.4 line 376.
func TestCredentialRedactionInterceptorRecordsErrorOutcome_spec_16_4_376(t *testing.T) {
	var buf bytes.Buffer
	interceptor := credentialRedactionInterceptor(jsonLogger(&buf))

	handler := func(_ context.Context, _ any) (any, error) {
		return nil, status.Error(codes.PermissionDenied, "denied")
	}
	_, err := interceptor(context.Background(), &adapterv1.RotateCredentialsRequest{
		Leases: map[string]*adapterv1.CredentialLease{"p": {LeaseId: "l", Provider: "p", Payload: []byte(secretPayload)}},
	}, &grpc.UnaryServerInfo{FullMethod: adapterv1.Adapter_RotateCredentials_FullMethodName}, handler)
	if err == nil {
		t.Fatal("interceptor swallowed the handler error")
	}
	out := buf.String()
	if strings.Contains(out, secretPayload) {
		t.Fatalf("error-path credential log leaked the secret payload:\n%s", out)
	}
	var rec map[string]any
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &rec)
	if rec["outcome"] != "error" {
		t.Errorf("outcome = %v, want error", rec["outcome"])
	}
	if rec["operation"] != "credential.rotate" {
		t.Errorf("operation = %v, want credential.rotate", rec["operation"])
	}
	if rec["grpc_code"] != codes.PermissionDenied.String() {
		t.Errorf("grpc_code = %v, want %v", rec["grpc_code"], codes.PermissionDenied.String())
	}
}

// A non-credential method passes through with no credential access line.
// spec: §16.4 line 376. F-16.4.8.
func TestCredentialRedactionInterceptorPassesThroughNonSensitive_spec_16_4_376(t *testing.T) {
	var buf bytes.Buffer
	interceptor := credentialRedactionInterceptor(jsonLogger(&buf))

	called := false
	handler := func(_ context.Context, _ any) (any, error) {
		called = true
		return &adapterv1.SendMessageResponse{}, nil
	}
	if _, err := interceptor(context.Background(), &adapterv1.SendMessageRequest{},
		&grpc.UnaryServerInfo{FullMethod: adapterv1.Adapter_SendMessage_FullMethodName}, handler); err != nil {
		t.Fatalf("passthrough returned error: %v", err)
	}
	if !called {
		t.Fatal("inner handler was not invoked for a non-sensitive method")
	}
	if buf.Len() != 0 {
		t.Errorf("non-sensitive method emitted a credential line: %s", buf.String())
	}
}
