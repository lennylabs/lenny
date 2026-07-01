// SPDX-License-Identifier: MIT

package connectorsecret

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func secretObj(ns, name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Data:       data,
	}
}

func resolverWith(secrets ...*corev1.Secret) *KubeResolver {
	b := fake.NewClientBuilder()
	for _, s := range secrets {
		b = b.WithObjects(s)
	}
	return NewKubeResolver(b.Build(), "")
}

// spec: §9.3 line 129 — a two-segment clientSecretRef resolves the
// conventional default Secret key.
func TestKubeResolver_DefaultKey_spec_9_3_129(t *testing.T) {
	r := resolverWith(secretObj("lenny-system", "github-client-secret",
		map[string][]byte{DefaultSecretKey: []byte("s3cr3t")}))
	got, err := r.Resolve(context.Background(), "lenny-system/github-client-secret")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "s3cr3t" {
		t.Fatalf("want s3cr3t, got %q", got)
	}
}

// A three-segment reference addresses an explicit Secret data key.
func TestKubeResolver_ExplicitKey(t *testing.T) {
	r := resolverWith(secretObj("lenny-system", "jira-creds",
		map[string][]byte{"oauthSecret": []byte("abc123")}))
	got, err := r.Resolve(context.Background(), "lenny-system/jira-creds/oauthSecret")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("want abc123, got %q", got)
	}
}

func TestKubeResolver_SecretNotFound(t *testing.T) {
	r := resolverWith()
	_, err := r.Resolve(context.Background(), "lenny-system/absent")
	if !errors.Is(err, ErrClientSecretNotFound) {
		t.Fatalf("want ErrClientSecretNotFound, got %v", err)
	}
}

func TestKubeResolver_KeyMissing(t *testing.T) {
	r := resolverWith(secretObj("lenny-system", "github-client-secret",
		map[string][]byte{"other": []byte("x")}))
	_, err := r.Resolve(context.Background(), "lenny-system/github-client-secret")
	if !errors.Is(err, ErrClientSecretNotFound) {
		t.Fatalf("want ErrClientSecretNotFound for missing key, got %v", err)
	}
}

func TestKubeResolver_EmptyValue(t *testing.T) {
	r := resolverWith(secretObj("lenny-system", "github-client-secret",
		map[string][]byte{DefaultSecretKey: {}}))
	_, err := r.Resolve(context.Background(), "lenny-system/github-client-secret")
	if !errors.Is(err, ErrClientSecretNotFound) {
		t.Fatalf("want ErrClientSecretNotFound for empty value, got %v", err)
	}
}

func TestKubeResolver_MalformedRef(t *testing.T) {
	r := resolverWith()
	for _, ref := range []string{"", "name-only", "a/b/c/d", "/name", "ns/", "ns//key"} {
		if _, err := r.Resolve(context.Background(), ref); err == nil {
			t.Fatalf("want error for malformed ref %q", ref)
		} else if errors.Is(err, ErrClientSecretNotFound) {
			t.Fatalf("malformed ref %q should not be ErrClientSecretNotFound", ref)
		}
	}
}

func TestNewKubeResolver_EmptyKeyDefaults(t *testing.T) {
	r := NewKubeResolver(fake.NewClientBuilder().Build(), "")
	if r.defaultKey != DefaultSecretKey {
		t.Fatalf("want defaultKey %q, got %q", DefaultSecretKey, r.defaultKey)
	}
}
