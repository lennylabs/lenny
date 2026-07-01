// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/jackc/pgx/v5"
)

// lennyGroup is the CRD API group the §25.11 config/CRD export scopes to.
// The backup covers platform-owned custom resources (Runtime,
// SandboxWarmPool, and the rest of the lenny.dev group); it does not dump
// unrelated third-party CRDs installed in the same cluster.
//
// spec: §25.11 (Exports CRD manifests from the K8s API, line 3989).
const lennyGroup = "lenny.dev"

// bootstrapConfigMapName is the §17.6 Day-1 seed ConfigMap the config
// export snapshots so a restore can re-seed the platform from the same
// values. It lives in the release namespace and the lenny-backup-sa holds
// get on ConfigMaps there (§25.11 line 3982).
const bootstrapConfigMapName = "lenny-bootstrap-values"

// Querier is the read surface the §25.11 config export needs against the
// shard Postgres. The lenny-backup Job connects through the read-only
// lenny-backup role (SELECT on shard tables, no write, §25.11 line 3980),
// so the export never mutates. *pgxpool.Pool satisfies it.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ConfigMapGetter reads a ConfigMap by name from a namespace. The
// client-go typed CoreV1().ConfigMaps(ns) satisfies it. It is a
// consumer-side interface so a test injects a fake.
type ConfigMapGetter interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.ConfigMap, error)
}

// CRDLister lists CustomResourceDefinitions. The apiextensions typed
// client's ApiextensionsV1().CustomResourceDefinitions() satisfies it.
type CRDLister interface {
	List(ctx context.Context, opts metav1.ListOptions) (*apiextensionsv1.CustomResourceDefinitionList, error)
}

// CRDExporter implements the §25.11 step-3 CRD-manifest export. It lists
// the lenny.dev-group CustomResourceDefinitions through the apiextensions
// API (using the §25.11 line 3982 get/list-on-CRDs grant) and serializes
// them as a deterministic JSON array a restore can re-apply.
type CRDExporter struct {
	// Lister lists CRDs. Required.
	Lister CRDLister
}

// NewCRDExporter builds a CRDExporter from an apiextensions clientset. It
// returns the config/CRD-export func the lenny-backup binary injects into
// the ExecDumper as CRDExport.
func NewCRDExporter(cs apiextensionsclientset.Interface) func(ctx context.Context) ([]byte, error) {
	e := &CRDExporter{Lister: cs.ApiextensionsV1().CustomResourceDefinitions()}
	return e.Export
}

// Export lists the lenny.dev-group CRDs and returns their manifests as a
// JSON array sorted by name. A cluster with no lenny.dev CRDs yields an
// empty array rather than an error, so a config backup taken before the
// CRDs are installed still records an explicit (empty) CRD component.
//
// spec: §25.11 (Exports CRD manifests from the K8s API, line 3989; the
// lenny-backup-sa get/list on CRDs, line 3982).
func (e *CRDExporter) Export(ctx context.Context) ([]byte, error) {
	list, err := e.Lister.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list CustomResourceDefinitions: %w", err)
	}
	crds := make([]apiextensionsv1.CustomResourceDefinition, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].Spec.Group != lennyGroup {
			continue
		}
		crds = append(crds, list.Items[i])
	}
	sort.Slice(crds, func(i, j int) bool { return crds[i].Name < crds[j].Name })
	data, err := json.Marshal(crds)
	if err != nil {
		return nil, fmt.Errorf("marshal CRD manifests: %w", err)
	}
	return data, nil
}

// platformConfig is the §25.11 step-2 platform-configuration snapshot: the
// runtime registry and the per-tenant records (which carry the quota
// columns) read from the shard Postgres, plus the §17.6 bootstrap seed
// ConfigMap. A restore re-seeds the platform from this snapshot.
type platformConfig struct {
	Tenants         []tenantConfig    `json:"tenants"`
	Runtimes        []runtimeConfig   `json:"runtimes"`
	BootstrapValues map[string]string `json:"bootstrapValues,omitempty"`
}

// tenantConfig is one tenants-table row in the config export. The quota
// columns (concurrentSessionQuota, storageQuotaBytes, tokenQuotaPerWindow,
// quotaResetPeriod) live on the tenants row (§11.2), so exporting tenants
// exports the "quotas" the §25.11 step-2 export names.
type tenantConfig struct {
	ID                     string `json:"id"`
	DisplayName            string `json:"displayName"`
	ComplianceProfile      string `json:"complianceProfile"`
	DataResidencyRegion    string `json:"dataResidencyRegion"`
	WorkspaceTier          string `json:"workspaceTier"`
	ConcurrentSessionQuota int64  `json:"concurrentSessionQuota"`
	StorageQuotaBytes      int64  `json:"storageQuotaBytes"`
	TokenQuotaPerWindow    int64  `json:"tokenQuotaPerWindow"`
	QuotaResetPeriod       string `json:"quotaResetPeriod"`
}

