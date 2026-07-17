// SPDX-License-Identifier: MIT

// Command lenny-preflight runs the §17.9 install-and-upgrade preflight
// checks against the cluster. The Helm chart deploys it as a
// pre-install/pre-upgrade Job; a non-zero exit aborts the install
// before any admission-gated resource is applied.
//
// Checks (see pkg/preflight):
//
//	admission-webhook-inventory — every expected ValidatingWebhookConfiguration
//	    is present, fail-closed, and CA-injected (§17.9).
//	phase-stamp-consistency     — no admission-plane feature flag recorded
//	    enabled is being downgraded without acknowledgement (§17.2, §17.9).
//	host-sharing-flags          — no Lenny-managed pod template enables a
//	    host-sharing or process-sharing flag (§13.1).
//	networkpolicy-*             — the §13.2 NetworkPolicy selector-consistency
//	    and SSRF parity audits: canonical-selector consistency (NET-047,
//	    NET-050, NET-068), DNS podSelector parity (NET-067), ipBlock
//	    family parity (NET-062), SSRF private-range parity (NET-057),
//	    cluster-CIDR/IMDS symmetry (NET-065), and operability-plane
//	    two-selector enforcement (NET-061).
//	spiffe-trust-domain-uniqueness / sa-token-audience-uniqueness —
//	    the §13.2/§10.3 NET-064 deployment-identity collision checks.
//
// The feature-flag, acceptFeatureFlagDowngrade, and NET-064 identity
// values are passed by the Job from the chart values.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	authorizationv1 "k8s.io/api/authorization/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/observability/logging"
	"github.com/lennylabs/lenny/pkg/preflight"
	"github.com/lennylabs/lenny/pkg/redisconn"
)

// parseRuntimeClassRequirements splits the comma-separated
// --required-runtime-classes value (each item `profile=name`) into the
// §5.3 RuntimeClass requirements the chart derived from the enabled,
// externally-managed runtimeClasses.profiles entries. Malformed items
// are skipped.
//
// spec: §5.3 line 676.
func parseRuntimeClassRequirements(s string) []preflight.RuntimeClassRequirement {
	var out []preflight.RuntimeClassRequirement
	for _, pair := range strings.Split(s, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		profile, name, ok := strings.Cut(pair, "=")
		profile, name = strings.TrimSpace(profile), strings.TrimSpace(name)
		if !ok || profile == "" || name == "" {
			continue
		}
		out = append(out, preflight.RuntimeClassRequirement{Profile: profile, Name: name})
	}
	return out
}

// minioGetBucketEncryption builds a MinIOEncryptionProber that calls
// the §12.5 MinIO server-side encryption configuration API. Empty
// algorithm with nil error means SSE is unconfigured; a non-nil error
// means the SDK could not read the configuration (regulated profiles
// fail closed; others surface as advisory).
//
// spec: §12.5 line 297.
func minioGetBucketEncryption(endpoint, accessKey, secretKey string, useSSL bool) preflight.MinIOEncryptionProber {
	if endpoint == "" {
		return nil
	}
	return preflight.MinIOEncryptionProbeFunc(func(ctx context.Context, bucket string) (string, error) {
		c, err := minio.New(endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
			Secure: useSSL,
		})
		if err != nil {
			return "", err
		}
		conf, err := c.GetBucketEncryption(ctx, bucket)
		if err != nil {
			// minio-go returns "ServerSideEncryptionConfigurationNotFoundError"
			// when no SSE is configured. Map that to (empty, nil) so
			// the preflight decision logic treats it as absent
			// rather than unreachable.
			if minio.ToErrorResponse(err).Code == "ServerSideEncryptionConfigurationNotFoundError" {
				return "", nil
			}
			return "", err
		}
		if conf == nil || len(conf.Rules) == 0 {
			return "", nil
		}
		algo := conf.Rules[0].Apply.SSEAlgorithm
		if algo == "" {
			return "", errors.New("preflight: GetBucketEncryption returned a rule with no algorithm")
		}
		return algo, nil
	})
}

