// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component tests for the lenny-drain-readiness
// ValidatingAdmissionWebhook (spec §12.5). The tier-1 unit suites in
// pkg/admission/drain_readiness and pkg/admission/webhook exercise the
// pure decision logic and the AdmissionReview transport against
// hand-built Go structs, and the gateway endpoint suite in
// pkg/gateway/podlifecycle/drainreadiness exercises the MinIO-probe ->
// 200/503 mapping. None of them drive a real kube-apiserver eviction
// through a registered, fail-closed webhook.
//
// This suite adds the missing higher-fidelity property: a real
// kube-apiserver routes a real pods/eviction CREATE through a
// registered ValidatingWebhookConfiguration (failurePolicy: Fail,
// resource pods/eviction, agent-namespace selector) into the real
// webhook.DrainReadiness handler served over HTTPS. The webhook queries
// a stub gateway whose /internal/drain-readiness endpoint reports MinIO
// unhealthy (HTTP 503), the exact signal the gateway emits when its S3
// HeadBucket probe fails. The suite asserts:
//
//   - With MinIO unhealthy and no drain-force override, the apiserver
//     rejects the eviction of a Ready agent pod with the §12.5 STR-006
//     message. This is the safety-critical block-eviction path.
//
//   - When the pod's node carries lenny.dev/drain-force: "true", the
//     same webhook admits the eviction even though MinIO is still
//     unhealthy, and emits the §16.7 node.drain.forced audit event.
//
// The probe result is stubbed (a gateway returning 503 models a MinIO
// outage) rather than driving a real MinIO fault, because the gateway
// endpoint's probe -> 503 mapping is covered by its own suite and a
// real MinIO fault on a shared cluster would disrupt parallel suites.
// The apiserver, the eviction subresource, the webhook wiring, the
// AdmissionReview transport, the live node drain-force lookup, and the
// decision code are all real here.

package admission_test

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dr "github.com/lennylabs/lenny/pkg/admission/drain_readiness"
	"github.com/lennylabs/lenny/pkg/admission/webhook"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const (
	drainNS       = "lenny-agents"
	drainNode     = "e2e-drain-node"
	drainWebhook  = "lenny-drain-readiness"
	drainForceKey = "lenny.dev/drain-force"
)

// recordingDrainMetrics records every §12.5 line 291 outcome the
// webhook reports so the test can assert allowed | blocked | forced.
type recordingDrainMetrics struct{ outcomes []string }

func (r *recordingDrainMetrics) IncDrainReadinessCheck(outcome string) {
	r.outcomes = append(r.outcomes, outcome)
}

func (r *recordingDrainMetrics) has(outcome string) bool {
	for _, o := range r.outcomes {
		if o == outcome {
			return true
		}
	}
	return false
}

// recordingDrainAudit records every §16.7 node.drain.forced event the
// webhook emits on a forced admission.
type recordingDrainAudit struct{ events []webhook.DrainForcedEvent }

func (r *recordingDrainAudit) RecordForcedDrain(_ context.Context, evt webhook.DrainForcedEvent) error {
	r.events = append(r.events, evt)
	return nil
}

// drainEnv boots envtest, a clientset, and a controller-runtime reader
// scoped to the core scheme, then creates the agent namespace and the
// hosting node.
func drainEnv(t *testing.T) (context.Context, *kubernetes.Clientset, client.Client) {
	t.Helper()
	env := envtest.Start(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	cs, err := kubernetes.NewForConfig(env.RESTConfig())
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig: %v", err)
	}
	reader, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   drainNS,
		Labels: map[string]string{"lenny.dev/agent-namespace": "true"},
	}}
	if _, err := cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: drainNode}}
	if _, err := cs.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create node: %v", err)
	}
	return ctx, cs, reader
}

