// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test for the supervised agent pod against the
// lenny-pod-security and lenny-cosign-verify ValidatingAdmissionWebhooks.
// The tier-1 suites check the pod builder's rendering and each decider
// against hand-built AdmissionRequests. This suite sends the pod the
// production builder renders for a supervised sidecar pod through a real
// kube-apiserver, which routes the Pod CREATE through both registered,
// fail-closed webhooks: the pod is admitted with its lenny-supervisor-stage
// init container and read-only supervisor mount, an unsigned supervisor
// image is refused by the cosign webhook, and a read-write supervisor mount
// on the runtime container is refused by the pod-security webhook.

package admission_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	cv "github.com/lennylabs/lenny/pkg/admission/cosign_verify"
	"github.com/lennylabs/lenny/pkg/admission/webhook"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/podsecurity"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const (
	supWebhookNS     = "lenny-agents"
	supWebhookConfig = "lenny-supervised-pod-admission"
	supRegistry      = "ghcr.io/lennylabs/"
)

// supImages are the supervised pod's images, each in the verified registry.
var supImages = struct{ runtime, adapter, supervisor, unsigned string }{
	runtime:    supRegistry + "agent@sha256:" + strings.Repeat("1", 64),
	adapter:    supRegistry + "lenny-adapter@sha256:" + strings.Repeat("2", 64),
	supervisor: supRegistry + "lenny-supervisor@sha256:" + strings.Repeat("3", 64),
	unsigned:   supRegistry + "lenny-supervisor@sha256:" + strings.Repeat("9", 64),
}

// signedVerifier returns a cosign public-key verifier that trusts a fresh
// key and holds a signature for each of refs.
func signedVerifier(t *testing.T, refs ...string) cv.Verifier {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	entries := map[string]cv.SignedDigest{}
	for _, ref := range refs {
		sum := sha256.Sum256([]byte(ref))
		sig, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		entries[ref] = cv.SignedDigest{Digest: "sha256:" + hex.EncodeToString(sum[:]), Signature: base64.StdEncoding.EncodeToString(sig)}
	}
	v, err := cv.NewPublicKeyVerifier(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), cv.NewStaticResolver(entries))
	if err != nil {
		t.Fatalf("NewPublicKeyVerifier: %v", err)
	}
	return v
}

// supervisedPod renders the supervised sidecar pod the controller creates.
func supervisedPod(t *testing.T, name, supervisorImage string) *corev1.Pod {
	t.Helper()
	pod, err := podspec.Build(podspec.Inputs{
		Name: name, Namespace: supWebhookNS,
		Labels:           map[string]string{"lenny.dev/managed": "true"},
		RuntimeImage:     supImages.runtime,
		AdapterImage:     supImages.adapter,
		SupervisorImage:  supervisorImage,
		IsolationProfile: "standard",
		RuntimeCommand:   []string{"/usr/local/bin/agent"},
		RuntimeEnv:       []podspec.EnvPair{{Name: "VIRTUAL_ENV", Value: "/opt/venv"}},
		RuntimeProcess:   "keep",
		LaunchMode:       podspec.LaunchSession,
		SuperviseTiming:  podspec.SuperviseTiming{PingInterval: time.Second, PingTimeout: time.Second, WaitTimeout: time.Minute, WarmUpTimeout: time.Minute},
	})
	if err != nil {
		t.Fatalf("podspec.Build: %v", err)
	}
	return pod
}

