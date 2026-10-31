// SPDX-License-Identifier: MIT

package secretprobe

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

const credNS = "lenny-credentials"

func secretObj(ns, name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Data: data}
}

// failGet makes every Secret get on cs return err.
func failGet(cs *fake.Clientset, err error) {
	cs.PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
}

// spec: §4.9 Token Service secret resolution rules 2 to 4 — one get maps to
// the verdict, a keyed ALLOWED returns the key's value, a missing or empty key
// is NOT_FOUND with no value, and an unkeyed read never returns a value.
func TestProbe_VerdictsAndValues(t *testing.T) {
	present := secretObj(credNS, "github-token-1", map[string][]byte{
		"token": []byte("ghp_value"), "empty": {},
	})
	cases := []struct {
		name, secret, key string
		want              tokenservice.SecretAccessVerdict
		wantValue         []byte
	}{
		{"unkeyed allowed", "github-token-1", "", tokenservice.SecretAccessAllowed, nil},
		{"keyed allowed", "github-token-1", "token", tokenservice.SecretAccessAllowed, []byte("ghp_value")},
		{"keyed missing key", "github-token-1", "apiKey", tokenservice.SecretAccessNotFound, nil},
		{"keyed empty value", "github-token-1", "empty", tokenservice.SecretAccessNotFound, nil},
		{"unkeyed absent", "absent", "", tokenservice.SecretAccessNotFound, nil},
		{"keyed absent", "absent", "token", tokenservice.SecretAccessNotFound, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := fake.NewSimpleClientset(present.DeepCopy())
			got, value, err := New(cs, credNS).ProbeSecretAccess(context.Background(), tc.secret, tc.key)
			if err != nil {
				t.Fatalf("ProbeSecretAccess: %v", err)
			}
			if got != tc.want || !bytes.Equal(value, tc.wantValue) {
				t.Fatalf("got (%v, %q), want (%v, %q)", got, value, tc.want, tc.wantValue)
			}
			assertSingleGet(t, cs)
		})
	}
}

// assertSingleGet checks that the read issued exactly one Secret get in the
// credentials namespace and no other request (no SelfSubjectAccessReview, no
// list or watch for a cache).
func assertSingleGet(t *testing.T, cs *fake.Clientset) {
	t.Helper()
	actions := cs.Actions()
	if len(actions) != 1 {
		t.Fatalf("actions = %v, want exactly one get", actions)
	}
	a := actions[0]
	if a.GetVerb() != "get" || a.GetResource().Resource != "secrets" || a.GetNamespace() != credNS {
		t.Fatalf("action = %s %s in %q, want get secrets in %q", a.GetVerb(), a.GetResource().Resource, a.GetNamespace(), credNS)
	}
}

// spec: §4.9 Token Service secret resolution rule 3 — Forbidden on the get is
// DENIED, keyed and unkeyed, with no value and no access review.
func TestProbe_ForbiddenIsDenied(t *testing.T) {
	for _, key := range []string{"", "token"} {
		cs := fake.NewSimpleClientset(secretObj(credNS, "k", map[string][]byte{"token": []byte("v")}))
		failGet(cs, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "k", nil))
		got, value, err := New(cs, credNS).ProbeSecretAccess(context.Background(), "k", key)
		if err != nil || got != tokenservice.SecretAccessDenied || value != nil {
			t.Fatalf("key %q: got (%v, %q, %v), want (Denied, nil, nil)", key, got, value, err)
		}
		assertSingleGet(t, cs)
	}
}

