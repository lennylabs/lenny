//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the Token Service side of the ProbeSecretAccess
// wire contract on the gateway-to-Token-Service mTLS link. It serves the
// production gRPC server and the client-go-backed prober over a fake
// clientset through an in-memory gRPC connection and drives it with the
// generated client, so the assertions hold for the encoded messages:
//
//   - the request names a Secret by name alone: field 2, the retired
//     namespace override, is reserved, and a peer that still encodes it
//     cannot move the read out of the configured credentials namespace;
//   - a keyed ALLOWED response carries the key's value in field 2;
//   - an unkeyed call and every non-ALLOWED verdict carry no value;
//   - a failed read is codes.Unavailable with no value in the status.
//
// spec: §4.9 (Credential Lifecycle and Secret Management, Token Service secret
// resolution), §28.7 (lenny-tokenservice.proto).
package tokenservice_secret_read_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/pkg/tokenservice/secretprobe"
)

const (
	credNS    = "lenny-credentials"
	tokenText = "ghp_contract_value"
)

// dial serves the Token Service gRPC surface over cs and returns a client.
func dial(t *testing.T, cs *fake.Clientset) tokensv1.TokenServiceClient {
	t.Helper()
	leases := credleasestore.New()
	ts := tokenservice.NewGRPCServer(credassign.New(leases, credcache.New()), leases)
	ts.SetSecretAccessProber(secretprobe.New(cs, credNS))
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
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
	return tokensv1.NewTokenServiceClient(conn)
}

func clientset() *fake.Clientset {
	return fake.NewSimpleClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: credNS, Name: "github-token-1"},
			Data:       map[string][]byte{"token": []byte(tokenText)},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "lenny-system", Name: "only-elsewhere"},
			Data:       map[string][]byte{"token": []byte(tokenText)},
		},
	)
}

// TestProbeSecretAccessWireValue pins which responses carry field 2.
//
// diagnosis: a value on an unkeyed or non-ALLOWED response means
// GRPCServer.ProbeSecretAccess forwards the prober's bytes without checking
// the key and the verdict; a missing value on the keyed ALLOWED case means the
// response field or the prober's key lookup regressed.
func TestProbeSecretAccessWireValue(t *testing.T) {
	cl := dial(t, clientset())
	cases := []struct {
		name, secret, key string
		want              tokensv1.Verdict
		wantValue         string
	}{
		{"keyed allowed", "github-token-1", "token", tokensv1.Verdict_VERDICT_ALLOWED, tokenText},
		{"unkeyed allowed", "github-token-1", "", tokensv1.Verdict_VERDICT_ALLOWED, ""},
		{"keyed missing key", "github-token-1", "apiKey", tokensv1.Verdict_VERDICT_NOT_FOUND, ""},
		{"keyed absent", "absent", "token", tokensv1.Verdict_VERDICT_NOT_FOUND, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := cl.ProbeSecretAccess(context.Background(),
				&tokensv1.ProbeSecretAccessRequest{SecretName: tc.secret, Key: tc.key})
			if err != nil {
				t.Fatalf("ProbeSecretAccess: %v", err)
			}
			if resp.GetVerdict() != tc.want || string(resp.GetValue()) != tc.wantValue {
				t.Fatalf("got (%v, %q), want (%v, %q)", resp.GetVerdict(), resp.GetValue(), tc.want, tc.wantValue)
			}
		})
	}
}

// TestProbeSecretAccessWireDenied pins that a DENIED response carries no
// value.
//
// diagnosis: a value here means the server forwards bytes on a non-ALLOWED
// verdict; a non-DENIED verdict means Forbidden is no longer mapped.
func TestProbeSecretAccessWireDenied(t *testing.T) {
	cs := clientset()
	cs.PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(corev1.Resource("secrets"), "github-token-1", nil)
	})
	resp, err := dial(t, cs).ProbeSecretAccess(context.Background(),
		&tokensv1.ProbeSecretAccessRequest{SecretName: "github-token-1", Key: "token"})
	if err != nil {
		t.Fatalf("ProbeSecretAccess: %v", err)
	}
	if resp.GetVerdict() != tokensv1.Verdict_VERDICT_DENIED || len(resp.GetValue()) != 0 {
		t.Fatalf("got (%v, %q), want (DENIED, empty)", resp.GetVerdict(), resp.GetValue())
	}
}

// TestProbeSecretAccessWireUnavailable pins that a failed read crosses the
// wire as codes.Unavailable with the value absent from the status.
//
// diagnosis: a code other than Unavailable breaks the gateway's 503
// CREDENTIAL_PROBE_UNAVAILABLE mapping; the value in the message is a §16.4
// leak in the handler's error formatting.
func TestProbeSecretAccessWireUnavailable(t *testing.T) {
	cs := clientset()
	cs.PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(context.DeadlineExceeded)
	})
	resp, err := dial(t, cs).ProbeSecretAccess(context.Background(),
		&tokensv1.ProbeSecretAccessRequest{SecretName: "github-token-1", Key: "token"})
	if status.Code(err) != codes.Unavailable || resp != nil {
		t.Fatalf("got (%v, %v), want (nil, Unavailable)", resp, err)
	}
	if strings.Contains(status.Convert(err).Message(), tokenText) {
		t.Fatalf("status message carries the value: %q", status.Convert(err).Message())
	}
}

// TestProbeSecretAccessWireIgnoresNamespaceField pins that the request names
// a Secret by name alone. The descriptor declares no field 2, and a request
// whose encoding still carries field 2 naming another namespace reads only the
// configured credentials namespace.
//
// diagnosis: a declared field 2 means the reserved range was dropped from
// lenny-tokenservice.proto; ALLOWED with a value on the encoded case means the
// server honors a namespace sent by the peer, which reopens the
// cross-namespace existence oracle.
func TestProbeSecretAccessWireIgnoresNamespaceField(t *testing.T) {
	if (&tokensv1.ProbeSecretAccessRequest{}).ProtoReflect().Descriptor().Fields().ByNumber(2) != nil {
		t.Fatal("ProbeSecretAccessRequest declares field 2")
	}
	base, err := proto.Marshal(&tokensv1.ProbeSecretAccessRequest{SecretName: "only-elsewhere", Key: "token"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw := protowire.AppendTag(base, 2, protowire.BytesType)
	raw = protowire.AppendString(raw, "lenny-system")
	req := &tokensv1.ProbeSecretAccessRequest{}
	if err := proto.Unmarshal(raw, req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("field 2 was not carried as an unknown field")
	}
	resp, err := dial(t, clientset()).ProbeSecretAccess(context.Background(), req)
	if err != nil {
		t.Fatalf("ProbeSecretAccess: %v", err)
	}
	if resp.GetVerdict() != tokensv1.Verdict_VERDICT_NOT_FOUND || len(resp.GetValue()) != 0 {
		t.Fatalf("got (%v, %q), want (NOT_FOUND, empty)", resp.GetVerdict(), resp.GetValue())
	}
}