// s3LifecycleProber builds a CloudObjectStorageLifecycleProber that
// reads the §17.9.4 lifecycle posture of an AWS S3 (or S3-compatible)
// bucket via the AWS SDK: GetBucketVersioning for the versioning flag
// and GetBucketLifecycleConfiguration for the noncurrent-version /
// expired-object-delete-marker rules. Credentials resolve through the
// default AWS chain (IRSA / instance profile / env). A
// `NoSuchLifecycleConfiguration` API error maps to "no rules" rather
// than "unreachable" so the check reports the missing-rule failure
// instead of a transport failure.
//
// GCS and Azure are not wired in v1; the chart passes a nil prober for
// those providers, which the check routes through its advisory path.
//
// spec: §17.9.4; §17.6 line 494. F-17.9.3.
func s3LifecycleProber(region string) preflight.CloudObjectStorageLifecycleProber {
	return preflight.CloudObjectStorageLifecycleProbeFunc(func(ctx context.Context, bucket string) (preflight.CloudObjectStorageLifecycleStatus, error) {
		var status preflight.CloudObjectStorageLifecycleStatus
		opts := []func(*awsconfig.LoadOptions) error{}
		if region != "" {
			opts = append(opts, awsconfig.WithRegion(region))
		}
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return status, err
		}
		c := s3.NewFromConfig(awsCfg)
		ver, err := c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: &bucket})
		if err != nil {
			return status, err
		}
		status.VersioningEnabled = ver.Status == s3types.BucketVersioningStatusEnabled

		lc, err := c.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: &bucket})
		if err != nil {
			// S3 returns NoSuchLifecycleConfiguration when no rules are
			// configured; that is an absent-rule signal, not a transport
			// failure, so report the empty status with a nil error.
			var coder interface{ ErrorCode() string }
			if errors.As(err, &coder) && coder.ErrorCode() == "NoSuchLifecycleConfiguration" {
				return status, nil
			}
			return status, err
		}
		for i := range lc.Rules {
			rule := lc.Rules[i]
			if rule.Status != s3types.ExpirationStatusEnabled {
				continue
			}
			if rule.NoncurrentVersionExpiration != nil && rule.NoncurrentVersionExpiration.NoncurrentDays != nil {
				days := int(*rule.NoncurrentVersionExpiration.NoncurrentDays)
				if days > 0 && (status.NoncurrentVersionExpirationDays == 0 || days < status.NoncurrentVersionExpirationDays) {
					status.NoncurrentVersionExpirationDays = days
				}
			}
			if rule.Expiration != nil && rule.Expiration.ExpiredObjectDeleteMarker != nil && *rule.Expiration.ExpiredObjectDeleteMarker {
				status.DeleteMarkerExpirationEnabled = true
			}
		}
		return status, nil
	})
}

// opsSARProbeFunc adapts a function to preflight.OpsSARBACProber.
type opsSARProbeFunc func(ctx context.Context, sa string, rule preflight.OpsSARBACRule) (bool, error)

// CanI calls f.
func (f opsSARProbeFunc) CanI(ctx context.Context, sa string, rule preflight.OpsSARBACRule) (bool, error) {
	return f(ctx, sa, rule)
}

// opsSARProber builds an OpsSARBACProber that runs a SubjectAccessReview
// for the lenny-ops ServiceAccount against each canonical §25.4 rule.
// A clientset build failure returns nil so the check skips (the chart
// Role/ClusterRole templates remain the source of truth). The preflight
// SA needs `create subjectaccessreviews.authorization.k8s.io`.
//
// spec: §17.6 line 519; §25.4. F-17.6.1.
func opsSARProber(cfg *rest.Config, serviceAccount string) preflight.OpsSARBACProber {
	if strings.TrimSpace(serviceAccount) == "" {
		return nil
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil
	}
	return opsSARProbeFunc(func(ctx context.Context, sa string, rule preflight.OpsSARBACRule) (bool, error) {
		sar := &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				User: sa,
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace: rule.Namespace,
					Group:     rule.Group,
					Resource:  rule.Resource,
					Verb:      rule.Verb,
				},
			},
		}
		out, err := cs.AuthorizationV1().SubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
		if err != nil {
			return false, err
		}
		return out.Status.Allowed, nil
	})
}

// discoverServerVersion reads the API server GitVersion via the
// discovery client for the §17.6 line 503 minimum-version gate. A
// discovery failure returns an empty string, which the check treats as
// advisory (a transient discovery error must not block an install).
//
// spec: §17.6 line 503. F-17.6.1.
func discoverServerVersion(cfg *rest.Config) string {
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return ""
	}
	info, err := dc.ServerVersion()
	if err != nil || info == nil {
		return ""
	}
	return info.GitVersion
}

// cloudLifecycleProber selects the §17.9.4 lifecycle prober for the
// configured objectStorage.provider. Only S3 has a wired SDK reader in
// v1; minio is configured by the post-install Job (the check self-skips)
// and gcs/azure route through the advisory path (nil prober).
//
// spec: §17.9.4; §17.6 line 494. F-17.9.3.
func cloudLifecycleProber(provider, region string) preflight.CloudObjectStorageLifecycleProber {
	if strings.EqualFold(strings.TrimSpace(provider), "s3") {
		return s3LifecycleProber(region)
	}
	return nil
}

// certManagerVersionProber reads the cert-manager version from the
// `certificates.cert-manager.io` CRD's `app.kubernetes.io/version`
// label. Reading a cluster-scoped CRD stays within the preflight Job's
// existing RBAC (it already reads the Lenny CRDs for the schema-version
// check), so the §10.3 line 304 check needs no cross-namespace
// Deployment read in the operator-owned cert-manager namespace. A
// missing CRD reports cert-manager as not installed; a present CRD with
// no version label reports it installed but version-unknown (advisory).
//
// spec: §10.3 line 304. F-10.3.12.
func certManagerVersionProber(reader client.Reader) preflight.CertManagerProber {
	return preflight.CertManagerProbeFunc(func(ctx context.Context) (string, bool, error) {
		var crd apiextensionsv1.CustomResourceDefinition
		err := reader.Get(ctx, client.ObjectKey{Name: "certificates.cert-manager.io"}, &crd)
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return strings.TrimSpace(crd.Labels["app.kubernetes.io/version"]), true, nil
	})
}

