// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// fakeTokenServiceClient stubs the ProbeSecretAccess RPC; the other RPCs
// are unused by the prober and panic if called.
type fakeTokenServiceClient struct {
	tokensv1.TokenServiceClient
	resp  *tokensv1.ProbeSecretAccessResponse
	err   error
	got   string
	key   string
	calls int
}

func (f *fakeTokenServiceClient) ProbeSecretAccess(_ context.Context, req *tokensv1.ProbeSecretAccessRequest, _ ...grpc.CallOption) (*tokensv1.ProbeSecretAccessResponse, error) {
	f.calls++
	f.got = req.GetSecretName()
	f.key = req.GetKey()
	return f.resp, f.err
}

const testCredNS = "lenny-credentials"

func verdictResp(v tokensv1.Verdict) *tokensv1.ProbeSecretAccessResponse {
	return &tokensv1.ProbeSecretAccessResponse{Verdict: v}
}

// spec: §4.9 (Credential Storage Topology) — the gateway adapter maps each
// wire verdict onto the admin verdict the credential-pool handler
// consumes, and sends the stored canonical ref's Secret name alone with no
// key.
func TestTokenServiceSecretProber_VerdictMapping(t *testing.T) {
	cases := []struct {
		name string
		in   tokensv1.Verdict
		want admin.SecretProbeVerdict
	}{
		{"allowed", tokensv1.Verdict_VERDICT_ALLOWED, admin.SecretProbeAllowed},
		{"denied", tokensv1.Verdict_VERDICT_DENIED, admin.SecretProbeDenied},
		{"not_found", tokensv1.Verdict_VERDICT_NOT_FOUND, admin.SecretProbeNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &fakeTokenServiceClient{resp: verdictResp(tc.in)}
			p := &tokenServiceSecretProber{stub: stub, credNS: testCredNS}
			got, err := p.ProbeSecretAccess(context.Background(), "lenny-credentials/anthropic-key-1")
			if err != nil {
				t.Fatalf("ProbeSecretAccess: %v", err)
			}
			if got != tc.want {
				t.Fatalf("verdict = %v, want %v", got, tc.want)
			}
			if stub.got != "anthropic-key-1" {
				t.Fatalf("probed %q, want anthropic-key-1", stub.got)
			}
			if stub.key != "" {
				t.Fatalf("probe key = %q, want an unkeyed probe", stub.key)
			}
		})
	}
}

// spec: §4.9 — an RPC error (the probe could not return a
// definitive verdict) is propagated so the handler maps it to 503.
func TestTokenServiceSecretProber_RPCErrorPropagates(t *testing.T) {
	p := &tokenServiceSecretProber{stub: &fakeTokenServiceClient{err: errors.New("unavailable")}, credNS: testCredNS}
	if _, err := p.ProbeSecretAccess(context.Background(), "anthropic-key-1"); err == nil {
		t.Fatal("want error from failed RPC")
	}
}

// An unspecified verdict is treated as indeterminate (error), never as a
// silent ALLOWED.
func TestTokenServiceSecretProber_UnspecifiedIsError(t *testing.T) {
	p := &tokenServiceSecretProber{stub: &fakeTokenServiceClient{resp: verdictResp(tokensv1.Verdict_VERDICT_UNSPECIFIED)}, credNS: testCredNS}
	if _, err := p.ProbeSecretAccess(context.Background(), "anthropic-key-1"); err == nil {
		t.Fatal("want error for unspecified verdict")
	}
}

