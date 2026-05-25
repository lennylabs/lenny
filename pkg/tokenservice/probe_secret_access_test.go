// SPDX-License-Identifier: MIT

package tokenservice

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// stubProber returns a fixed verdict or error for ProbeSecretAccess.
type stubProber struct {
	verdict SecretAccessVerdict
	err     error
	gotNS   string
	gotName string
}

func (s *stubProber) ProbeSecretAccess(_ context.Context, ns, name string) (SecretAccessVerdict, error) {
	s.gotNS, s.gotName = ns, name
	return s.verdict, s.err
}

func newProbeServer() *GRPCServer {
	leases := credleasestore.New()
	return NewGRPCServer(credassign.New(leases, credcache.New()), leases)
}

// spec: §4.9 line 1212 — a definitive verdict maps to the wire enum.
func TestProbeSecretAccess_VerdictMapping(t *testing.T) {
	cases := []struct {
		name string
		in   SecretAccessVerdict
		want tokensv1.Verdict
	}{
		{"allowed", SecretAccessAllowed, tokensv1.Verdict_VERDICT_ALLOWED},
		{"denied", SecretAccessDenied, tokensv1.Verdict_VERDICT_DENIED},
		{"not_found", SecretAccessNotFound, tokensv1.Verdict_VERDICT_NOT_FOUND},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newProbeServer()
			srv.SetSecretAccessProber(&stubProber{verdict: tc.in})
			resp, err := srv.ProbeSecretAccess(context.Background(),
				&tokensv1.ProbeSecretAccessRequest{SecretName: "anthropic-key-1"})
			if err != nil {
				t.Fatalf("ProbeSecretAccess: %v", err)
			}
			if resp.GetVerdict() != tc.want {
				t.Fatalf("verdict = %v, want %v", resp.GetVerdict(), tc.want)
			}
		})
	}
}

// spec: §4.9 line 1212 — with no prober wired (no in-cluster client) the
// RPC returns Unavailable so the gateway maps it to 503 and never fails
// open by treating an unevaluated probe as ALLOWED.
func TestProbeSecretAccess_NoProberUnavailable(t *testing.T) {
	srv := newProbeServer()
	_, err := srv.ProbeSecretAccess(context.Background(),
		&tokensv1.ProbeSecretAccessRequest{SecretName: "anthropic-key-1"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable", status.Code(err))
	}
}

// spec: §4.9 line 1212 — an indeterminate probe (prober error) is
// Unavailable, distinct from a definitive DENIED.
func TestProbeSecretAccess_ProberErrorUnavailable(t *testing.T) {
	srv := newProbeServer()
	srv.SetSecretAccessProber(&stubProber{err: errors.New("api timeout")})
	_, err := srv.ProbeSecretAccess(context.Background(),
		&tokensv1.ProbeSecretAccessRequest{SecretName: "anthropic-key-1"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable", status.Code(err))
	}
}

// An empty secret_name is rejected before the prober is consulted.
func TestProbeSecretAccess_EmptySecretName(t *testing.T) {
	srv := newProbeServer()
	pr := &stubProber{verdict: SecretAccessAllowed}
	srv.SetSecretAccessProber(pr)
	_, err := srv.ProbeSecretAccess(context.Background(), &tokensv1.ProbeSecretAccessRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
	}
	if pr.gotName != "" {
		t.Fatal("prober consulted despite empty secret_name")
	}
}

// The request namespace is threaded to the prober (empty selects the
// prober's configured default).
func TestProbeSecretAccess_NamespacePassthrough(t *testing.T) {
	srv := newProbeServer()
	pr := &stubProber{verdict: SecretAccessAllowed}
	srv.SetSecretAccessProber(pr)
	if _, err := srv.ProbeSecretAccess(context.Background(),
		&tokensv1.ProbeSecretAccessRequest{SecretName: "k", Namespace: "lenny-system"}); err != nil {
		t.Fatalf("ProbeSecretAccess: %v", err)
	}
	if pr.gotNS != "lenny-system" || pr.gotName != "k" {
		t.Fatalf("prober got (%q,%q), want (lenny-system,k)", pr.gotNS, pr.gotName)
	}
}
