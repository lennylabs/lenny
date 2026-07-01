// SPDX-License-Identifier: MIT

package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1alpha1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
)

// lennyCRDScheme builds a runtime.Scheme carrying the lenny.dev/v1alpha1
// Runtime and SandboxWarmPool types the config export lists, for the
// controller-runtime fake client.
func lennyCRDScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("lenny AddToScheme: %v", err)
	}
	return s
}

// crdReaderWith builds a fake CRDReader carrying the given Runtime and
// SandboxWarmPool custom resources.
func crdReaderWith(t *testing.T, objs ...client.Object) runner.CRDReader {
	t.Helper()
	return crfake.NewClientBuilder().WithScheme(lennyCRDScheme(t)).WithObjects(objs...).Build()
}

// runtimeCR builds a Runtime custom resource with the spec fields the config
// export snapshots.
func runtimeCR(name, typ, image, execMode, isoProfile, level, deployModel string) *lennyv1alpha1.Runtime {
	return &lennyv1alpha1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: lennyv1alpha1.RuntimeSpec{
			Type:             typ,
			Image:            image,
			ExecutionMode:    execMode,
			IsolationProfile: isoProfile,
			IntegrationLevel: level,
			DeploymentModel:  deployModel,
		},
	}
}

// poolCR builds a SandboxWarmPool custom resource with the spec sizing fields
// the config export snapshots.
func poolCR(name, templateRef string, minWarm, maxWarm int32, sdkWarmDisabled bool) *lennyv1alpha1.SandboxWarmPool {
	return &lennyv1alpha1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: lennyv1alpha1.SandboxWarmPoolSpec{
			TemplateRef:     templateRef,
			MinWarm:         minWarm,
			MaxWarm:         maxWarm,
			SDKWarmDisabled: sdkWarmDisabled,
		},
	}
}

// TestCRDExporterDumpsOnlyLennyGroupCRDs is the SEC-BACKUP-1 regression:
// the wired CRD export lists the lenny.dev-group CustomResourceDefinitions
// from the apiextensions API and serializes their manifests, rather than
// the pre-fix zero-byte fallback. It asserts the corrected outcome — real,
// filtered CRD manifests — which fails against the nil-CRDExport code.
//
// spec: §25.11 (Exports CRD manifests from the K8s API, line 3989; the
// lenny-backup-sa get/list on CRDs, line 3982).
//
// diagnosis: the config/full backup archive's CRD component is empty; the
// SEC-BACKUP-1 CRD export is unwired or lists the wrong group.
func TestCRDExporterDumpsOnlyLennyGroupCRDs(t *testing.T) {
	cs := apiextensionsfake.NewSimpleClientset(
		crd("runtimes.lenny.dev", "lenny.dev"),
		crd("sandboxwarmpools.lenny.dev", "lenny.dev"),
		crd("certificates.cert-manager.io", "cert-manager.io"),
	)
	export := runner.NewCRDExporter(cs)

	data, err := export(context.Background())
	if err != nil {
		t.Fatalf("CRD export: %v", err)
	}
	if len(data) == 0 || string(data) == "" {
		t.Fatal("CRD export produced no bytes (the pre-fix zero-byte fallback)")
	}
	var got []apiextensionsv1.CustomResourceDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal CRD export: %v", err)
	}
	// Only the two lenny.dev CRDs are exported, sorted by name; the
	// cert-manager CRD is excluded.
	if len(got) != 2 {
		t.Fatalf("exported %d CRDs, want 2 lenny.dev CRDs: %v", len(got), names(got))
	}
	if got[0].Name != "runtimes.lenny.dev" || got[1].Name != "sandboxwarmpools.lenny.dev" {
		t.Fatalf("exported CRDs = %v, want [runtimes.lenny.dev sandboxwarmpools.lenny.dev]", names(got))
	}
	for _, c := range got {
		if c.Spec.Group != "lenny.dev" {
			t.Errorf("exported a non-lenny.dev CRD: %s (group %q)", c.Name, c.Spec.Group)
		}
	}
}

