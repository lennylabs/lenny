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
	resp *tokensv1.ProbeSecretAccessResponse
	err  error
	got  string
}

func (f *fakeTokenServiceClient) ProbeSecretAccess(_ context.Context, req *tokensv1.ProbeSecretAccessRequest, _ ...grpc.CallOption) (*tokensv1.ProbeSecretAccessResponse, error) {
	f.got = req.GetSecretName()
	return f.resp, f.err
}

func verdictResp(v tokensv1.Verdict) *tokensv1.ProbeSecretAccessResponse {
	return &tokensv1.ProbeSecretAccessResponse{Verdict: v}
}

// spec: §4.9 line 1212 — the gateway adapter maps each wire verdict onto
// the admin verdict the credential-pool handler consumes.
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
			p := &tokenServiceSecretProber{stub: stub}
			got, err := p.ProbeSecretAccess(context.Background(), "anthropic-key-1")
			if err != nil {
				t.Fatalf("ProbeSecretAccess: %v", err)
			}
			if got != tc.want {
				t.Fatalf("verdict = %v, want %v", got, tc.want)
			}
			if stub.got != "anthropic-key-1" {
				t.Fatalf("probed %q, want anthropic-key-1", stub.got)
			}
		})
	}
}

// spec: §4.9 line 1212 — an RPC error (the probe could not return a
// definitive verdict) is propagated so the handler maps it to 503.
func TestTokenServiceSecretProber_RPCErrorPropagates(t *testing.T) {
	p := &tokenServiceSecretProber{stub: &fakeTokenServiceClient{err: errors.New("unavailable")}}
	if _, err := p.ProbeSecretAccess(context.Background(), "anthropic-key-1"); err == nil {
		t.Fatal("want error from failed RPC")
	}
}

// An unspecified verdict is treated as indeterminate (error), never as a
// silent ALLOWED.
func TestTokenServiceSecretProber_UnspecifiedIsError(t *testing.T) {
	p := &tokenServiceSecretProber{stub: &fakeTokenServiceClient{resp: verdictResp(tokensv1.Verdict_VERDICT_UNSPECIFIED)}}
	if _, err := p.ProbeSecretAccess(context.Background(), "anthropic-key-1"); err == nil {
		t.Fatal("want error for unspecified verdict")
	}
}
