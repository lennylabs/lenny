// SPDX-License-Identifier: MIT

package tokenservice

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// stubProber returns a fixed verdict, value, or error for ProbeSecretAccess
// and records what it was asked.
type stubProber struct {
	verdict SecretAccessVerdict
	value   []byte
	err     error
	calls   int
	gotName string
	gotKey  string
}

func (s *stubProber) ProbeSecretAccess(_ context.Context, name, key string) (SecretAccessVerdict, []byte, error) {
	s.calls++
	s.gotName, s.gotKey = name, key
	return s.verdict, s.value, s.err
}

func newProbeServer() (*GRPCServer, *recordingMetrics) {
	leases := credleasestore.New()
	srv := NewGRPCServer(credassign.New(leases, credcache.New()), leases)
	m := &recordingMetrics{}
	srv.SetMetrics(m)
	return srv, m
}

const testSecretValue = "sk-ant-secret-value-0123456789"

// spec: §4.9 Token Service secret resolution rules 3 and 4, §16.1 — each
// definitive verdict maps to the wire enum, a keyed ALLOWED carries the
// value, an unkeyed call or a non-ALLOWED verdict carries none, and each call
// records exactly one outcome.
func TestProbeSecretAccess_VerdictValueAndOutcome(t *testing.T) {
	cases := []struct {
		name        string
		verdict     SecretAccessVerdict
		key         string
		want        tokensv1.Verdict
		wantValue   []byte
		wantOutcome string
	}{
		{"unkeyed allowed", SecretAccessAllowed, "", tokensv1.Verdict_VERDICT_ALLOWED, nil, "success"},
		{"keyed allowed", SecretAccessAllowed, "token", tokensv1.Verdict_VERDICT_ALLOWED, []byte(testSecretValue), "success"},
		{"unkeyed denied", SecretAccessDenied, "", tokensv1.Verdict_VERDICT_DENIED, nil, "denied"},
		{"keyed denied", SecretAccessDenied, "token", tokensv1.Verdict_VERDICT_DENIED, nil, "denied"},
		{"unkeyed not found", SecretAccessNotFound, "", tokensv1.Verdict_VERDICT_NOT_FOUND, nil, "not_found"},
		{"keyed not found", SecretAccessNotFound, "token", tokensv1.Verdict_VERDICT_NOT_FOUND, nil, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, m := newProbeServer()
			// The stub returns a value on every verdict, so a handler that
			// forwarded it without checking the key and the verdict fails.
			pr := &stubProber{verdict: tc.verdict, value: []byte(testSecretValue)}
			srv.SetSecretAccessProber(pr)
			resp, err := srv.ProbeSecretAccess(context.Background(),
				&tokensv1.ProbeSecretAccessRequest{SecretName: "anthropic-key-1", Key: tc.key})
			if err != nil {
				t.Fatalf("ProbeSecretAccess: %v", err)
			}
			if resp.GetVerdict() != tc.want {
				t.Fatalf("verdict = %v, want %v", resp.GetVerdict(), tc.want)
			}
			if !bytes.Equal(resp.GetValue(), tc.wantValue) {
				t.Fatalf("value = %q, want %q", resp.GetValue(), tc.wantValue)
			}
			if pr.gotName != "anthropic-key-1" || pr.gotKey != tc.key {
				t.Fatalf("prober got (%q,%q), want (anthropic-key-1,%q)", pr.gotName, pr.gotKey, tc.key)
			}
			if got := m.secretReadOutcomes(); !slices.Equal(got, []string{tc.wantOutcome}) {
				t.Fatalf("outcomes = %v, want [%s]", got, tc.wantOutcome)
			}
		})
	}
}

// spec: §4.9 Token Service secret resolution rule 3, §16.1 — with no prober
// wired (no in-cluster client) the RPC returns Unavailable, records the
// unavailable outcome, and never fails open by treating an unevaluated read
// as ALLOWED.
func TestProbeSecretAccess_NoProberUnavailable(t *testing.T) {
	srv, m := newProbeServer()
	_, err := srv.ProbeSecretAccess(context.Background(),
		&tokensv1.ProbeSecretAccessRequest{SecretName: "anthropic-key-1", Key: "token"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable", status.Code(err))
	}
	if got := m.secretReadOutcomes(); !slices.Equal(got, []string{"unavailable"}) {
		t.Fatalf("outcomes = %v, want [unavailable]", got)
	}
}

// spec: §4.9 Token Service secret resolution rule 3, §16.4 — a failed read
// is Unavailable with no value, records the unavailable outcome, and the
// status message carries no value even when the prober returned bytes.
func TestProbeSecretAccess_ProberErrorUnavailable(t *testing.T) {
	srv, m := newProbeServer()
	srv.SetSecretAccessProber(&stubProber{
		verdict: SecretAccessAllowed, value: []byte(testSecretValue), err: errors.New("api timeout"),
	})
	resp, err := srv.ProbeSecretAccess(context.Background(),
		&tokensv1.ProbeSecretAccessRequest{SecretName: "anthropic-key-1", Key: "token"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable", status.Code(err))
	}
	if resp != nil {
		t.Fatalf("response = %v, want nil on a failed read", resp)
	}
	if strings.Contains(status.Convert(err).Message(), testSecretValue) {
		t.Fatalf("status message carries the value: %q", status.Convert(err).Message())
	}
	if got := m.secretReadOutcomes(); !slices.Equal(got, []string{"unavailable"}) {
		t.Fatalf("outcomes = %v, want [unavailable]", got)
	}
}

// An empty secret_name is rejected before the prober is consulted, and no
// read outcome is recorded because no read happened.
func TestProbeSecretAccess_EmptySecretName(t *testing.T) {
	srv, m := newProbeServer()
	pr := &stubProber{verdict: SecretAccessAllowed}
	srv.SetSecretAccessProber(pr)
	_, err := srv.ProbeSecretAccess(context.Background(), &tokensv1.ProbeSecretAccessRequest{Key: "token"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
	}
	if pr.calls != 0 {
		t.Fatal("prober consulted despite empty secret_name")
	}
	if got := m.secretReadOutcomes(); len(got) != 0 {
		t.Fatalf("outcomes = %v, want none", got)
	}
}

// spec: §4.9 Token Service secret resolution rule 1 — the request names a
// Secret by name alone. Field 2 (the retired namespace override) is
// reserved, so a client that still sends it has the bytes dropped as
// unknown and the prober receives only the name and the key.
func TestProbeSecretAccess_RequestCarriesNoNamespace(t *testing.T) {
	fields := (&tokensv1.ProbeSecretAccessRequest{}).ProtoReflect().Descriptor().Fields()
	if fields.ByName("namespace") != nil || fields.ByNumber(2) != nil {
		t.Fatal("ProbeSecretAccessRequest still declares a namespace field")
	}
	if fields.ByName("key") == nil || fields.ByNumber(3) == nil {
		t.Fatal("ProbeSecretAccessRequest declares no key field 3")
	}
	if (&tokensv1.ProbeSecretAccessResponse{}).ProtoReflect().Descriptor().Fields().ByNumber(2) == nil {
		t.Fatal("ProbeSecretAccessResponse declares no value field 2")
	}
}
