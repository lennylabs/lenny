// SPDX-License-Identifier: MIT

package stack_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// TestCreateDevBearerSecretCreatesAndUpdatesInPlace_spec_10_2 covers the §17.4
// dev bearer-trust Secret create against a real kube-apiserver: lenny up creates
// the Secret under the fixed name the development profile sets, holding the
// persisted dev HMAC key under data key "key", before the gateway Deployment is
// applied so the mount resolves; a re-run (the key rotated) updates it in place
// rather than failing on AlreadyExists.
//
// diagnosis: a failure means the in-cluster gateway cannot trust the bearer the
// CLI mints. Either the Secret was not created under the name and data key the
// gateway's --bearer-trust-hmac-key-file mount reads, or a second bring-up
// failed on AlreadyExists instead of reconverging the rotated key.
//
// spec: §10.2 (the gateway loads the dev HMAC key as a second verifier), §17.4
// (the dev bearer-trust Secret is created before the gateway Deployment).
func TestCreateDevBearerSecretCreatesAndUpdatesInPlace_spec_10_2(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	keyFile := filepath.Join(t.TempDir(), "signing.key")
	if err := os.WriteFile(keyFile, []byte(`{"keyId":"k1","secret":"AAAA"}`), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	if err := stack.CreateDevBearerSecretFromConfigForTest(ctx, cfg, keyFile); err != nil {
		t.Fatalf("create dev-bearer secret: %v", err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build core client: %v", err)
	}
	ns := stack.ControlPlaneNamespaceForTest()
	name := stack.DevBearerTrustSecretNameForTest()
	key := stack.DevBearerTrustSecretKeyForTest()

	got, err := cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get created secret: %v", err)
	}
	if string(got.Data[key]) != `{"keyId":"k1","secret":"AAAA"}` {
		t.Errorf("secret data[%q] = %q, want the persisted key file content", key, string(got.Data[key]))
	}

	// A re-run with a rotated key must reconverge in place.
	if err := os.WriteFile(keyFile, []byte(`{"keyId":"k2","secret":"BBBB"}`), 0o600); err != nil {
		t.Fatalf("rotate key file: %v", err)
	}
	if err := stack.CreateDevBearerSecretFromConfigForTest(ctx, cfg, keyFile); err != nil {
		t.Fatalf("re-create dev-bearer secret: %v", err)
	}
	got, err = cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated secret: %v", err)
	}
	if string(got.Data[key]) != `{"keyId":"k2","secret":"BBBB"}` {
		t.Errorf("secret data[%q] after re-run = %q, want the rotated key", key, string(got.Data[key]))
	}
}

// TestWaitDeploymentReady_spec_17_4 covers the §17.4 gateway-readiness wait
// against a real kube-apiserver: the wait returns once the Deployment reports a
// ready replica, and times out when it never does. The status subresource on a
// Deployment is not advanced by a controller in envtest, so the test sets the
// status directly to drive the ready transition.
//
// diagnosis: a failure means lenny up either reports the gateway ready before
// it is (the wait returned too early) or never returns (the readiness predicate
// does not recognize a ready Deployment).
//
// spec: §17.4 (lenny up reports the gateway ready when its Deployment is ready).
func TestWaitDeploymentReady_spec_17_4(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build core client: %v", err)
	}
	const ns = "default"
	const name = "lenny-gateway"
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "gateway", Image: "ghcr.io/lennylabs/lenny-gateway:0.1.0"}},
				},
			},
		},
	}
	if _, err := cs.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	// Not ready yet: a short-timeout wait must fail.
	if err := stack.WaitDeploymentReadyForTest(ctx, cfg, ns, name, 200*time.Millisecond, 50*time.Millisecond); err == nil {
		t.Fatal("waitDeploymentReady on a not-ready Deployment = nil, want a timeout")
	}

	// Drive the status to ready, then the wait must succeed.
	live, err := cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	live.Status.ObservedGeneration = live.Generation
	live.Status.Replicas = 1
	live.Status.ReadyReplicas = 1
	if _, err := cs.AppsV1().Deployments(ns).UpdateStatus(ctx, live, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update deployment status: %v", err)
	}
	if err := stack.WaitDeploymentReadyForTest(ctx, cfg, ns, name, 5*time.Second, 100*time.Millisecond); err != nil {
		t.Fatalf("waitDeploymentReady on a ready Deployment: %v", err)
	}
}