// TestCRDExporterEmptyClusterYieldsEmptyArray asserts a cluster with no
// lenny.dev CRDs exports an empty JSON array rather than failing, so a
// config backup taken before the CRDs are installed still records an
// explicit CRD component.
//
// spec: §25.11 (Exports CRD manifests from the K8s API, line 3989).
func TestCRDExporterEmptyClusterYieldsEmptyArray(t *testing.T) {
	cs := apiextensionsfake.NewSimpleClientset(crd("certificates.cert-manager.io", "cert-manager.io"))
	export := runner.NewCRDExporter(cs)
	data, err := export(context.Background())
	if err != nil {
		t.Fatalf("CRD export: %v", err)
	}
	var got []apiextensionsv1.CustomResourceDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("exported %d CRDs from a cluster with no lenny.dev CRDs, want 0", len(got))
	}
}

// TestConfigExporterDumpsRuntimesPoolsTenantsQuotasAndBootstrap is the
// SEC-BACKUP-1 regression for the config export: the wired export collects
// all four §25.11 step-2 categories — the Runtime CRDs, the §5.2
// SandboxWarmPool CRDs, the tenants (with their quota columns) — plus the
// §17.6 bootstrap ConfigMap, rather than the pre-fix literal "{}". Per the
// C4 design it reads runtimes and pools from the K8s API custom resources,
// reserving Postgres for tenants and quotas.
//
// The pool assertions pin the SandboxWarmPool CRD spec fields (templateRef,
// minWarm, maxWarm), which are absent from the Postgres sandbox_warm_pools
// row the pre-conformance code read: a Postgres-sourced export would emit an
// empty templateRef and zero min/max, so this test fails against the
// pre-fix (Postgres-sourced) runtime/pool export as well as against the
// nil-ConfigExport "{}" fallback.
//
// spec: §25.11 (Exports platform configuration (runtimes, pools, tenants,
// quotas) as JSON, line 3988; the runtime and pool CRDs from the K8s API,
// line 3989; the lenny-backup-sa get on ConfigMaps, line 3982).
//
// diagnosis: the config/full backup archive's config component is empty,
// literal "{}", or omits a category (a restore cannot re-seed the missing
// runtimes/pools/tenants); the SEC-BACKUP-1 config export is unwired or
// reads runtimes/pools from the wrong source.
func TestConfigExporterDumpsRuntimesPoolsTenantsQuotasAndBootstrap(t *testing.T) {
	db := &fakeQuerier{
		tenants: [][]any{
			{"acme", "Acme Corp", "soc2", "us-east-1", "premium", int64(50), int64(1 << 30), int64(1_000_000), "monthly"},
		},
	}
	crds := crdReaderWith(
		t,
		runtimeCR("python-agent", "agent", "reg/python@sha256:"+hex64, "session", "sandboxed", "standard", "sidecar"),
		poolCR("default-gvisor", "python-template", 5, 12, false),
	)
	k8s := k8sfake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-bootstrap-values", Namespace: "lenny-system"},
		Data:       map[string]string{"seed.yaml": "adminUser: alice@acme.com"},
	})
	export := runner.NewConfigExporter(db, crds, k8s, "lenny-system")

	data, err := export(context.Background())
	if err != nil {
		t.Fatalf("config export: %v", err)
	}
	if string(data) == "{}" || len(data) == 0 {
		t.Fatal("config export produced the pre-fix empty {} fallback")
	}
	var cfg struct {
		Tenants []struct {
			ID                     string `json:"id"`
			WorkspaceTier          string `json:"workspaceTier"`
			ConcurrentSessionQuota int64  `json:"concurrentSessionQuota"`
			StorageQuotaBytes      int64  `json:"storageQuotaBytes"`
			TokenQuotaPerWindow    int64  `json:"tokenQuotaPerWindow"`
			QuotaResetPeriod       string `json:"quotaResetPeriod"`
		} `json:"tenants"`
		Runtimes []struct {
			Name             string `json:"name"`
			Type             string `json:"type"`
			IntegrationLevel string `json:"integrationLevel"`
		} `json:"runtimes"`
		Pools []struct {
			Name        string `json:"name"`
			TemplateRef string `json:"templateRef"`
			MinWarm     int32  `json:"minWarm"`
			MaxWarm     int32  `json:"maxWarm"`
		} `json:"pools"`
		BootstrapValues map[string]string `json:"bootstrapValues"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config export: %v", err)
	}
	if len(cfg.Tenants) != 1 || cfg.Tenants[0].ID != "acme" {
		t.Fatalf("tenants = %+v, want one acme tenant", cfg.Tenants)
	}
	// The quota columns travel with the tenant row (the §25.11 "quotas").
	q := cfg.Tenants[0]
	if q.ConcurrentSessionQuota != 50 || q.StorageQuotaBytes != 1<<30 ||
		q.TokenQuotaPerWindow != 1_000_000 || q.QuotaResetPeriod != "monthly" {
		t.Errorf("tenant quotas not exported: %+v", q)
	}
	if len(cfg.Runtimes) != 1 || cfg.Runtimes[0].Name != "python-agent" {
		t.Fatalf("runtimes = %+v, want one python-agent runtime from the Runtime CRD", cfg.Runtimes)
	}
	if cfg.Runtimes[0].Type != "agent" || cfg.Runtimes[0].IntegrationLevel != "standard" {
		t.Errorf("runtime CRD spec fields not exported: %+v", cfg.Runtimes[0])
	}
	// The §5.2 SandboxWarmPool CRD registry is the fourth §25.11 category.
	// The CRD spec fields (templateRef, minWarm, maxWarm) are absent from
	// the Postgres row the pre-conformance code read, so a Postgres-sourced
	// export would leave templateRef empty and min/max zero.
	if len(cfg.Pools) != 1 {
		t.Fatalf("pools = %+v, want one default-gvisor pool from the SandboxWarmPool CRD", cfg.Pools)
	}
	p := cfg.Pools[0]
	if p.Name != "default-gvisor" || p.TemplateRef != "python-template" ||
		p.MinWarm != 5 || p.MaxWarm != 12 {
		t.Errorf("pool CRD spec fields not exported faithfully (a Postgres-sourced export loses templateRef/min/max): %+v", p)
	}
	if cfg.BootstrapValues["seed.yaml"] != "adminUser: alice@acme.com" {
		t.Errorf("bootstrap values not exported: %+v", cfg.BootstrapValues)
	}
}

// TestConfigExporterReadsRuntimesAndPoolsFromCRDsNotPostgres is the C4
// design-conformance regression: the config export lists runtimes and pools
// from the K8s API custom resources, and never issues a Postgres query for
// the runtime_definitions or sandbox_warm_pools tables. It fails against the
// pre-fix code that read both from Postgres, because that code would issue
// those two SELECTs (recorded here by the querier) and would emit empty
// runtimes/pools when only CRDs are present.
//
// spec: §25.11 (the K8s API for the runtime and pool CRDs, line 3989; the
// read-only lenny-backup Postgres role for tenants and quotas, line 3980).
//
// diagnosis: the config export reads runtimes/pools from Postgres rather
// than the CRDs the C4 design assigns; the read-only lenny-backup role has no
// grant on runtime_definitions/sandbox_warm_pools and the export can fail a
// permission check the CRD path never hits.
func TestConfigExporterReadsRuntimesAndPoolsFromCRDsNotPostgres(t *testing.T) {
	db := &fakeQuerier{
		tenants: [][]any{
			{"acme", "Acme Corp", "soc2", "us-east-1", "premium", int64(1), int64(1), int64(1), "monthly"},
		},
	}
	crds := crdReaderWith(
		t,
		runtimeCR("python-agent", "agent", "reg/python@sha256:"+hex64, "session", "sandboxed", "standard", "sidecar"),
		poolCR("default-gvisor", "python-template", 2, 4, false),
	)
	k8s := k8sfake.NewSimpleClientset()
	export := runner.NewConfigExporter(db, crds, k8s, "lenny-system")

	data, err := export(context.Background())
	if err != nil {
		t.Fatalf("config export: %v", err)
	}
	// The export must not have SELECTed the runtime/pool tables: those reads
	// belong to the CRD path, not Postgres.
	for _, sql := range db.seen {
		if containsAny(sql, "runtime_definitions", "sandbox_warm_pools") {
			t.Errorf("config export queried Postgres for a runtime/pool table (should read the CRDs): %q", sql)
		}
	}
	var cfg struct {
		Runtimes []struct {
			Name string `json:"name"`
		} `json:"runtimes"`
		Pools []struct {
			Name        string `json:"name"`
			TemplateRef string `json:"templateRef"`
		} `json:"pools"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The CRD-sourced runtimes and pools are present even though Postgres
	// returned no runtime/pool rows.
	if len(cfg.Runtimes) != 1 || cfg.Runtimes[0].Name != "python-agent" {
		t.Errorf("runtimes not sourced from the CRDs: %+v", cfg.Runtimes)
	}
	if len(cfg.Pools) != 1 || cfg.Pools[0].TemplateRef != "python-template" {
		t.Errorf("pools not sourced from the CRDs: %+v", cfg.Pools)
	}
}

