// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// errToken is a gateway.TokenSource whose mint always fails, exercising
// the gateway-auth probe's TokenError classification path.
type errToken struct{}

func (errToken) Token(context.Context) (string, error) { return "", errors.New("token unavailable") }

// TestBuildGatewayClient_NoURL covers the single-process degraded mode:
// no --gateway-url yields a nil client and a nil (not-applicable) probe.
func TestBuildGatewayClient_NoURL(t *testing.T) {
	c, err := buildGatewayClient(gatewayClientConfig{}, prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("buildGatewayClient: %v", err)
	}
	if c != nil {
		t.Fatal("expected a nil client when no gateway URL is configured")
	}
	if gatewayAuthProbe(c) != nil {
		t.Fatal("expected a nil probe for a nil client")
	}
}

// TestGatewayAuthProbe_Success covers a healthy gateway round-trip.
func TestGatewayAuthProbe_Success_spec_25_4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, err := gateway.NewClient(gateway.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := gatewayAuthProbe(c)(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
}

// TestGatewayAuthProbe_TokenError covers the §25.4 gateway-auth
// classification: a token-mint failure surfaces as a TokenError so the
// self-health check reports unhealthy.
func TestGatewayAuthProbe_TokenError_spec_25_4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c, err := gateway.NewClient(gateway.Config{BaseURL: srv.URL, Token: errToken{}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	probeErr := gatewayAuthProbe(c)(context.Background())
	var te *opsservice.TokenError
	if !errors.As(probeErr, &te) {
		t.Fatalf("probe error = %v, want a *opsservice.TokenError", probeErr)
	}
}

// TestGatewayAuthProbe_Unreachable covers a gateway reachability failure
// classifying as a plain (degraded) error rather than a TokenError.
func TestGatewayAuthProbe_Unreachable_spec_25_4(t *testing.T) {
	c, err := gateway.NewClient(gateway.Config{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	probeErr := gatewayAuthProbe(c)(context.Background())
	if probeErr == nil {
		t.Fatal("expected a reachability error")
	}
	var te *opsservice.TokenError
	if errors.As(probeErr, &te) {
		t.Fatal("a reachability error must not classify as a TokenError")
	}
}

// TestGatewayMetrics covers the Prometheus adapter: the refresh and
// handshake counters register and increment under their §25.4 names.
func TestGatewayMetrics_spec_25_4(t *testing.T) {
	reg := prometheus.NewRegistry()
	gm := newGatewayMetrics(reg)
	gm.RefreshDone("success")
	gm.RefreshDone("success")
	gm.Handshake("plaintext")
	if got := testutil.ToFloat64(gm.tokenRefresh.WithLabelValues("success")); got != 2 {
		t.Fatalf("token_refresh{success} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(gm.handshake.WithLabelValues("plaintext")); got != 1 {
		t.Fatalf("handshake{plaintext} = %v, want 1", got)
	}
}

// TestIsTokenError covers the token-error prefix classifier.
func TestIsTokenError(t *testing.T) {
	if !isTokenError(errors.New("gateway client: token: load: boom")) {
		t.Fatal("a token-mint error must classify as a token error")
	}
	if isTokenError(errors.New("gateway client: 502")) {
		t.Fatal("a non-token error must not classify as a token error")
	}
	if isTokenError(nil) {
		t.Fatal("nil must not classify as a token error")
	}
}