// redisMaxmemoryProber builds a RedisConfigProber over the operator's
// bring-your-own Redis for the §12.4 maxmemory-policy=noeviction audit.
// An empty url returns nil so the check is skipped (the cloud Terraform
// sets the policy natively; only a self-managed BYO Redis needs the
// preflight probe). The client is constructed lazily on first probe so a
// dial failure surfaces as a failed check rather than aborting the Job
// before the other checks run.
//
// spec: §12.4 — billing stream and per-tenant counters must not evict.
func redisMaxmemoryProber(url, password string, allowInsecure bool) preflight.RedisConfigProber {
	if url == "" {
		return nil
	}
	return preflight.RedisConfigProbeFunc(func(ctx context.Context, param string) (string, error) {
		client, err := redisconn.NewClient(redisconn.Config{
			URL:           url,
			Password:      password,
			AllowInsecure: allowInsecure,
		})
		if err != nil {
			return "", err
		}
		defer func() { _ = client.Close() }()
		m, err := client.ConfigGet(ctx, param).Result()
		if err != nil {
			return "", err
		}
		return m[param], nil
	})
}

// poolerSentinelProber builds a PoolerSentinelProber over the operator's
// Postgres for the §17.6 line 488 cloud-managed pooler sentinel defense.
// An empty or unexpanded ($(...) placeholder, when the optional
// datastore Secret is absent at the pre-install hook) DSN returns nil so
// the check defers to the gateway's runtime LENNY_POOLER_MODE defense
// rather than blocking the install on a connection it cannot make. The
// pgx pool is constructed lazily on first probe so a dial failure
// surfaces as a failed check rather than aborting the Job before the
// other checks run.
//
// spec: §17.6 line 488; §12.3 line 56.
func poolerSentinelProber(dsn string) preflight.PoolerSentinelProber {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" || strings.Contains(dsn, "$(") {
		return nil
	}
	return preflight.PoolerSentinelProbeFunc(func(ctx context.Context) ([]string, error) {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return nil, err
		}
		defer pool.Close()
		return integrity.TenantGuardCoverageGaps(ctx, pool)
	})
}

// otlpTLSProber builds an OTLPTLSProber for the §13.2 OTLP-068 live TLS
// handshake check. It dials the collector endpoint, performs a TLS 1.2+
// handshake validating the server certificate's SAN against the endpoint
// host using the deployer's trust bundle (the system roots, augmented
// with caBundlePath when the chart mounts observability.otlpCaBundle), and
// returns the handshake error otherwise. An empty endpoint returns nil so
// the check is skipped; an empty caBundlePath uses the system trust store.
//
// spec: §13.2 lines 176-178. F-13.2.9.
func otlpTLSProber(endpoint, caBundlePath string, tlsEnabled bool) preflight.OTLPTLSProber {
	if endpoint == "" || !tlsEnabled {
		return nil
	}
	return preflight.OTLPTLSProbeFunc(func(ctx context.Context, endpoint string) error {
		hostPort, serverName, err := otlpDialTarget(endpoint)
		if err != nil {
			return err
		}
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
		if caBundlePath != "" {
			pemBytes, err := os.ReadFile(caBundlePath)
			if err != nil {
				return fmt.Errorf("read otlp CA bundle %q: %w", caBundlePath, err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pemBytes) {
				return fmt.Errorf("otlp CA bundle %q contains no PEM certificates", caBundlePath)
			}
			tlsCfg.RootCAs = pool
		}
		dialer := tls.Dialer{Config: tlsCfg}
		conn, err := dialer.DialContext(ctx, "tcp", hostPort)
		if err != nil {
			return err
		}
		return conn.Close()
	})
}

// opsAdminTLSProber builds an OpsAdminTLSProber for the §25.4 NET-070
// live TLS handshake check. It dials the gateway internal admin-API
// endpoint, completes a TLS 1.2+ handshake validating the server
// certificate against the supplied SAN host (the lenny-gateway ClusterIP
// hostname), and returns the handshake error otherwise. The trust bundle
// is the system roots, augmented with caBundlePath when the chart mounts
// ops.tls.caBundleConfigMap. An empty endpoint or disabled internal TLS
// returns nil so the check is skipped.
//
// spec: §25.4 lines 2544-2546. F-25.4.19.
func opsAdminTLSProber(endpoint, caBundlePath string, internalEnabled bool) preflight.OpsAdminTLSProber {
	if endpoint == "" || !internalEnabled {
		return nil
	}
	return preflight.OpsAdminTLSProbeFunc(func(ctx context.Context, endpoint, sanHost string) error {
		hostPort := endpoint
		if _, _, err := net.SplitHostPort(endpoint); err != nil {
			// Allow a scheme://host:port form too.
			if u, perr := url.Parse(endpoint); perr == nil && u.Host != "" {
				hostPort = u.Host
				if sanHost == "" {
					sanHost = u.Hostname()
				}
			}
		}
		serverName := sanHost
		if serverName == "" {
			if host, _, err := net.SplitHostPort(hostPort); err == nil {
				serverName = host
			}
		}
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
		if caBundlePath != "" {
			pemBytes, err := os.ReadFile(caBundlePath)
			if err != nil {
				return fmt.Errorf("read ops admin CA bundle %q: %w", caBundlePath, err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pemBytes) {
				return fmt.Errorf("ops admin CA bundle %q contains no PEM certificates", caBundlePath)
			}
			tlsCfg.RootCAs = pool
		}
		dialer := tls.Dialer{Config: tlsCfg}
		conn, err := dialer.DialContext(ctx, "tcp", hostPort)
		if err != nil {
			return err
		}
		return conn.Close()
	})
}

// prometheusReachabilityProber builds a PrometheusProber for the §25.4
// prometheus-reachability check. It issues a short-timeout HTTP GET
// against the Prometheus query API (`/api/v1/query?query=1`, served by
// every Prometheus-HTTP-API-compatible backend), treating any HTTP
// response below 500 as "reachable" — an auth-protected endpoint that
// answers 401/403 is still present. Only a transport failure or a 5xx
// counts as unreachable, matching the §25.4 "Prometheus Unreachable"
// definition. An empty URL returns nil so the check routes through its
// not-configured advisory.
//
// spec: §25.4 lines 1462-1470. F-25.4.25.
func prometheusReachabilityProber(rawURL string) preflight.PrometheusProber {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	return preflight.PrometheusProbeFunc(func(ctx context.Context, target string) error {
		probeURL := strings.TrimRight(target, "/") + "/api/v1/query?query=1"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			return fmt.Errorf("prometheus %q returned HTTP %d", target, resp.StatusCode)
		}
		return nil
	})
}

// otlpDialTarget derives the host:port to dial and the TLS ServerName
// from an OTLP endpoint, which may carry a scheme (https://host:4317),
// omit the port (defaulting to OTLP's 4317), or be a bare host:port.
func otlpDialTarget(endpoint string) (hostPort, serverName string, err error) {
	const defaultPort = "4317"
	if u, perr := url.Parse(endpoint); perr == nil && u.Host != "" {
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			port = defaultPort
		}
		return net.JoinHostPort(host, port), host, nil
	}
	host, port, serr := net.SplitHostPort(endpoint)
	if serr != nil {
		// No port in a schemeless endpoint; treat the whole value as host.
		return net.JoinHostPort(endpoint, defaultPort), endpoint, nil
	}
	return net.JoinHostPort(host, port), host, nil
}

