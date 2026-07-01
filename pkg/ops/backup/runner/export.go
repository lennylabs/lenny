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
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/jackc/pgx/v5"

	lennyv1alpha1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
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
// shard Postgres for the tenants table (which carries the quota columns). The
// lenny-backup Job connects through the read-only lenny-backup role (SELECT on
// shard tables, no write, §25.11 line 3980), so the export never mutates.
// *pgxpool.Pool satisfies it. Runtimes and pools are read from the K8s API
// (CRDReader), not Postgres.
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
// Runtime and SandboxWarmPool custom resources read from the K8s API, the
// per-tenant records (which carry the quota columns) read from the shard
// Postgres, plus the §17.6 bootstrap seed ConfigMap. A restore re-seeds the
// platform from this snapshot. The four platform-configuration categories
// §25.11 names — runtimes, pools, tenants, and quotas — all travel here:
// runtimes in Runtimes, pools in Pools (both from the lenny.dev CRDs), and
// tenants with their quota columns in Tenants (from Postgres).
type platformConfig struct {
	Tenants         []tenantConfig    `json:"tenants"`
	Runtimes        []runtimeConfig   `json:"runtimes"`
	Pools           []poolConfig      `json:"pools"`
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

// runtimeConfig is one Runtime custom resource in the config export. The
// Runtime CRD is the declarative source of a registered runtime (§5.1); the
// export snapshots the resource name and the spec fields a restore re-applies
// so the platform's registered runtimes are recoverable from the config
// archive. spec: §25.11 (Exports platform configuration (runtimes, ...), line
// 3988; Exports CRD manifests from the K8s API, line 3989).
type runtimeConfig struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	Image            string `json:"image"`
	ExecutionMode    string `json:"executionMode,omitempty"`
	IsolationProfile string `json:"isolationProfile,omitempty"`
	IntegrationLevel string `json:"integrationLevel"`
	DeploymentModel  string `json:"deploymentModel,omitempty"`
}

// poolConfig is one SandboxWarmPool custom resource in the config export.
// Pools are the §5.2 SandboxWarmPool registry: platform-global CRDs keyed by
// name, and one of the four platform-configuration categories §25.11 step-2
// names (runtimes, pools, tenants, quotas). The SandboxWarmPool CRD is the
// declarative source (§4.6.3); the export snapshots the resource name and the
// spec sizing fields so a restore re-seeds the operator-configured warm pools
// from the config archive rather than leaving them unrecoverable.
type poolConfig struct {
	Name            string `json:"name"`
	TemplateRef     string `json:"templateRef"`
	MinWarm         int32  `json:"minWarm"`
	MaxWarm         int32  `json:"maxWarm"`
	SDKWarmDisabled bool   `json:"sdkWarmDisabled,omitempty"`
}

// CRDReader lists lenny.dev custom resources by object list. The
// controller-runtime client (and its fake) satisfies it. It is a
// consumer-side interface so a test injects a fake reader with pre-set
// Runtime and SandboxWarmPool objects. spec: §25.11 (the K8s API for the
// runtime and pool CRDs, via the lenny-backup-sa get/list on CRDs, line
// 3982).
type CRDReader interface {
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

// ConfigExporter implements the §25.11 step-2 platform-configuration
// export. It reads only from sources the backup Job can reach: the K8s API
// for the Runtime and SandboxWarmPool custom resources (runtimes and pools)
// and the §17.6 bootstrap ConfigMap, and the shard Postgres via the read-only
// lenny-backup role for the tenants table with its quota columns (tenants and
// quotas). The runtime and pool registries are lenny.dev CRDs, whose custom
// resources are the declarative source (§5.1, §4.6.3), so they are read from
// the K8s API here through the lenny-backup-sa get/list-on-CRDs grant (§25.11
// line 3982), reserving Postgres for tenants and quotas. The Job has no
// egress to the gateway (its NetworkPolicy permits egress to Postgres, MinIO,
// and the K8s API only, §25.11 line 3984), so the gateway admin API is not a
// source.
type ConfigExporter struct {
	// DB is the read-only shard Postgres surface for tenants and quotas.
	// Required.
	DB Querier
	// CRDs lists the Runtime and SandboxWarmPool custom resources. Required.
	CRDs CRDReader
	// ConfigMaps reads the bootstrap ConfigMap from the release namespace.
	// Required.
	ConfigMaps ConfigMapGetter
}

// NewConfigExporter builds a ConfigExporter from the read-only shard
// Postgres pool, a lenny.dev CRD reader, a Kubernetes clientset, and the
// release namespace, and returns the config-export func the lenny-backup
// binary injects into the ExecDumper as ConfigExport.
func NewConfigExporter(db Querier, crds CRDReader, cs kubernetes.Interface, namespace string) func(ctx context.Context) ([]byte, error) {
	e := &ConfigExporter{DB: db, CRDs: crds, ConfigMaps: cs.CoreV1().ConfigMaps(namespace)}
	return e.Export
}

// lennyScheme is a runtime.Scheme carrying only the lenny.dev/v1alpha1 types
// the config export lists (Runtime, SandboxWarmPool). It is built once at
// package init so NewCRDReader does not rebuild it per call.
var lennyScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(lennyv1alpha1.AddToScheme(s))
	return s
}()