// runtimeConfig is one runtime_definitions row in the config export.
type runtimeConfig struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	Image            string `json:"image"`
	ExecutionMode    string `json:"executionMode"`
	IsolationProfile string `json:"isolationProfile"`
	IntegrationLevel string `json:"integrationLevel"`
	Description      string `json:"description"`
}

// ConfigExporter implements the §25.11 step-2 platform-configuration
// export. It reads only from sources the backup Job can reach: the shard
// Postgres via the read-only lenny-backup role for tenants and quotas, and
// the K8s API for the runtime/pool CRDs (through CRDExporter) and the
// bootstrap ConfigMap. The Job has no egress to the gateway (its
// NetworkPolicy permits egress to Postgres, MinIO, and the K8s API only,
// §25.11 line 3984), so the gateway admin API is not a source.
type ConfigExporter struct {
	// DB is the read-only shard Postgres surface. Required.
	DB Querier
	// ConfigMaps reads the bootstrap ConfigMap from the release namespace.
	// Required.
	ConfigMaps ConfigMapGetter
}

// NewConfigExporter builds a ConfigExporter from the read-only shard
// Postgres pool, a Kubernetes clientset, and the release namespace, and
// returns the config-export func the lenny-backup binary injects into the
// ExecDumper as ConfigExport.
func NewConfigExporter(db Querier, cs kubernetes.Interface, namespace string) func(ctx context.Context) ([]byte, error) {
	e := &ConfigExporter{DB: db, ConfigMaps: cs.CoreV1().ConfigMaps(namespace)}
	return e.Export
}

// Export reads the platform configuration and returns it as JSON.
//
// spec: §25.11 (Exports platform configuration (runtimes, pools, tenants,
// quotas) as JSON, line 3988; the lenny-backup-sa get on ConfigMaps in
// lenny-system, line 3982; egress to Postgres and the K8s API only, line
// 3984).
func (e *ConfigExporter) Export(ctx context.Context) ([]byte, error) {
	tenants, err := e.exportTenants(ctx)
	if err != nil {
		return nil, err
	}
	runtimes, err := e.exportRuntimes(ctx)
	if err != nil {
		return nil, err
	}
	bootstrap, err := e.exportBootstrapValues(ctx)
	if err != nil {
		return nil, err
	}
	cfg := platformConfig{Tenants: tenants, Runtimes: runtimes, BootstrapValues: bootstrap}
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal platform config: %w", err)
	}
	return data, nil
}

// exportTenants reads the tenants table (which carries the quota columns)
// ordered by id so the export is deterministic. Soft-deleted tenants
// (deleted_at set, §12.8) are excluded — a restore re-seeds live tenants.
func (e *ConfigExporter) exportTenants(ctx context.Context) ([]tenantConfig, error) {
	rows, err := e.DB.Query(ctx, `
		SELECT id, display_name, compliance_profile, data_residency_region,
		       workspace_tier, concurrent_session_quota, storage_quota_bytes,
		       token_quota_per_window, quota_reset_period
		  FROM tenants
		 WHERE deleted_at IS NULL
		 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query tenants: %w", err)
	}
	defer rows.Close()
	var tenants []tenantConfig
	for rows.Next() {
		var t tenantConfig
		if err := rows.Scan(&t.ID, &t.DisplayName, &t.ComplianceProfile,
			&t.DataResidencyRegion, &t.WorkspaceTier, &t.ConcurrentSessionQuota,
			&t.StorageQuotaBytes, &t.TokenQuotaPerWindow, &t.QuotaResetPeriod); err != nil {
			return nil, fmt.Errorf("scan tenant row: %w", err)
		}
		tenants = append(tenants, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tenants: %w", err)
	}
	return tenants, nil
}

// exportRuntimes reads the runtime_definitions registry ordered by name.
// Soft-deleted runtimes are excluded.
func (e *ConfigExporter) exportRuntimes(ctx context.Context) ([]runtimeConfig, error) {
	rows, err := e.DB.Query(ctx, `
		SELECT name, type, image, execution_mode, isolation_profile,
		       integration_level, description
		  FROM runtime_definitions
		 WHERE deleted_at IS NULL
		 ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query runtime_definitions: %w", err)
	}
	defer rows.Close()
	var runtimes []runtimeConfig
	for rows.Next() {
		var r runtimeConfig
		if err := rows.Scan(&r.Name, &r.Type, &r.Image, &r.ExecutionMode,
			&r.IsolationProfile, &r.IntegrationLevel, &r.Description); err != nil {
			return nil, fmt.Errorf("scan runtime row: %w", err)
		}
		runtimes = append(runtimes, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime_definitions: %w", err)
	}
	return runtimes, nil
}

// exportBootstrapValues reads the §17.6 lenny-bootstrap-values ConfigMap.
// A missing ConfigMap yields a nil map rather than an error: a deployment
// that seeded no bootstrap values (or already garbage-collected the Job's
// ConfigMap) still produces a config export.
func (e *ConfigExporter) exportBootstrapValues(ctx context.Context) (map[string]string, error) {
	cm, err := e.ConfigMaps.Get(ctx, bootstrapConfigMapName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get %s ConfigMap: %w", bootstrapConfigMapName, err)
	}
	return cm.Data, nil
}
