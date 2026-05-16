// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dr "github.com/lennylabs/lenny/pkg/admission/drain_readiness"
	"github.com/lennylabs/lenny/pkg/admission/webhook"
)

// spec: §12.5 — the lenny-drain-readiness webhook blocks a node-drain
// pod eviction while the artifact store is degraded.

const drainNS = "lenny-agents"

// stubProbe is a DrainProbe returning a fixed artifact-store status.
type stubProbe struct{ status dr.MinIOStatus }

func (s stubProbe) Probe(context.Context) dr.MinIOStatus { return s.status }

// drainClient returns a fake cluster client seeded with objs.
func drainClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
}

// evictionRequest is the admission request for a pods/eviction CREATE
// targeting the named pod.
func evictionRequest(podNamespace, podName string) *admissionv1.AdmissionRequest {
	return &admissionv1.AdmissionRequest{
		Operation:   admissionv1.Create,
		Namespace:   podNamespace,
		Name:        podName,
		Resource:    metav1.GroupVersionResource{Version: "v1", Resource: "pods"},
		SubResource: "eviction",
	}
}

func TestDrainReadinessAdmitsWhenMinIOHealthy(t *testing.T) {
	decide := webhook.DrainReadiness(drainClient(t), stubProbe{dr.MinIOHealthy}, nil)
	resp := decide(context.Background(), evictionRequest(drainNS, "agent-pod"))
	if !resp.Allowed {
		t.Errorf("eviction rejected with a healthy MinIO probe: %+v", resp.Result)
	}
}

func TestDrainReadinessBlocksWhenMinIOUnhealthy(t *testing.T) {
	decide := webhook.DrainReadiness(drainClient(t), stubProbe{dr.MinIOUnhealthy}, nil)
	resp := decide(context.Background(), evictionRequest(drainNS, "agent-pod"))
	if resp.Allowed {
		t.Fatal("eviction admitted with an unhealthy MinIO probe")
	}
	if resp.Result == nil || resp.Result.Code != 403 {
		t.Fatalf("response = %+v, want a 403 rejection", resp.Result)
	}
	if resp.Result.Message != dr.RejectionMessage {
		t.Errorf("message = %q, want the §12.5 drain-blocked message", resp.Result.Message)
	}
}

func TestDrainReadinessBlocksWhenEndpointUnreachable(t *testing.T) {
	decide := webhook.DrainReadiness(drainClient(t), stubProbe{dr.MinIOUnreachable}, nil)
	resp := decide(context.Background(), evictionRequest(drainNS, "agent-pod"))
	if resp.Allowed {
		t.Error("eviction admitted though the drain-readiness endpoint was unreachable")
	}
}

func TestDrainReadinessForceOverrideAdmitsAndSignals(t *testing.T) {
	// The evicted pod runs on a node carrying the drain-force override.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: drainNS, Name: "agent-pod"},
		Spec:       corev1.PodSpec{NodeName: "node-1"},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "node-1",
			Annotations: map[string]string{"lenny.dev/drain-force": "true"},
		},
	}
	var forcedNS, forcedName string
	decide := webhook.DrainReadiness(drainClient(t, pod, node), stubProbe{dr.MinIOUnhealthy},
		func(ns, name string) { forcedNS, forcedName = ns, name })

	resp := decide(context.Background(), evictionRequest(drainNS, "agent-pod"))
	if !resp.Allowed {
		t.Fatal("the drain-force override did not admit the eviction")
	}
	if forcedNS != drainNS || forcedName != "agent-pod" {
		t.Errorf("onForced got %s/%s, want %s/agent-pod", forcedNS, forcedName, drainNS)
	}
}

func TestDrainReadinessIgnoresForceWhenNodeNotAnnotated(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: drainNS, Name: "agent-pod"},
		Spec:       corev1.PodSpec{NodeName: "node-1"},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}} // no override
	decide := webhook.DrainReadiness(drainClient(t, pod, node), stubProbe{dr.MinIOUnhealthy}, nil)
	resp := decide(context.Background(), evictionRequest(drainNS, "agent-pod"))
	if resp.Allowed {
		t.Error("eviction admitted without the drain-force annotation on the node")
	}
}

func TestHTTPDrainProbeMapsStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   dr.MinIOStatus
	}{
		{http.StatusOK, dr.MinIOHealthy},
		{http.StatusServiceUnavailable, dr.MinIOUnhealthy},
		{http.StatusInternalServerError, dr.MinIOUnreachable},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			probe := webhook.HTTPDrainProbe{URL: srv.URL}
			if got := probe.Probe(context.Background()); got != tc.want {
				t.Errorf("Probe with upstream %d = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

func TestHTTPDrainProbeUnreachableOnTransportFailure(t *testing.T) {
	probe := webhook.HTTPDrainProbe{URL: "http://127.0.0.1:1/internal/drain-readiness"}
	if got := probe.Probe(context.Background()); got != dr.MinIOUnreachable {
		t.Errorf("Probe against a refused connection = %q, want unreachable", got)
	}
}