// newAgentPod creates a Ready agent pod bound to drainNode and returns
// its name. A Ready, scheduled pod is the §12.5 subject: a node drain
// evicts exactly these.
func newAgentPod(t *testing.T, ctx context.Context, cs *kubernetes.Clientset, name string) string {
	t.Helper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: drainNS,
			Labels:    map[string]string{"lenny.dev/managed": "true"},
		},
		Spec: corev1.PodSpec{
			NodeName:   drainNode,
			Containers: []corev1.Container{{Name: "agent", Image: "busybox:1.36"}},
		},
	}
	created, err := cs.CoreV1().Pods(drainNS).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create agent pod %q: %v", name, err)
	}
	created.Status.Phase = corev1.PodRunning
	created.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	if _, err := cs.CoreV1().Pods(drainNS).UpdateStatus(ctx, created, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("mark agent pod %q Ready: %v", name, err)
	}
	return name
}

// evict issues a real pods/eviction CREATE for the named pod through
// the apiserver, which routes it through the registered webhook.
func evict(ctx context.Context, cs *kubernetes.Clientset, name string) error {
	return cs.CoreV1().Pods(drainNS).EvictV1(ctx, &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: drainNS},
	})
}

// registerDrainWebhook serves the real webhook.DrainReadiness handler
// over HTTPS and registers a fail-closed ValidatingWebhookConfiguration
// on pods/eviction CREATE in the agent namespace, with the httptest
// server's self-signed certificate as the caBundle. It returns once the
// webhook is observed to be active (the apiserver caches webhook configs
// asynchronously, so registration must be confirmed by driving a real
// eviction that the webhook rejects).
func registerDrainWebhook(t *testing.T, ctx context.Context, cs *kubernetes.Clientset, decider webhook.Decider) {
	t.Helper()

	srv := httptest.NewTLSServer(webhook.Handler(decider))
	t.Cleanup(srv.Close)
	caBundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	url := srv.URL + "/"

	fail := admissionregistrationv1.Fail
	none := admissionregistrationv1.SideEffectClassNone
	equivalent := admissionregistrationv1.Equivalent
	scope := admissionregistrationv1.NamespacedScope
	cfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: drainWebhook},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{{
			Name: "drain-readiness.lenny.dev",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				URL:      &url,
				CABundle: caBundle,
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"pods/eviction"},
					Scope:       &scope,
				},
			}},
			NamespaceSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "lenny.dev/agent-namespace",
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{"true"},
				}},
			},
			FailurePolicy:           &fail,
			MatchPolicy:             &equivalent,
			SideEffects:             &none,
			AdmissionReviewVersions: []string{"v1"},
		}},
	}
	if _, err := cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().Create(ctx, cfg, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create ValidatingWebhookConfiguration: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(context.Background(), drainWebhook, metav1.DeleteOptions{})
	})

	// The apiserver picks up a new webhook config asynchronously. Poll by
	// evicting throwaway probe pods until the webhook rejects one: that
	// confirms the config is active before the real assertions run. A
	// probe eviction that succeeds means the webhook was not yet active
	// (the pod is gone), so the next iteration uses a fresh probe pod.
	deadline := time.Now().Add(30 * time.Second)
	for i := 0; ; i++ {
		if time.Now().After(deadline) {
			t.Fatalf("drain-readiness webhook never became active within 30s")
		}
		probe := newAgentPod(t, ctx, cs, fmt.Sprintf("drain-probe-%d", i))
		err := evict(ctx, cs, probe)
		if err != nil && strings.Contains(err.Error(), "STR-006") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// spec: §12.5 (pre-drain MinIO health-check webhook), §16.7
// (node.drain.forced audit event)
// diagnosis: the deployed lenny-drain-readiness webhook does not block a
// pods/eviction while MinIO is unhealthy, or the drain-force node
// override no longer admits and audits a forced eviction. The test
// registers the real webhook on a real apiserver, drives a real
// eviction of a Ready agent pod while a stub gateway reports MinIO
// unhealthy, and asserts the §12.5 STR-006 rejection; then annotates the
// node with lenny.dev/drain-force and asserts the same webhook admits
// the eviction and emits the §16.7 node.drain.forced audit event. A
// failure means a planned node drain can silently evict agent pods into
// a MinIO outage and lose un-checkpointed workspace state, or that a
// forced override escapes the audit trail.
func TestDrainReadinessWebhookEvictionDecision(t *testing.T) {
	ctx, cs, reader := drainEnv(t)

	// Stub gateway: GET /internal/drain-readiness reports MinIO unhealthy
	// (HTTP 503), the exact signal the gateway emits when its S3
	// HeadBucket probe fails. The webhook's HTTPDrainProbe maps 503 to
	// MinIOUnhealthy.
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"not_ready","minio":"unhealthy","reason":"HeadBucket: dial tcp: connect: connection refused"}`))
	}))
	t.Cleanup(gateway.Close)

	metrics := &recordingDrainMetrics{}
	audit := &recordingDrainAudit{}
	decider := webhook.DrainReadiness(
		reader,
		webhook.HTTPDrainProbe{URL: gateway.URL},
		metrics,
		audit,
	)
	registerDrainWebhook(t, ctx, cs, decider)

	target := newAgentPod(t, ctx, cs, "drain-target")

	t.Run("blocks eviction when MinIO unhealthy", func(t *testing.T) {
		err := evict(ctx, cs, target)
		if err == nil {
			t.Fatal("§12.5: the apiserver admitted a pods/eviction while MinIO was unhealthy and the node " +
				"carried no drain-force override; the webhook must reject it so a planned drain cannot evict " +
				"agent pods into a MinIO outage")
		}
		if !strings.Contains(err.Error(), dr.RejectionMessage) {
			t.Errorf("eviction rejection = %q, want it to carry the §12.5 message %q", err.Error(), dr.RejectionMessage)
		}
		if !strings.Contains(err.Error(), "STR-006") {
			t.Errorf("eviction rejection = %q, want it to carry the §12.5 STR-006 code", err.Error())
		}
		if !metrics.has("blocked") {
			t.Errorf("webhook did not record a §12.5 line 291 blocked outcome; outcomes=%v", metrics.outcomes)
		}
		// The denied pod must still exist: the whole point is that the
		// eviction did not proceed.
		if _, err := cs.CoreV1().Pods(drainNS).Get(ctx, target, metav1.GetOptions{}); err != nil {
			t.Errorf("target pod should survive a rejected eviction: %v", err)
		}
	})

	t.Run("admits and audits forced drain even when MinIO unhealthy", func(t *testing.T) {
		// Annotate the hosting node with the §12.5 emergency-drain
		// override. MinIO stays unhealthy (the stub still returns 503),
		// so the admission is attributable to the override alone.
		node, err := cs.CoreV1().Nodes().Get(ctx, drainNode, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get node: %v", err)
		}
		if node.Annotations == nil {
			node.Annotations = map[string]string{}
		}
		node.Annotations[drainForceKey] = "true"
		if _, err := cs.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("annotate node with drain-force: %v", err)
		}

		before := len(audit.events)
		if err := evict(ctx, cs, target); err != nil {
			t.Fatalf("§12.5: the apiserver rejected a pods/eviction on a node carrying "+
				"lenny.dev/drain-force=true; the webhook must permit an operator-forced emergency drain: %v", err)
		}
		if len(audit.events) != before+1 {
			t.Fatalf("§16.7: a forced drain must emit exactly one node.drain.forced audit event; "+
				"recorded %d (was %d)", len(audit.events), before)
		}
		evt := audit.events[len(audit.events)-1]
		if evt.NodeName != drainNode {
			t.Errorf("node.drain.forced event NodeName = %q, want %q", evt.NodeName, drainNode)
		}
		if evt.PodName != target || evt.PodNamespace != drainNS {
			t.Errorf("node.drain.forced event pod = %s/%s, want %s/%s",
				evt.PodNamespace, evt.PodName, drainNS, target)
		}
		if !metrics.has("forced") {
			t.Errorf("webhook did not record a §12.5 line 291 forced outcome; outcomes=%v", metrics.outcomes)
		}
	})
}
