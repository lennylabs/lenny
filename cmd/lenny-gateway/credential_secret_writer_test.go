// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const writerTestNS = "lenny-credentials"

var secretsGR = schema.GroupResource{Resource: "secrets"}

// forbiddenReadClientset returns a fake clientset holding objs whose
// reactors refuse get and patch on secrets with Forbidden, the verbs the
// gateway's credentials-namespace Role does not grant. A writer that reads
// first or patches fails against it.
func forbiddenReadClientset(objs ...runtime.Object) *fake.Clientset {
	cs := fake.NewSimpleClientset(objs...)
	for _, verb := range []string{"get", "patch"} {
		cs.PrependReactor(verb, "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(secretsGR, "", errors.New("verb not granted"))
		})
	}
	return cs
}

func verbsOf(cs *fake.Clientset) []string {
	var out []string
	for _, a := range cs.Actions() {
		out = append(out, a.GetVerb())
	}
	return out
}

func wantVerbs(t *testing.T, cs *fake.Clientset, want ...string) {
	t.Helper()
	if got := verbsOf(cs); !reflect.DeepEqual(got, want) {
		t.Fatalf("API verbs = %v, want %v", got, want)
	}
}

var testOwner = admin.SecretOwner{TenantID: "acme", Pool: "p1", CredentialID: "key-1"}

func storedSecret(t *testing.T, cs *fake.Clientset, name string) *corev1.Secret {
	t.Helper()
	obj, err := cs.Tracker().Get(schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, writerTestNS, name)
	if err != nil {
		t.Fatalf("tracker get %s: %v", name, err)
	}
	return obj.(*corev1.Secret)
}

// wantOwned asserts the §4.9 rule 2 annotations and label and exactly the
// submitted data.
func wantOwned(t *testing.T, s *corev1.Secret, owner admin.SecretOwner, data map[string]string) {
	t.Helper()
	wantAnn := map[string]string{
		"lenny.dev/tenant-id":       owner.TenantID,
		"lenny.dev/credential-pool": owner.Pool,
		"lenny.dev/credential-id":   owner.CredentialID,
	}
	if !reflect.DeepEqual(s.Annotations, wantAnn) {
		t.Fatalf("annotations = %v, want %v", s.Annotations, wantAnn)
	}
	if got := s.Labels["lenny.dev/tenant-hash"]; got != credential.ManagedSecretTenantHash(owner.TenantID) {
		t.Fatalf("tenant-hash label = %q, want %q", got, credential.ManagedSecretTenantHash(owner.TenantID))
	}
	got := map[string]string{}
	for k, v := range s.Data {
		got[k] = string(v)
	}
	if !reflect.DeepEqual(got, data) {
		t.Fatalf("data = %v, want exactly %v", got, data)
	}
}

