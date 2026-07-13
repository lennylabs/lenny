//go:build component

// SPDX-License-Identifier: MIT

// Component tests for the four §25.3 dependency probes that carry no
// tier2 coverage otherwise: the MinIO object-store probe (driven against
// a real MinIO container), the Kubernetes API server and registered-
// connectors probes (driven against stub HTTP backends), and the
// cert-manager probe (driven through the real FileCertReader against
// on-disk certificate fixtures). Each case asserts the status verdict,
// the stamped issue code or inline hint, and the spec's hard 2-second
// probe timeout.
package backends_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
	"github.com/lennylabs/lenny/pkg/gateway/operability/health/backends"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// probeTimeoutBounds brackets the deadline a §25.3 checker must stamp on
// its probe. spec: §25.3 line 441 — "Each probe has a hard timeout of 2
// seconds." The lower bound tolerates the microseconds spent between the
// checker's WithTimeout call and the probe reading its deadline; the
// upper bound is the exact 2s the spec mandates.
const (
	probeTimeoutMin = 1900 * time.Millisecond
	probeTimeoutMax = 2 * time.Second
)

// deadlineRecorder is a backends.ProbeFunc that records the remaining
// time on the deadline its checker handed it, then fails so the checker
// reports unhealthy. It lets a test observe the §25.3 hard probe timeout
// as the checker configured it.
func deadlineRecorder(seen *time.Duration) backends.ProbeFunc {
	return func(ctx context.Context) error {
		dl, ok := ctx.Deadline()
		if !ok {
			*seen = -1
			return fmt.Errorf("probe context carried no deadline")
		}
		*seen = time.Until(dl)
		return fmt.Errorf("probe failed (deadline recorded)")
	}
}

// assertProbeTimeout fails the test unless the recorded remaining time
// sits inside the §25.3 2-second window.
func assertProbeTimeout(t *testing.T, seen time.Duration) {
	t.Helper()
	if seen < probeTimeoutMin || seen > probeTimeoutMax {
		t.Errorf("probe deadline = %s remaining, want within (%s, %s]: §25.3 hard 2-second timeout", seen, probeTimeoutMin, probeTimeoutMax)
	}
}

// spec: 25.3
// diagnosis: the §25.3 MinIO object-store dependency probe
// (backends.ObjectStore) did not behave as specified against a live
// backend. It must report healthy when a HeadBucket-equivalent query
// against a reachable MinIO succeeds, report unhealthy and stamp
// MINIO_UNREACHABLE when MinIO is down, and bound the probe at the hard
// 2-second timeout. spec: §25.3 line 440 ("MinIO (HeadBucket)"), line
// 441 (hard 2s timeout), lines 527-528 (objectStore.status unhealthy
// when MinIO is unreachable).
func TestObjectStoreCheckerAgainstMinIO(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mn := containers.StartMinIO(t, containers.MinIOOptions{})

	// headBucket is the §25.3 "MinIO (HeadBucket)" single-query probe:
	// minio-go's BucketExists issues a HEAD against the bucket.
	headBucket := func(pctx context.Context) error {
		if _, err := mn.Client.BucketExists(pctx, mn.Bucket); err != nil {
			return err
		}
		return nil
	}

	t.Run("reports healthy against a live MinIO", func(t *testing.T) {
		comp := backends.ObjectStore(headBucket, "objectStore").Check(ctx)
		if comp.Name != "objectStore" {
			t.Errorf("Name = %q, want objectStore", comp.Name)
		}
		if comp.Status != health.StatusHealthy {
			t.Errorf("Status = %q, want healthy (detail: %s)", comp.Status, comp.Detail)
		}
	})

	t.Run("reports unhealthy and stamps MINIO_UNREACHABLE when MinIO is down", func(t *testing.T) {
		mn.Stop(t)
		comp := backends.ObjectStore(headBucket, "objectStore").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy", comp.Status)
		}
		if comp.Issue != "MINIO_UNREACHABLE" {
			t.Errorf("Issue = %q, want MINIO_UNREACHABLE", comp.Issue)
		}
		// The stamped issue code must resolve to a §25.3 singular hint
		// with a runbook through the aggregator's catalog.
		if single, _ := health.ActionsForIssue(comp.Issue, comp.Name); single == nil || single.Runbook == "" {
			t.Errorf("issue %q does not resolve to a §25.3 hint with a runbook", comp.Issue)
		}
	})

	t.Run("bounds the probe at the §25.3 hard 2-second timeout", func(t *testing.T) {
		var seen time.Duration
		comp := backends.ObjectStore(deadlineRecorder(&seen), "objectStore").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy", comp.Status)
		}
		assertProbeTimeout(t, seen)
	})
}

