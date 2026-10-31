// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/pkg/tokenservice/secretprobe"
)

const vcsWireToken = "ghs_wire_value"

// vcsWireClientset holds the credential Secret in the credentials namespace
// and a same-valued Secret in the release namespace under two names, one of
// which exists only there.
func vcsWireClientset() *fake.Clientset {
	secret := func(ns, name string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Data:       map[string][]byte{"token": []byte(vcsWireToken)},
		}
	}
	return fake.NewSimpleClientset(
		secret(testCredNS, "gh-token"),
		secret("lenny-system", "gh-token"),
		secret("lenny-system", "only-elsewhere"),
	)
}

// dialVCSWireTokenService serves the production Token Service gRPC surface
// over cs and returns a generated client on an in-memory connection.
func dialVCSWireTokenService(t *testing.T, cs *fake.Clientset) tokensv1.TokenServiceClient {
	t.Helper()
	leases := credleasestore.New()
	ts := tokenservice.NewGRPCServer(credassign.New(leases, credcache.New()), leases)
	ts.SetSecretAccessProber(secretprobe.New(cs, testCredNS))
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

// secretGets counts the Secret gets the Token Service issued.
func secretGets(cs *fake.Clientset) int {
	n := 0
	for _, a := range cs.Actions() {
		if a.GetVerb() == "get" && a.GetResource().Resource == "secrets" {
			n++
		}
	}
	return n
}

// resolveVCSToken runs the production StoreResolver over a github pool whose
// one credential stores ref, with the gateway's Token Service keyed read.
func resolveVCSToken(t *testing.T, cs *fake.Clientset, ref string) (vcscred.Credential, error) {
	t.Helper()
	pools := credentialpoolstore.NewMemory()
	if err := pools.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "github-pool", Provider: "github", HostPatterns: []string{"github.com"},
		Credentials: []credentialpoolstore.Credential{{ID: "c1", SecretRef: ref}},
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	r := &vcscred.StoreResolver{
		Pools:   pools,
		Secrets: newTokenServiceVCSReader(dialVCSWireTokenService(t, cs), testCredNS, freshTokenServiceSubsystem()),
	}
	return r.Resolve(context.Background(), "acme", "https://github.com/acme/r.git", "vcs.github.read")
}

// spec: §4.9 (Credentials namespace, Token Service secret resolution), §14 —
// the gateway VCS read reaches only the credentials namespace through the
// Token Service. A canonical ref returns the token. A stored release-namespace
// ref is refused by the gateway parser before any Token Service read, and a
// bare name whose Secret exists only in another namespace is NOT_FOUND, so
// no ref selects a Secret outside the credentials namespace. A kube-apiserver
// failure fails the read closed with no token and no value in the error.
func TestVCSReadThroughTokenService_CredentialsNamespaceOnly(t *testing.T) {
	t.Run("canonical ref returns the token", func(t *testing.T) {
		cs := vcsWireClientset()
		cred, err := resolveVCSToken(t, cs, testCredNS+"/gh-token")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if cred.Token != vcsWireToken || cred.Username != vcscred.GitHubTokenUsername {
			t.Fatalf("credential = %+v, want the stored token", cred)
		}
	})
	t.Run("release namespace ref refused with no read", func(t *testing.T) {
		cs := vcsWireClientset()
		cred, err := resolveVCSToken(t, cs, "lenny-system/gh-token")
		if !errors.Is(err, vcscred.ErrNoUsableCredential) || !cred.IsZero() {
			t.Fatalf("Resolve = (%+v, %v), want no token and ErrNoUsableCredential", cred, err)
		}
		if n := secretGets(cs); n != 0 {
			t.Fatalf("Token Service issued %d Secret gets, want 0", n)
		}
	})
	t.Run("name present only in another namespace is not found", func(t *testing.T) {
		cs := vcsWireClientset()
		cred, err := resolveVCSToken(t, cs, "only-elsewhere")
		if !errors.Is(err, vcscred.ErrNoUsableCredential) || !cred.IsZero() {
			t.Fatalf("Resolve = (%+v, %v), want no token and ErrNoUsableCredential", cred, err)
		}
	})
	t.Run("kube-apiserver failure fails closed", func(t *testing.T) {
		cs := vcsWireClientset()
		cs.PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewInternalError(context.DeadlineExceeded)
		})
		cred, err := resolveVCSToken(t, cs, testCredNS+"/gh-token")
		if err == nil || errors.Is(err, vcscred.ErrNoUsableCredential) || !cred.IsZero() {
			t.Fatalf("Resolve = (%+v, %v), want no token and a transient error", cred, err)
		}
		if strings.Contains(err.Error(), vcsWireToken) {
			t.Fatalf("error carries the token value: %v", err)
		}
	})
}
