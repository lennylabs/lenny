// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test that exercises the concrete §25.8 version
// aggregation sources against real backing services: the gateway
// version over HTTP (an httptest stand-in for GET
// /v1/admin/platform/version), the controller Deployment image tag, the
// installed CRDs' lenny.dev/schema-version annotation, and a
// helm.sh/release.v1 Secret's chart version over a real kube-apiserver
// (envtest), and the Postgres schema-migration version over an embedded
// Postgres. Unit tests cover the pure aggregator; this test pins the SQL
// query, the Deployment image-tag parse, the CRD annotation read, the
// Helm release Secret decode, and the gateway HTTP decode that
// lenny-ops wires, so a regression in any one of them fails here rather
// than shipping silently.

package observability_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/ops/versionsource"
	"github.com/lennylabs/lenny/pkg/preflight"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// spec: §25.8 Version Aggregation — "GET /v1/admin/platform/version/full
// aggregates: Gateway binary metadata from GatewayClient.GetVersion();
// ... Controller Deployment versions from K8s API. CRD versions from
// K8s API. Helm chart version from K8s API (helm.sh/release.v1 Secret).
// Postgres schema version from SELECT version FROM schema_migrations
// ORDER BY version DESC LIMIT 1. When any component's current version
// does not match the compiled-in required version, the response
// includes versionDrift: true and each drifted component includes
// drift: true and requiredAction."
//
// diagnosis: a failure means one of the concrete version sources no
// longer reads its real backing service correctly: the gateway HTTP call
// or its gatewayVersion decode, the schema_migrations SQL query, the
// lenny-controller Deployment image-tag parse, the CRD
// lenny.dev/schema-version annotation read, or the helm.sh/release.v1
// Secret decode. Inspect pkg/ops/versionsource against the gateway
// version handler, the migrations table shape, charts/lenny/templates/
// controller-deployment.yaml container/Deployment names, the
// charts/lenny/crds annotation, and the Helm 3 release-secret encoding
// (gzip + base64 JSON under the "release" data key).
func TestVersionAggregationOverRealSources(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	// envtest is required for the controller source; skip cleanly when
	// the kube-apiserver binaries are not installed.
	env := envtest.Start(t)

	const buildVersion = "9.9.9-test"

	// Gateway source: an httptest stand-in for the §25.3 gateway version
	// endpoint, returning the same version lenny-ops is built at (no
	// gateway drift for the baseline aggregation).
	gwVersion := buildVersion
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/platform/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"gatewayVersion": gwVersion})
	}))
	t.Cleanup(gw.Close)

	// Postgres source: embedded Postgres seeded with a golang-migrate
	// schema_migrations table so the §25.8 query resolves a real row.
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         0, // ephemeral; hardcoded ports collide under parallel tests
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	// The golang-migrate tracking table: (version bigint, dirty boolean).
	// Two rows verify the ORDER BY version DESC LIMIT 1 picks the latest.
	if _, err := pool.Exec(ctx,
		"CREATE TABLE schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)"); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"INSERT INTO schema_migrations (version, dirty) VALUES (17, false), (42, false)"); err != nil {
		t.Fatalf("seed schema_migrations: %v", err)
	}

	// Controller source: a real Deployment on the envtest API server,
	// matching the chart's Deployment name (lenny-controller) and
	// container name (controller).
	clientset, err := kubernetes.NewForConfig(env.RESTConfig())
	if err != nil {
		t.Fatalf("kubernetes client: %v", err)
	}
	const ns = "default"
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-controller", Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "lenny-controller"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "lenny-controller"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "controller", Image: "registry.example.com/lenny-controllers:" + buildVersion},
					},
				},
			},
		},
	}
	if _, err := clientset.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create lenny-controller Deployment: %v", err)
	}

	// CRD-version source: envtest.Start already installed the real
	// charts/lenny/crds manifests (see tests/testinfra/envtest), each
	// carrying the lenny.dev/schema-version annotation the chart ships
	// (preflight.CurrentCRDSchemaVersion), so no fixture CRDs are needed.
	apiextClient, err := apiextensionsclientset.NewForConfig(env.RESTConfig())
	if err != nil {
		t.Fatalf("apiextensions client: %v", err)
	}

	// Helm-chart-version source: a real Secret in the shape Helm 3's
	// Secret storage backend writes — type helm.sh/release.v1, the
	// owner/name/status labels the driver selects by, and a "release"
	// data key holding gzip(json(release))'s base64 text.
	const helmReleaseName = "test-release"
	const chartVersion = "3.2.1-test"
	helmSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sh.helm.release.v1." + helmReleaseName + ".v1",
			Namespace: ns,
			Labels: map[string]string{
				"owner":   "helm",
				"name":    helmReleaseName,
				"status":  "deployed",
				"version": "1",
			},
		},
		Type: corev1.SecretType("helm.sh/release.v1"),
		Data: map[string][]byte{
			"release": encodeHelmReleaseSecret(t, chartVersion),
		},
	}
	if _, err := clientset.CoreV1().Secrets(ns).Create(ctx, helmSecret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create helm.sh/release.v1 secret: %v", err)
	}

	// Assemble the aggregator over the concrete sources exactly as
	// lenny-ops wires them: ops anchors the report at buildVersion, the
	// gateway/controllers/crd-schema sources are required to match their
	// compiled-in expectation, and the schema and helm-chart sources
	// carry no required value (a migration counter and an independently
	// versioned chart respectively), reported for introspection.
	newAgg := func() *upgradeservice.VersionAggregator {
		return upgradeservice.NewVersionAggregator(upgradeservice.VersionAggregatorOptions{
			PlatformVersion: buildVersion,
			Sources: []upgradeservice.VersionSource{
				upgradeservice.NewFuncVersionSource("ops", buildVersion, func(context.Context) (string, error) {
					return buildVersion, nil
				}),
				upgradeservice.NewFuncVersionSource("gateway", buildVersion, versionsource.Gateway(gw.Client(), gw.URL)),
				upgradeservice.NewFuncVersionSource("controllers", buildVersion, versionsource.Controller(clientset, ns)),
				upgradeservice.NewFuncVersionSource("crd-schema", preflight.CurrentCRDSchemaVersion, versionsource.CRD(apiextClient)),
				upgradeservice.NewFuncVersionSource("helm-chart", "", versionsource.HelmChart(clientset, ns, helmReleaseName)),
				upgradeservice.NewFuncVersionSource("postgres-schema", "", versionsource.Schema(pool)),
			},
		})
	}

	// Baseline: every source available, every version matching, no drift.
	report := newAgg().Aggregate(ctx)
	comps := byName(report.Components)

	if report.RequiredVersion != buildVersion {
		t.Errorf("RequiredVersion = %q, want %q", report.RequiredVersion, buildVersion)
	}
	for name, wantCurrent := range map[string]string{
		"ops":             buildVersion,
		"gateway":         buildVersion,
		"controllers":     buildVersion,
		"crd-schema":      preflight.CurrentCRDSchemaVersion,
		"helm-chart":      chartVersion,
		"postgres-schema": "42",
	} {
		c, ok := comps[name]
		if !ok {
			t.Fatalf("report missing %q component; got %v", name, keys(comps))
		}
		if !c.Available {
			t.Errorf("%s: Available = false, Error=%q; source did not answer", name, c.Error)
		}
		if c.Current != wantCurrent {
			t.Errorf("%s: Current = %q, want %q", name, c.Current, wantCurrent)
		}
	}
	if report.VersionDrift {
		t.Errorf("VersionDrift = true on matching versions; drifted=%v", report.DegradationWarnings)
	}
	if report.DriftCount != 0 {
		t.Errorf("DriftCount = %d, want 0", report.DriftCount)
	}

	// Drift: point the controller Deployment at an older image tag, and
	// stamp every installed CRD's schema-version annotation to a stale
	// value (the realistic §10 line 438 "Helm does not update CRDs on
	// helm upgrade" scenario — every CRD stays uniformly stale, not one
	// out of several). The §25.8 contract requires versionDrift: true and
	// each drifted component to carry drift: true and a requiredAction.
	dep, err = clientset.AppsV1().Deployments(ns).Get(ctx, "lenny-controller", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get Deployment for update: %v", err)
	}
	dep.Spec.Template.Spec.Containers[0].Image = "registry.example.com/lenny-controllers:1.0.0"
	if _, err := clientset.AppsV1().Deployments(ns).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update Deployment image: %v", err)
	}
	for _, name := range preflight.LennyCRDNames {
		crd, err := apiextClient.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get CRD %q for update: %v", name, err)
		}
		crd.Annotations[preflight.CRDSchemaVersionAnnotation] = "0"
		if _, err := apiextClient.ApiextensionsV1().CustomResourceDefinitions().Update(ctx, crd, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("stamp stale schema-version on CRD %q: %v", name, err)
		}
	}

	drifted := newAgg().Aggregate(ctx)
	dc := byName(drifted.Components)
	ctrl := dc["controllers"]
	if !ctrl.Available || ctrl.Current != "1.0.0" {
		t.Fatalf("controllers after image change: Available=%v Current=%q, want available 1.0.0", ctrl.Available, ctrl.Current)
	}
	if !ctrl.Drift {
		t.Errorf("controllers: Drift = false, want true (current 1.0.0 != required %s)", buildVersion)
	}
	if ctrl.RequiredAction == "" {
		t.Errorf("controllers: RequiredAction empty, want a drift remediation action")
	}
	crdSchema := dc["crd-schema"]
	if !crdSchema.Available || crdSchema.Current != "0" {
		t.Fatalf("crd-schema after annotation change: Available=%v Current=%q, want available \"0\"", crdSchema.Available, crdSchema.Current)
	}
	if !crdSchema.Drift {
		t.Errorf("crd-schema: Drift = false, want true (current 0 != required %s)", preflight.CurrentCRDSchemaVersion)
	}
	if crdSchema.RequiredAction == "" {
		t.Errorf("crd-schema: RequiredAction empty, want a drift remediation action")
	}
	if !drifted.VersionDrift {
		t.Errorf("VersionDrift = false, want true after controller and CRD drift")
	}
	if drifted.DriftCount != 2 {
		t.Errorf("DriftCount = %d, want 2 (controllers and crd-schema drifted)", drifted.DriftCount)
	}
	// The schema and helm-chart sources carry no required value, so
	// neither ever drifts even though their reported values do not equal
	// the build version.
	if dc["postgres-schema"].Drift {
		t.Errorf("postgres-schema drifted; a source with no required version must not drift")
	}
	if dc["helm-chart"].Drift {
		t.Errorf("helm-chart drifted; a source with no required version must not drift")
	}
	if dc["helm-chart"].Current != chartVersion {
		t.Errorf("helm-chart: Current = %q, want %q (unaffected by the controller/CRD drift)", dc["helm-chart"].Current, chartVersion)
	}
}

// encodeHelmReleaseSecret builds the base64(gzip(json)) payload Helm 3's
// Secret storage backend writes under a helm.sh/release.v1 Secret's
// "release" data key, carrying only the chart.metadata.version field the
// §25.8 Helm-chart-version source reads.
func encodeHelmReleaseSecret(t *testing.T, chartVersion string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name": "test-release",
		"info": map[string]any{"status": "deployed"},
		"chart": map[string]any{
			"metadata": map[string]any{
				"name":    "lenny",
				"version": chartVersion,
			},
		},
		"version": 1,
	})
	if err != nil {
		t.Fatalf("marshal fake helm release: %v", err)
	}
	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	if _, err := gz.Write(body); err != nil {
		t.Fatalf("gzip fake helm release: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return []byte(base64.StdEncoding.EncodeToString(gzBuf.Bytes()))
}

// byName indexes components by their Name for assertion lookup.
func byName(comps []upgradeservice.ComponentVersion) map[string]upgradeservice.ComponentVersion {
	m := make(map[string]upgradeservice.ComponentVersion, len(comps))
	for _, c := range comps {
		m[c.Name] = c
	}
	return m
}

func keys(m map[string]upgradeservice.ComponentVersion) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
