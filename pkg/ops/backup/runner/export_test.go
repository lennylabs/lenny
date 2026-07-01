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
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
)

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

// TestConfigExporterDumpsTenantsQuotasRuntimesAndBootstrap is the
// SEC-BACKUP-1 regression for the config export: the wired export collects
// the tenants (with their quota columns), the runtime registry, and the
// §17.6 bootstrap ConfigMap, rather than the pre-fix literal "{}". It
// asserts the corrected outcome — a populated platform config — which
// fails against the nil-ConfigExport code that returned "{}".
//
// spec: §25.11 (Exports platform configuration (runtimes, pools, tenants,
// quotas) as JSON, line 3988; the lenny-backup-sa get on ConfigMaps, line
// 3982).
//
// diagnosis: the config/full backup archive's config component is empty or
// literal "{}"; the SEC-BACKUP-1 config export is unwired or reads the
// wrong tables.
func TestConfigExporterDumpsTenantsQuotasRuntimesAndBootstrap(t *testing.T) {
	db := &fakeQuerier{
		tenants: [][]any{
			{"acme", "Acme Corp", "soc2", "us-east-1", "premium", int64(50), int64(1 << 30), int64(1_000_000), "monthly"},
		},
		runtimes: [][]any{
			{"python-agent", "agent", "reg/python:1", "session", "sandboxed", "standard", "Python runtime"},
		},
	}
	k8s := k8sfake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "lenny-bootstrap-values", Namespace: "lenny-system"},
		Data:       map[string]string{"seed.yaml": "adminUser: alice@acme.com"},
	})
	export := runner.NewConfigExporter(db, k8s, "lenny-system")

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
			Name string `json:"name"`
		} `json:"runtimes"`
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
		t.Fatalf("runtimes = %+v, want one python-agent runtime", cfg.Runtimes)
	}
	if cfg.BootstrapValues["seed.yaml"] != "adminUser: alice@acme.com" {
		t.Errorf("bootstrap values not exported: %+v", cfg.BootstrapValues)
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
	k8s := k8sfake.NewSimpleClientset() // no ConfigMap
	export := runner.NewConfigExporter(db, k8s, "lenny-system")
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

// TestConfigExporterQueryErrorSurfaces asserts a Postgres read error is
// wrapped and returned rather than silently producing a partial config.
//
// spec: §25.11 (config export, line 3988).
func TestConfigExporterQueryErrorSurfaces(t *testing.T) {
	sentinel := errors.New("postgres down")
	db := &fakeQuerier{queryErr: sentinel}
	k8s := k8sfake.NewSimpleClientset()
	export := runner.NewConfigExporter(db, k8s, "lenny-system")
	if _, err := export(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped %v", err, sentinel)
	}
}

// TestConfigExporterRuntimeQueryErrorSurfaces asserts the runtime_definitions
// read error is wrapped and returned (the tenants read succeeds first).
//
// spec: §25.11 (config export, line 3988).
func TestConfigExporterRuntimeQueryErrorSurfaces(t *testing.T) {
	sentinel := errors.New("runtime read failed")
	db := &fakeQuerier{runtimeErr: sentinel}
	k8s := k8sfake.NewSimpleClientset()
	export := runner.NewConfigExporter(db, k8s, "lenny-system")
	if _, err := export(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped %v", err, sentinel)
	}
}

// TestConfigExporterScanErrorSurfaces asserts a row-scan failure is
// wrapped and returned rather than producing a partial config.
//
// spec: §25.11 (config export, line 3988).
func TestConfigExporterScanErrorSurfaces(t *testing.T) {
	db := &fakeQuerier{tenantScanErr: true}
	k8s := k8sfake.NewSimpleClientset()
	export := runner.NewConfigExporter(db, k8s, "lenny-system")
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

// fakeQuerier is a runner.Querier that returns fixed tenant and runtime
// rows keyed by the SELECT'd table. It routes on whether the SQL mentions
// "tenants" or "runtime_definitions" so one fake serves both reads.
type fakeQuerier struct {
	tenants       [][]any
	runtimes      [][]any
	queryErr      error // fails every Query
	runtimeErr    error // fails only the runtime_definitions Query
	tenantScanErr bool  // returns a scan-error row for tenants
}

func (q *fakeQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if q.queryErr != nil {
		return nil, q.queryErr
	}
	if strings.Contains(sql, "runtime_definitions") {
		if q.runtimeErr != nil {
			return nil, q.runtimeErr
		}
		return &fakeRows{data: q.runtimes}, nil
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