// TestConfigExporterRuntimeListErrorSurfaces asserts the Runtime CRD list
// error is wrapped and returned (the tenants read succeeds first) rather than
// silently producing a config export that omits runtimes.
//
// spec: §25.11 (Exports platform configuration (runtimes, ...), line 3988; the
// runtime CRD from the K8s API, line 3989).
func TestConfigExporterRuntimeListErrorSurfaces(t *testing.T) {
	sentinel := errors.New("runtime list failed")
	db := &fakeQuerier{}
	export := runner.NewConfigExporter(db, errCRDReader{err: sentinel}, k8sfake.NewSimpleClientset(), "lenny-system")
	if _, err := export(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped %v", err, sentinel)
	}
}

// TestConfigExporterPoolListErrorSurfaces asserts the SandboxWarmPool CRD
// list error is wrapped and returned rather than silently producing a config
// export that omits pools. The reader fails only the SandboxWarmPool list, so
// the runtime list succeeds first.
//
// spec: §25.11 (Exports platform configuration (runtimes, pools, ...), line
// 3988; the pool CRD from the K8s API, line 3989).
func TestConfigExporterPoolListErrorSurfaces(t *testing.T) {
	sentinel := errors.New("pool list failed")
	db := &fakeQuerier{}
	export := runner.NewConfigExporter(db, poolErrCRDReader{err: sentinel}, k8sfake.NewSimpleClientset(), "lenny-system")
	if _, err := export(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped %v", err, sentinel)
	}
}

