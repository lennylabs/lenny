// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test that exercises the concrete §25.8 version
// aggregation sources against real backing services: the gateway
// version over HTTP (an httptest stand-in for GET
// /v1/admin/platform/version), the controller Deployment image tag over
// a real kube-apiserver (envtest), and the Postgres schema-migration
// version over an embedded Postgres. Unit tests cover the pure
// aggregator; this test pins the SQL query, the Deployment image-tag
// parse, and the gateway HTTP decode that lenny-ops wires, so a
// regression in any one of them fails here rather than shipping silently.

package observability_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/ops/versionsource"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// spec: §25.8 Version Aggregation — "GET /v1/admin/platform/version/full
// aggregates: Gateway binary metadata from GatewayClient.GetVersion();
// Controller Deployment versions from K8s API; Postgres schema version
// from SELECT version FROM schema_migrations ORDER BY version DESC LIMIT
// 1. When any component's current version does not match the compiled-in
// required version, the response includes versionDrift: true and each
// drifted component includes drift: true and requiredAction."
//
// diagnosis: a failure means one of the concrete version sources no
// longer reads its real backing service correctly: the gateway HTTP call
// or its gatewayVersion decode, the schema_migrations SQL query, or the
// lenny-controller Deployment image-tag parse. Inspect
// pkg/ops/versionsource against the gateway version handler, the
// migrations table shape, and charts/lenny/templates/controller-
// deployment.yaml container/Deployment names.
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

	// Assemble the aggregator over the concrete sources exactly as
	// lenny-ops wires them: ops anchors the report at buildVersion, the
	// gateway/controllers sources are required to match buildVersion, and
	// the schema source carries no required value (it is a migration
	// counter, reported for introspection).
	newAgg := func() *upgradeservice.VersionAggregator {
		return upgradeservice.NewVersionAggregator(upgradeservice.VersionAggregatorOptions{
			PlatformVersion: buildVersion,
			Sources: []upgradeservice.VersionSource{
				upgradeservice.NewFuncVersionSource("ops", buildVersion, func(context.Context) (string, error) {
					return buildVersion, nil
				}),
				upgradeservice.NewFuncVersionSource("gateway", buildVersion, versionsource.Gateway(gw.Client(), gw.URL)),
				upgradeservice.NewFuncVersionSource("controllers", buildVersion, versionsource.Controller(clientset, ns)),
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

	// Drift: point the controller Deployment at an older image tag. The
	// §25.8 contract requires versionDrift: true and the drifted
	// component to carry drift: true and a requiredAction.
	dep, err = clientset.AppsV1().Deployments(ns).Get(ctx, "lenny-controller", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get Deployment for update: %v", err)
	}
	dep.Spec.Template.Spec.Containers[0].Image = "registry.example.com/lenny-controllers:1.0.0"
	if _, err := clientset.AppsV1().Deployments(ns).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update Deployment image: %v", err)
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
	if !drifted.VersionDrift {
		t.Errorf("VersionDrift = false, want true after controller drift")
	}
	if drifted.DriftCount != 1 {
		t.Errorf("DriftCount = %d, want 1 (only controllers drifted)", drifted.DriftCount)
	}
	// The schema source carries no required value, so it never drifts
	// even though "42" does not equal the build version.
	if dc["postgres-schema"].Drift {
		t.Errorf("postgres-schema drifted; a source with no required version must not drift")
	}
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
