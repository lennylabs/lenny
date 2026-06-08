// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/ctl"
	"github.com/lennylabs/lenny/pkg/preflight"
	"github.com/lennylabs/lenny/pkg/preflight/infra"
)

func preflightScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		t.Fatalf("apiextensions AddToScheme: %v", err)
	}
	return s
}

func crd(name, version string) *apiextensionsv1.CustomResourceDefinition {
	c := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if version != "" {
		c.Annotations = map[string]string{preflight.CRDSchemaVersionAnnotation: version}
	}
	return c
}

// spec: §10.5 line 443 — `lenny-ctl preflight` exits 0 when every
// installed CRD is current and non-zero with the verbatim stale-CRD
// message otherwise. F-10.5.4.
func TestRunPreflightCRDCheck_spec_10_5_443(t *testing.T) {
	t.Run("all current passes", func(t *testing.T) {
		var objs []client.Object
		for _, name := range preflight.LennyCRDNames {
			objs = append(objs, crd(name, preflight.CurrentCRDSchemaVersion))
		}
		cl := fake.NewClientBuilder().WithScheme(preflightScheme(t)).WithObjects(objs...).Build()
		var out, errb bytes.Buffer
		if code := runPreflightCRDCheck(context.Background(), cl, &out, &errb); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errb.String())
		}
		if !strings.Contains(out.String(), preflight.CurrentCRDSchemaVersion) {
			t.Errorf("stdout %q should cite the schema version", out.String())
		}
	})

	t.Run("stale CRD fails with the spec message", func(t *testing.T) {
		var objs []client.Object
		for i, name := range preflight.LennyCRDNames {
			v := preflight.CurrentCRDSchemaVersion
			if i == 0 {
				v = "0" // one stale CRD
			}
			objs = append(objs, crd(name, v))
		}
		cl := fake.NewClientBuilder().WithScheme(preflightScheme(t)).WithObjects(objs...).Build()
		var out, errb bytes.Buffer
		if code := runPreflightCRDCheck(context.Background(), cl, &out, &errb); code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "schema version is") {
			t.Errorf("stderr %q should carry the §10.5 line 443 stale-CRD message", errb.String())
		}
	})

	t.Run("missing CRD fails", func(t *testing.T) {
		// Install nothing: every CRD is reported missing.
		cl := fake.NewClientBuilder().WithScheme(preflightScheme(t)).Build()
		var out, errb bytes.Buffer
		if code := runPreflightCRDCheck(context.Background(), cl, &out, &errb); code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "missing") {
			t.Errorf("stderr %q should report missing CRDs", errb.String())
		}
	})
}

// --- §24.2 infrastructure-connectivity preflight (F-24.2.x) ---

type fakePG struct {
	version string
	err     error
}

func (f fakePG) ProbePostgres(context.Context, string) (string, error) { return f.version, f.err }

type fakeRD struct{ err error }

func (f fakeRD) ProbeRedis(context.Context, string) error { return f.err }

type fakeMIO struct{ err error }

func (f fakeMIO) ProbeMinIO(context.Context, string, string, string, string, bool) error {
	return f.err
}