// TestCreateMasterKeySecretCreatesWhenAbsentAndPreservesExisting_spec_17_4
// covers the §17.4 local KMS master key Secret against a real kube-apiserver:
// lenny up creates the Secret under the fixed name the development profile
// sets, holding kms.DEKSize random bytes under data key "master-key", when the
// cluster has none, and a later bring-up leaves the existing Secret
// byte-identical rather than replacing the key. A Secret the operator seeded
// with other content is likewise left unchanged.
//
// diagnosis: a failure means the embedded gateway and lenny-ops either cannot
// start (the Secret their non-optional mount names was not created, or was
// created with the wrong name, data key, or length) or lose every wrapped DEK
// and every signed token across lenny up (a re-run replaced the persisted
// master key).
//
// spec: §17.4 (the local provider is keyed by a master key Secret that lenny
// up generates when the embedded cluster has none, so the key persists with
// the substrate), §10.2 "Signing key and algorithm".
func TestCreateMasterKeySecretCreatesWhenAbsentAndPreservesExisting_spec_17_4(t *testing.T) {
	env := envtest.Start(t)
	ctx := context.Background()
	cfg := env.RESTConfig()
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build core client: %v", err)
	}
	ns := stack.ControlPlaneNamespaceForTest()
	name := stack.DevKMSMasterKeySecretNameForTest()
	key := stack.KMSMasterKeySecretKeyForTest()
	secrets := cs.CoreV1().Secrets(ns)
	// Sweep a Secret a killed earlier run may have left on a shared server.
	_ = secrets.Delete(ctx, name, metav1.DeleteOptions{})

	if err := stack.CreateMasterKeySecretFromConfigForTest(ctx, cfg); err != nil {
		t.Fatalf("create master key secret: %v", err)
	}
	created, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get created secret: %v", err)
	}
	first := created.Data[key]
	if len(first) != kms.DEKSize {
		t.Fatalf("secret data[%q] is %d bytes, want kms.DEKSize (%d)", key, len(first), kms.DEKSize)
	}
	if bytes.Equal(first, make([]byte, kms.DEKSize)) {
		t.Fatalf("secret data[%q] is all zero bytes, want random key material", key)
	}

	// A second bring-up must keep the persisted key byte-identical.
	if err := stack.CreateMasterKeySecretFromConfigForTest(ctx, cfg); err != nil {
		t.Fatalf("re-run master key secret create: %v", err)
	}
	again, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret after re-run: %v", err)
	}
	if !bytes.Equal(again.Data[key], first) {
		t.Errorf("secret data[%q] changed across a re-run; want the existing key preserved", key)
	}
	if again.ResourceVersion != created.ResourceVersion {
		t.Errorf("secret resourceVersion %s -> %s across a re-run; want no update", created.ResourceVersion, again.ResourceVersion)
	}

	// An operator-seeded Secret is left exactly as it is.
	if err := secrets.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete secret: %v", err)
	}
	seeded := bytes.Repeat([]byte{0x7e}, 48)
	if _, err := secrets.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Data:       map[string][]byte{key: seeded},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	if err := stack.CreateMasterKeySecretFromConfigForTest(ctx, cfg); err != nil {
		t.Fatalf("create over a seeded secret: %v", err)
	}
	kept, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get seeded secret: %v", err)
	}
	if !bytes.Equal(kept.Data[key], seeded) {
		t.Errorf("seeded secret data[%q] was replaced; want it left unchanged", key)
	}
	t.Cleanup(func() { _ = secrets.Delete(context.Background(), name, metav1.DeleteOptions{}) })
}