// TestConfigExporterMissingBootstrapConfigMapIsNotFatal asserts a missing
// bootstrap ConfigMap (a deployment that seeded none, or one whose Job
// ConfigMap was garbage-collected) yields a config export with no
// bootstrap values rather than an error.
//
// spec: §25.11 (the config export reads only reachable sources, line 3984).
func TestConfigExporterMissingBootstrapConfigMapIsNotFatal(t *testing.T) {
	db := &fakeQuerier{}
	crds := crdReaderWith(t)
	k8s := k8sfake.NewSimpleClientset() // no ConfigMap
	export := runner.NewConfigExporter(db, crds, k8s, "lenny-system")
	data, err := export(context.Background())
	if err != nil {
		t.Fatalf("config export with no bootstrap ConfigMap: %v", err)
	}
	var cfg struct {
		BootstrapValues map[string]string `json:"bootstrapValues"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.BootstrapValues) != 0 {
		t.Errorf("bootstrap values = %v, want none for a missing ConfigMap", cfg.BootstrapValues)
	}
}

// TestConfigExporterQueryErrorSurfaces asserts a Postgres read error (on the
// tenants read) is wrapped and returned rather than silently producing a
// partial config.
//
// spec: §25.11 (config export, line 3988).
func TestConfigExporterQueryErrorSurfaces(t *testing.T) {
	sentinel := errors.New("postgres down")
	db := &fakeQuerier{queryErr: sentinel}
	crds := crdReaderWith(t)
	k8s := k8sfake.NewSimpleClientset()
	export := runner.NewConfigExporter(db, crds, k8s, "lenny-system")
	if _, err := export(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped %v", err, sentinel)
	}
}

// TestConfigExporterScanErrorSurfaces asserts a tenant row-scan failure is
// wrapped and returned rather than producing a partial config.
//
// spec: §25.11 (config export, line 3988).
func TestConfigExporterScanErrorSurfaces(t *testing.T) {
	db := &fakeQuerier{tenantScanErr: true}
	crds := crdReaderWith(t)
	k8s := k8sfake.NewSimpleClientset()
	export := runner.NewConfigExporter(db, crds, k8s, "lenny-system")
	if _, err := export(context.Background()); err == nil {
		t.Fatal("config export accepted a scan error")
	}
}

// TestCRDExporterListErrorSurfaces asserts an apiextensions List error is
// wrapped and returned rather than yielding a silently empty CRD component.
//
// spec: §25.11 (Exports CRD manifests from the K8s API, line 3989).
func TestCRDExporterListErrorSurfaces(t *testing.T) {
	sentinel := errors.New("apiserver unreachable")
	e := &runner.CRDExporter{Lister: errLister{err: sentinel}}
	if _, err := e.Export(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped %v", err, sentinel)
	}
}

// TestNewCRDReaderBuildsAClient asserts NewCRDReader returns a non-nil
// CRDReader from a valid in-cluster-style rest.Config (the lenny-backup Job's
// injected config). The client is built lazily and does not connect at
// construction, so this exercises the constructor without a live apiserver.
//
// spec: §25.11 (the K8s API for the runtime and pool CRDs, line 3989).
func TestNewCRDReaderBuildsAClient(t *testing.T) {
	r, err := runner.NewCRDReader(&rest.Config{Host: "https://127.0.0.1:6443"})
	if err != nil {
		t.Fatalf("NewCRDReader: %v", err)
	}
	if r == nil {
		t.Fatal("NewCRDReader returned a nil reader")
	}
}

// TestNewCRDReaderRejectsInvalidConfig asserts NewCRDReader wraps and returns
// the error when the rest.Config cannot build a client (an unparseable host),
// rather than returning a nil reader with a nil error.
//
// spec: §25.11 (the K8s API for the runtime and pool CRDs, line 3989).
func TestNewCRDReaderRejectsInvalidConfig(t *testing.T) {
	if _, err := runner.NewCRDReader(&rest.Config{Host: "://not a url"}); err == nil {
		t.Fatal("NewCRDReader accepted an invalid rest.Config")
	}
}

// errLister is a runner.CRDLister that always fails.
type errLister struct{ err error }

func (l errLister) List(context.Context, metav1.ListOptions) (*apiextensionsv1.CustomResourceDefinitionList, error) {
	return nil, l.err
}

// crd builds a minimal CustomResourceDefinition for name in group.
func crd(name, group string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       apiextensionsv1.CustomResourceDefinitionSpec{Group: group},
	}
}

// names extracts the CRD names for a failure message.
func names(crds []apiextensionsv1.CustomResourceDefinition) []string {
	out := make([]string, len(crds))
	for i, c := range crds {
		out[i] = c.Name
	}
	return out
}

// hex64 is a 64-hex-digit sha256 body the §5.3 Runtime CRD image Pattern
// requires (the test builds `reg/python@sha256:<hex64>`). The controller-
// runtime fake does not enforce the CRD OpenAPI validation, but a valid
// digest keeps the fixture realistic.
const hex64 = "0000000000000000000000000000000000000000000000000000000000000000"

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// errCRDReader is a runner.CRDReader whose List always fails. It exercises the
// runtime-list error path (the first CRD list the export issues).
type errCRDReader struct{ err error }

func (r errCRDReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return r.err
}

// poolErrCRDReader is a runner.CRDReader that fails only the SandboxWarmPool
// list, so the runtime list succeeds first and the export reaches the pool
// read before failing.
type poolErrCRDReader struct{ err error }

func (r poolErrCRDReader) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	if _, ok := list.(*lennyv1alpha1.SandboxWarmPoolList); ok {
		return r.err
	}
	return nil
}

// fakeQuerier is a runner.Querier that returns fixed tenant rows. Runtimes
// and pools are read from the CRD reader (not Postgres), so the querier serves
// only the tenants read. It records every SQL string in seen so a test can
// assert the export never queried the runtime/pool tables.
type fakeQuerier struct {
	tenants       [][]any
	queryErr      error    // fails every Query
	tenantScanErr bool     // returns a scan-error row for tenants
	seen          []string // every SQL string Query received
}

func (q *fakeQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	q.seen = append(q.seen, sql)
	if q.queryErr != nil {
		return nil, q.queryErr
	}
	if q.tenantScanErr {
		return &fakeRows{data: [][]any{{"bad"}}}, nil // column-count mismatch on Scan
	}
	return &fakeRows{data: q.tenants}, nil
}

// fakeRows is a pgx.Rows over a slice of pre-set row value slices. Scan
// assigns each column value through the destination pointer by type.
type fakeRows struct {
	data [][]any
	i    int
}

func (r *fakeRows) Next() bool {
	if r.i >= len(r.data) {
		return false
	}
	r.i++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	row := r.data[r.i-1]
	if len(row) != len(dest) {
		return errors.New("fakeRows: column count mismatch")
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = row[i].(string)
		case *int64:
			*d = row[i].(int64)
		case *bool:
			*d = row[i].(bool)
		default:
			return errors.New("fakeRows: unsupported dest type")
		}
	}
	return nil
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
