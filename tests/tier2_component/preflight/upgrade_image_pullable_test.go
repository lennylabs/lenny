//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §25.8 Phase-1 upgrade-preflight image-pullability
// gate wired against a real registry: the production
// upgradeservice.RegistryImagePullChecker issuing an OCI Distribution API
// v2 manifest HEAD request per resolved image, served by an httptest stub
// registry that answers 200 (present) for one image and 404 (absent) for
// another. The test drives the real opsserver HTTP surface end to end
// (POST /v1/admin/platform/upgrade/preflight) so the assertions cover the
// same handler chain production traffic reaches, and it observes the
// lenny_platform_image_pull_check_duration_seconds histogram through the
// same ImagePullCheckRecorder hook cmd/lenny-ops wires into the
// Preflighter, confirming the production check records a real sample
// rather than staying dormant.
package preflight_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
)

// stubRegistry serves the OCI Distribution API v2 manifest HEAD endpoint:
// 200 for a manifest path in present, 404 otherwise. It requires no
// authentication, matching a simple air-gapped mirror.
func stubRegistry(t *testing.T, present map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("registry stub got method %s, want HEAD", r.Method)
		}
		if present[r.URL.Path] {
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

// doPreflight issues the §25.8 preflight request against srv and returns
// the recorder.
func doPreflight(srv *opsserver.Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/platform/upgrade/preflight", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// histogramSampleCount reads the observation count for one label
// combination of a registered HistogramVec, mirroring the
// lenny_platform_image_pull_check_duration_seconds{component=...} series
// the §25.8 line 3619 metric exposes.
func histogramSampleCount(t *testing.T, h *prometheus.HistogramVec, labelValue string) uint64 {
	t.Helper()
	var m dto.Metric
	if err := h.WithLabelValues(labelValue).(prometheus.Histogram).Write(&m); err != nil {
		t.Fatalf("write histogram metric: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

// diagnosis: a failure means the production image-pullability check (the
// real HTTP manifest probe cmd/lenny-ops wires into the upgrade
// preflighter) is not actually validating images against the registry, so
// an air-gapped install with a mirror missing a target image would pass
// preflight and only fail mid-upgrade instead of being caught up front.
//
// spec: §25.8 line 3500 — "Resolves all target images through
// ImageResolver and validates they are pullable. For each component,
// issues a HEAD request to the registry manifest endpoint (or `crane
// manifest --platform linux/amd64` equivalent). This catches missing
// mirrors before any changes are made."
func TestUpgradePreflight_ImageNotPullableAgainstRealRegistry(t *testing.T) {
	stub := stubRegistry(t, map[string]bool{
		"/v2/lenny-gateway/manifests/1.6.0": true, // present
		// lenny-ops manifest is absent: the registry answers 404.
	})
	defer stub.Close()
	host := strings.TrimPrefix(stub.URL, "http://")

	reg := prometheus.NewRegistry()
	hist := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lenny_platform_image_pull_check_duration_seconds",
		Help:    "test-local mirror of the §25.8 line 3619 histogram.",
		Buckets: prometheus.DefBuckets,
	}, []string{"component"})
	if err := reg.Register(hist); err != nil {
		t.Fatalf("register histogram: %v", err)
	}

	pf := upgradeservice.NewPreflighter(upgradeservice.PreflighterOptions{
		Store:  upgradeservice.NewMemoryStore(),
		Images: upgradeservice.NewRegistryImagePullChecker(0),
		ImageDuration: func(component string, d time.Duration) {
			hist.WithLabelValues(component).Observe(d.Seconds())
		},
	})
	srv := opsserver.New(opsserver.Options{UpgradePreflighter: pf})

	body := `{"version":"1.6.0","images":{"gateway":"` + host + `/lenny-gateway:1.6.0","ops":"` + host + `/lenny-ops:1.6.0"}}`
	w := doPreflight(srv, body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("preflight status = %d, body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	errBody, _ := resp["error"].(map[string]any)
	if errBody["code"] != "UPGRADE_IMAGE_NOT_PULLABLE" {
		t.Fatalf("code = %v, want UPGRADE_IMAGE_NOT_PULLABLE; body=%s", errBody["code"], w.Body.String())
	}
	details, _ := errBody["details"].(map[string]any)
	images, _ := details["images"].([]any)
	if len(images) != 1 || images[0] != host+"/lenny-ops:1.6.0" {
		t.Fatalf("details.images = %v, want [%q]", images, host+"/lenny-ops:1.6.0")
	}

	if n := histogramSampleCount(t, hist, "gateway"); n == 0 {
		t.Errorf("lenny_platform_image_pull_check_duration_seconds{component=\"gateway\"} sample count = 0, want > 0")
	}
	if n := histogramSampleCount(t, hist, "ops"); n == 0 {
		t.Errorf("lenny_platform_image_pull_check_duration_seconds{component=\"ops\"} sample count = 0, want > 0")
	}
}

// diagnosis: a failure means the production checker reports a present
// image as unpullable (or vice versa), which would either block a valid
// upgrade or let a genuinely missing mirror image through.
//
// spec: §25.8 line 3500 (image-pullability HEAD check).
func TestUpgradePreflight_ImagePullableAgainstRealRegistry(t *testing.T) {
	stub := stubRegistry(t, map[string]bool{
		"/v2/lenny-gateway/manifests/1.6.0": true,
		"/v2/lenny-ops/manifests/1.6.0":     true,
	})
	defer stub.Close()
	host := strings.TrimPrefix(stub.URL, "http://")

	pf := upgradeservice.NewPreflighter(upgradeservice.PreflighterOptions{
		Store:  upgradeservice.NewMemoryStore(),
		Images: upgradeservice.NewRegistryImagePullChecker(0),
	})
	srv := opsserver.New(opsserver.Options{UpgradePreflighter: pf})

	body := `{"version":"1.6.0","images":{"gateway":"` + host + `/lenny-gateway:1.6.0","ops":"` + host + `/lenny-ops:1.6.0"}}`
	w := doPreflight(srv, body)
	if w.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["passed"] != true {
		t.Fatalf("passed = %v, want true; body=%s", resp["passed"], w.Body.String())
	}
}
