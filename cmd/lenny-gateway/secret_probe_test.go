// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
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