// spec: §4.9 (Credential Storage Topology) — Credentials namespace rule 4
// and Credential values submitted through the admin API rules 2 and 3: Put
// creates an absent Secret with the owner annotations, the tenant-hash
// label, and exactly the submitted keys, and on an existing Secret issues
// an unconditional update with no get or patch, which the gateway's Role
// refuses.
func TestCredentialSecretWriterPut_spec_4_9(t *testing.T) {
	data := map[string]string{"apiKey": "sk-ant-1"}
	t.Run("absent Secret is created", func(t *testing.T) {
		cs := forbiddenReadClientset()
		w := newKubeCredentialSecretWriter(cs.CoreV1().Secrets(writerTestNS), 0)
		if err := w.Put(context.Background(), "lenny-cred-a", testOwner, data); err != nil {
			t.Fatalf("Put: %v", err)
		}
		wantVerbs(t, cs, "create")
		wantOwned(t, storedSecret(t, cs, "lenny-cred-a"), testOwner, data)
	})
	t.Run("existing Secret is updated unconditionally", func(t *testing.T) {
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "lenny-cred-a", Namespace: writerTestNS, ResourceVersion: "7"},
			Data:       map[string][]byte{"apiKey": []byte("old"), "stale": []byte("x")},
		}
		cs := forbiddenReadClientset(existing)
		w := newKubeCredentialSecretWriter(cs.CoreV1().Secrets(writerTestNS), 0)
		if err := w.Put(context.Background(), "lenny-cred-a", testOwner, data); err != nil {
			t.Fatalf("Put: %v", err)
		}
		wantVerbs(t, cs, "create", "update")
		upd := cs.Actions()[1].(k8stesting.UpdateAction).GetObject().(*corev1.Secret)
		if upd.ResourceVersion != "" {
			t.Fatalf("update resourceVersion = %q, want empty (unconditional)", upd.ResourceVersion)
		}
		wantOwned(t, storedSecret(t, cs, "lenny-cred-a"), testOwner, data)
	})
	t.Run("create refused", func(t *testing.T) {
		cs := forbiddenReadClientset()
		cs.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(secretsGR, "lenny-cred-a", errors.New("denied"))
		})
		w := newKubeCredentialSecretWriter(cs.CoreV1().Secrets(writerTestNS), 0)
		if err := w.Put(context.Background(), "lenny-cred-a", testOwner, data); !apierrors.IsForbidden(err) {
			t.Fatalf("Put err = %v, want Forbidden", err)
		}
		wantVerbs(t, cs, "create")
	})
	t.Run("update refused", func(t *testing.T) {
		cs := forbiddenReadClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "lenny-cred-a", Namespace: writerTestNS}})
		cs.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewServiceUnavailable("etcd down")
		})
		w := newKubeCredentialSecretWriter(cs.CoreV1().Secrets(writerTestNS), 0)
		if err := w.Put(context.Background(), "lenny-cred-a", testOwner, data); !apierrors.IsServiceUnavailable(err) {
			t.Fatalf("Put err = %v, want ServiceUnavailable", err)
		}
	})
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 3: a Delete of an absent Secret succeeds, and
// any other refusal is returned.
func TestCredentialSecretWriterDelete_spec_4_9(t *testing.T) {
	t.Run("existing", func(t *testing.T) {
		cs := forbiddenReadClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "lenny-cred-a", Namespace: writerTestNS}})
		w := newKubeCredentialSecretWriter(cs.CoreV1().Secrets(writerTestNS), 0)
		if err := w.Delete(context.Background(), "lenny-cred-a"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		wantVerbs(t, cs, "delete")
	})
	t.Run("absent", func(t *testing.T) {
		cs := forbiddenReadClientset()
		w := newKubeCredentialSecretWriter(cs.CoreV1().Secrets(writerTestNS), 0)
		if err := w.Delete(context.Background(), "lenny-cred-a"); err != nil {
			t.Fatalf("Delete of an absent Secret = %v, want nil", err)
		}
	})
	t.Run("refused", func(t *testing.T) {
		cs := forbiddenReadClientset()
		cs.PrependReactor("delete", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(secretsGR, "lenny-cred-a", errors.New("denied"))
		})
		w := newKubeCredentialSecretWriter(cs.CoreV1().Secrets(writerTestNS), 0)
		if err := w.Delete(context.Background(), "lenny-cred-a"); !apierrors.IsForbidden(err) {
			t.Fatalf("Delete err = %v, want Forbidden", err)
		}
	})
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 3, tenant deletion row: one delete-collection
// selected by the tenant-hash label.
func TestCredentialSecretWriterDeleteTenant_spec_4_9(t *testing.T) {
	cs := forbiddenReadClientset()
	w := newKubeCredentialSecretWriter(cs.CoreV1().Secrets(writerTestNS), 0)
	if err := w.DeleteTenant(context.Background(), "acme"); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}
	wantVerbs(t, cs, "delete-collection")
	got := cs.Actions()[0].(k8stesting.DeleteCollectionAction).GetListRestrictions().Labels.String()
	if want := "lenny.dev/tenant-hash=" + credential.ManagedSecretTenantHash("acme"); got != want {
		t.Fatalf("selector = %q, want %q", got, want)
	}
	cs.PrependReactor("delete-collection", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("etcd down")
	})
	if err := w.DeleteTenant(context.Background(), "acme"); err == nil {
		t.Fatal("DeleteTenant swallowed a refusal")
	}
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 5: each writer call carries a bounded
// context, so a kube-apiserver that never answers fails the call.
func TestCredentialSecretWriterBoundedContext_spec_4_9(t *testing.T) {
	cs := forbiddenReadClientset()
	cs.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})
	w := newKubeCredentialSecretWriter(cs.CoreV1().Secrets(writerTestNS), 1)
	if w.timeout != 1 {
		t.Fatalf("timeout = %v, want the configured 1ns", w.timeout)
	}
	if err := w.Put(context.Background(), "lenny-cred-a", testOwner, map[string]string{"k": "v"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Put err = %v, want DeadlineExceeded", err)
	}
	if d := newKubeCredentialSecretWriter(cs.CoreV1().Secrets(writerTestNS), 0); d.timeout != defaultCredentialSecretTimeout {
		t.Fatalf("zero timeout = %v, want the default", d.timeout)
	}
}

