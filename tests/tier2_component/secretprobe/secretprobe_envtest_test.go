// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component coverage for the Token Service credential Secret read
// against a real kube-apiserver with RBAC authorization. The prober runs under
// an impersonated Token Service identity that holds a namespace-wide get on
// secrets in the credentials namespace and nothing else, which is the grant
// the chart renders. The fake clientset cannot evaluate RBAC or namespace
// scoping, so the Forbidden mapping, the namespace confinement, and the
// uncached read are pinned here.
//
// spec: §4.9 (Credential Lifecycle and Secret Management, Token Service secret
// resolution).
package secretprobe_test

import (
	"bytes"
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/pkg/tokenservice/secretprobe"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const (
	credNS    = "lenny-credentials"
	otherNS   = "lenny-system"
	tsUser    = "system:serviceaccount:lenny-system:lenny-token-service"
	plainUser = "system:serviceaccount:lenny-system:no-grant"
)

// apiFixture holds an envtest admin clientset and the rest config used to
// build impersonating clients.
type apiFixture struct {
	admin kubernetes.Interface
	cfg   *rest.Config
}

// startAPI boots envtest, creates both namespaces, and binds tsUser to a Role
// granting get on secrets in the credentials namespace.
func startAPI(t *testing.T) *apiFixture {
	t.Helper()
	env := envtest.Start(t)
	admin, err := kubernetes.NewForConfig(env.RESTConfig())
	if err != nil {
		t.Fatalf("admin clientset: %v", err)
	}
	ctx := context.Background()
	for _, ns := range []string{credNS, otherNS} {
		if _, err := admin.CoreV1().Namespaces().Create(ctx,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create namespace %s: %v", ns, err)
		}
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-token-service-credential-reader", Namespace: credNS},
		Rules:      []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get"}}},
	}
	if _, err := admin.RbacV1().Roles(credNS).Create(ctx, role, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: role.Name, Namespace: credNS},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: tsUser}},
	}
	if _, err := admin.RbacV1().RoleBindings(credNS).Create(ctx, binding, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create rolebinding: %v", err)
	}
	return &apiFixture{admin: admin, cfg: env.RESTConfig()}
}

// proberAs returns a prober for namespace ns whose client impersonates user.
func (f *apiFixture) proberAs(t *testing.T, user, ns string) *secretprobe.Prober {
	t.Helper()
	cfg := rest.CopyConfig(f.cfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("impersonating clientset: %v", err)
	}
	return secretprobe.New(cs, ns)
}

func (f *apiFixture) putSecret(t *testing.T, ns, name string, data map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Data: data}
	if _, err := f.admin.CoreV1().Secrets(ns).Create(ctx, s, metav1.CreateOptions{}); err == nil {
		return
	}
	if _, err := f.admin.CoreV1().Secrets(ns).Update(ctx, s, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("put secret %s/%s: %v", ns, name, err)
	}
}

// TestTokenServiceSecretReadAgainstAPIServer pins the read verdicts, values,
// namespace confinement, Forbidden mapping, and the absence of a cache.
//
// spec: §4.9 (Credential Lifecycle and Secret Management, Token Service secret
// resolution).
//
// diagnosis: a wrong verdict on a keyed case points at the key handling in
// secretprobe.ProbeSecretAccess; DENIED where NOT_FOUND is expected on the
// other-namespace case means the prober read outside its configured namespace;
// a stale value on the second read means a cache was reintroduced; a failure
// before any case runs is an envtest or RBAC fixture failure.
func TestTokenServiceSecretReadAgainstAPIServer(t *testing.T) {
	f := startAPI(t)
	f.putSecret(t, credNS, "github-token-1", map[string][]byte{"token": []byte("ghp_old"), "empty": {}})
	f.putSecret(t, otherNS, "only-elsewhere", map[string][]byte{"token": []byte("elsewhere")})
	ts := f.proberAs(t, tsUser, credNS)
	ctx := context.Background()

	cases := []struct {
		name, secret, key string
		want              tokenservice.SecretAccessVerdict
		wantValue         []byte
	}{
		{"unkeyed allowed returns no value", "github-token-1", "", tokenservice.SecretAccessAllowed, nil},
		{"keyed allowed returns value", "github-token-1", "token", tokenservice.SecretAccessAllowed, []byte("ghp_old")},
		{"missing key", "github-token-1", "apiKey", tokenservice.SecretAccessNotFound, nil},
		{"empty value", "github-token-1", "empty", tokenservice.SecretAccessNotFound, nil},
		{"absent secret", "absent", "token", tokenservice.SecretAccessNotFound, nil},
		{"other namespace unkeyed", "only-elsewhere", "", tokenservice.SecretAccessNotFound, nil},
		{"other namespace keyed", "only-elsewhere", "token", tokenservice.SecretAccessNotFound, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, value, err := ts.ProbeSecretAccess(ctx, tc.secret, tc.key)
			if err != nil {
				t.Fatalf("ProbeSecretAccess: %v", err)
			}
			if got != tc.want || !bytes.Equal(value, tc.wantValue) {
				t.Fatalf("got (%v, %q), want (%v, %q)", got, value, tc.want, tc.wantValue)
			}
		})
	}

	t.Run("no grant is denied", func(t *testing.T) {
		for _, key := range []string{"", "token"} {
			got, value, err := f.proberAs(t, plainUser, credNS).ProbeSecretAccess(ctx, "github-token-1", key)
			if err != nil || got != tokenservice.SecretAccessDenied || value != nil {
				t.Fatalf("key %q: got (%v, %q, %v), want (Denied, nil, nil)", key, got, value, err)
			}
		}
	})

	t.Run("update is visible on the next read", func(t *testing.T) {
		f.putSecret(t, credNS, "github-token-1", map[string][]byte{"token": []byte("ghp_new")})
		_, value, err := ts.ProbeSecretAccess(ctx, "github-token-1", "token")
		if err != nil || string(value) != "ghp_new" {
			t.Fatalf("read after update = (%q, %v), want ghp_new", value, err)
		}
	})
}