// spec: §24.2 line 47 — the values file supplies the connection fallback;
// the spec-named key (postgres.connectionString) wins over the chart key
// (postgres.dsn), and minio.useSSL is honoured.
func TestLoadPreflightValues_spec_24_2_47(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/values.yaml"
	body := []byte(`
postgres:
  connectionString: postgres://spec
  dsn: postgres://chart
redis:
  url: redis://chart
minio:
  endpoint: minio:9000
  accessKey: ak
  secretKey: sk
  bucket: artifacts
  useSSL: false
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, useSSL, err := loadPreflightValues(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.PostgresDSN != "postgres://spec" {
		t.Errorf("postgres: spec key should win, got %q", cfg.PostgresDSN)
	}
	if cfg.RedisDSN != "redis://chart" {
		t.Errorf("redis: chart key fallback, got %q", cfg.RedisDSN)
	}
	if cfg.MinIOEndpoint != "minio:9000" || cfg.MinIOBucket != "artifacts" {
		t.Errorf("minio fields: %+v", cfg)
	}
	if useSSL {
		t.Error("minio.useSSL:false should disable TLS")
	}
}

func TestLoadPreflightValues_EmptyPathDefaultsSSLTrue(t *testing.T) {
	cfg, useSSL, err := loadPreflightValues("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Configured() {
		t.Error("empty path should yield an empty config")
	}
	if !useSSL {
		t.Error("default useSSL should be true")
	}
}

// spec: §24.2 line 39 — standalone mode probes locally; all-pass exits 0.
func TestRunStandalonePreflight_AllPass(t *testing.T) {
	var out, errb bytes.Buffer
	cfg := infra.Config{PostgresDSN: "postgres://x", RedisDSN: "redis://x"}
	code := runStandalonePreflight(context.Background(), cfg,
		infra.Probers{Postgres: fakePG{version: "116"}, Redis: fakeRD{}, MinIO: fakeMIO{}},
		nil, &out, &errb)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "PASS postgres-connectivity") {
		t.Errorf("missing postgres PASS line: %s", out.String())
	}
	if !strings.Contains(out.String(), "SKIP crd-schema-version") {
		t.Errorf("expected CRD skip with no cluster: %s", out.String())
	}
}

// spec: §15.1 line 890 — an unreachable backend fails the standalone run.
func TestRunStandalonePreflight_BackendFailureExits1(t *testing.T) {
	var out, errb bytes.Buffer
	cfg := infra.Config{PostgresDSN: "postgres://x"}
	code := runStandalonePreflight(context.Background(), cfg,
		infra.Probers{Postgres: fakePG{err: context.DeadlineExceeded}}, nil, &out, &errb)
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	if !strings.Contains(out.String(), "FAIL postgres-connectivity") {
		t.Errorf("missing FAIL line: %s", out.String())
	}
}

// spec: §10.5 line 443 — when Lenny CRDs are installed (upgrade), the
// CRD-currency check runs and a stale CRD fails the preflight.
func TestRunStandalonePreflight_CRDCheckRunsWhenInstalled(t *testing.T) {
	var objs []client.Object
	for i, name := range preflight.LennyCRDNames {
		v := preflight.CurrentCRDSchemaVersion
		if i == 0 {
			v = "0" // stale
		}
		objs = append(objs, crd(name, v))
	}
	cl := fake.NewClientBuilder().WithScheme(preflightScheme(t)).WithObjects(objs...).Build()
	var out, errb bytes.Buffer
	code := runStandalonePreflight(context.Background(), infra.Config{}, infra.RealProbers(), cl, &out, &errb)
	if code != 1 {
		t.Fatalf("stale CRD should fail, exit=%d", code)
	}
	if !strings.Contains(errb.String(), "schema version is") {
		t.Errorf("missing §10.5 stale-CRD message: %s", errb.String())
	}
}

// A reachable cluster with no Lenny CRDs (fresh install) skips the
// currency check rather than failing on "missing".
func TestRunStandalonePreflight_FreshInstallSkipsCRD(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(preflightScheme(t)).Build()
	var out, errb bytes.Buffer
	code := runStandalonePreflight(context.Background(), infra.Config{}, infra.RealProbers(), cl, &out, &errb)
	if code != 0 {
		t.Fatalf("fresh install should pass, exit=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "no Lenny CRDs installed") {
		t.Errorf("expected fresh-install skip: %s", out.String())
	}
}

// spec: §24.2 line 43 — API-backed mode renders the gateway's report and
// exits non-zero on a failed check.
func TestRunAPIPreflight_spec_24_2_43(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"passed":true,"checks":[{"name":"postgres-connectivity","passed":true,"reason":"ok"}]}`))
		}))
		defer ts.Close()
		var out, errb bytes.Buffer
		if code := runAPIPreflight(context.Background(), ctl.New(ctl.Options{BaseURL: ts.URL}), &out, &errb); code != 0 {
			t.Fatalf("exit=%d stderr=%s", code, errb.String())
		}
		if !strings.Contains(out.String(), "PASS postgres-connectivity") {
			t.Errorf("rendered report missing: %s", out.String())
		}
	})
	t.Run("fail", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"passed":false,"checks":[{"name":"redis-connectivity","passed":false,"reason":"REDIS_UNREACHABLE"}]}`))
		}))
		defer ts.Close()
		var out, errb bytes.Buffer
		if code := runAPIPreflight(context.Background(), ctl.New(ctl.Options{BaseURL: ts.URL}), &out, &errb); code != 1 {
			t.Fatalf("want exit 1, got %d", code)
		}
	})
}

// spec: §24.2 line 39 — reachability decides the mode; a live /healthz is
// reachable, a dead URL is not.
func TestPreflightGatewayReachable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	if !preflightGatewayReachable(context.Background(), ctl.New(ctl.Options{BaseURL: ts.URL})) {
		t.Error("live gateway should be reachable")
	}
	if preflightGatewayReachable(context.Background(), ctl.New(ctl.Options{BaseURL: "http://127.0.0.1:1", Timeout: time.Second})) {
		t.Error("dead gateway should be unreachable")
	}
}
