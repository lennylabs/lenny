//go:build contract

// SPDX-License-Identifier: MIT

// Package admissionpodsecurity_test is the Tier 3 contract suite for the
// lenny-pod-security AdmissionReview wire contract as it applies to the
// §13.1 container identity clause. The kube-apiserver posts an
// admission.k8s.io/v1 AdmissionReview carrying the post-mutation Pod and
// reads back allowed, status.code, and status.message; this suite drives
// the webhook's HTTP handler with the raw JSON body the apiserver sends
// and asserts the decision and the reason it carries.
package admissionpodsecurity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/admission/webhook"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/podsecurity"
)

// Overridden security.podUIDs values, distinct from the chart defaults
// so the test shows the webhook keys on the UIDs it is wired with.
const (
	adapterUID int64 = 70000
	agentUID   int64 = 70001
)

// builtPod returns a controller-built sidecar pod at the overridden UIDs
// with one extra regular container, modelling a container a deployer's
// mutating webhook injects, running at injectedUID.
func builtPod(t *testing.T, injectedUID int64) corev1.Pod {
	t.Helper()
	pod, err := podspec.Build(podspec.Inputs{
		Name:             "claude-worker-identity",
		Namespace:        "lenny-agents",
		RuntimeImage:     "ghcr.io/acme/claude-code:v1",
		AdapterImage:     "ghcr.io/lennylabs/lenny-adapter:v1",
		IsolationProfile: "sandboxed",
		AdapterUID:       adapterUID,
		AgentUID:         agentUID,
		CredReadersGID:   podspec.CredReadersGID,
	})
	if err != nil {
		t.Fatalf("podspec.Build: %v", err)
	}
	injected := *pod.Spec.Containers[0].DeepCopy()
	injected.Name = "mesh-proxy"
	injected.VolumeMounts = nil
	injected.SecurityContext.RunAsUser = &injectedUID
	injected.SecurityContext.RunAsGroup = &injectedUID
	pod.Spec.Containers = append(pod.Spec.Containers, injected)
	return *pod
}

// postReview sends pod to the webhook as the raw AdmissionReview JSON
// body the kube-apiserver posts, and decodes the reply.
func postReview(t *testing.T, pod corev1.Pod) admissionv1.AdmissionReview {
	t.Helper()
	srv := httptest.NewServer(webhook.Handler(webhook.PodSecurity(
		adapterUID, agentUID, podspec.CredReadersGID, podspec.CredVolumeName, podsecurity.RuntimeClassPolicy{},
	)))
	t.Cleanup(srv.Close)

	podRaw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	body := []byte(`{"apiVersion":"admission.k8s.io/v1","kind":"AdmissionReview","request":{` +
		`"uid":"identity-1","kind":{"group":"","version":"v1","kind":"Pod"},` +
		`"resource":{"group":"","version":"v1","resource":"pods"},` +
		`"namespace":"lenny-agents","operation":"CREATE","object":` + string(podRaw) + `}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post review: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d, want 200: %s", resp.StatusCode, raw)
	}
	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if out.APIVersion != "admission.k8s.io/v1" || out.Kind != "AdmissionReview" || out.Response == nil || out.Response.UID != "identity-1" {
		t.Fatalf("reply is not an AdmissionReview answering identity-1: %s", raw)
	}
	return out
}

// TestPodSecurityReviewDeniesInjectedContainerAtAgentUID_spec_13_1 pins
// the AdmissionReview reply for a post-mutation pod carrying an injected
// container at the agent UID: allowed false, status code 403, and a
// message naming the container, its UID, and the runtime container that
// owns the reservation.
//
// spec: 13.1 (Pod Security)
//
// diagnosis: the lenny-pod-security webhook admits a container a mutating
// webhook injected at the agent UID, or answers without the §13.1
// container identity reason. Either the webhook is not wired with the
// configured security.podUIDs values, the validator lost the identity
// clause, or the AdmissionReview reply drops the status.
func TestPodSecurityReviewDeniesInjectedContainerAtAgentUID_spec_13_1(t *testing.T) {
	out := postReview(t, builtPod(t, agentUID))
	if out.Response.Allowed {
		t.Fatalf("an injected container at the agent UID must be denied")
	}
	if out.Response.Result == nil || out.Response.Result.Code != http.StatusForbidden {
		t.Fatalf("status = %+v, want code 403", out.Response.Result)
	}
	want := `container "mesh-proxy" runAsUser 70001 is reserved for the "runtime" container (§13.1 Container identity)`
	if !strings.Contains(out.Response.Result.Message, want) {
		t.Errorf("message = %q, want it to carry %q", out.Response.Result.Message, want)
	}
}

// TestPodSecurityReviewAdmitsInjectedContainerAtFreeUID_spec_13_1 pins
// the admitting reply: the same pod with the injected container at a UID
// outside both reserved UIDs is admitted, so the denial above is caused
// by the reservation and not by another baseline control.
//
// spec: 13.1 (Pod Security)
//
// diagnosis: the lenny-pod-security webhook denies a compliant injected
// container, so the container identity clause or another §13.1 control
// rejects a controller-built pod it should admit.
func TestPodSecurityReviewAdmitsInjectedContainerAtFreeUID_spec_13_1(t *testing.T) {
	out := postReview(t, builtPod(t, 1000))
	if !out.Response.Allowed {
		t.Fatalf("an injected container at a non-reserved UID must be admitted: %+v", out.Response.Result)
	}
}