// parseCommaList splits a comma-separated flag value into its non-empty,
// trimmed elements. It returns nil for an empty value so the §13.1
// agent-namespace audits stay disabled when no namespaces are configured.
func parseCommaList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// parseAcceptDowngrade splits the comma-separated --accept-downgrade
// value into the set of feature flags whose admission-plane downgrade
// the operator has explicitly acknowledged.
func parseAcceptDowngrade(s string) map[string]bool {
	accepted := map[string]bool{}
	for _, flag := range strings.Split(s, ",") {
		if flag = strings.TrimSpace(flag); flag != "" {
			accepted[flag] = true
		}
	}
	return accepted
}

func main() {
	// spec: §16.4 lines 370-372 — structured JSON logs; routes the stdlib
	// log package through the §16.4 handler (component=preflight). F-16.4.1.
	logging.Setup(os.Stderr, "preflight")

	namespace := flag.String("namespace", "lenny-system",
		"release namespace holding the phase-stamp ConfigMap")
	// §17.6 "Helm post-upgrade CRD validation hook" — the lenny-crd-validate
	// Job (helm.sh/hook: post-upgrade) runs the binary with this flag so
	// only the CRD schema-version check runs after helm upgrade completes,
	// surfacing a stale CRD with the recovery command even when preflight
	// was skipped (preflight.enabled: false). F-17.6.4.
	crdValidate := flag.Bool("crd-validate", false,
		"post-upgrade mode: run only the CRD schema-version check and exit non-zero "+
			"with the §17.6 recovery message on a stale CRD")
	agentNamespaces := flag.String("agent-namespaces", "",
		"comma-separated §17.2 agent namespaces (agentNamespaces[].name) the §13.1 "+
			"host-sharing and credential-fsGroup audits cover in addition to the release "+
			"namespace; empty scopes the host-sharing audit to the release namespace and "+
			"skips the agent-pod credential audit")
	// spec: §13.1 line 7 — the operator-tunable lenny-cred-readers GID the
	// agent-pod fsGroup audit checks against. 0 falls back to the podspec
	// default (65534); the chart passes security.podUIDs.credReadersGID so
	// the audit stays in lock-step with the controller/webhook. F-13.1.16.
	credReadersGID := flag.Int64("cred-readers-gid", 0,
		"§13.1 lenny-cred-readers GID the agent-pod fsGroup audit checks against; 0 uses the default 65534")
	llmProxy := flag.Bool("feature-llm-proxy", false, "value of the features.llmProxy chart flag")
	drainReadiness := flag.Bool("feature-drain-readiness", false,
		"value of the features.drainReadiness chart flag")
	compliance := flag.Bool("feature-compliance", false, "value of the features.compliance chart flag")
	registryDigest := flag.Bool("feature-registry-digest", false,
		"value of the platform.registry.requireDigest chart flag; gates the fail-closed lenny-registry-digest webhook (§17.2 line 56)")
	cosignVerify := flag.Bool("feature-cosign-verify", false,
		"value of the imageVerification.cosign.enabled chart flag; a production/staging install with it false gets a §5.3 line 669 advisory")
	environment := flag.String("environment", "",
		"value of the chart `environment` value (dev | staging | prod); feeds the §5.3 line 669 cosign-production advisory")
	monitoringFormat := flag.String("monitoring-format", "",
		"value of the monitoring.format chart value (prometheusrule | configmap | both); feeds the §16.9 R8 Prometheus Operator CRD-presence advisory")
	prometheusURL := flag.String("prometheus-url", "",
		"value of the ops.prometheus.url chart value; the §25.4 prometheus-reachability check probes it (non-blocking, tier-specific INFO/WARN). Empty means no Prometheus configured.")
	platformTier := flag.String("platform-tier", "",
		"value of the global.deploymentTier chart value (tier1 | tier2 | tier3); the §25.4 prometheus-reachability check emits INFO at Tier 1 and WARN at Tier 2/3")
	acknowledgeNoPrometheus := flag.Bool("acknowledge-no-prometheus", false,
		"value of the monitoring.acknowledgeNoPrometheus chart value; suppresses the §25.4 Tier 2/3 Prometheus WARN when the operator intentionally runs without Prometheus")
	// §17.9.3 / §17.6 line 488 — the preflight-job template passes
	// --connection-pooler so the cloud-pooler sentinel defense check can
	// see the effective pooler mode. When "external" the check connects to
	// Postgres (--postgres-dsn) and verifies the lenny_tenant_guard
	// per-transaction trigger. F-17.9.2.
	connectionPooler := flag.String("connection-pooler", "",
		"value of the effective postgres.connectionPooler (pgbouncer | external); §17.9.3")
	// §17.6 line 488 — the Postgres DSN the cloud-pooler sentinel defense
	// dials to query pg_trigger for lenny_tenant_guard. The Job wires it
	// from the lenny-datastore-conn Secret only when connectionPooler is
	// external; an empty or unexpanded value leaves the prober nil and the
	// check defers to the gateway's runtime LENNY_POOLER_MODE defense.
	postgresDSN := flag.String("postgres-dsn", "",
		"Postgres DSN for the §17.6 line 488 cloud-pooler sentinel defense (lenny_tenant_guard trigger probe). Empty skips the live probe.")
	acceptDowngrade := flag.String("accept-downgrade", "",
		"comma-separated feature flags whose admission-plane downgrade is acknowledged")
	spiffeTrustDomain := flag.String("spiffe-trust-domain", "",
		"value of the global.spiffeTrustDomain chart value (NET-064 uniqueness check)")
	saTokenAudience := flag.String("sa-token-audience", "",
		"value of the global.saTokenAudience chart value (NET-064 uniqueness check)")
	complianceProfile := flag.String("compliance-profile", "",
		"value of the complianceProfile chart value (regulated values fail closed on absent MinIO SSE; §12.5 line 297)")
	minioEndpoint := flag.String("minio-endpoint", "",
		"MinIO endpoint host:port for the §12.5 line 297 SSE audit. Empty skips the check.")
	minioBucket := flag.String("minio-bucket", "",
		"MinIO artifact bucket name for the §12.5 line 297 SSE audit. Empty skips the check.")
	minioUseSSL := flag.Bool("minio-use-ssl", true,
		"use TLS when probing MinIO for the §12.5 line 297 SSE audit.")
	objectStorageProvider := flag.String("object-storage-provider", "",
		"value of the objectStorage.provider chart value (minio | s3 | gcs | azure) for the §17.9.4 cloud lifecycle audit.")
	objectStorageBucket := flag.String("object-storage-bucket", "",
		"value of the objectStorage.bucket chart value for the §17.9.4 cloud lifecycle audit.")
	objectStorageRegion := flag.String("object-storage-region", "",
		"value of the objectStorage.region chart value (AWS region) for the §17.9.4 cloud lifecycle audit.")
	// §17.6 Checkpoint T4 default-encryption gate — the GCS V4 signed-URL
	// and Azure SAS checkpoint PUT paths sign no encryption header, so a
	// workspaceTier T4 tenant's per-tenant encryption rests on a backend
	// default the mint cannot bind per request. These flags carry the same
	// objectStorage.{gcs,azure} configuration the gateway-startup assertion
	// reads, so the two enforcement points stay in lock-step.
	objectStorageServesT4Tenant := flag.Bool("object-storage-serves-t4-tenant", false,
		"whether the deployment serves any workspaceTier T4 tenant; gates the §17.6 checkpoint T4 default-encryption check for gcs/azure backends.")
	objectStorageGCSBucketDefaultCMEK := flag.String("object-storage-gcs-bucket-default-cmek", "",
		"value of objectStorage.gcs.bucketDefaultCmek; the §17.6 checkpoint T4 default-encryption check requires it for a gcs backend serving a T4 tenant.")
	objectStorageAzureDefaultEncryptionScope := flag.String("object-storage-azure-default-encryption-scope", "",
		"value of objectStorage.azure.defaultEncryptionScope; the §17.6 checkpoint T4 default-encryption check requires it for an azure backend serving a T4 tenant.")
	objectStorageAzureDenyEncryptionScopeOverride := flag.Bool("object-storage-azure-deny-encryption-scope-override", false,
		"value of objectStorage.azure.denyEncryptionScopeOverride; the §17.6 checkpoint T4 default-encryption check requires it for an azure backend serving a T4 tenant.")
	siemEndpoint := flag.String("siem-endpoint", "",
		"value of the audit.siem.endpoint chart value for the §17.6 line 517 SIEM advisory. Empty in a production environment emits a non-blocking warning.")
	opsIngressClusterIssuer := flag.String("ops-ingress-cluster-issuer", "",
		"the cert-manager.io/cluster-issuer annotation on ops.ingress for the §17.6 line 520 advisory. Empty skips the check.")
	monitoringNamespace := flag.String("monitoring-namespace", "",
		"value of the monitoring.namespace chart value for the §17.6 line 521 advisory.")
	monitoringPodLabel := flag.String("monitoring-pod-label", "",
		"value of the monitoring.podLabel chart value (key=value) the Prometheus pod must carry for the §17.6 line 521 advisory.")
	ingressControllerNamespace := flag.String("ingress-controller-namespace", "",
		"value of the ingressControllerNamespace chart value for the §13.2 NET-038 ingress-controller advisory. Empty skips the check.")
	ingressControllerPodLabelKey := flag.String("ingress-controller-pod-label-key", "",
		"value of ingress.controllerPodLabel.key the Ingress controller pods must carry for the §13.2 NET-038 advisory.")
	ingressControllerPodLabelValue := flag.String("ingress-controller-pod-label-value", "",
		"value of ingress.controllerPodLabel.value the Ingress controller pods must carry for the §13.2 NET-038 advisory.")
	opsServiceAccount := flag.String("ops-service-account", "",
		"the fully-qualified lenny-ops SA username (system:serviceaccount:<ns>:lenny-ops-sa) for the §17.6 line 519 RBAC audit. Empty skips the check.")
	redisURL := flag.String("redis-url", "",
		"bring-your-own Redis URL for the §12.4 maxmemory-policy=noeviction audit. Empty skips the check.")
	redisAllowInsecure := flag.Bool("redis-allow-insecure", false,
		"opt out of the §12.4 AUTH-and-TLS invariant when dialing the BYO Redis for the maxmemory-policy audit (dev only).")
	kubeAPIServerCIDR := flag.String("kube-apiserver-cidr", "",
		"value of the kubeApiServerCIDR chart value; the §13.2 NET-040 check validates the kubernetes.default Service ClusterIP falls within it. Empty skips the check.")
	excludeClusterServiceCIDR := flag.String("exclude-cluster-service-cidr", "",
		"value of egressCIDRs.excludeClusterServiceCIDR; §13.2 NET-022 (the apiserver ClusterIP must fall within it).")
	excludeClusterPodCIDR := flag.String("exclude-cluster-pod-cidr", "",
		"value of egressCIDRs.excludeClusterPodCIDR; §13.2 NET-022 (node pod CIDRs must fall within it).")
	otlpEndpoint := flag.String("otlp-endpoint", "",
		"value of observability.otlpEndpoint; the §13.2 OTLP-068 otlp-tls check rejects an http:// scheme and probes the collector TLS handshake when otlpTlsEnabled. Empty skips the check.")
	otlpTLSEnabled := flag.Bool("otlp-tls-enabled", true,
		"value of observability.otlpTlsEnabled; gates the §13.2 OTLP-068 otlp-tls scheme guard and live handshake probe.")
	otlpCaBundle := flag.String("otlp-ca-bundle", "",
		"path to the deployer OTLP collector CA bundle (observability.otlpCaBundle); augments the system trust store for the §13.2 OTLP-068 handshake probe.")
	opsAdminTLSEndpoint := flag.String("ops-admin-tls-endpoint", "",
		"host:port of the gateway internal admin API lenny-ops reaches over TLS; the §25.4 NET-070 ops-admin-tls check probes its TLS handshake when ops.tls.internalEnabled. Empty skips the check.")
	opsAdminTLSInternalEnabled := flag.Bool("ops-admin-tls-internal-enabled", false,
		"value of ops.tls.internalEnabled; gates the §25.4 NET-070 ops-admin-tls handshake probe (skipped under the acknowledged plaintext path).")
	opsAdminTLSSANHost := flag.String("ops-admin-tls-san-host", "",
		"the lenny-gateway ClusterIP hostname the gateway admin-API certificate SAN must cover (§25.4 NET-070).")
	opsAdminTLSCaBundle := flag.String("ops-admin-tls-ca-bundle", "",
		"path to the trust bundle for the §25.4 NET-070 admin-API handshake probe (ops.tls.caBundleConfigMap); empty uses the system trust store.")
	requiredRuntimeClasses := flag.String("required-runtime-classes", "",
		"comma-separated profile=runtimeClassName pairs the §5.3 line 676 RuntimeClass "+
			"presence check requires; empty skips the check")
	// §27.2 playground-config preflight inputs. Disabled by default;
	// the chart renders these only when playground.enabled=true.
	playgroundEnabled := flag.Bool("playground-enabled", false,
		"value of the playground.enabled chart flag (§27.2)")
	playgroundAuthMode := flag.String("playground-auth-mode", "oidc",
		"value of the playground.authMode chart flag (oidc | apiKey | dev) (§27.2)")
	playgroundDevTenantID := flag.String("playground-dev-tenant-id", "default",
		"value of the playground.devTenantId chart flag (§27.2)")
	playgroundMultiTenant := flag.Bool("playground-multi-tenant", false,
		"value of the auth.multiTenant chart flag, propagated for the playground cross-field check (§27.2)")
	playgroundGlobalDevMode := flag.Bool("playground-global-dev-mode", false,
		"value of the global.devMode chart flag, propagated for the §27.3 authMode=dev gate (§27.2)")
	playgroundAcknowledgeAPIKeyMode := flag.Bool("playground-acknowledge-api-key-mode", false,
		"value of the playground.acknowledgeApiKeyMode chart flag (§27.2 line 42; §27.9)")
	devMode := flag.Bool("dev-mode", false,
		"value of the global.devMode chart flag; exempts the §12.9 volume-encryption check")
	attestVolumeEncryption := flag.Bool("attest-volume-encryption", false,
		"value of the preflight.attestVolumeEncryption chart flag; operator attestation that Postgres/Redis volumes are encrypted (§12.9 line 1050)")
	certManagerEnabled := flag.Bool("certmanager-enabled", true,
		"value of the certmanager.enabled chart flag; when true the §10.3 line 304 cert-manager version check requires cert-manager >= v1.12.0")
	// §17.9.2 line 1372 — the airgap answer file sets preflight.skipNetworkProbes
	// so the backend-reachability probes (MinIO SSE, BYO-Redis maxmemory,
	// OTLP TLS handshake, ops-admin internal-TLS handshake) are dropped while
	// the cluster-API checks still run. F-17.9.11.
	skipNetworkProbes := flag.Bool("skip-network-probes", false,
		"value of the preflight.skipNetworkProbes chart flag; skips the backend-reachability probes for air-gapped installs (§17.9.2 line 1372)")
	flag.Parse()

	// MinIO credentials for the §12.5 line 297 SSE preflight are read
	// from env vars so a Helm template can mount them via a secretKeyRef
	// without embedding the secret value in the Job spec.
	minioAccessKey := os.Getenv("MINIO_ACCESS_KEY_ENV")
	minioSecretKey := os.Getenv("MINIO_SECRET_KEY_ENV")

	// The §12.4 Redis AUTH password is read from the environment so the
	// Job spec can mount it via a secretKeyRef without embedding the
	// secret in the command line.
	redisPassword := os.Getenv("REDIS_PASSWORD_ENV")

	// §12.8 clock-injection harness production-default gate. A
	// production install that carries a non-zero
	// LENNY_CLOCK_OFFSET_SECONDS in its environment is a
	// misconfiguration: the variable is TEST-ONLY and must be unset
	// outside chaos runs. Fail the install loudly here so the offset
	// cannot reach a live gateway.
	if err := clockinject.AssertProductionDefault(); err != nil {
		log.Fatalf("lenny-preflight: %v", err)
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("lenny-preflight: resolve cluster config: %v", err)
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	// Register lenny.dev/v1alpha1 so the §5.2 line 516 node-drain-timeout
	// warning can list SandboxTemplate pools.
	utilruntime.Must(lennyv1.AddToScheme(scheme))
	// spec: §10 line 443 — the crd-schema-version check fetches the
	// installed CRDs by name; register apiextensions.k8s.io/v1 so the
	// reader can deserialize them. F-15.5.12.
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("lenny-preflight: build cluster client: %v", err)
	}

	// §17.6 line 503 — read the API server version via the discovery
	// client for the minimum-version gate. A discovery error degrades to
	// an empty version, which the check treats as advisory rather than
	// blocking the install. F-17.6.1.
	kubernetesVersion := discoverServerVersion(cfg)

	// §17.6 post-upgrade CRD validation hook (F-17.6.4). In this mode the
	// binary runs only the CRD schema-version check and exits, so the
	// lenny-crd-validate Job does not re-run the full admission-plane
	// inventory after the chart has already rolled.
	if *crdValidate {
		decision := preflight.CRDSchemaVersionCheck{
			Expected:    preflight.CurrentCRDSchemaVersion,
			PostUpgrade: true,
			Namespace:   *namespace,
		}.Decide(context.Background(), cl)
		if !decision.Passed {
			log.Fatalf("lenny-crd-validate: %s", decision.Reason)
		}
		log.Printf("lenny-crd-validate: %s", decision.Reason)
		return
	}

	report := preflight.Run(context.Background(), cl, preflight.Config{
		Namespace:        *namespace,
		AgentNamespaces:  parseCommaList(*agentNamespaces),
		CredReadersGID:   *credReadersGID,
		Environment:      *environment,
		MonitoringFormat: *monitoringFormat,
		Features: preflight.WebhookFeatureFlags{
			LLMProxy:       *llmProxy,
			DrainReadiness: *drainReadiness,
			Compliance:     *compliance,
			RegistryDigest: *registryDigest,
			CosignVerify:   *cosignVerify,
		},
		AcceptDowngrade:       parseAcceptDowngrade(*acceptDowngrade),
		SPIFFETrustDomain:     *spiffeTrustDomain,
		SATokenAudience:       *saTokenAudience,
		ComplianceProfile:     *complianceProfile,
		MinIOBucket:           *minioBucket,
		MinIOEncryptionProber: minioGetBucketEncryption(*minioEndpoint, minioAccessKey, minioSecretKey, *minioUseSSL),
		ObjectStorage: preflight.ObjectStorageConfig{
			Provider: *objectStorageProvider,
			Bucket:   *objectStorageBucket,
		},
		ObjectStorageT4: preflight.ObjectStorageT4Config{
			ServesT4Tenant:                   *objectStorageServesT4Tenant,
			GCSBucketDefaultCMEK:             *objectStorageGCSBucketDefaultCMEK,
			AzureDefaultEncryptionScope:      *objectStorageAzureDefaultEncryptionScope,
			AzureDenyEncryptionScopeOverride: *objectStorageAzureDenyEncryptionScopeOverride,
		},
		CloudObjectStorageLifecycleProber: cloudLifecycleProber(*objectStorageProvider, *objectStorageRegion),
		KubernetesVersion:                 kubernetesVersion,
		SIEMEndpoint:                      *siemEndpoint,
		OpsIngressClusterIssuer:           *opsIngressClusterIssuer,
		Monitoring: preflight.MonitoringConfig{
			Namespace: *monitoringNamespace,
			PodLabel:  *monitoringPodLabel,
		},
		IngressController: preflight.IngressControllerConfig{
			Namespace:     *ingressControllerNamespace,
			PodLabelKey:   *ingressControllerPodLabelKey,
			PodLabelValue: *ingressControllerPodLabelValue,
		},
		OpsServiceAccount:      *opsServiceAccount,
		OpsSARBACProber:        opsSARProber(cfg, *opsServiceAccount),
		RedisConfigProber:      redisMaxmemoryProber(*redisURL, redisPassword, *redisAllowInsecure),
		ConnectionPooler:       *connectionPooler,
		PoolerSentinelProber:   poolerSentinelProber(*postgresDSN),
		RequiredRuntimeClasses: parseRuntimeClassRequirements(*requiredRuntimeClasses),
		ClusterCIDR: preflight.ClusterCIDRConfig{
			KubeAPIServerCIDR:         *kubeAPIServerCIDR,
			ExcludeClusterServiceCIDR: *excludeClusterServiceCIDR,
			ExcludeClusterPodCIDR:     *excludeClusterPodCIDR,
		},
		OTLP: preflight.OTLPTLSConfig{
			Endpoint:   *otlpEndpoint,
			TLSEnabled: *otlpTLSEnabled,
		},
		OTLPTLSProber: otlpTLSProber(*otlpEndpoint, *otlpCaBundle, *otlpTLSEnabled),
		OpsAdminTLS: preflight.OpsAdminTLSConfig{
			Endpoint:        *opsAdminTLSEndpoint,
			InternalEnabled: *opsAdminTLSInternalEnabled,
			ExpectedSANHost: *opsAdminTLSSANHost,
		},
		OpsAdminTLSProber:      opsAdminTLSProber(*opsAdminTLSEndpoint, *opsAdminTLSCaBundle, *opsAdminTLSInternalEnabled),
		DevMode:                *devMode,
		AttestVolumeEncryption: *attestVolumeEncryption,
		Playground: preflight.PlaygroundConfig{
			Enabled:               *playgroundEnabled,
			AuthMode:              *playgroundAuthMode,
			DevTenantID:           *playgroundDevTenantID,
			MultiTenant:           *playgroundMultiTenant,
			GlobalDevMode:         *playgroundGlobalDevMode,
			AcknowledgeAPIKeyMode: *playgroundAcknowledgeAPIKeyMode,
		},
		CertManagerEnabled: *certManagerEnabled,
		CertManagerProber:  certManagerVersionProber(cl),
		Prometheus: preflight.PrometheusConfig{
			URL:                     *prometheusURL,
			Tier:                    *platformTier,
			AcknowledgeNoPrometheus: *acknowledgeNoPrometheus,
		},
		PrometheusProber:  prometheusReachabilityProber(*prometheusURL),
		SkipNetworkProbes: *skipNetworkProbes,
	})

	for _, r := range report {
		if r.Decision.Passed {
			// A passed check may still carry a non-blocking advisory
			// (INFO/WARN) in its Reason — the §5.3 cosign posture, the
			// §25.4 prometheus-reachability advisory, etc. Surface it so
			// the operator sees the advisory instead of a bare "ok".
			if r.Decision.Reason != "" {
				log.Printf("lenny-preflight: %s: %s", r.Name, r.Decision.Reason)
			} else {
				log.Printf("lenny-preflight: %s: ok", r.Name)
			}
			continue
		}
		log.Printf("lenny-preflight: %s: FAIL — %s", r.Name, r.Decision.Reason)
	}
	if preflight.Failed(report) {
		log.Fatal("lenny-preflight: preflight checks failed; aborting install")
	}
	log.Print("lenny-preflight: all checks passed")
}
