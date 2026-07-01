// SPDX-License-Identifier: MIT

package backends_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
	"github.com/lennylabs/lenny/pkg/gateway/operability/health/backends"
)

// spec: §11.7 / §25.3 — the gateway health service reports the
// circuit-breaker cache as degraded while it is stale.

// fakeBreakerCache is an in-test backends.BreakerCache.
type fakeBreakerCache struct{ last time.Time }

func (f fakeBreakerCache) LastRefresh() time.Time { return f.last }

func TestCircuitBreakerCacheFreshIsHealthy(t *testing.T) {
	c := backends.CircuitBreakerCache(fakeBreakerCache{last: time.Now()}, "cb-cache")
	got := c.Check(context.Background())
	if got.Status != health.StatusHealthy {
		t.Errorf("a freshly-refreshed cache: status %q, want healthy", got.Status)
	}
}

func TestCircuitBreakerCacheStaleIsDegraded(t *testing.T) {
	c := backends.CircuitBreakerCache(fakeBreakerCache{last: time.Now().Add(-30 * time.Second)}, "cb-cache")
	got := c.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Errorf("a 30s-stale cache: status %q, want degraded", got.Status)
	}
	// The breaker-cache degradation is not a Path B issue code, so the
	// checker carries the structured singular hint directly.
	// spec: §25.3 lines 459-501.
	if got.SuggestedAction == nil || got.SuggestedAction.Runbook == "" {
		t.Error("a degraded component must carry a §25.3 structured suggested action with a runbook")
	}
}

func TestCircuitBreakerCacheBeforeFirstRefreshIsDegraded(t *testing.T) {
	c := backends.CircuitBreakerCache(fakeBreakerCache{}, "cb-cache")
	got := c.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Errorf("before the first refresh: status %q, want degraded", got.Status)
	}
}

// fakeSIEM is an in-test SIEMForwarder + SIEMFailureRate.
type fakeSIEM struct {
	healthy bool
	rate    float64
}

func (f fakeSIEM) Healthy() bool        { return f.healthy }
func (f fakeSIEM) FailureRate() float64 { return f.rate }

// spec: §11.7 item 4 line 372 — below the configured failure threshold
// and with a healthy last delivery, the SIEM component is healthy.
func TestSIEMHealthyBelowThreshold(t *testing.T) {
	c := backends.SIEM(fakeSIEM{healthy: true, rate: 1}, fakeSIEM{rate: 1}, 5, "siem")
	got := c.Check(context.Background())
	if got.Status != health.StatusHealthy {
		t.Errorf("1%% failure rate under a 5%% threshold: status %q, want healthy", got.Status)
	}
}

// spec: §11.7 item 4 line 372 — when the delivery failure rate exceeds
// the threshold, the §25.3 health API reports the siem component
// degraded with an operability hint. F-11.7.16.
func TestSIEMDegradedAboveThreshold(t *testing.T) {
	c := backends.SIEM(fakeSIEM{healthy: true, rate: 12}, fakeSIEM{rate: 12}, 5, "siem")
	got := c.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Errorf("12%% failure rate over a 5%% threshold: status %q, want degraded", got.Status)
	}
	if got.Issue != "AUDIT_SIEM_DELIVERY_DEGRADED" {
		t.Errorf("issue = %q, want AUDIT_SIEM_DELIVERY_DEGRADED", got.Issue)
	}
	// The checker stamps the Issue; the aggregator's catalog resolves the
	// structured suggestedAction and the Path B runbook from it. Assert
	// the issue code is one the catalog can resolve to a hint with a
	// runbook. spec: §25.3 lines 459-501; §25.7 line 3234.
	single, _ := health.ActionsForIssue(got.Issue, got.Name)
	if single == nil || single.Runbook == "" {
		t.Errorf("ActionsForIssue(%q) = %+v, want a singular hint with a runbook", got.Issue, single)
	}
}

// spec: §11.7 item 4 — a failed most-recent delivery (failure rate not
// yet over the window threshold) still reports degraded so a fresh
// outage surfaces immediately.
func TestSIEMDegradedWhenLastDeliveryFailed(t *testing.T) {
	c := backends.SIEM(fakeSIEM{healthy: false, rate: 0}, fakeSIEM{rate: 0}, 5, "siem")
	got := c.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Errorf("unhealthy forwarder: status %q, want degraded", got.Status)
	}
}

// A zero threshold falls back to the §11.7 default of 5%.
func TestSIEMDefaultThreshold(t *testing.T) {
	c := backends.SIEM(fakeSIEM{healthy: true, rate: 6}, fakeSIEM{rate: 6}, 0, "siem")
	if got := c.Check(context.Background()); got.Status != health.StatusDegraded {
		t.Errorf("6%% with the default 5%% threshold: status %q, want degraded", got.Status)
	}
}

// errProbe returns a ProbeFunc that always fails, for the §25.3
// dependency-probe failure paths.
func errProbe(msg string) backends.ProbeFunc {
	return func(context.Context) error { return errors.New(msg) }
}

// okProbe returns a ProbeFunc that always succeeds.
func okProbe() backends.ProbeFunc {
	return func(context.Context) error { return nil }
}