// registerSupervisedPodWebhooks serves the pod-security and cosign deciders
// over one TLS server and registers both, fail-closed, on Pod CREATE in the
// agent namespace, then waits until a pod the pod-security webhook refuses
// is refused.
func registerSupervisedPodWebhooks(t *testing.T, ctx context.Context, cs *kubernetes.Clientset, verifier cv.Verifier) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/pod-security", webhook.Handler(webhook.PodSecurity(podspec.AdapterUID, podspec.AgentUID,
		podspec.CredReadersGID, podspec.CredVolumeName, podspec.SupervisorVolumeName, podsecurity.RuntimeClassPolicy{})))
	mux.Handle("/cosign-verify", webhook.Handler(webhook.CosignVerify(verifier, cv.Config{VerifiedRegistries: []string{supRegistry}})))
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	caBundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	fail := admissionregistrationv1.Fail
	none := admissionregistrationv1.SideEffectClassNone
	scope := admissionregistrationv1.NamespacedScope
	hook := func(name, path string) admissionregistrationv1.ValidatingWebhook {
		url := srv.URL + path
		return admissionregistrationv1.ValidatingWebhook{
			Name:         name,
			ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: &url, CABundle: caBundle},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
				Rule:       admissionregistrationv1.Rule{APIGroups: []string{""}, APIVersions: []string{"v1"}, Resources: []string{"pods"}, Scope: &scope},
			}},
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"lenny.dev/agent-namespace": "true"}},
			FailurePolicy:     &fail, SideEffects: &none, AdmissionReviewVersions: []string{"v1"},
		}
	}
	cfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: supWebhookConfig},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{hook("pod-security.lenny.dev", "/pod-security"), hook("cosign-verify.lenny.dev", "/cosign-verify")},
	}
	if _, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().Create(ctx, cfg, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create ValidatingWebhookConfiguration: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(context.Background(), supWebhookConfig, metav1.DeleteOptions{})
	})
	deadline := time.Now().Add(30 * time.Second)
	for i := 0; ; i++ {
		if time.Now().After(deadline) {
			t.Fatal("the admission webhooks never became active within 30s")
		}
		probe := supervisedPod(t, fmt.Sprintf("sup-probe-%d", i), supImages.supervisor)
		probe.Spec.HostPID = true
		_, err := cs.CoreV1().Pods(supWebhookNS).Create(ctx, probe, metav1.CreateOptions{})
		if err != nil && strings.Contains(err.Error(), "POD_SPEC_HOST_SHARING_FORBIDDEN") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// spec: 13.1 (Runtime supervisor volume; Container identity), 4.7.10 (Runtime supervisor), 5.2 (cosign image verification)
// diagnosis: the supervised pod the controller renders is refused by the
// lenny-pod-security or lenny-cosign-verify webhook on a real apiserver, so
// no sidecar type: agent pool can warm a pod; or a writable supervisor mount
// or an unsigned supervisor image is admitted, so the binary the runtime
// container runs as its first process is not the signed, unwritable one.
func TestSupervisedPodPassesPodSecurityAndCosignWebhooks(t *testing.T) {
	env := envtest.Start(t)
	cs, err := kubernetes.NewForConfig(env.RESTConfig())
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig: %v", err)
	}
	ctx := context.Background()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: supWebhookNS, Labels: map[string]string{"lenny.dev/agent-namespace": "true"}}}
	if _, err := cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	if _, err := cs.NodeV1().RuntimeClasses().Create(ctx, &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: "runc"}, Handler: "runc"}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create RuntimeClass: %v", err)
	}
	registerSupervisedPodWebhooks(t, ctx, cs, signedVerifier(t, supImages.runtime, supImages.adapter, supImages.supervisor))

	t.Run("supervised pod admitted", func(t *testing.T) {
		created, err := cs.CoreV1().Pods(supWebhookNS).Create(ctx, supervisedPod(t, "sup-admitted", supImages.supervisor), metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("the supervised pod was refused: %v", err)
		}
		if len(created.Spec.InitContainers) != 1 || created.Spec.InitContainers[0].Name != podspec.SupervisorStageContainerName {
			t.Errorf("stored init containers = %+v, want the stage container", created.Spec.InitContainers)
		}
		if created.Labels["lenny.dev/runtime-process"] != "keep" {
			t.Errorf("stored lifetime label = %q", created.Labels["lenny.dev/runtime-process"])
		}
	})

	t.Run("unsigned supervisor image refused", func(t *testing.T) {
		_, err := cs.CoreV1().Pods(supWebhookNS).Create(ctx, supervisedPod(t, "sup-unsigned", supImages.unsigned), metav1.CreateOptions{})
		if err == nil || !strings.Contains(err.Error(), "cosign-verify.lenny.dev") {
			t.Fatalf("err = %v, want a refusal by the cosign webhook", err)
		}
	})

	t.Run("writable supervisor mount refused", func(t *testing.T) {
		pod := supervisedPod(t, "sup-writable", supImages.supervisor)
		for i := range pod.Spec.Containers[1].VolumeMounts {
			if pod.Spec.Containers[1].VolumeMounts[i].Name == podspec.SupervisorVolumeName {
				pod.Spec.Containers[1].VolumeMounts[i].ReadOnly = false
			}
		}
		_, err := cs.CoreV1().Pods(supWebhookNS).Create(ctx, pod, metav1.CreateOptions{})
		if err == nil || !strings.Contains(err.Error(), "Runtime supervisor volume") {
			t.Fatalf("err = %v, want a refusal by the pod-security webhook's supervisor volume rule", err)
		}
	})
}