// diagnosis: a failure means the gateway exposes the credential Secret
// writer's per-call deadline as an operator flag or environment variable.
// §4.9 bounds each writer call without naming a tunable, so the bound is the
// fixed defaultCredentialSecretTimeout and no flag carries it.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 2.
func TestCredentialSecretWriterTimeoutIsNotAnOperatorFlag_spec_4_9(t *testing.T) {
	savedCmdLine, savedArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = savedCmdLine, savedArgs })
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	t.Setenv("LENNY_CREDENTIAL_SECRET_TIMEOUT", "1ns")
	f := &gatewayFlags{}
	f.registerCoreFlags()
	f.registerStorageFlags()
	f.registerPolicyFlags()
	f.registerSessionFlags()
	f.registerArtifactFlags()
	f.registerOpsFlags()
	flag.CommandLine.VisitAll(func(fl *flag.Flag) {
		if strings.Contains(fl.Name, "secret-timeout") {
			t.Errorf("gateway registers --%s; the credential Secret writer deadline is a fixed constant", fl.Name)
		}
	})
	if err := flag.CommandLine.Parse([]string{"--credential-secret-timeout=1s"}); err == nil {
		t.Error("--credential-secret-timeout parsed; want an undefined-flag error")
	}
	if defaultCredentialSecretTimeout <= 0 {
		t.Fatalf("defaultCredentialSecretTimeout = %v, want a positive bound", defaultCredentialSecretTimeout)
	}
}

// diagnosis: a failure means a Token Service gateway without a usable
// cluster client no longer gets a failing writer. A nil writer reports
// TOKEN_SERVICE_NOT_CONFIGURED for a configured Token Service, and a returned
// error stops the gateway at startup, which the design does not require of a
// Token Service address.
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 5 and 6
func TestBuildCredentialSecretWriterFailsEveryOperationWithoutClusterClient_spec_4_9(t *testing.T) {
	cases := []struct {
		name      string
		getConfig func() (*rest.Config, error)
		wantErr   string
	}{
		{
			name:      "cluster config does not resolve",
			getConfig: func() (*rest.Config, error) { return nil, errors.New("no kubeconfig") },
			wantErr:   "no kubeconfig",
		},
		{
			name: "clientset cannot be built",
			// client-go refuses a positive QPS with no burst and no rate
			// limiter.
			getConfig: func() (*rest.Config, error) { return &rest.Config{Host: "https://kube.invalid", QPS: 5}, nil },
			wantErr:   "clientset",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := buildCredentialSecretWriter(tc.getConfig, writerTestNS, 0)
			if w == nil {
				t.Fatal("writer = nil, want a non-nil failing writer so rule 6 stays true")
			}
			ctx := context.Background()
			ops := map[string]error{
				"Put":          w.Put(ctx, "s", admin.SecretOwner{}, nil),
				"Delete":       w.Delete(ctx, "s"),
				"DeleteTenant": w.DeleteTenant(ctx, "acme"),
			}
			for op, err := range ops {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("%s err = %v, want an error containing %q", op, err, tc.wantErr)
				}
			}
		})
	}
	t.Run("resolvable cluster config yields the client-go writer", func(t *testing.T) {
		w := buildCredentialSecretWriter(func() (*rest.Config, error) {
			return &rest.Config{Host: "https://kube.invalid"}, nil
		}, writerTestNS, 0)
		if _, ok := w.(*kubeCredentialSecretWriter); !ok {
			t.Fatalf("writer = %T, want *kubeCredentialSecretWriter", w)
		}
	})
}