// NewCRDReader builds the CRDReader the config export lists the Runtime and
// SandboxWarmPool custom resources through, from the in-cluster REST config.
// It uses a read-only controller-runtime client scoped to the lenny.dev
// scheme; the lenny-backup-sa holds get/list on CRDs (§25.11 line 3982), and
// the Job has no write access. spec: §25.11 (the K8s API for the runtime and
// pool CRDs, line 3989).
func NewCRDReader(cfg *rest.Config) (CRDReader, error) {
	cl, err := client.New(cfg, client.Options{Scheme: lennyScheme})
	if err != nil {
		return nil, fmt.Errorf("build lenny.dev CRD client: %w", err)
	}
	return cl, nil
}

// Export reads the platform configuration and returns it as JSON. It covers
// all four §25.11 step-2 categories: runtimes (the Runtime CRDs) and pools
// (the SandboxWarmPool CRDs) from the K8s API, and tenants plus the quota
// columns on the tenants rows from Postgres.
//
// spec: §25.11 (Exports platform configuration (runtimes, pools, tenants,
// quotas) as JSON, line 3988; the runtime and pool CRDs from the K8s API via
// the lenny-backup-sa get/list on CRDs, line 3982; get on ConfigMaps in
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
	pools, err := e.exportPools(ctx)
	if err != nil {
		return nil, err
	}
	bootstrap, err := e.exportBootstrapValues(ctx)
	if err != nil {
		return nil, err
	}
	cfg := platformConfig{Tenants: tenants, Runtimes: runtimes, Pools: pools, BootstrapValues: bootstrap}
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

// exportRuntimes lists the Runtime custom resources from the K8s API and
// snapshots each one's spec fields, sorted by name so the export is
// deterministic. Runtimes are cluster-scoped lenny.dev CRDs (§5.1); reading
// them here (rather than from Postgres) uses the lenny-backup-sa
// get/list-on-CRDs grant the C4 design assigns to the runtime and pool export
// (§25.11 line 3982).
//
// spec: §25.11 (Exports platform configuration (runtimes, ...), line 3988;
// the runtime CRD from the K8s API, line 3989).
func (e *ConfigExporter) exportRuntimes(ctx context.Context) ([]runtimeConfig, error) {
	var list lennyv1alpha1.RuntimeList
	if err := e.CRDs.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("list Runtime custom resources: %w", err)
	}
	runtimes := make([]runtimeConfig, 0, len(list.Items))
	for i := range list.Items {
		rt := &list.Items[i]
		runtimes = append(runtimes, runtimeConfig{
			Name:             rt.Name,
			Type:             rt.Spec.Type,
			Image:            rt.Spec.Image,
			ExecutionMode:    rt.Spec.ExecutionMode,
			IsolationProfile: rt.Spec.IsolationProfile,
			IntegrationLevel: rt.Spec.IntegrationLevel,
			DeploymentModel:  rt.Spec.DeploymentModel,
		})
	}
	sort.Slice(runtimes, func(i, j int) bool { return runtimes[i].Name < runtimes[j].Name })
	return runtimes, nil
}

// exportPools lists the §5.2 SandboxWarmPool custom resources from the K8s
// API and snapshots each one's spec sizing fields, sorted by name. Pools are
// platform-global lenny.dev CRDs (§4.6.3); reading them from the K8s API here
// uses the same lenny-backup-sa get/list-on-CRDs grant as runtimes (§25.11
// line 3982), so a restore re-seeds only the operator-configured pool sizing.
//
// spec: §25.11 (Exports platform configuration (runtimes, pools, ...), line
// 3988; the pool CRD from the K8s API, line 3989).
func (e *ConfigExporter) exportPools(ctx context.Context) ([]poolConfig, error) {
	var list lennyv1alpha1.SandboxWarmPoolList
	if err := e.CRDs.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("list SandboxWarmPool custom resources: %w", err)
	}
	pools := make([]poolConfig, 0, len(list.Items))
	for i := range list.Items {
		p := &list.Items[i]
		pools = append(pools, poolConfig{
			Name:            p.Name,
			TemplateRef:     p.Spec.TemplateRef,
			MinWarm:         p.Spec.MinWarm,
			MaxWarm:         p.Spec.MaxWarm,
			SDKWarmDisabled: p.Spec.SDKWarmDisabled,
		})
	}
	sort.Slice(pools, func(i, j int) bool { return pools[i].Name < pools[j].Name })
	return pools, nil
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
