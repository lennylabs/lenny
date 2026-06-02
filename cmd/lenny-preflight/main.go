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
	"errors"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
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
	agentNamespaces := flag.String("agent-namespaces", "",
		"comma-separated §17.2 agent namespaces (agentNamespaces[].name) the §13.1 "+
			"host-sharing and credential-fsGroup audits cover in addition to the release "+
			"namespace; empty scopes the host-sharing audit to the release namespace and "+
			"skips the agent-pod credential audit")
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
	// §17.9.3 / §17.6 line 488 — the preflight-job template passes
	// --connection-pooler so the CloudPoolerSentinelDefense check can
	// see the effective pooler mode. The flag must be accepted here or
	// flag.Parse (ExitOnError) aborts the Job before any check runs. The
	// value is consumed once that check is wired; until then it is
	// accepted so the rendered Job runs.
	_ = flag.String("connection-pooler", "",
		"value of the effective postgres.connectionPooler (pgbouncer | external); §17.9.3")
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
	redisURL := flag.String("redis-url", "",
		"bring-your-own Redis URL for the §12.4 maxmemory-policy=noeviction audit. Empty skips the check.")
	redisAllowInsecure := flag.Bool("redis-allow-insecure", false,
		"opt out of the §12.4 AUTH-and-TLS invariant when dialing the BYO Redis for the maxmemory-policy audit (dev only).")
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
	// Register lenny.dev/v1 so the §5.2 line 516 node-drain-timeout
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

	report := preflight.Run(context.Background(), cl, preflight.Config{
		Namespace:       *namespace,
		AgentNamespaces: parseCommaList(*agentNamespaces),
		Environment:     *environment,
		Features: preflight.WebhookFeatureFlags{
			LLMProxy:       *llmProxy,
			DrainReadiness: *drainReadiness,
			Compliance:     *compliance,
			RegistryDigest: *registryDigest,
			CosignVerify:   *cosignVerify,
		},
		AcceptDowngrade:        parseAcceptDowngrade(*acceptDowngrade),
		SPIFFETrustDomain:      *spiffeTrustDomain,
		SATokenAudience:        *saTokenAudience,
		ComplianceProfile:      *complianceProfile,
		MinIOBucket:            *minioBucket,
		MinIOEncryptionProber:  minioGetBucketEncryption(*minioEndpoint, minioAccessKey, minioSecretKey, *minioUseSSL),
		RedisConfigProber:      redisMaxmemoryProber(*redisURL, redisPassword, *redisAllowInsecure),
		RequiredRuntimeClasses: parseRuntimeClassRequirements(*requiredRuntimeClasses),
		Playground: preflight.PlaygroundConfig{
			Enabled:               *playgroundEnabled,
			AuthMode:              *playgroundAuthMode,
			DevTenantID:           *playgroundDevTenantID,
			MultiTenant:           *playgroundMultiTenant,
			GlobalDevMode:         *playgroundGlobalDevMode,
			AcknowledgeAPIKeyMode: *playgroundAcknowledgeAPIKeyMode,
		},
	})

	for _, r := range report {
		if r.Decision.Passed {
			log.Printf("lenny-preflight: %s: ok", r.Name)
			continue
		}
		log.Printf("lenny-preflight: %s: FAIL — %s", r.Name, r.Decision.Reason)
	}
	if preflight.Failed(report) {
		log.Fatal("lenny-preflight: preflight checks failed; aborting install")
	}
	log.Print("lenny-preflight: all checks passed")
}