// spec: §25.3 line 441 / lines 527-528 — the MinIO/objectStore probe
// reports unhealthy and stamps MINIO_UNREACHABLE on a failed HeadBucket.
func TestObjectStoreUnhealthy_spec_25_3_441(t *testing.T) {
	c := backends.ObjectStore(errProbe("head bucket: connection refused"), "objectStore")
	got := c.Check(context.Background())
	if got.Status != health.StatusUnhealthy {
		t.Fatalf("status = %q, want unhealthy", got.Status)
	}
	if got.Issue != "MINIO_UNREACHABLE" {
		t.Fatalf("issue = %q, want MINIO_UNREACHABLE", got.Issue)
	}
}

func TestObjectStoreHealthy_spec_25_3_441(t *testing.T) {
	c := backends.ObjectStore(okProbe(), "objectStore")
	if got := c.Check(context.Background()); got.Status != health.StatusHealthy {
		t.Fatalf("status = %q, want healthy", got.Status)
	}
}

// spec: §25.3 line 441 — the K8s API server probe reports unhealthy with
// an investigate hint when GET /healthz fails.
func TestAPIServerUnhealthy_spec_25_3_441(t *testing.T) {
	c := backends.APIServer(errProbe("/healthz returned 500"), "kubernetes-api")
	got := c.Check(context.Background())
	if got.Status != health.StatusUnhealthy {
		t.Fatalf("status = %q, want unhealthy", got.Status)
	}
	if got.SuggestedAction == nil || got.SuggestedAction.Action != "INVESTIGATE_KUBE_API" {
		t.Fatalf("suggestedAction = %+v, want INVESTIGATE_KUBE_API", got.SuggestedAction)
	}
}

func TestAPIServerHealthy_spec_25_3_441(t *testing.T) {
	c := backends.APIServer(okProbe(), "kubernetes-api")
	if got := c.Check(context.Background()); got.Status != health.StatusHealthy {
		t.Fatalf("status = %q, want healthy", got.Status)
	}
}

// spec: §25.3 line 441 — the registered-connectors probe reports
// unhealthy with an investigate hint when the registry query fails.
func TestConnectorsUnhealthy_spec_25_3_441(t *testing.T) {
	c := backends.Connectors(errProbe("registry list: timeout"), "connectors")
	got := c.Check(context.Background())
	if got.Status != health.StatusUnhealthy {
		t.Fatalf("status = %q, want unhealthy", got.Status)
	}
	if got.SuggestedAction == nil || got.SuggestedAction.Action != "INVESTIGATE_CONNECTOR_REGISTRY" {
		t.Fatalf("suggestedAction = %+v, want INVESTIGATE_CONNECTOR_REGISTRY", got.SuggestedAction)
	}
}

func TestConnectorsHealthy_spec_25_3_441(t *testing.T) {
	c := backends.Connectors(okProbe(), "connectors")
	if got := c.Check(context.Background()); got.Status != health.StatusHealthy {
		t.Fatalf("status = %q, want healthy", got.Status)
	}
}

// fakeCertReader returns a fixed NotAfter / error for the cert-manager
// probe tests.
type fakeCertReader struct {
	notAfter time.Time
	err      error
}

func (f fakeCertReader) NotAfter(context.Context) (time.Time, error) {
	return f.notAfter, f.err
}

// spec: §25.3 line 441 / §16.5 CertExpiryImminent — a valid certificate
// with ample lifetime is healthy.
func TestCertManagerHealthy_spec_25_3_441(t *testing.T) {
	c := backends.CertManager(fakeCertReader{notAfter: time.Now().Add(48 * time.Hour)}, "cert-manager")
	if got := c.Check(context.Background()); got.Status != health.StatusHealthy {
		t.Fatalf("status = %q, want healthy", got.Status)
	}
}

// A certificate inside the warning window is degraded and carries the
// CERT_EXPIRY_IMMINENT issue.
func TestCertManagerDegradedWhenExpiringSoon_spec_25_3_441(t *testing.T) {
	c := backends.CertManager(fakeCertReader{notAfter: time.Now().Add(30 * time.Minute)}, "cert-manager")
	got := c.Check(context.Background())
	if got.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
	if got.Issue != "CERT_EXPIRY_IMMINENT" {
		t.Fatalf("issue = %q, want CERT_EXPIRY_IMMINENT", got.Issue)
	}
}

// An expired certificate is unhealthy with CERT_EXPIRY_IMMINENT.
func TestCertManagerUnhealthyWhenExpired_spec_25_3_441(t *testing.T) {
	c := backends.CertManager(fakeCertReader{notAfter: time.Now().Add(-time.Minute)}, "cert-manager")
	got := c.Check(context.Background())
	if got.Status != health.StatusUnhealthy {
		t.Fatalf("status = %q, want unhealthy", got.Status)
	}
	if got.Issue != "CERT_EXPIRY_IMMINENT" {
		t.Fatalf("issue = %q, want CERT_EXPIRY_IMMINENT", got.Issue)
	}
}

// An unreadable certificate is unhealthy.
func TestCertManagerUnhealthyWhenUnreadable_spec_25_3_441(t *testing.T) {
	c := backends.CertManager(fakeCertReader{err: errors.New("open cert.pem: no such file")}, "cert-manager")
	if got := c.Check(context.Background()); got.Status != health.StatusUnhealthy {
		t.Fatalf("status = %q, want unhealthy", got.Status)
	}
}