// spec: 25.3
// diagnosis: the §25.3 Kubernetes API server probe (backends.APIServer)
// did not behave as specified against a real GET /healthz backend. It
// must report healthy when /healthz answers, report unhealthy with the
// INVESTIGATE_KUBE_API inline hint when the API server errors or is
// unreachable, and bound the probe at the hard 2-second timeout.
// spec: §25.3 line 440 ("K8s API server (/healthz)"), line 441.
func TestAPIServerCheckerAgainstStubHealthz(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// healthzProbe is the §25.3 "K8s API server (/healthz)" GET probe.
	healthzProbe := func(url string) backends.ProbeFunc {
		return func(pctx context.Context) error {
			req, err := http.NewRequestWithContext(pctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("/healthz returned %d", resp.StatusCode)
			}
			return nil
		}
	}

	t.Run("reports healthy when the stub /healthz answers 200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		comp := backends.APIServer(healthzProbe(srv.URL+"/healthz"), "kubernetes-api").Check(ctx)
		if comp.Status != health.StatusHealthy {
			t.Errorf("Status = %q, want healthy (detail: %s)", comp.Status, comp.Detail)
		}
	})

	t.Run("reports unhealthy with an investigate hint when /healthz returns 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		comp := backends.APIServer(healthzProbe(srv.URL+"/healthz"), "kubernetes-api").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy", comp.Status)
		}
		if comp.SuggestedAction == nil || comp.SuggestedAction.Action != "INVESTIGATE_KUBE_API" {
			t.Errorf("SuggestedAction = %+v, want INVESTIGATE_KUBE_API", comp.SuggestedAction)
		}
	})

	t.Run("reports unhealthy when the API server is unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL + "/healthz"
		srv.Close() // the listener is gone; the probe cannot connect.
		comp := backends.APIServer(healthzProbe(url), "kubernetes-api").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy", comp.Status)
		}
	})

	t.Run("bounds the probe at the §25.3 hard 2-second timeout", func(t *testing.T) {
		var seen time.Duration
		comp := backends.APIServer(deadlineRecorder(&seen), "kubernetes-api").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy", comp.Status)
		}
		assertProbeTimeout(t, seen)
	})
}

