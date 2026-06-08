// SPDX-License-Identifier: MIT

package ctlcli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"sigs.k8s.io/yaml"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/ctl"
	"github.com/lennylabs/lenny/pkg/preflight"
	"github.com/lennylabs/lenny/pkg/preflight/infra"
)

// cmdPreflight implements `lenny-ctl preflight --config <values.yaml>`,
// the §24.2 pre-deployment validation command. It runs in two modes:
//
//   - Standalone (the default when the gateway is unreachable): it
//     resolves the Postgres / Redis / MinIO connection strings from the
//     §24.2 line 47 precedence (CLI flags, then env vars, then the
//     values file) and probes each backend directly from the operator's
//     machine. No running Lenny deployment is required, which is the
//     primary use case (CI pre-deployment validation and manual checks
//     before `helm install`). When a cluster with Lenny CRDs is
//     reachable it additionally runs the §10.5 CRD-currency check.
//   - API-backed (when the gateway is reachable): it delegates to the
//     `POST /v1/admin/preflight` Admin API, which runs the same probes
//     server-side.
//
// spec: §24.2 lines 39-47; §15.1 line 890; §10.5 line 443.
// F-24.2.1 / F-17.6.6 / F-24.2.4 / F-24.2.7 / F-10.5.4.
func cmdPreflight(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to the Helm values.yaml (Postgres/Redis/MinIO connection fallback)")
	pgDSN := fs.String("postgres-dsn", "", "Postgres DSN (overrides $LENNY_POSTGRES_DSN and the values file)")
	redisDSN := fs.String("redis-dsn", "", "Redis DSN (overrides $LENNY_REDIS_DSN and the values file)")
	minioEndpoint := fs.String("minio-endpoint", "", "MinIO endpoint (overrides $LENNY_MINIO_ENDPOINT and the values file)")
	minioAccess := fs.String("minio-access-key", "", "MinIO access key (overrides $LENNY_MINIO_ACCESS_KEY and the values file)")
	minioSecret := fs.String("minio-secret-key", "", "MinIO secret key (overrides $LENNY_MINIO_SECRET_KEY and the values file)")
	minioBucket := fs.String("minio-bucket", "", "MinIO bucket to verify (defaults to the values file's minio.bucket)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// §24.2 line 47 — resolve each connection field with precedence: CLI
	// flags, then environment variables, then the values file.
	valuesCfg, useSSL, err := loadPreflightValues(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl preflight: read --config: %v\n", err)
		return 1
	}
	flagCfg := infra.Config{
		PostgresDSN:    *pgDSN,
		RedisDSN:       *redisDSN,
		MinIOEndpoint:  *minioEndpoint,
		MinIOAccessKey: *minioAccess,
		MinIOSecretKey: *minioSecret,
		MinIOBucket:    *minioBucket,
	}
	cfg := infra.Resolve(flagCfg, preflightEnvConfig(), valuesCfg)
	cfg.MinIOUseSSL = useSSL

	// §24.2 line 39 — mode selection. When the gateway is reachable,
	// delegate to the Admin API; otherwise probe locally.
	if preflightGatewayReachable(ctx, c) {
		fmt.Fprintln(stdout, "lenny-ctl preflight: gateway reachable — running API-backed preflight")
		return runAPIPreflight(ctx, c, stdout, stderr)
	}
	fmt.Fprintln(stdout, "lenny-ctl preflight: no reachable gateway — running standalone preflight")
	return runStandalonePreflight(ctx, cfg, infra.RealProbers(), ambientCRDReader(), stdout, stderr)
}

// preflightValues mirrors the subset of the Helm values file the §24.2
// standalone preflight reads. Both the spec-named keys
// (postgres.connectionString, redis.connectionString) and the chart's
// keys (postgres.dsn, redis.url) are accepted; the spec-named key wins
// when both are present. Unknown values keys are ignored (the file is a
// full chart values document).
type preflightValues struct {
	Postgres struct {
		ConnectionString string `json:"connectionString"`
		DSN              string `json:"dsn"`
	} `json:"postgres"`
	Redis struct {
		ConnectionString string `json:"connectionString"`
		URL              string `json:"url"`
	} `json:"redis"`
	MinIO struct {
		Endpoint  string `json:"endpoint"`
		AccessKey string `json:"accessKey"`
		SecretKey string `json:"secretKey"`
		Bucket    string `json:"bucket"`
		UseSSL    *bool  `json:"useSSL"`
	} `json:"minio"`
}

// loadPreflightValues reads the --config values file into an infra.Config
// and the resolved MinIO TLS setting. An empty path returns an empty
// config and useSSL=true (MinIO defaults to HTTPS everywhere except
// §17.4 Embedded Mode). spec: §24.2 line 47.
func loadPreflightValues(path string) (infra.Config, bool, error) {
	if path == "" {
		return infra.Config{}, true, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return infra.Config{}, true, err
	}
	var v preflightValues
	if err := yaml.Unmarshal(raw, &v); err != nil {
		return infra.Config{}, true, fmt.Errorf("not a valid values file: %w", err)
	}
	cfg := infra.Config{
		PostgresDSN:    firstNonEmpty(v.Postgres.ConnectionString, v.Postgres.DSN),
		RedisDSN:       firstNonEmpty(v.Redis.ConnectionString, v.Redis.URL),
		MinIOEndpoint:  v.MinIO.Endpoint,
		MinIOAccessKey: v.MinIO.AccessKey,
		MinIOSecretKey: v.MinIO.SecretKey,
		MinIOBucket:    v.MinIO.Bucket,
	}
	useSSL := true
	if v.MinIO.UseSSL != nil {
		useSSL = *v.MinIO.UseSSL
	}
	return cfg, useSSL, nil
}

// preflightEnvConfig reads the §24.2 line 47 environment-variable layer.
func preflightEnvConfig() infra.Config {
	return infra.Config{
		PostgresDSN:    os.Getenv("LENNY_POSTGRES_DSN"),
		RedisDSN:       os.Getenv("LENNY_REDIS_DSN"),
		MinIOEndpoint:  os.Getenv("LENNY_MINIO_ENDPOINT"),
		MinIOAccessKey: os.Getenv("LENNY_MINIO_ACCESS_KEY"),
		MinIOSecretKey: os.Getenv("LENNY_MINIO_SECRET_KEY"),
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// preflightGatewayReachable probes the gateway's unauthenticated
// liveness endpoint with a short timeout so the mode-selection decision
// does not block on a hung or absent gateway. spec: §24.2 line 39.
func preflightGatewayReachable(ctx context.Context, c *ctl.Client) bool {
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return c.Do(rctx, "GET", "/healthz", nil, nil) == nil
}

// apiPreflightResponse mirrors the gateway's PreflightResponse JSON. It
// is duplicated here (rather than importing pkg/gateway/admin) so the
// CLI does not pull in the gateway server packages.
type apiPreflightResponse struct {
	Passed bool `json:"passed"`
	Checks []struct {
		Name   string `json:"name"`
		Passed bool   `json:"passed"`
		Reason string `json:"reason"`
	} `json:"checks"`
}

// runAPIPreflight delegates to POST /v1/admin/preflight and renders the
// returned report. spec: §15.1 line 890; §24.2 line 43.
func runAPIPreflight(ctx context.Context, c *ctl.Client, stdout, stderr io.Writer) int {
	var resp apiPreflightResponse
	if err := c.Do(ctx, "POST", "/v1/admin/preflight", nil, &resp); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl preflight: API-backed preflight call failed: %v\n", err)
		return 1
	}
	for _, ch := range resp.Checks {
		printPreflightCheck(stdout, ch.Name, ch.Passed, ch.Reason)
	}
	if !resp.Passed {
		fmt.Fprintln(stderr, "lenny-ctl preflight: one or more checks failed")
		return 1
	}
	fmt.Fprintln(stdout, "lenny-ctl preflight: all checks passed")
	return 0
}

// runStandalonePreflight runs the §24.2 infrastructure probes locally
// and, when a cluster with Lenny CRDs is reachable, the §10.5
// CRD-currency check. It is split from cmdPreflight so a test can drive
// it with fake probers and a fake cluster reader.
//
// spec: §24.2 lines 39-47; §10.5 line 443.
func runStandalonePreflight(ctx context.Context, cfg infra.Config, probers infra.Probers, crdReader client.Reader, stdout, stderr io.Writer) int {
	failed := false
	if !cfg.Configured() {
		fmt.Fprintln(stdout, "lenny-ctl preflight: no Postgres/Redis/MinIO connection configured; skipping infrastructure probes")
	}
	for _, r := range infra.Run(ctx, cfg, probers) {
		printPreflightCheck(stdout, r.Name, r.Decision.Passed, r.Decision.Reason)
		if !r.Decision.Passed {
			failed = true
		}
	}

	// §10.5 line 443 CRD-currency check. It applies only to an upgrade
	// (CRDs already installed); a fresh install or a pure infrastructure
	// preflight with no reachable cluster skips it, matching §24.2's
	// "no running Lenny deployment is required".
	switch {
	case crdReader == nil:
		fmt.Fprintln(stdout, "SKIP crd-schema-version: no cluster reachable (infrastructure-only preflight)")
	case !anyLennyCRDInstalled(ctx, crdReader):
		fmt.Fprintln(stdout, "SKIP crd-schema-version: no Lenny CRDs installed (fresh install)")
	default:
		if runPreflightCRDCheck(ctx, crdReader, stdout, stderr) != 0 {
			failed = true
		}
	}

	if failed {
		fmt.Fprintln(stderr, "lenny-ctl preflight: one or more checks failed")
		return 1
	}
	fmt.Fprintln(stdout, "lenny-ctl preflight: all checks passed")
	return 0
}

// printPreflightCheck renders one check line in a stable PASS/FAIL form
// the CLI and runbooks read.
func printPreflightCheck(w io.Writer, name string, passed bool, reason string) {
	status := "PASS"
	if !passed {
		status = "FAIL"
	}
	fmt.Fprintf(w, "%s %s: %s\n", status, name, reason)
}

// anyLennyCRDInstalled reports whether at least one Lenny CRD exists in
// the cluster, distinguishing an upgrade (currency matters) from a fresh
// install (the CRD-currency check is not applicable).
func anyLennyCRDInstalled(ctx context.Context, reader client.Reader) bool {
	for _, name := range preflight.LennyCRDNames {
		var crd apiextensionsv1.CustomResourceDefinition
		if err := reader.Get(ctx, client.ObjectKey{Name: name}, &crd); err == nil {
			return true
		}
	}
	return false
}

// ambientCRDReader builds a controller-runtime client from the ambient
// kubeconfig (KUBECONFIG / in-cluster). It returns nil when no cluster
// config is available, so a pure pre-deployment preflight runs without a
// cluster. spec: §24.2 line 39.
func ambientCRDReader() client.Reader {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil
	}
	return cl
}

// runPreflightCRDCheck runs the §10.5 CRD schema-version currency check
// against reader and reports the outcome. It is split out so a test can
// drive it with a fake client.
//
// spec: §10.5 line 443. F-10.5.4.
func runPreflightCRDCheck(ctx context.Context, reader client.Reader, stdout, stderr io.Writer) int {
	decision := preflight.CRDSchemaVersionCheck{
		Expected: preflight.CurrentCRDSchemaVersion,
	}.Decide(ctx, reader)
	if !decision.Passed {
		fmt.Fprintf(stderr, "lenny-ctl preflight: %s\n", decision.Reason)
		return 1
	}
	fmt.Fprintf(stdout, "lenny-ctl preflight: %s\n", decision.Reason)
	return 0
}