// diagnosis: a failure means the client-go credential Secret writer
// disagrees with a real kube-apiserver: a Put does not leave the §4.9 rule 2
// annotations and label on a Secret it created or overwrote, its
// unconditional update is refused, a Delete of an absent Secret fails, or
// the tenant delete-collection selects the wrong Secrets.
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 2 and 3
func TestCredentialSecretWriterAgainstAPIServer_spec_4_9(t *testing.T) {
	env := envtest.Start(t)
	cs, err := kubernetes.NewForConfig(env.RESTConfig())
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}
	ctx := context.Background()
	ns := envtest.NamespaceName(t)
	if _, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	secrets := cs.CoreV1().Secrets(ns)
	w := newKubeCredentialSecretWriter(secrets, 0)
	read := func(name string) *corev1.Secret {
		t.Helper()
		s, err := secrets.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return s
	}
	acme := credential.ManagedSecretName("acme", "p1", "key-1")
	t.Run("Put creates", func(t *testing.T) {
		data := map[string]string{"apiKey": "v1"}
		if err := w.Put(ctx, acme, testOwner, data); err != nil {
			t.Fatalf("Put: %v", err)
		}
		wantOwned(t, read(acme), testOwner, data)
	})
	t.Run("Put overwrites an unlabelled Secret", func(t *testing.T) {
		name := credential.ManagedSecretName("acme", "p1", "key-2")
		if _, err := secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Data:       map[string][]byte{"stale": []byte("x")},
		}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		owner := admin.SecretOwner{TenantID: "acme", Pool: "p1", CredentialID: "key-2"}
		data := map[string]string{"apiKey": "v2"}
		if err := w.Put(ctx, name, owner, data); err != nil {
			t.Fatalf("Put: %v", err)
		}
		wantOwned(t, read(name), owner, data)
	})
	t.Run("Delete and Delete absent", func(t *testing.T) {
		name := credential.ManagedSecretName("acme", "p1", "key-3")
		if err := w.Put(ctx, name, testOwner, map[string]string{"apiKey": "v"}); err != nil {
			t.Fatalf("Put: %v", err)
		}
		for i := 0; i < 2; i++ {
			if err := w.Delete(ctx, name); err != nil {
				t.Fatalf("Delete %d: %v", i, err)
			}
		}
		if _, err := secrets.Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("Secret still present: %v", err)
		}
	})
	t.Run("DeleteTenant selects by label", func(t *testing.T) {
		globex := admin.SecretOwner{TenantID: "globex", Pool: "p1", CredentialID: "key-1"}
		globexName := credential.ManagedSecretName("globex", "p1", "key-1")
		if err := w.Put(ctx, globexName, globex, map[string]string{"apiKey": "g"}); err != nil {
			t.Fatalf("Put globex: %v", err)
		}
		if _, err := secrets.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "operator-made"}}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed unlabelled: %v", err)
		}
		if err := w.DeleteTenant(ctx, "acme"); err != nil {
			t.Fatalf("DeleteTenant: %v", err)
		}
		list, err := secrets.List(ctx, metav1.ListOptions{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var left []string
		for _, s := range list.Items {
			left = append(left, s.Name)
		}
		want := map[string]bool{globexName: true, "operator-made": true}
		if len(left) != len(want) || !want[left[0]] || !want[left[1]] {
			t.Fatalf("remaining Secrets = %v, want globex's and the unlabelled one", left)
		}
	})
}
