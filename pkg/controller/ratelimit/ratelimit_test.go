// SPDX-License-Identifier: MIT

package ratelimit

import (
	"net/http"
	"testing"

	"k8s.io/client-go/rest"
)

// spec: §4.6.1 "API server rate limiting" — Create calls for new Sandbox
// pods route to the pod-creation bucket; UpdateStatus calls on Sandbox
// and SandboxWarmPool route to the status bucket; everything else routes
// to the default bucket.
func TestClassifyRoutesRequestsToBuckets(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   bucket
	}{
		{"create sandbox", http.MethodPost, "/apis/lenny.dev/v1alpha1/namespaces/lenny-agents/sandboxes", bucketCreate},
		{"create sandbox trailing slash", http.MethodPost, "/apis/lenny.dev/v1alpha1/namespaces/lenny-agents/sandboxes/", bucketCreate},
		{"sandbox status patch", http.MethodPatch, "/apis/lenny.dev/v1alpha1/namespaces/lenny-agents/sandboxes/pod-1/status", bucketStatus},
		{"sandbox status put", http.MethodPut, "/apis/lenny.dev/v1alpha1/namespaces/lenny-agents/sandboxes/pod-1/status", bucketStatus},
		{"warmpool status patch", http.MethodPatch, "/apis/lenny.dev/v1alpha1/namespaces/lenny-agents/sandboxwarmpools/p/status", bucketStatus},
		{"sandbox get", http.MethodGet, "/apis/lenny.dev/v1alpha1/namespaces/lenny-agents/sandboxes/pod-1", bucketOther},
		{"sandbox list", http.MethodGet, "/apis/lenny.dev/v1alpha1/namespaces/lenny-agents/sandboxes", bucketOther},
		{"sandbox delete", http.MethodDelete, "/apis/lenny.dev/v1alpha1/namespaces/lenny-agents/sandboxes/pod-1", bucketOther},
		{"finalizer patch on the object, not status", http.MethodPatch, "/apis/lenny.dev/v1alpha1/namespaces/lenny-agents/sandboxes/pod-1", bucketOther},
		{"sandboxclaim create is not a pod create", http.MethodPost, "/apis/lenny.dev/v1alpha1/namespaces/lenny-agents/sandboxclaims", bucketOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.method, c.path); got != c.want {
				t.Errorf("classify(%s, %s) = %d, want %d", c.method, c.path, got, c.want)
			}
		})
	}
}

// WrapConfig disables the rest client's own limiter so the transport
// buckets are the sole client-side control, and installs the wrapping
// transport.
func TestWrapConfigDisablesClientLimiterAndWrapsTransport(t *testing.T) {
	cfg := &rest.Config{QPS: 10, Burst: 100}
	WrapConfig(cfg, Config{})

	if cfg.QPS != -1 {
		t.Errorf("QPS = %v, want -1 (client limiter disabled)", cfg.QPS)
	}
	if cfg.WrapTransport == nil {
		t.Fatal("WrapTransport not installed")
	}
	rt := cfg.WrapTransport(http.DefaultTransport)
	if _, ok := rt.(*transport); !ok {
		t.Errorf("wrapped transport type = %T, want *transport", rt)
	}
}

// WrapConfig composes with an existing WrapTransport rather than dropping
// it, so other transport decorators (auth, tracing) survive.
func TestWrapConfigComposesWithExistingWrapTransport(t *testing.T) {
	called := false
	cfg := &rest.Config{
		WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
			called = true
			return rt
		},
	}
	WrapConfig(cfg, Config{})
	cfg.WrapTransport(http.DefaultTransport)
	if !called {
		t.Error("existing WrapTransport must still be invoked")
	}
}

// withDefaults fills zero fields from the §4.6.1 defaults.
func TestConfigDefaults(t *testing.T) {
	got := Config{}.withDefaults()
	want := Config{
		CreateQPS: DefaultCreateQPS, CreateBurst: DefaultCreateBurst,
		StatusQPS: DefaultStatusQPS, StatusBurst: DefaultStatusBurst,
		OtherQPS: DefaultOtherQPS, OtherBurst: DefaultOtherBurst,
	}
	if got != want {
		t.Errorf("defaults = %+v, want %+v", got, want)
	}
}