// spec: §4.9 Token Service secret resolution rule 3 — a failure other than
// Forbidden or NotFound is an error with no value, and the RPC maps it to
// Unavailable with the value absent from the status message.
func TestProbe_ServerErrorFailsClosed(t *testing.T) {
	cs := fake.NewSimpleClientset(secretObj(credNS, "k", map[string][]byte{"token": []byte("sk-leak-check")}))
	failGet(cs, apierrors.NewInternalError(context.DeadlineExceeded))
	got, value, err := New(cs, credNS).ProbeSecretAccess(context.Background(), "k", "token")
	if err == nil || value != nil {
		t.Fatalf("got (%v, %q, %v), want an error and no value", got, value, err)
	}
	srv := newGRPCServer(New(cs, credNS))
	resp, rerr := srv.ProbeSecretAccess(context.Background(), &tokensv1.ProbeSecretAccessRequest{SecretName: "k", Key: "token"})
	if status.Code(rerr) != codes.Unavailable || resp != nil {
		t.Fatalf("RPC = (%v, %v), want (nil, Unavailable)", resp, rerr)
	}
	if strings.Contains(status.Convert(rerr).Message(), "sk-leak-check") {
		t.Fatalf("status message carries the value: %q", status.Convert(rerr).Message())
	}
}

// spec: §4.9 Token Service secret resolution rule 2 — no cache serves a read,
// so two reads around a Secret update return the old then the new value.
func TestProbe_NoCache(t *testing.T) {
	cs := fake.NewSimpleClientset(secretObj(credNS, "k", map[string][]byte{"token": []byte("old")}))
	p := New(cs, credNS)
	_, v1, err := p.ProbeSecretAccess(context.Background(), "k", "token")
	if err != nil || string(v1) != "old" {
		t.Fatalf("first read = (%q, %v), want old", v1, err)
	}
	if _, err := cs.CoreV1().Secrets(credNS).Update(context.Background(),
		secretObj(credNS, "k", map[string][]byte{"token": []byte("new")}), metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	_, v2, err := p.ProbeSecretAccess(context.Background(), "k", "token")
	if err != nil || string(v2) != "new" {
		t.Fatalf("second read = (%q, %v), want new", v2, err)
	}
}

// spec: §4.9 Token Service secret resolution rule 1 — the prober reads only
// its configured namespace, so a name whose Secret exists only in another
// namespace is NOT_FOUND with no value, keyed and unkeyed.
func TestProbe_OtherNamespaceIsNotFound(t *testing.T) {
	for _, key := range []string{"", "token"} {
		cs := fake.NewSimpleClientset(secretObj("lenny-system", "k", map[string][]byte{"token": []byte("v")}))
		got, value, err := New(cs, credNS).ProbeSecretAccess(context.Background(), "k", key)
		if err != nil || got != tokenservice.SecretAccessNotFound || value != nil {
			t.Fatalf("key %q: got (%v, %q, %v), want (NotFound, nil, nil)", key, got, value, err)
		}
	}
}

// spec: §4.9 Token Service secret resolution rule 3 — a kube-apiserver that
// never answers fails the read within ReadTimeout, and the RPC returns
// Unavailable with no value.
func TestProbe_HangingAPIServerUnavailable(t *testing.T) {
	release := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); api.Close() })
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: api.URL})
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}
	srv := newGRPCServer(New(cs, credNS))
	start := time.Now()
	resp, rerr := srv.ProbeSecretAccess(context.Background(), &tokensv1.ProbeSecretAccessRequest{SecretName: "k", Key: "token"})
	elapsed := time.Since(start)
	if status.Code(rerr) != codes.Unavailable || resp != nil {
		t.Fatalf("RPC = (%v, %v), want (nil, Unavailable)", resp, rerr)
	}
	if elapsed < ReadTimeout || elapsed > ReadTimeout+5*time.Second {
		t.Fatalf("read returned after %v, want about %v", elapsed, ReadTimeout)
	}
}

func newGRPCServer(p tokenservice.SecretAccessProber) *tokenservice.GRPCServer {
	leases := credleasestore.New()
	srv := tokenservice.NewGRPCServer(credassign.New(leases, credcache.New()), leases)
	srv.SetSecretAccessProber(p)
	return srv
}
