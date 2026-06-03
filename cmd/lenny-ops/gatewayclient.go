// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// gatewayClientConfig carries the §25.4 ops.gateway settings the
// GatewayClient and its fan-out path consume.
type gatewayClientConfig struct {
	gatewayURL          string
	headlessService     string
	namespace           string
	internalTLS         bool
	internalTLSPort     int
	plaintextPort       int
	perRequestTimeout   time.Duration
	fanOutTimeout       time.Duration
	breakerThreshold    int
	breakerResetAfter   time.Duration
	saTokenPath         string
	refreshBeforeExpiry time.Duration
	minTokenTTL         time.Duration
	caBundlePath        string
}

// buildGatewayClient constructs the §25.4 gateway admin-API client from
// the resolved ops.gateway / ops.tls configuration. It returns nil when
// no gateway URL is configured (single-process dev), in which case the
// gateway-auth self-health check reports not-applicable. The headless
// ReplicaDiscovery is wired only when a headless Service name and
// namespace are known, so the fan-out fallback resolves real pod IPs.
//
// spec: §25.4 lines 1740-1790 ("Calling the Gateway").
func buildGatewayClient(cfg gatewayClientConfig, reg prometheus.Registerer) (*gateway.Client, error) {
	if cfg.gatewayURL == "" {
		log.Printf("lenny-ops: §25.4 gateway client not configured (no --gateway-url); gateway-auth self-health reports not-applicable")
		return nil, nil
	}
	gm := newGatewayMetrics(reg)

	// The §25.4 service-account token source: a projected-token loader
	// that pre-emptively refreshes before expiry and reloads on a 401.
	// When the token file is absent (single-process dev), fall back to a
	// no-token client so the dev gateway's AllowDevHeaders path serves.
	var tokenSrc gateway.TokenSource
	if cfg.saTokenPath != "" {
		if _, err := os.Stat(cfg.saTokenPath); err == nil {
			refreshing, terr := gateway.NewRefreshingTokenSource(gateway.TokenSourceConfig{
				Loader:              gateway.FileTokenLoader(cfg.saTokenPath),
				RefreshBeforeExpiry: cfg.refreshBeforeExpiry,
				MinTokenTTL:         cfg.minTokenTTL,
				Metrics:             gm,
			})
			if terr != nil {
				return nil, terr
			}
			tokenSrc = refreshing
		} else {
			log.Printf("lenny-ops: §25.4 gateway client running without a service-account token (%s absent); dev-headers path only", cfg.saTokenPath)
		}
	}

	scheme := "http"
	port := cfg.plaintextPort
	if cfg.internalTLS {
		scheme = "https"
		port = cfg.internalTLSPort
	}
	var disc gateway.ReplicaDiscovery
	if cfg.headlessService != "" && cfg.namespace != "" {
		disc = gateway.HeadlessDiscovery{
			Scheme:      scheme,
			ServiceName: cfg.headlessService,
			Namespace:   cfg.namespace,
			Port:        port,
		}
	}

	// §25.4 line 1743: the admin-API HTTP client trusts the cluster
	// trust store plus any operator-supplied CA via ops.tls.caBundle-
	// ConfigMap (the cert-manager case needs no extra material). A nil
	// client uses the default transport (system roots).
	hc, herr := buildGatewayHTTPClient(cfg.caBundlePath, cfg.perRequestTimeout)
	if herr != nil {
		return nil, herr
	}

	return gateway.NewClient(gateway.Config{
		BaseURL:           cfg.gatewayURL,
		Token:             tokenSrc,
		Discovery:         disc,
		HTTPClient:        hc,
		PerRequestTimeout: cfg.perRequestTimeout,
		FanOutTimeout:     cfg.fanOutTimeout,
		Breaker:           gateway.NewCircuitBreaker(cfg.breakerThreshold, cfg.breakerResetAfter),
		TLSMetrics:        gm,
	})
}

// buildGatewayHTTPClient returns an *http.Client for the admin-API link.
// When caBundlePath is set it augments the system trust store with the
// operator-supplied CA (§25.4 ops.tls.caBundleConfigMap). A nil result
// lets NewClient build the default-transport client.
func buildGatewayHTTPClient(caBundlePath string, timeout time.Duration) (*http.Client, error) {
	if caBundlePath == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(caBundlePath)
	if err != nil {
		return nil, fmt.Errorf("read gateway CA bundle %q: %w", caBundlePath, err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("gateway CA bundle %q contains no PEM certificates", caBundlePath)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
	}, nil
}

// gatewayAuthProbe returns the §25.4 gateway-auth self-health probe over
// client. It issues a GET /healthz over the configured admin-API
// transport, exercising the NET-070 link so the handshake metric
// reflects every self-health tick; a token-mint failure surfaces as a
// TokenError (→ unhealthy) and a reachability error as a plain error
// (→ degraded). A nil client returns a nil probe so the self-health
// check reports not-applicable.
//
// spec: §25.4 lines 1956-1971.
func gatewayAuthProbe(client *gateway.Client) opsservice.GatewayAuthProbe {
	if client == nil {
		return nil
	}
	return func(ctx context.Context) error {
		// out is nil so the client does not decode the body. A 401 trips
		// the token source's revocation path inside the client.
		if err := client.Get(ctx, "/healthz", nil); err != nil {
			if isTokenError(err) {
				return &opsservice.TokenError{Err: err}
			}
			return err
		}
		return nil
	}
}

// isTokenError reports whether err originates in the gateway client's
// token-minting step (the "gateway client: token:" prefix the client
// wraps a TokenSource failure with) rather than gateway reachability.
func isTokenError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "gateway client: token:")
}

// gatewayMetrics is the Prometheus-backed adapter for the §25.4 gateway
// client instruments: the OIDC token-refresh counter and the NET-070
// admin-API handshake counter. Registered on the process registry so the
// §16.9 /metrics exposition scrapes them and the OpsAdminAPIPlaintext-
// Detected alert (§16.5) reads the handshake counter.
//
// spec: §25.4 lines 1971 (token refresh) and 2538-2546 (handshake).
type gatewayMetrics struct {
	tokenRefresh *prometheus.CounterVec
	handshake    *prometheus.CounterVec
}

func newGatewayMetrics(reg prometheus.Registerer) *gatewayMetrics {
	m := &gatewayMetrics{
		tokenRefresh: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lenny_ops_gateway_auth_token_refresh_total",
			Help: "§25.4 service-account token refresh attempts by outcome.",
		}, []string{"status"}),
		handshake: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lenny_ops_admin_api_tls_handshake_total",
			Help: "§25.4 NET-070 gateway admin-API connection attempts by transport result.",
		}, []string{"result"}),
	}
	reg.MustRegister(m.tokenRefresh, m.handshake)
	return m
}

// RefreshDone implements gateway.RefreshMetrics.
func (m *gatewayMetrics) RefreshDone(status string) { m.tokenRefresh.WithLabelValues(status).Inc() }

// Handshake implements gateway.TLSMetrics.
func (m *gatewayMetrics) Handshake(result string) { m.handshake.WithLabelValues(result).Inc() }

// compile-time guards on the metric adapter.
var (
	_ gateway.RefreshMetrics = (*gatewayMetrics)(nil)
	_ gateway.TLSMetrics     = (*gatewayMetrics)(nil)
)