// spec: §4.9 (Credential Storage Topology) — the prober parses the stored
// ref with credential.ParseSecretRef. A ref the parser refuses, such as one
// naming the release namespace, returns an error with no RPC made, so the
// handler answers 503 rather than probing a Secret outside the credentials
// namespace.
func TestTokenServiceSecretProber_RefusedRefMakesNoRPC(t *testing.T) {
	for _, ref := range []string{"lenny-system/anthropic-key-1", "a/b/c", "Not_A_Name", ""} {
		t.Run(ref, func(t *testing.T) {
			stub := &fakeTokenServiceClient{resp: verdictResp(tokensv1.Verdict_VERDICT_ALLOWED)}
			p := &tokenServiceSecretProber{stub: stub, credNS: testCredNS}
			if _, err := p.ProbeSecretAccess(context.Background(), ref); err == nil {
				t.Fatalf("ProbeSecretAccess(%q): want error", ref)
			}
			if stub.calls != 0 {
				t.Fatalf("ProbeSecretAccess(%q) made %d RPCs, want 0", ref, stub.calls)
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — a bare stored ref also parses,
// and the RPC carries the name alone.
func TestTokenServiceSecretProber_BareRefSendsName(t *testing.T) {
	stub := &fakeTokenServiceClient{resp: verdictResp(tokensv1.Verdict_VERDICT_NOT_FOUND)}
	p := &tokenServiceSecretProber{stub: stub, credNS: testCredNS}
	got, err := p.ProbeSecretAccess(context.Background(), "anthropic-key-2")
	if err != nil {
		t.Fatalf("ProbeSecretAccess: %v", err)
	}
	if got != admin.SecretProbeNotFound || stub.got != "anthropic-key-2" || stub.calls != 1 {
		t.Fatalf("verdict=%v name=%q calls=%d, want NOT_FOUND anthropic-key-2 1", got, stub.got, stub.calls)
	}
}

// spec: §4.9 (Credential Storage Topology), §17.6 (Packaging and
// Installation) — the composition root resolves --credentials-namespace
// once and hands the same value to the admin Router and the Token Service
// prober. An explicit empty flag value (envOr defaults only an empty
// environment variable) resolves to the default namespace for both, so a
// pool create with a bare secretRef is canonicalized by the Router, parsed
// by the prober, probed by name, and persisted with 201. A prober that kept
// the raw empty value would refuse the canonical ref and the create would
// answer 503 CREDENTIAL_PROBE_UNAVAILABLE.
func TestCredentialsNamespace_RouterAndProberAgree(t *testing.T) {
	for _, tc := range []struct {
		flag, wantNS string
	}{
		{flag: "", wantNS: credential.DefaultNamespace},
		{flag: "acme-creds", wantNS: "acme-creds"},
	} {
		t.Run("flag="+tc.flag, func(t *testing.T) {
			flagValue := tc.flag
			f := &gatewayFlags{credentialsNamespace: &flagValue}
			ns := f.resolvedCredentialsNamespace()
			if ns != tc.wantNS {
				t.Fatalf("resolvedCredentialsNamespace() = %q, want %q", ns, tc.wantNS)
			}
			stub := &fakeTokenServiceClient{resp: verdictResp(tokensv1.Verdict_VERDICT_ALLOWED)}
			pools := credentialpoolstore.NewMemory()
			router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{CredentialsNamespace: ns}).
				WithCredentialPools(pools).
				WithSecretAccessProber(newTokenServiceSecretProber(stub, ns))

			rr := postCredentialPool(t, router.Handler(), "anthropic-key-1")
			if rr.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
			}
			if stub.calls != 1 || stub.got != "anthropic-key-1" {
				t.Fatalf("probe calls=%d name=%q, want 1 anthropic-key-1", stub.calls, stub.got)
			}
			stored, err := pools.Get(context.Background(), "acme", "p1")
			if err != nil {
				t.Fatalf("pool not persisted: %v", err)
			}
			if got, want := stored.Credentials[0].SecretRef, tc.wantNS+"/anthropic-key-1"; got != want {
				t.Fatalf("stored secretRef = %q, want %q", got, want)
			}
		})
	}
}

// postCredentialPool creates pool acme/p1 holding one credential with
// secretRef ref, as a platform-admin.
func postCredentialPool(t *testing.T, h http.Handler, ref string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(admin.CredentialPoolPayload{
		TenantID: "acme", Name: "p1", Provider: "anthropic_direct",
		AssignmentStrategy: "least-loaded", MaxConcurrentSessions: 10,
		Credentials: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: ref}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credential-pools", bytes.NewReader(body))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "alice@acme.com", TenantID: "platform", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// keyedResp is a keyed ProbeSecretAccess response carrying value.
func keyedResp(v tokensv1.Verdict, value string) *tokensv1.ProbeSecretAccessResponse {
	return &tokensv1.ProbeSecretAccessResponse{Verdict: v, Value: []byte(value)}
}

// freshTokenServiceSubsystem returns a token_service subsystem with a
// closed breaker at the default threshold, as the composition root builds it.
func freshTokenServiceSubsystem() *subsystem.Subsystem {
	return &subsystem.Subsystem{Name: "token_service", Breaker: &subsystem.Breaker{}}
}

// spec: §4.9 (Token Service secret resolution), §14 — the VCS reader maps
// each keyed verdict. ALLOWED returns the value, NOT_FOUND and DENIED wrap
// vcscred.ErrNoUsableCredential with no value, and the request carries the
// parsed Secret name and the key `token` alone.
func TestTokenServiceVCSReader_KeyedVerdicts(t *testing.T) {
	cases := []struct {
		name      string
		resp      *tokensv1.ProbeSecretAccessResponse
		wantToken string
		wantNoUse bool
	}{
		{"allowed", keyedResp(tokensv1.Verdict_VERDICT_ALLOWED, "ghs_abc123"), "ghs_abc123", false},
		{"not_found", keyedResp(tokensv1.Verdict_VERDICT_NOT_FOUND, ""), "", true},
		{"denied", keyedResp(tokensv1.Verdict_VERDICT_DENIED, ""), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &fakeTokenServiceClient{resp: tc.resp}
			r := newTokenServiceVCSReader(stub, testCredNS, freshTokenServiceSubsystem())
			got, err := r.Resolve(context.Background(), testCredNS+"/gh-token")
			if tc.wantNoUse {
				if !errors.Is(err, vcscred.ErrNoUsableCredential) {
					t.Fatalf("Resolve error = %v, want ErrNoUsableCredential", err)
				}
			} else if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != tc.wantToken {
				t.Fatalf("token = %q, want %q", got, tc.wantToken)
			}
			if stub.calls != 1 || stub.got != "gh-token" || stub.key != "token" {
				t.Fatalf("calls=%d name=%q key=%q, want 1 gh-token token", stub.calls, stub.got, stub.key)
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology, Token Service secret resolution)
// — a stored ref the parser refuses, such as the release-namespace
// lenny-system/gh-token, yields ErrNoUsableCredential with no RPC made, so
// the gateway never asks the Token Service for a Secret outside the
// credentials namespace.
func TestTokenServiceVCSReader_RefusedRefIsNoUsableCredentialWithNoRPC(t *testing.T) {
	for _, ref := range []string{"lenny-system/gh-token", "a/b/c", "Not_A_Name", ""} {
		t.Run(ref, func(t *testing.T) {
			stub := &fakeTokenServiceClient{resp: keyedResp(tokensv1.Verdict_VERDICT_ALLOWED, "ghs_abc123")}
			r := newTokenServiceVCSReader(stub, testCredNS, freshTokenServiceSubsystem())
			got, err := r.Resolve(context.Background(), ref)
			if !errors.Is(err, vcscred.ErrNoUsableCredential) {
				t.Fatalf("Resolve(%q) error = %v, want ErrNoUsableCredential", ref, err)
			}
			if got != "" || stub.calls != 0 {
				t.Fatalf("Resolve(%q) token=%q calls=%d, want empty and 0", ref, got, stub.calls)
			}
		})
	}
}

// spec: §4.9 (Token Service secret resolution) rule 3, §4.3 — a gRPC error
// fails the read closed: no token, an error that does not wrap
// ErrNoUsableCredential (so the tool path answers INTERNAL_ERROR and plan
// binding answers the transient code), and no retry. An unspecified verdict
// is indeterminate in the same way.
func TestTokenServiceVCSReader_TransientFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		stub *fakeTokenServiceClient
	}{
		{"unavailable", &fakeTokenServiceClient{err: status.Error(codes.Unavailable, "token service down")}},
		{"unspecified", &fakeTokenServiceClient{resp: keyedResp(tokensv1.Verdict_VERDICT_UNSPECIFIED, "stale")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTokenServiceVCSReader(tc.stub, testCredNS, freshTokenServiceSubsystem())
			got, err := r.Resolve(context.Background(), "gh-token")
			if err == nil || errors.Is(err, vcscred.ErrNoUsableCredential) {
				t.Fatalf("Resolve error = %v, want a transient error that is not ErrNoUsableCredential", err)
			}
			if got != "" {
				t.Fatalf("token = %q, want none", got)
			}
			if tc.stub.calls != 1 {
				t.Fatalf("RPC calls = %d, want 1 (no retry)", tc.stub.calls)
			}
		})
	}
	stub := &fakeTokenServiceClient{err: status.Error(codes.Unavailable, "token service down")}
	_, err := newTokenServiceVCSReader(stub, testCredNS, nil).Resolve(context.Background(), "gh-token")
	if status.Code(errors.Unwrap(err)) != codes.Unavailable {
		t.Fatalf("Resolve error = %v, want the wrapped gRPC Unavailable", err)
	}
}

// spec: §4.3, §4.9 (Token Service secret resolution) — the keyed read runs
// inside Do on the token_service subsystem. After DefaultFailureThreshold
// consecutive Unavailable results the breaker opens and the next Resolve
// returns an error and no token with no RPC made. Definitive NOT_FOUND and
// DENIED verdicts never count as breaker failures. The no-RPC assertion
// fails against a reader that calls the stub outside the breaker.
func TestTokenServiceVCSReader_BreakerOpensOnUnavailableOnly(t *testing.T) {
	t.Run("unavailable opens", func(t *testing.T) {
		sub := freshTokenServiceSubsystem()
		stub := &fakeTokenServiceClient{err: status.Error(codes.Unavailable, "token service down")}
		r := newTokenServiceVCSReader(stub, testCredNS, sub)
		for i := 0; i < subsystem.DefaultFailureThreshold; i++ {
			if _, err := r.Resolve(context.Background(), "gh-token"); err == nil {
				t.Fatalf("call %d: want error", i)
			}
		}
		stub.err, stub.resp = nil, keyedResp(tokensv1.Verdict_VERDICT_ALLOWED, "ghs_abc123")
		got, err := r.Resolve(context.Background(), "gh-token")
		if !errors.Is(err, subsystem.ErrCircuitOpen) || got != "" {
			t.Fatalf("Resolve after threshold = (%q, %v), want no token and ErrCircuitOpen", got, err)
		}
		if errors.Is(err, vcscred.ErrNoUsableCredential) {
			t.Fatalf("open breaker error %v must not read as a missing credential", err)
		}
		if stub.calls != subsystem.DefaultFailureThreshold {
			t.Fatalf("RPC calls = %d, want %d (none once open)", stub.calls, subsystem.DefaultFailureThreshold)
		}
	})
	for _, v := range []tokensv1.Verdict{tokensv1.Verdict_VERDICT_NOT_FOUND, tokensv1.Verdict_VERDICT_DENIED} {
		t.Run(v.String()+" does not open", func(t *testing.T) {
			sub := freshTokenServiceSubsystem()
			stub := &fakeTokenServiceClient{resp: keyedResp(v, "")}
			r := newTokenServiceVCSReader(stub, testCredNS, sub)
			for i := 0; i < 2*subsystem.DefaultFailureThreshold; i++ {
				if _, err := r.Resolve(context.Background(), "gh-token"); !errors.Is(err, vcscred.ErrNoUsableCredential) {
					t.Fatalf("call %d: error = %v, want ErrNoUsableCredential", i, err)
				}
			}
			if sub.State() != subsystem.StateClosed {
				t.Fatalf("breaker state = %v, want closed", sub.State())
			}
			if stub.calls != 2*subsystem.DefaultFailureThreshold {
				t.Fatalf("RPC calls = %d, want %d", stub.calls, 2*subsystem.DefaultFailureThreshold)
			}
		})
	}
}

// spec: §4.9 (Token Service secret resolution), §14 — the composition root
// wires the VCS resolver only over the Token Service link. With no link it
// is nil (an authenticated gitClone fails with "no resolver wired"), and
// with one its SecretReader is the Token Service keyed read gated by the
// shared token_service subsystem in the credentials namespace.
func TestBuildVCSResolver_TokenServiceOnly(t *testing.T) {
	pools := credentialpoolstore.NewMemory()
	if r := buildVCSResolver(nil, pools, testCredNS, freshTokenServiceSubsystem()); r != nil {
		t.Fatalf("buildVCSResolver(nil conn) = %#v, want nil", r)
	}
	conn, err := grpc.NewClient("passthrough:///token-service", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sub := freshTokenServiceSubsystem()
	r := buildVCSResolver(conn, pools, testCredNS, sub)
	sr, ok := r.(*vcscred.StoreResolver)
	if !ok {
		t.Fatalf("buildVCSResolver = %T, want *vcscred.StoreResolver", r)
	}
	reader, ok := sr.Secrets.(*tokenServiceSecretProber)
	if !ok {
		t.Fatalf("SecretReader = %T, want the Token Service keyed read", sr.Secrets)
	}
	if reader.credNS != testCredNS || reader.tokenService != sub || reader.stub == nil {
		t.Fatalf("reader = %+v, want namespace %q and the shared subsystem", reader, testCredNS)
	}
}