// spec: 25.3
// diagnosis: the §25.3 registered-connectors probe (backends.Connectors)
// did not behave as specified against a real connector-registry backend.
// It must report healthy when the registry answers a single-query
// reachability check, report unhealthy with the
// INVESTIGATE_CONNECTOR_REGISTRY inline hint when the registry query
// fails, and bound the probe at the hard 2-second timeout.
// spec: §25.3 line 440 ("registered connectors"), line 441.
func TestConnectorsCheckerAgainstStubRegistry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// registryProbe is the §25.3 single-query reachability check against
	// the connector-registry stub.
	registryProbe := func(url string) backends.ProbeFunc {
		return func(pctx context.Context) error {
			req, err := http.NewRequestWithContext(pctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("connector registry list: status %d", resp.StatusCode)
			}
			return nil
		}
	}

	t.Run("reports healthy when the registry stub answers", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"connectors":[]}`))
		}))
		defer srv.Close()
		comp := backends.Connectors(registryProbe(srv.URL+"/v1/connectors"), "connectors").Check(ctx)
		if comp.Status != health.StatusHealthy {
			t.Errorf("Status = %q, want healthy (detail: %s)", comp.Status, comp.Detail)
		}
	})

	t.Run("reports unhealthy with an investigate hint when the registry query fails", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		comp := backends.Connectors(registryProbe(srv.URL+"/v1/connectors"), "connectors").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy", comp.Status)
		}
		if comp.SuggestedAction == nil || comp.SuggestedAction.Action != "INVESTIGATE_CONNECTOR_REGISTRY" {
			t.Errorf("SuggestedAction = %+v, want INVESTIGATE_CONNECTOR_REGISTRY", comp.SuggestedAction)
		}
	})

	t.Run("bounds the probe at the §25.3 hard 2-second timeout", func(t *testing.T) {
		var seen time.Duration
		comp := backends.Connectors(deadlineRecorder(&seen), "connectors").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy", comp.Status)
		}
		assertProbeTimeout(t, seen)
	})
}

// writeCertFixture generates a real leaf certificate expiring at
// notAfter and writes it as PEM to a temp file, returning the path. The
// §25.3 cert-manager probe reads exactly this on-disk PEM through
// FileCertReader, so the fixture exercises the real parse path rather
// than a fake reader.
func writeCertFixture(t *testing.T, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "gateway.acme.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "tls.crt")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write cert fixture: %v", err)
	}
	return path
}

// spec: 25.3
// diagnosis: the §25.3 cert-manager probe (backends.CertManager driven
// through the real FileCertReader) did not behave as specified against
// on-disk certificate fixtures. It must report healthy for a certificate
// with ample lifetime, degraded and stamp CERT_EXPIRY_IMMINENT for one
// inside the expiry-warning window, unhealthy and stamp
// CERT_EXPIRY_IMMINENT for an expired certificate, and unhealthy for an
// unreadable one; the probe is bounded at the hard 2-second timeout.
// spec: §25.3 line 440 ("cert-manager (certificate status)"), line 441.
func TestCertManagerCheckerAgainstFileFixtures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("reports healthy for a certificate with ample lifetime", func(t *testing.T) {
		reader := backends.FileCertReader(writeCertFixture(t, time.Now().Add(48*time.Hour)))
		comp := backends.CertManager(reader, "cert-manager").Check(ctx)
		if comp.Status != health.StatusHealthy {
			t.Errorf("Status = %q, want healthy (detail: %s)", comp.Status, comp.Detail)
		}
	})

	t.Run("reports degraded and stamps CERT_EXPIRY_IMMINENT inside the warning window", func(t *testing.T) {
		reader := backends.FileCertReader(writeCertFixture(t, time.Now().Add(30*time.Minute)))
		comp := backends.CertManager(reader, "cert-manager").Check(ctx)
		if comp.Status != health.StatusDegraded {
			t.Errorf("Status = %q, want degraded (detail: %s)", comp.Status, comp.Detail)
		}
		if comp.Issue != "CERT_EXPIRY_IMMINENT" {
			t.Errorf("Issue = %q, want CERT_EXPIRY_IMMINENT", comp.Issue)
		}
		if single, _ := health.ActionsForIssue(comp.Issue, comp.Name); single == nil || single.Runbook == "" {
			t.Errorf("issue %q does not resolve to a §25.3 hint with a runbook", comp.Issue)
		}
	})

	t.Run("reports unhealthy and stamps CERT_EXPIRY_IMMINENT for an expired certificate", func(t *testing.T) {
		reader := backends.FileCertReader(writeCertFixture(t, time.Now().Add(-time.Minute)))
		comp := backends.CertManager(reader, "cert-manager").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy (detail: %s)", comp.Status, comp.Detail)
		}
		if comp.Issue != "CERT_EXPIRY_IMMINENT" {
			t.Errorf("Issue = %q, want CERT_EXPIRY_IMMINENT", comp.Issue)
		}
	})

	t.Run("reports unhealthy when the certificate file is unreadable", func(t *testing.T) {
		reader := backends.FileCertReader(filepath.Join(t.TempDir(), "does-not-exist.crt"))
		comp := backends.CertManager(reader, "cert-manager").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy", comp.Status)
		}
	})

	t.Run("bounds the probe at the §25.3 hard 2-second timeout", func(t *testing.T) {
		var seen time.Duration
		reader := deadlineCertReader{seen: &seen}
		comp := backends.CertManager(reader, "cert-manager").Check(ctx)
		if comp.Status != health.StatusUnhealthy {
			t.Errorf("Status = %q, want unhealthy", comp.Status)
		}
		assertProbeTimeout(t, seen)
	})
}

// deadlineCertReader is a backends.CertReader that records the remaining
// time on the deadline its checker handed it, then fails so the checker
// reports unhealthy. It observes the §25.3 hard probe timeout for the
// cert-manager probe, which takes a CertReader rather than a ProbeFunc.
type deadlineCertReader struct{ seen *time.Duration }

func (r deadlineCertReader) NotAfter(ctx context.Context) (time.Time, error) {
	dl, ok := ctx.Deadline()
	if !ok {
		*r.seen = -1
		return time.Time{}, fmt.Errorf("cert read context carried no deadline")
	}
	*r.seen = time.Until(dl)
	return time.Time{}, fmt.Errorf("cert read failed (deadline recorded)")
}
