// SPDX-License-Identifier: MIT

package webhook_test

import (
	"context"
	"errors"
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

// recordingMetricsSink records every IncDrainReadinessCheck call so
// tests can assert §12.5 line 291 outcomes.
type recordingMetricsSink struct{ outcomes []string }

func (r *recordingMetricsSink) IncDrainReadinessCheck(outcome string) {
	r.outcomes = append(r.outcomes, outcome)
}

// recordingAuditSink records every §16.7 node.drain.forced event a
// test admits. failErr, when set, surfaces on RecordForcedDrain so
// tests can exercise the fail-closed §11.7 audit branch.
type recordingAuditSink struct {
	events  []webhook.DrainForcedEvent
	failErr error
}

func (r *recordingAuditSink) RecordForcedDrain(_ context.Context, evt webhook.DrainForcedEvent) error {
	if r.failErr != nil {
		return r.failErr
	}
	r.events = append(r.events, evt)
	return nil
}

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
	m := &recordingMetricsSink{}
	decide := webhook.DrainReadiness(drainClient(t), stubProbe{dr.MinIOHealthy}, m, nil)
	resp := decide(context.Background(), evictionRequest(drainNS, "agent-pod"))
	if !resp.Allowed {
		t.Errorf("eviction rejected with a healthy MinIO probe: %+v", resp.Result)
	}
	if len(m.outcomes) != 1 || m.outcomes[0] != "allowed" {
		t.Errorf("outcomes = %v, want [allowed]", m.outcomes)
	}
}

func TestDrainReadinessBlocksWhenMinIOUnhealthy(t *testing.T) {
	m := &recordingMetricsSink{}
	decide := webhook.DrainReadiness(drainClient(t), stubProbe{dr.MinIOUnhealthy}, m, nil)
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
	if len(m.outcomes) != 1 || m.outcomes[0] != "blocked" {
		t.Errorf("outcomes = %v, want [blocked]", m.outcomes)
	}
}

func TestDrainReadinessBlocksWhenEndpointUnreachable(t *testing.T) {
	decide := webhook.DrainReadiness(drainClient(t), stubProbe{dr.MinIOUnreachable}, nil, nil)
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
	m := &recordingMetricsSink{}
	a := &recordingAuditSink{}
	decide := webhook.DrainReadiness(drainClient(t, pod, node), stubProbe{dr.MinIOUnhealthy}, m, a)

	resp := decide(context.Background(), evictionRequest(drainNS, "agent-pod"))
	if !resp.Allowed {
		t.Fatal("the drain-force override did not admit the eviction")
	}
	if len(m.outcomes) != 1 || m.outcomes[0] != "forced" {
		t.Errorf("outcomes = %v, want [forced]", m.outcomes)
	}
	if len(a.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(a.events))
	}
	got := a.events[0]
	if got.PodNamespace != drainNS || got.PodName != "agent-pod" || got.NodeName != "node-1" {
		t.Errorf("audit payload = %+v", got)
	}
}

// spec: §12.5 line 291 — when the audit chain rejects the
// node.drain.forced write, the webhook fails the eviction closed so
// the override never escapes the trail.
func TestDrainReadinessDeniesWhenForcedAuditFails(t *testing.T) {
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
	m := &recordingMetricsSink{}
	a := &recordingAuditSink{failErr: errors.New("chain unreachable")}
	decide := webhook.DrainReadiness(drainClient(t, pod, node), stubProbe{dr.MinIOUnhealthy}, m, a)

	resp := decide(context.Background(), evictionRequest(drainNS, "agent-pod"))
	if resp.Allowed {
		t.Fatal("forced eviction must be denied when the audit append fails")
	}
	// The metrics outcome timeline records the original forced
	// decision plus the audit_failed override.
	if len(m.outcomes) < 2 || m.outcomes[0] != "forced" || m.outcomes[1] != "audit_failed" {
		t.Errorf("outcomes = %v, want [forced audit_failed]", m.outcomes)
	}
}

func TestDrainReadinessIgnoresForceWhenNodeNotAnnotated(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: drainNS, Name: "agent-pod"},
		Spec:       corev1.PodSpec{NodeName: "node-1"},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}} // no override
	decide := webhook.DrainReadiness(drainClient(t, pod, node), stubProbe{dr.MinIOUnhealthy}, nil, nil)
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
