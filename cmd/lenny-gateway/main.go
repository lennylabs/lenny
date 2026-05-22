// SPDX-License-Identifier: MIT

// Command lenny-gateway is the minimal Lenny gateway binary. It
// serves:
//
//   - §15.1 REST session endpoints (POST/GET/list/derive/upload/...).
//   - §15.1 admin endpoints (tenant + runtime CRUD) gated on
//     platform-admin.
//   - §15.1 GET /v1/blobs/{ref} blob dereference.
//
// The handler stack wraps every request with:
//
//   - §10.2 auth middleware — Bearer JWT or dev-mode header
//     fallback, configurable via LENNY_DEV_MODE.
//   - §11.6 circuit-breaker admission middleware.
//   - §11.5 idempotency replay cache middleware.
//
// Backed by in-memory stores. The tier-3 contract suites and the
// tier-4 integration tests drive the same binary; production swaps
// the in-memory backends for Postgres / Redis / Kubernetes wiring
// behind the same interfaces.
//
// Usage:
//
//	lenny-gateway --addr :8080
//
// The binary exits 0 on graceful SIGTERM, non-zero on bind failure.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore/failover"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore/failover/redisstream"
	billingpg "github.com/lennylabs/lenny/pkg/gateway/billingstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/cachingstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	connectorpg "github.com/lennylabs/lenny/pkg/gateway/connectorstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/correctionstore"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	credentialpoolpg "github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	credentialpg "github.com/lennylabs/lenny/pkg/gateway/credentialstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	credleasepg "github.com/lennylabs/lenny/pkg/gateway/credleasestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/credrenewal"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	customrolepg "github.com/lennylabs/lenny/pkg/gateway/customrolestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	delegationpolicypg "github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/drainreadiness"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	environmentpg "github.com/lennylabs/lenny/pkg/gateway/environmentstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	evalpg "github.com/lennylabs/lenny/pkg/gateway/evalstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	experimentpg "github.com/lennylabs/lenny/pkg/gateway/experimentstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/gitref"
	"github.com/lennylabs/lenny/pkg/gateway/health"
	"github.com/lennylabs/lenny/pkg/gateway/health/backends"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	interactionpg "github.com/lennylabs/lenny/pkg/gateway/interactionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/gateway/jwtaudit"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/leasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	memorypg "github.com/lennylabs/lenny/pkg/gateway/memorystore/pgstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	environmentmw "github.com/lennylabs/lenny/pkg/gateway/middleware/environment"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	idempgstore "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency/pgstore"
	ratelimitmw "github.com/lennylabs/lenny/pkg/gateway/middleware/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/openapi"
	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
	"github.com/lennylabs/lenny/pkg/gateway/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/playground"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	poolpg "github.com/lennylabs/lenny/pkg/gateway/poolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/pubsub"
	"github.com/lennylabs/lenny/pkg/gateway/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/ratelimit"
	ratelimitredis "github.com/lennylabs/lenny/pkg/gateway/ratelimit/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/retentiongc"
	"github.com/lennylabs/lenny/pkg/gateway/revocation"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
	storagequotaredis "github.com/lennylabs/lenny/pkg/gateway/storagequota/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	tenantaccesspg "github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	transcriptpg "github.com/lennylabs/lenny/pkg/gateway/transcriptstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/translator"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	usagepg "github.com/lennylabs/lenny/pkg/gateway/usagestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/watchdog"
	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/pkg/kms"
	mtlsdenylist "github.com/lennylabs/lenny/pkg/mtls/denylist"
	mtlsdenylistprop "github.com/lennylabs/lenny/pkg/mtls/denylist/propagator"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// Build metadata, overridable at link time via -ldflags
// "-X main.buildVersion=... -X main.buildCommit=... -X main.buildDate=...".
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildDate    = "unknown"
)

// adapterGRPCPort is the TCP port a Sandbox pod's §4.7 adapter listens
// on. §13.2 fixes the gateway↔adapter link to TCP 50051.
const adapterGRPCPort = 50051

func main() {
	addr := flag.String("addr", ":8080", "address to bind (host:port)")
	multiTenant := flag.Bool("multi-tenant", false, "enable §10.2 multi-tenant claim extraction")
	devMode := flag.Bool("dev-mode", envFlag("LENNY_DEV_MODE"),
		"enable dev-mode auth shortcuts (X-Lenny-Roles dev-header). Override via LENNY_DEV_MODE.")
	bearerTrustHMACKeyFile := flag.String("bearer-trust-hmac-key-file", os.Getenv("LENNY_BEARER_TRUST_HMAC_KEY_FILE"),
		"path to an additional HMAC-SHA256 signing key the §10.2 Bearer path trusts, on top of the Token Service signer. Unset in a production install; §17.4 Embedded Mode sets it so the gateway accepts the embedded OIDC provider's tokens. Override via LENNY_BEARER_TRUST_HMAC_KEY_FILE.")
	jwksPublish := flag.Bool("jwks-publish", envFlagDefault("LENNY_JWKS_PUBLISH", true),
		"§10.3 publish the gateway's JWT signing keys as a JWK Set at /.well-known/jwks.json. Defaults on; clients caching the document verify tokens minted under the current key plus every retained previous key during the §10.3 24h overlap window. Set to false to suppress the endpoint (the endpoint returns 404). Override via LENNY_JWKS_PUBLISH.")
	runtimeBin := flag.String("runtime-bin", "",
		"path to a Basic-level runtime binary. When set, the gateway dispatches messages to a child process speaking the §15.4.1 adapter protocol instead of the in-process echo executor.")
	postgresDSN := flag.String("postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string. When set, sessions, transcripts, tenants, and runtimes are persisted to Postgres (the migrations/ schema must already be applied). When empty, in-memory stores are used.")
	redisURL := flag.String("redis-url", os.Getenv("LENNY_REDIS_URL"),
		"Redis URL (redis://host:port/db). When set, circuit-breaker state is held in Redis so operator safety blocks survive a restart and stay consistent across replicas. When empty, an in-memory breaker store is used. Mutually exclusive with --redis-sentinel-addrs.")
	redisSentinelAddrs := flag.String("redis-sentinel-addrs", os.Getenv("LENNY_REDIS_SENTINEL_ADDRS"),
		"Comma-separated list of §12.8 Redis Sentinel host:port pairs. When set with --redis-sentinel-master, the gateway discovers the master via Sentinel and follows automatic failover. Mutually exclusive with --redis-url.")
	redisSentinelMaster := flag.String("redis-sentinel-master", os.Getenv("LENNY_REDIS_SENTINEL_MASTER"),
		"§12.8 Redis Sentinel monitored master name (e.g., lenny-master). Required when --redis-sentinel-addrs is set.")
	redisPassword := flag.String("redis-password", os.Getenv("LENNY_REDIS_PASSWORD"),
		"Redis AUTH password applied to both direct and Sentinel modes. Empty leaves authentication off.")
	redisSentinelPassword := flag.String("redis-sentinel-password", os.Getenv("LENNY_REDIS_SENTINEL_PASSWORD"),
		"AUTH password for the sentinels themselves. Optional; sentinels typically run unauthenticated.")
	coordInterval := flag.Duration("coordination-interval", 15*time.Second,
		"§10.1 session-coordination lease sweep interval. Each sweep renews this replica's lease on every non-terminal session. Only active when --redis-url is set.")
	shutdownTimeout := flag.Duration("shutdown-timeout", 5*time.Second, "graceful shutdown timeout")
	rlGlobalPerMin := flag.Int("rate-limit-global-per-min", 0,
		"§11.1 global requests-per-minute admission limit. Zero disables the global rate limit.")
	rlPerUserPerMin := flag.Int("rate-limit-per-user-per-min", 0,
		"§11.1 per-user requests-per-minute admission limit. Zero disables the per-user rate limit.")
	globalTokenQuota := flag.Int64("global-token-quota-per-window", 0,
		"§11.2 platform-wide LLM-token budget per reset-period window, enforced by the §4.8 QuotaEvaluator at the global scope. Zero disables the global token cap. Only active when --redis-url is set.")
	userTokenQuota := flag.Int64("user-token-quota-per-window", 0,
		"§11.2 per-user LLM-token budget per reset-period window, enforced by the §4.8 QuotaEvaluator at the user scope. Zero disables the per-user token cap. Only active when --redis-url is set.")
	agentNamespace := flag.String("agent-namespace", os.Getenv("LENNY_AGENT_NAMESPACE"),
		"Kubernetes namespace the §5 warm pools and Sandboxes live in. When set, the gateway places each started session on a warm pod via the §4.7 adapter instead of the in-process executor.")
	clusterQPS := flag.Float64("cluster-qps", envFloat("LENNY_CLUSTER_QPS", 100),
		"client-go QPS for the cluster client the gateway uses to list/get/patch SandboxWarmPool / SandboxTemplate / Sandbox / SandboxClaim. The session-start path issues 5+ API calls per request, so client-go's default of 5 saturates at trivial load. The spec mandates explicit QPS values for the controller (§4.6.1) but leaves the gateway's client throttle to operator tuning; the kube-apiserver's own priority+fairness is the production-bounded gate. Override via LENNY_CLUSTER_QPS.")
	clusterBurst := flag.Int("cluster-burst", envInt("LENNY_CLUSTER_BURST", 200),
		"client-go burst (token-bucket size) for the cluster client. Pairs with --cluster-qps. Override via LENNY_CLUSTER_BURST.")
	defaultIsolationProfile := flag.String("default-isolation-profile", os.Getenv("LENNY_DEFAULT_ISOLATION_PROFILE"),
		"§5.3 isolation profile applied to a session that omits isolationProfile on the create body. Defaults to the chart's compiled-in fallback (`sandboxed`); the e2e overlay sets `standard` so every k6 scenario lands on the warm pool the agent-workload defines.")
	adapterTLSCert := flag.String("adapter-tls-cert", os.Getenv("LENNY_ADAPTER_TLS_CERT"),
		"path to the gateway's client certificate for the §4.7 mTLS link to pod adapters. Empty dials adapters in plaintext (local development only).")
	adapterTLSKey := flag.String("adapter-tls-key", os.Getenv("LENNY_ADAPTER_TLS_KEY"),
		"path to the private key for --adapter-tls-cert.")
	adapterCA := flag.String("adapter-ca", os.Getenv("LENNY_ADAPTER_CA"),
		"path to the CA bundle that verifies a pod adapter's server certificate on the §4.7 mTLS link.")
	tokenServiceAddr := flag.String("token-service-grpc-addr", os.Getenv("LENNY_TOKEN_SERVICE_GRPC_ADDR"),
		"§4.3 lenny-token-service gRPC address (host:port). When set, the gateway materializes every §4.9 credential lease over mTLS against the Token Service instead of running pkg/credential.MintLease in-process, enforcing the §4.3 'gateway has no KMS decrypt rights' boundary. Empty falls back to the in-process credassign.Service for dev mode and self-contained tests.")
	tokenServiceCert := flag.String("token-service-tls-cert", os.Getenv("LENNY_TOKEN_SERVICE_TLS_CERT"),
		"path to the gateway's client certificate for the §4.3 mTLS link to lenny-token-service. Empty dials the Token Service in plaintext (dev mode only).")
	tokenServiceKey := flag.String("token-service-tls-key", os.Getenv("LENNY_TOKEN_SERVICE_TLS_KEY"),
		"path to the private key for --token-service-tls-cert.")
	tokenServiceCA := flag.String("token-service-ca", os.Getenv("LENNY_TOKEN_SERVICE_CA"),
		"path to the CA bundle that verifies the Token Service's server certificate on the §4.3 mTLS link.")
	tokenServiceTenant := flag.String("token-service-tenant", os.Getenv("LENNY_TOKEN_SERVICE_TENANT"),
		"tenant id the gateway carries on every §4.3 Token Service request. The Token Service applies §4.2 RLS against this id. Empty disables tenant binding (dev mode).")
	elicitationFloor := flag.String("elicitation-content-integrity-floor", os.Getenv("LENNY_ELICITATION_CONTENT_INTEGRITY_FLOOR"),
		"§9.2 platform-wide elicitation content-integrity floor (off | detect-only | enforce). The §15.1 admin GET endpoint reports the resolved effective mode as max(floor, tenantStored). A PUT below the floor is rejected with ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR. Empty defaults to `off` (no floor).")
	grpcAddr := flag.String("grpc-addr", os.Getenv("LENNY_GRPC_ADDR"),
		"§8.6 GatewayControl gRPC listen address (host:port, e.g. :50061). When set, the gateway serves the adapter→gateway control surface — currently the ExtendLease lease-extension RPC — on this address. Empty disables the GatewayControl listener.")
	llmProxyAddr := flag.String("llm-proxy-addr", os.Getenv("LENNY_LLM_PROXY_ADDR"),
		"§4.9 LLM reverse-proxy listen address (host:port, e.g. :8443). When set, the gateway serves the proxy for proxy-mode agent pods on this address. Empty disables the LLM proxy listener.")
	anthropicVersion := flag.String("anthropic-version", os.Getenv("LENNY_ANTHROPIC_VERSION"),
		"default anthropic-version header the §4.9 LLM proxy injects when a request omits it. Empty rejects a request that omits the header.")
	minioEndpoint := flag.String("minio-endpoint", os.Getenv("LENNY_MINIO_ENDPOINT"),
		"MinIO endpoint (host:port). When set, the §4.5 artifact store is the MinIO-backed blob store; the drain-readiness endpoint runs a real §12.5 bucket probe. When empty, an in-memory blob store is used.")
	minioAccessKey := flag.String("minio-access-key", os.Getenv("LENNY_MINIO_ACCESS_KEY"),
		"MinIO access key. Required when --minio-endpoint is set.")
	minioSecretKey := flag.String("minio-secret-key", os.Getenv("LENNY_MINIO_SECRET_KEY"),
		"MinIO secret key. Required when --minio-endpoint is set.")
	minioBucket := flag.String("minio-bucket", os.Getenv("LENNY_MINIO_BUCKET"),
		"MinIO bucket for §4.5 artifacts. Required when --minio-endpoint is set.")
	minioUseSSL := flag.Bool("minio-use-ssl", envFlag("LENNY_MINIO_USE_SSL"),
		"connect to MinIO over HTTPS. Override via LENNY_MINIO_USE_SSL.")
	checkpointInterval := flag.Duration("checkpoint-interval", 5*time.Minute,
		"§4.4 periodic-checkpoint cadence. The gateway snapshots every coordinated session's workspace on this interval; active only with --agent-namespace.")
	noEnvPolicy := flag.String("no-environment-policy", os.Getenv("LENNY_NO_ENVIRONMENT_POLICY"),
		"§10.6 platform-wide noEnvironmentPolicy (deny-all or allow-all). Required outside --dev-mode.")
	connectorOAuthCallbackURL := flag.String("connector-oauth-callback-url", os.Getenv("LENNY_CONNECTOR_OAUTH_CALLBACK_URL"),
		"§9.3 absolute URL the connector OAuth provider redirects back to (the gateway's GET /v1/admin/connectors/oauth/callback). Wiring the connector OAuth 2.1 flow requires it. Override via LENNY_CONNECTOR_OAUTH_CALLBACK_URL.")
	connectorOAuthCA := flag.String("connector-oauth-ca", os.Getenv("LENNY_CONNECTOR_OAUTH_CA"),
		"path to a CA bundle that verifies the §9.3 connector OAuth provider's token-endpoint TLS certificate. Empty uses the system trust store. Set this for a provider behind a private CA. Override via LENNY_CONNECTOR_OAUTH_CA.")
	opsServiceURL := flag.String("ops-service-url", os.Getenv("LENNY_OPS_SERVICE_URL"),
		"§25.14 public URL of the lenny-ops service (the ops.ingress.host Helm value). Advertised in GET /v1/admin/platform/version so lenny-ctl auto-discovers the ops endpoint. Override via LENNY_OPS_SERVICE_URL.")
	billingDualControlThreshold := flag.Float64("billing-dual-control-threshold", envFloat("LENNY_BILLING_DUAL_CONTROL_THRESHOLD", 0),
		"§11.2.1 billing.dualControlThreshold: an operator-initiated billing correction whose absolute adjustment value exceeds this requires a second platform-admin's approval. The default of 0 makes every correction dual-control. Override via LENNY_BILLING_DUAL_CONTROL_THRESHOLD.")
	// §27.2 web-playground flags. These mirror the playground.* Helm
	// values; the gateway reads them from its own configuration so the
	// playground is gated without a separate deployment target.
	playgroundEnabled := flag.Bool("playground-enabled", envFlag("LENNY_PLAYGROUND_ENABLED"),
		"§27.2 playground.enabled: serve the web playground at /playground. When false, /playground/* returns 404. Override via LENNY_PLAYGROUND_ENABLED.")
	playgroundAuthMode := flag.String("playground-auth-mode", envOr("LENNY_PLAYGROUND_AUTH_MODE", "oidc"),
		"§27.2 playground.authMode: one of oidc, apiKey, or dev. Override via LENNY_PLAYGROUND_AUTH_MODE.")
	playgroundDevTenantID := flag.String("playground-dev-tenant-id", envOr("LENNY_PLAYGROUND_DEV_TENANT_ID", "default"),
		"§27.2 playground.devTenantId: the tenant bound to the dev HMAC JWT when playground.authMode=dev. Override via LENNY_PLAYGROUND_DEV_TENANT_ID.")
	playgroundAllowedRuntimes := flag.String("playground-allowed-runtimes", envOr("LENNY_PLAYGROUND_ALLOWED_RUNTIMES", "*"),
		"§27.2 playground.allowedRuntimes: a comma-separated glob list of runtime IDs visible in the playground runtime picker. Override via LENNY_PLAYGROUND_ALLOWED_RUNTIMES.")
	playgroundMaxSessionMinutes := flag.Int("playground-max-session-minutes", envInt("LENNY_PLAYGROUND_MAX_SESSION_MINUTES", 30),
		"§27.2 playground.maxSessionMinutes: the hard cap on playground-initiated session duration. Override via LENNY_PLAYGROUND_MAX_SESSION_MINUTES.")
	playgroundMaxIdleTimeSeconds := flag.Int("playground-max-idle-time-seconds", envInt("LENNY_PLAYGROUND_MAX_IDLE_TIME_SECONDS", 300),
		"§27.2 playground.maxIdleTimeSeconds: the hard idle-timeout override for playground-initiated sessions. Override via LENNY_PLAYGROUND_MAX_IDLE_TIME_SECONDS.")
	playgroundOIDCSessionTTL := flag.Int("playground-oidc-session-ttl-seconds", envInt("LENNY_PLAYGROUND_OIDC_SESSION_TTL_SECONDS", 3600),
		"§27.2 playground.oidcSessionTtlSeconds: the lifetime of the server-side playground session record and cookie. Override via LENNY_PLAYGROUND_OIDC_SESSION_TTL_SECONDS.")
	playgroundBearerTTL := flag.Int("playground-bearer-ttl-seconds", envInt("LENNY_PLAYGROUND_BEARER_TTL_SECONDS", 900),
		"§27.2 playground.bearerTtlSeconds: the TTL of MCP bearer tokens minted by POST /v1/playground/token (bounded 60..3600). Override via LENNY_PLAYGROUND_BEARER_TTL_SECONDS.")
	playgroundGatewayHost := flag.String("playground-gateway-host", os.Getenv("LENNY_PLAYGROUND_GATEWAY_HOST"),
		"§27.7 the public gateway host the playground UI connects to over the MCP WebSocket; interpolated into the playground connect-src CSP directive. Override via LENNY_PLAYGROUND_GATEWAY_HOST.")
	flag.Parse()

	// §12.8 clock-injection harness. Read LENNY_CLOCK_OFFSET_SECONDS
	// once at startup so the gateway and every clock-using subsystem
	// pick up a chaos-test offset. A non-integer value fails loudly
	// here rather than silently behaving as production. Production
	// installs run with the variable unset; the §17.6 preflight Job
	// asserts that separately via AssertProductionDefault.
	if err := clockinject.FromEnv(); err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}

	// §10.6: the platform-wide noEnvironmentPolicy must be set
	// explicitly. Dev mode derives allow-all for local convenience;
	// outside dev mode an unset value is a fatal misconfiguration so a
	// chart with the default stripped fails closed at startup.
	resolvedNoEnvPolicy := *noEnvPolicy
	if resolvedNoEnvPolicy == "" && *devMode {
		resolvedNoEnvPolicy = tenantstore.NoEnvPolicyAllowAll
	}
	if resolvedNoEnvPolicy == "" {
		log.Fatalf("lenny-gateway: LENNY_CONFIG_MISSING config_key=noEnvironmentPolicy scope=platform: " +
			"set --no-environment-policy or LENNY_NO_ENVIRONMENT_POLICY to deny-all or allow-all (§10.6)")
	}
	if resolvedNoEnvPolicy != tenantstore.NoEnvPolicyDenyAll && resolvedNoEnvPolicy != tenantstore.NoEnvPolicyAllowAll {
		log.Fatalf("lenny-gateway: --no-environment-policy must be deny-all or allow-all, got %q", resolvedNoEnvPolicy)
	}

	// ----- Stores -----
	// session, transcript, tenant, and runtime state is persisted to
	// Postgres when --postgres-dsn is set, and held in memory
	// otherwise. The remaining stores are in-memory pending their
	// Redis (circuit breakers, quota) or Postgres backings.
	var (
		sessions    sessionstore.Store
		tenants     tenantstore.Store
		runtimes    runtimestore.Store
		transcripts transcriptstore.Store
		users       userstore.Store
		connectors  connectorstore.Store
		billing     billingstore.Store
		pgPool      *pgxpool.Pool
	)
	if *postgresDSN != "" {
		pool, err := pgxpool.New(context.Background(), *postgresDSN)
		if err != nil {
			log.Fatalf("lenny-gateway: postgres: %v", err)
		}
		if err := verifyPostgresSchema(context.Background(), pool); err != nil {
			log.Fatalf("lenny-gateway: %v", err)
		}
		// §11.7 startup integrity check: the append-only ledgers must
		// keep their grants, triggers, and erasure guard intact.
		// Production refuses to start on a violation; other
		// environments log a warning and continue.
		if err := integrity.Verify(context.Background(), pool); err != nil {
			if os.Getenv("LENNY_ENV") == "production" {
				log.Fatalf("lenny-gateway: audit integrity check failed: %v", err)
			}
			log.Printf("lenny-gateway: WARNING: audit integrity check failed (non-production, continuing): %v", err)
		}
		pgPool = pool
		sessions = sessionpg.New(pool)
		tenants = tenantpg.New(pool)
		runtimes = runtimepg.New(pool)
		transcripts = transcriptpg.New(pool)
		users = userpg.New(pool)
		connectors = connectorpg.New(pool)
		billing = billingpg.New(pool)
		log.Printf("lenny-gateway: persisting sessions, transcripts, tenants, runtimes, users, connectors, and billing events to Postgres")
	} else {
		sessions = memstore.New()
		tenants = tenantstore.NewMemory()
		runtimes = runtimestore.NewMemory()
		transcripts = transcriptstore.NewMemory()
		users = userstore.NewMemory()
		connectors = connectorstore.NewMemory()
		billing = billingstore.NewMemory()
	}
	// §4.5 artifact store: MinIO-backed when --minio-endpoint is set,
	// otherwise an in-memory store for the minimal gateway. blobProbe
	// is the §12.5 drain-readiness liveness probe — a real MinIO
	// bucket check with MinIO, an always-ready stub for the in-memory
	// store, which is process-local and cannot degrade.
	var blobs blobstore.Store = blobstore.NewMemoryStore(nil)
	var blobProbe drainreadiness.Prober = drainreadiness.ProberFunc(func(context.Context) error { return nil })
	if *minioEndpoint != "" {
		ms, err := miniostore.New(miniostore.Config{
			Endpoint:  *minioEndpoint,
			AccessKey: *minioAccessKey,
			SecretKey: *minioSecretKey,
			Bucket:    *minioBucket,
			UseSSL:    *minioUseSSL,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: minio: %v", err)
		}
		blobs = ms
		blobProbe = ms
		log.Printf("lenny-gateway: §4.5 artifact store is MinIO at %s (bucket %q)", *minioEndpoint, *minioBucket)
	}
	var pools poolstore.Store = poolstore.NewMemory()
	if pgPool != nil {
		pools = poolpg.New(pgPool)
	}

	// Circuit-breaker state goes to Redis when --redis-url is set, so
	// an operator-opened breaker survives a restart and stays
	// consistent across replicas (§12.4). The §10.1 session-
	// coordination lease sweeper runs against the same Redis.
	replica := resolveReplicaID()
	var (
		breakers       breakerRegistry
		breakerCache   *cachingstore.Store
		redisClient    *redis.Client
		coordinator    *coordination.Sweeper
		storageCounter storagequota.Counter = storagequota.NewMemory()
		rateLimiter    ratelimit.Counter    = ratelimit.NewMemory()
	)
	if *redisURL != "" || *redisSentinelAddrs != "" {
		if *redisURL != "" && *redisSentinelAddrs != "" {
			log.Fatalf("lenny-gateway: --redis-url and --redis-sentinel-addrs are mutually exclusive")
		}
		var rcfg redisconn.Config
		switch {
		case *redisURL != "":
			rcfg = redisconn.Config{URL: *redisURL, Password: *redisPassword}
		default:
			rcfg = redisconn.Config{
				SentinelAddrs:    splitAndTrim(*redisSentinelAddrs),
				MasterName:       *redisSentinelMaster,
				Password:         *redisPassword,
				SentinelPassword: *redisSentinelPassword,
			}
		}
		client, err := redisconn.NewClient(rcfg)
		if err != nil {
			log.Fatalf("lenny-gateway: redis client: %v", err)
		}
		redisClient = client
		if err := redisconn.PingWithTimeout(redisClient, 5*time.Second); err != nil {
			log.Fatalf("lenny-gateway: redis: %v", err)
		}
		// The §11.6 breaker registry lives in Redis; the cachingstore
		// keeps a local open-breaker snapshot so the request-path check
		// never round-trips to Redis and survives a Redis outage.
		breakerCache = cachingstore.New(redisstore.New(redisClient), redisClient)
		breakers = breakerCache
		coordinator = coordination.NewSweeper(
			tenantsLister{tenants}, sessions, leasestore.New(redisClient),
			coordination.Options{ReplicaID: replica, Interval: *coordInterval},
		)
		// The §11.2 storage-quota counter lives in Redis so the quota
		// holds across replicas; its reserve is Lua-atomic.
		storageCounter = storagequotaredis.New(redisClient)
		// The §11.1 rate-limit counter is Redis-backed so requests-per-
		// minute limits hold across replicas.
		rateLimiter = ratelimitredis.New(redisClient)
		switch {
		case *redisSentinelAddrs != "":
			log.Printf("lenny-gateway: Redis via Sentinel master=%q sentinels=%d; coordination replica %s",
				*redisSentinelMaster, len(splitAndTrim(*redisSentinelAddrs)), replica)
		default:
			log.Printf("lenny-gateway: circuit-breaker state in Redis; coordination replica %s", replica)
		}
	} else {
		breakers = breakerstore.NewMemory()
	}

	// §4.9 / §10.3 / §13.3 security-cache pub/sub substrate. The
	// gateway's revocation cache and the two deny lists are per-replica
	// in-memory sets; the Bus fans a local mutation out to peer replicas
	// over Redis pub/sub so a revocation takes effect fleet-wide. With
	// no Redis the Bus stays nil, which the propagators treat as the
	// single-replica mode: every cache stays local and nothing is
	// published. The Bus is constructed only when redisClient is a real
	// client — a nil *redis.Client passed through the UniversalClient
	// interface is a non-nil typed-nil interface, so pubsub.New cannot
	// detect it; the guard here is the gateway's, matching the
	// breakerstore cachingstore wiring.
	var securityBus *pubsub.Bus
	if redisClient != nil {
		securityBus = pubsub.New(redisClient)
		log.Printf("lenny-gateway: security caches converge across replicas over Redis pub/sub")
	}

	// ----- §11.2.1 two-tier billing failover pipeline -----
	// The billing ledger is wrapped in the failover Pipeline so a
	// transient Postgres outage never drops a billing event: on a
	// primary write failure the event routes to the Tier 1 durable
	// stream, then to the bounded Tier 2 in-memory write-ahead buffer.
	// The Tier 1 stream is Redis-backed when a Postgres ledger and Redis
	// are both wired (the durable, multi-replica path); otherwise it is
	// the in-process MemStream, which gives a single-replica deployment
	// the same two-tier code path without a Redis dependency.
	var billingStream failover.StreamTier
	if pgStore, ok := billing.(*billingpg.Store); ok && redisClient != nil {
		tier, err := redisstream.New(redisstream.Options{
			Client:       redisClient,
			ConsumerName: replica,
			Inserter:     pgStore,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: billing failover stream: %v", err)
		}
		billingStream = tier
		log.Printf("lenny-gateway: §11.2.1 billing failover Tier 1 backed by the Redis stream (consumer %s)", replica)
	} else {
		billingStream = failover.NewMemStream()
	}
	billingPipeline := failover.New(failover.Options{
		Primary: billing,
		Stream:  billingStream,
		Clock:   clockinject.Now,
	})
	// The pipeline is a billingstore.Store, so it replaces the bare
	// ledger everywhere downstream — billing emission, the metering API,
	// and the billing-correction workflow all write through the failover
	// path. billingLedger keeps a handle to the un-wrapped store for the
	// erasure job's pseudonymize path, which operates on the durable
	// store directly.
	billingLedger := billing
	billing = billingPipeline

	// ----- §7.1 uploadToken KeyRing -----
	// One ephemeral signing key per process. Production deployers
	// rotate this key on the §7.1 24-hour schedule via the KMS-backed
	// implementation; the minimal gateway uses a random key per boot.
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		log.Fatalf("lenny-gateway: rand: %v", err)
	}
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "boot", Secret: seed[:]})
	uploadIssuer := uploadtoken.NewIssuer(ring, nil)
	uploadTracker := uploadtoken.NewMemoryTracker()
	uploadVerifier := uploadtoken.NewVerifier(ring, uploadTracker, nil)

	// ----- §4 KMS provider -----
	// The §4 / §12.9 envelope-encryption KEK seam. The minimal gateway
	// uses the in-process kms.Local provider seeded from crypto/rand;
	// a cloud deployment swaps in an AWS/GCP/Azure KEM provider behind
	// the same kms.Provider interface. The provider wraps the Token
	// Service signing key and the per-tenant credential-secret DEKs.
	kmsProvider, err := kms.NewLocalRandom()
	if err != nil {
		log.Fatalf("lenny-gateway: kms provider: %v", err)
	}

	// ----- §13.3 Token Service -----
	// §4 KMS-envelope-backed JWT signer: the HMAC-SHA256 signing key is
	// sealed under a KMS KEK rather than being a plaintext per-process
	// dev secret. The token-service handler mounted below serves POST
	// /v1/oauth/token (RFC 8693).
	jwtSigner, err := jwt.NewKMSSigner(context.Background(), kmsProvider, jwt.TokenServiceKEKAlias, "boot")
	if err != nil {
		log.Fatalf("lenny-gateway: kms-backed jwt signer: %v", err)
	}

	// ----- §10.3 RotatingVerifier -----
	// Wrap the Token Service signer in a §10.3 RotatingVerifier so the
	// JWKS publication endpoint, the rotation lifecycle audit event,
	// and a future operator-driven Rotate call all converge on one
	// canonical key holder. The rotating verifier starts with the
	// boot-time KMS signer as its sole current key; until a Rotate
	// lands, JWKSHandler advertises exactly that key and the bearer
	// path verifies against it. The §13.3 24h overlap window is the
	// jwt.DefaultOverlapWindow default.
	rotatingVerifier := jwt.NewRotatingVerifier(jwtSigner, jwt.DefaultOverlapWindow)

	// ----- §10.2 Bearer verifier -----
	// The Token Service signer verifies tokens it minted itself. A
	// production install runs with that single verifier. §17.4 Embedded
	// Mode additionally trusts the embedded OIDC provider's HMAC key:
	// when --bearer-trust-hmac-key-file points at a key file, the
	// gateway loads it and accepts tokens signed with it alongside
	// Token Service tokens through a jwt.MultiVerifier. The flag is
	// unset in a production install, so the production posture is
	// unchanged. The Token Service signer stays the primary verifier:
	// its rejection reason is surfaced when neither verifier accepts a
	// token. The verifier is the §10.3 RotatingVerifier; once a
	// rotation lands, a token signed by the now-previous key keeps
	// verifying through the overlap window without a code change here.
	var bearerVerifier jwt.Verifier = rotatingVerifier
	if *bearerTrustHMACKeyFile != "" {
		trusted, err := jwt.LoadHMACKeyFile(*bearerTrustHMACKeyFile)
		if err != nil {
			log.Fatalf("lenny-gateway: --bearer-trust-hmac-key-file: %v", err)
		}
		bearerVerifier = jwt.NewMultiVerifier(rotatingVerifier, trusted)
		log.Printf("lenny-gateway: trusting an additional HMAC bearer key from %s (kid %s)",
			*bearerTrustHMACKeyFile, trusted.KeyID())
	}
	// With Postgres the §13.3 write-before-issue record is durable in
	// the issued_tokens table; otherwise the Token Service keeps only
	// its in-memory jti set.
	var issuedTokens tokenservice.IssuedTokenStore
	if pgPool != nil {
		issuedTokens = issuedtokenstore.New(pgPool)
	}
	tokSvc := tokenservice.NewServer(tokenservice.Options{
		Signer: jwtSigner,
		Issuer: "https://lenny.dev.local/token",
		PerDialectCap: map[string]time.Duration{
			"lenny-gateway": 24 * time.Hour,
			"lenny-ops":     1 * time.Hour,
			"llm-proxy":     1 * time.Hour,
		},
		IssuedTokens: issuedTokens,
	})

	// ----- Session API + Executor -----
	// Default: the in-process echo executor. With --runtime-bin, the
	// gateway dispatches to a child process speaking the §15.4.1
	// adapter protocol — the `make run` developer loop.
	var exec executor.Executor = executor.NewEchoExecutor()
	if *runtimeBin != "" {
		exec = executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: *runtimeBin})
		log.Printf("lenny-gateway: dispatching sessions to runtime binary %s", *runtimeBin)
	}

	// ----- §4.9 credential-assignment service -----
	// credCache is the §4.9 upstream-credential cache. The §4.7 binder's
	// credential-assignment path populates it through the credassign
	// service below, and the §4.9 LLM reverse proxy reads it on every
	// upstream call. Both reference this one instance, so a lease the
	// binder assigns resolves on the proxy hot path.
	credCache := credcache.New()
	// llmLeases is the §4.9 credential-lease store: the credassign
	// service records each minted lease here, and the §4.9 LLM proxy
	// resolves an inbound lease token against it. Postgres-backed when
	// configured, otherwise the in-memory per-replica working set.
	var llmLeases credleasestore.LeaseStore = credleasestore.New()
	if pgPool != nil {
		llmLeases = credleasepg.New(pgPool)
	}
	// credAssign mints a session's §4.9 credential leases. It is one of
	// two implementations:
	//
	//   - The §4.3-compliant Client when --token-service-grpc-addr is
	//     set: the gateway calls lenny-token-service over mTLS and the
	//     Token Service is the only component with KMS decrypt rights;
	//     the gateway materializes nothing in-process. The Client
	//     mirrors each minted lease into llmLeases and the upstream
	//     credential into credCache so the §4.9 LLM proxy hot path is
	//     unchanged.
	//
	//   - The in-process Service when --token-service-grpc-addr is
	//     empty: dev mode and self-contained tests run without a
	//     separate Token Service process. The Service registers
	//     deployer-configured credential pools and mints leases
	//     locally.
	//
	// In both modes the §4.7 binder pushes the minted leases to a pod
	// via the adapter's AssignCredentials RPC and the §4.9 renewal
	// worker tracks them for proactive rotation.
	var (
		credAssign        credassign.Assigner
		inProcessAssign   *credassign.Service
		tokenServiceConn  *grpc.ClientConn
	)
	if *tokenServiceAddr != "" {
		conn, err := dialTokenService(*tokenServiceAddr, *tokenServiceCert, *tokenServiceKey, *tokenServiceCA)
		if err != nil {
			log.Fatalf("lenny-gateway: dial Token Service %q: %v", *tokenServiceAddr, err)
		}
		tokenServiceConn = conn
		credAssign = credassign.NewClient(credassign.ClientOptions{
			Stub:     tokensv1.NewTokenServiceClient(conn),
			Leases:   llmLeases,
			Creds:    credCache,
			TenantID: *tokenServiceTenant,
		})
		log.Printf("lenny-gateway: §4.3 credential materialization via lenny-token-service at %s", *tokenServiceAddr)
	} else {
		inProcessAssign = credassign.New(llmLeases, credCache)
		credAssign = inProcessAssign
	}
	defer func() {
		if tokenServiceConn != nil {
			_ = tokenServiceConn.Close()
		}
	}()

	// §15.1 pod placement: with --agent-namespace the gateway claims a
	// §5 warm pod for each started session and dispatches its messages
	// to the pod's §4.7 adapter. The in-process and subprocess
	// executors stay available for local development.
	var (
		podBinder     *podsession.Binder
		podRegistry   *podsession.Registry
		checkpointSvc *checkpointer.Checkpointer
	)
	if *agentNamespace != "" {
		cfg, err := ctrl.GetConfig()
		if err != nil {
			log.Fatalf("lenny-gateway: resolve cluster config for --agent-namespace: %v", err)
		}
		// The gateway's session-start path issues 5+ Kubernetes API
		// calls per request (list pools, get template, list sandboxes,
		// patch sandbox, create claim). client-go's default of QPS=5 /
		// Burst=10 saturates at trivial load; the gateway logs spam
		// "Waited Ns due to client-side throttling" and each
		// session-start picks up >1s of added latency. The spec
		// mandates explicit QPS for the controller (§4.6.1) but leaves
		// the gateway-side throttle to operator tuning, so --cluster-qps
		// / --cluster-burst (defaults 100 / 200) are configurable like
		// the controller's --create-qps / --status-qps flags. The
		// kube-apiserver's own priority+fairness shaping remains the
		// production-bounded gate; this client-side limit is the safety
		// net against runaway clients.
		cfg.QPS = float32(*clusterQPS)
		cfg.Burst = *clusterBurst
		scheme := k8sruntime.NewScheme()
		utilruntime.Must(clientgoscheme.AddToScheme(scheme))
		utilruntime.Must(lennyv1.AddToScheme(scheme))
		k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
		if err != nil {
			log.Fatalf("lenny-gateway: build cluster client: %v", err)
		}
		dialOpt, err := adapter.TLSClientOption(*adapterTLSCert, *adapterTLSKey, *adapterCA)
		if err != nil {
			log.Fatalf("lenny-gateway: adapter TLS: %v", err)
		}
		podRegistry = podsession.NewRegistry()
		podBinder = &podsession.Binder{
			Client:           k8sClient,
			Namespace:        *agentNamespace,
			AdapterPort:      adapterGRPCPort,
			AcceptedVersions: []string{adapter.ProtocolVersionV1},
			DialAdapter: func(addr string) (*adapterclient.Client, error) {
				return adapterclient.Dial(addr, dialOpt)
			},
			Blobs: blobs,
			// §4.9: the binder mints a session's credential leases and
			// pushes them to the pod via AssignCredentials before
			// StartSession. A BindRequest that names no credential pools
			// assigns nothing.
			Credentials: credAssign,
		}
		// §4.6.1 Postgres-backed fallback claim: when Postgres is
		// configured the binder reads the agent_pod_state mirror to
		// claim a pod after the Kubernetes-API claim finds none. Without
		// Postgres the Fallback field stays nil and the no-idle-pod
		// result surfaces directly.
		if pgPool != nil {
			podBinder.Fallback = agentpodstatepg.New(pgPool)
			log.Printf("lenny-gateway: §4.6.1 Postgres-backed pod-claim fallback enabled")
		}
		exec = executor.NewPodExecutor(podRegistry, podBinder)
		checkpointSvc = &checkpointer.Checkpointer{
			Sessions: sessions,
			Registry: podRegistry,
			Interval: *checkpointInterval,
			OnError: func(sessionID string, err error) {
				log.Printf("lenny-gateway: checkpoint of session %s failed: %v", sessionID, err)
			},
		}
		log.Printf("lenny-gateway: placing sessions on warm pods in namespace %q", *agentNamespace)
	}

	// §7.1 seal-and-export uses the same checkpointer; an untyped-nil
	// Sealer keeps seal-and-export disabled without --agent-namespace.
	var sessionSealer sessionserver.Sealer
	if checkpointSvc != nil {
		sessionSealer = checkpointSvc
	}

	eventBus := events.NewBus(0)
	// One §8.10 tree archive shared by the sessionserver (which archives
	// children on terminal transitions) and the platform MCP tools.
	treeArchive := treearchive.NewMemory()
	// One §9.2 interaction store shared by the sessionserver (which
	// serves the respond/dismiss endpoints) and the platform MCP tools
	// (lenny/request_elicitation), so an elicitation a tool records is
	// resolvable through the REST surface.
	var interactions interactionstore.Store = interactionstore.NewMemory()
	if pgPool != nil {
		interactions = interactionpg.New(pgPool)
	}
	var evals evalstore.Store = evalstore.NewMemory(0, nil)
	if pgPool != nil {
		evals = evalpg.New(pgPool)
	}
	var experiments experimentstore.Store = experimentstore.NewMemory()
	if pgPool != nil {
		experiments = experimentpg.New(pgPool)
	}
	var memories memorystore.Store = memorystore.NewInMemory(0, nil)
	if pgPool != nil {
		memories = memorypg.New(pgPool)
	}

	// ----- §16.1 Prometheus metrics -----
	gwMetrics, err := gatewaymetrics.New()
	if err != nil {
		log.Fatalf("lenny-gateway: metrics: %v", err)
	}

	// The §11.7 per-tenant audit hash chain. With Postgres the chain is
	// durable (auditstore); otherwise it is in-memory and lost on
	// restart. Both the admin router and the §10.7 ExperimentRouter
	// rejection reporter commit events to it.
	var (
		auditSink     admin.AuditSink
		wireAudit     func(*admin.Router) *admin.Router
		auditAppender policy.AuditAppender
	)
	if pgPool != nil {
		pgAudit := auditstore.New(pgPool)
		auditSink = admin.NewAuditLogSink(pgAudit, nil)
		wireAudit = func(rt *admin.Router) *admin.Router { return rt.WithAuditLog(pgAudit) }
		// The §11.7 `interceptor.rejected` policy-rejection rows share
		// the durable Postgres-backed per-tenant hash chain.
		auditAppender = pgAudit
	} else {
		auditChains := audit.NewChainSet()
		auditSink = admin.NewChainAuditSink(auditChains, nil)
		wireAudit = func(rt *admin.Router) *admin.Router { return rt.WithAuditChains(auditChains) }
		// In-memory chain — lost on restart, used by the minimal gateway.
		auditAppender = policy.NewChainSetAppender(auditChains, nil)
	}

	// §10.3 JWT signing-key rotation audit. Each Rotate or
	// RetireExpired call against the rotatingVerifier emits one
	// `platform.jwt_signing_key_rotated` audit row through this
	// observer; the observer shares the per-tenant chain backend
	// chosen above and writes to the platform tenant.
	rotatingVerifier.SetObserver(jwtaudit.NewObserver(auditAppender))

	// §12.8: re-surface any tenant that combines billingErasurePolicy
	// exempt with a regulated compliance profile so the retention
	// posture cannot silently persist across redeployments.
	if err := admin.EmitBillingErasureExemptRegulatedStartup(
		context.Background(), tenants, auditSink, nil,
	); err != nil {
		log.Printf("lenny-gateway: WARNING: billing-erasure-exempt startup scan: %v", err)
	}

	// environments backs the §10.6 admin environment CRUD, the
	// transparent filtering on lenny/discover_agents, and the §9.1
	// GET /v1/runtimes discovery surface.
	var environments environmentstore.Store = environmentstore.NewMemory()
	if pgPool != nil {
		environments = environmentpg.New(pgPool)
	}
	// §4 runtime tenant-access registry, shared by the admin
	// tenant-access endpoints and the §5.1 internal meta-fetch endpoint.
	var tenantAccess tenantaccessstore.Store = tenantaccessstore.NewMemory()
	if pgPool != nil {
		tenantAccess = tenantaccesspg.New(pgPool)
	}

	// §25.3 operational-event emitter, shared by the gateway subsystems
	// that emit and the admin event-buffer query endpoint.
	opsEmitter := opsevents.NewEmitter(opsevents.NewEventBuffer(0), buildVersion)

	// §4.9 credential-pool registry, shared by the admin credential-pool
	// CRUD and the §14 gitClone auth host-to-pool binding check.
	var credentialPools credentialpoolstore.Store = credentialpoolstore.NewMemory()
	if pgPool != nil {
		credentialPools = credentialpoolpg.New(pgPool)
	}

	// §10.2 tenant custom-role registry, shared by the admin custom-role
	// CRUD and the §10.2 session-endpoint authorization gate (so a
	// custom role granting manage_own_sessions / read_own_sessions is
	// honored on the session endpoints as well as the admin surface).
	var customRoles customrolestore.Store = customrolestore.NewMemory()
	if pgPool != nil {
		customRoles = customrolepg.New(pgPool)
	}

	var usage usagestore.Store = usagestore.NewMemory()
	if pgPool != nil {
		usage = usagepg.New(pgPool)
	}

	// ----- §4.8 policy interceptor chain -----
	// The PostAuth chain runs the built-in §4.8 QuotaEvaluator (priority
	// 200) on the session-creation path. QuotaEvaluator enforces the
	// §11.2 hierarchical token budget against the Redis token-usage
	// counter, so it is registered only when --redis-url is set; without
	// Redis there is no §11.2 token counter and the chain stays empty.
	policyChain := interceptor.NewChain()
	var policyAuditSink *policy.AuditSink
	if redisClient != nil {
		quotaCounter := quotastore.New(redisClient)
		tenantLimits := policy.NewTenantStoreLimits(tenants, policy.TenantStoreLimitsOptions{
			GlobalTokenQuotaPerWindow: *globalTokenQuota,
			UserTokenQuotaPerWindow:   *userTokenQuota,
		})
		quotaEval := policy.NewQuotaEvaluator(tenantLimits, quotaCounter, nil)
		if err := policyChain.Register(interceptor.PhasePostAuth, quotaEval); err != nil {
			log.Fatalf("lenny-gateway: register QuotaEvaluator: %v", err)
		}
		policyAuditSink = policy.NewAuditSink(auditAppender, nil)
		log.Printf("lenny-gateway: §4.8 QuotaEvaluator enforcing §11.2 token budgets on the PostAuth chain")
	}

	sessionSrv := sessionserver.New(sessions, sessionserver.Options{
		UploadTokenIssuer:          uploadIssuer,
		UploadTokenVerifier:        uploadVerifier,
		Blobs:                      blobs,
		Executor:                   exec,
		Transcripts:                transcripts,
		Events:                     eventBus,
		Interactions:               interactions,
		Evals:                      evals,
		Experiments:                experiments,
		Pools:                      pools,
		Runtimes:                   runtimes,
		Environments:               environments,
		TenantAccess:               tenantAccess,
		OpsEmitter:                 opsEmitter,
		RefResolver:                gitref.NewLsRemoteResolver(gitref.Options{}),
		CredentialPools:            credentialPools,
		CustomRoles:                customRoles,
		DefaultNoEnvironmentPolicy: resolvedNoEnvPolicy,
		ExperimentRejections: experimentRejectionReporter{
			audit:   auditSink,
			metrics: gwMetrics,
			emitter: opsEmitter,
		},
		Usage:           usage,
		Users:           users,
		Billing:         billing,
		Tenants:         tenants,
		StorageQuota:            storageCounter,
		PodBinder:               podBinder,
		PodRegistry:             podRegistry,
		AgentNamespace:          *agentNamespace,
		DefaultIsolationProfile: isolation.Profile(*defaultIsolationProfile),
		Sealer:                  sessionSealer,
		TreeArchive:             treeArchive,
		Interceptors:            policyChain,
		PolicyAuditSink:         policyAuditSink,
		Clock:                   clockinject.Now,
	})

	// ----- OpenAI Chat + Open Responses translators -----
	openaiHandler := translator.NewOpenAIChatHandler(sessions, exec, translator.OpenAIChatOptions{Clock: clockinject.Now})
	responsesHandler := translator.NewOpenResponsesHandler(sessions, exec, translator.OpenResponsesOptions{Clock: clockinject.Now})

	// ----- §4.9 end-user credential registry -----
	// The Postgres-backed store envelope-encrypts the §12.9 T4 secret
	// column under per-tenant KMS KEKs; the in-memory store keeps the
	// secret process-local and never persists it.
	var credentials credentialstore.Store = credentialstore.NewMemory(nil)
	if pgPool != nil {
		credentials, err = credentialpg.New(pgPool, kmsProvider)
		if err != nil {
			log.Fatalf("lenny-gateway: credential store: %v", err)
		}
	}
	credServer := credentialserver.New(credentials)

	// ----- §9.3 connector OAuth 2.1 authorization-code flow -----
	// The connector-credential store holds the access/refresh tokens a
	// completed connector OAuth flow produces, keyed by the
	// (tenant, connector, user) triple. The in-memory store keeps the
	// tokens process-local; a Postgres-backed store envelope-encrypts
	// them under the same per-tenant KMS KEKs the credential store
	// uses. The flow is wired only when --connector-oauth-callback-url
	// is set: the OAuth provider needs an absolute redirect URI.
	connectorCreds := connectorcredstore.NewMemory(nil)
	var connectorOAuth *admin.ConnectorOAuth
	if *connectorOAuthCallbackURL != "" {
		var stateSeed [32]byte
		if _, err := rand.Read(stateSeed[:]); err != nil {
			log.Fatalf("lenny-gateway: connector OAuth state signing key: %v", err)
		}
		stateSigner, err := connectoroauth.NewStateSigner(connectoroauth.SigningKey{
			KeyID: "boot", Secret: stateSeed[:],
		})
		if err != nil {
			log.Fatalf("lenny-gateway: connector OAuth state signer: %v", err)
		}
		connectorOAuth = &admin.ConnectorOAuth{
			StateSigner: stateSigner,
			StateStore:  connectoroauth.NewMemoryStateStore(),
			Credentials: connectorCreds,
			CallbackURL: *connectorOAuthCallbackURL,
		}
		// §9.3: when the provider's token endpoint is behind a private
		// CA, --connector-oauth-ca supplies the bundle that verifies it.
		if *connectorOAuthCA != "" {
			caPEM, err := os.ReadFile(*connectorOAuthCA)
			if err != nil {
				log.Fatalf("lenny-gateway: connector OAuth CA bundle: %v", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caPEM) {
				log.Fatalf("lenny-gateway: connector OAuth CA bundle %s contains no PEM certificates", *connectorOAuthCA)
			}
			connectorOAuth.HTTPClient = &http.Client{
				Timeout: 15 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
				},
			}
		}
		log.Printf("lenny-gateway: §9.3 connector OAuth 2.1 flow enabled, callback %s", *connectorOAuthCallbackURL)
	}

	// ----- MCP adapter -----
	delegationSvc := delegation.NewService(sessions, delegation.Options{Experiments: experiments, Clock: clockinject.Now})
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:                      sessions,
		Executor:                   exec,
		Delegation:                 delegationSvc,
		Runtimes:                   runtimes,
		Environments:               environments,
		Tenants:                    tenants,
		Pools:                      pools,
		Audit:                      mcpDelegationAuditor{sink: auditSink},
		DefaultNoEnvironmentPolicy: resolvedNoEnvPolicy,
		Interceptors:               policyChain,
		Events:                     eventBus,
		InputWaits:                 inputwait.NewRegistry(),
		TreeArchive:                treeArchive,
		Interactions:               interactions,
		Memory:                     memories,
		ElicitationMetrics:         gwMetrics,
		TenantID:                   "default",
		Clock:                      clockinject.Now,
	})

	// §13.3 revocation cache: the auth middleware rejects a token
	// whose jti is in this set. It is rehydrated from the Postgres
	// issued-token index below. The propagator wraps the cache with
	// Redis pub/sub fan-out so a revocation on any replica reaches every
	// replica within pub/sub latency; with no Redis the propagator is a
	// local-only pass-through. revCache stays the read primitive the
	// auth middleware and the rehydration loop use directly.
	revCache := revocation.NewCache()
	revProp := revocationprop.New(revCache, securityBus, revocationprop.WithErrorHandler(func(err error) {
		log.Printf("lenny-gateway: token-revocation pub/sub publish failed: %v", err)
	}))

	// §10.3 mTLS certificate deny list: the per-replica SPIFFE-URI deny
	// set checked on every mTLS handshake. Its propagator carries an
	// Add or Remove across replicas over Redis pub/sub. The deny list is
	// a single-replica primitive; the propagator owns the fan-out the
	// package doc defers to a wrapping controller.
	mtlsDeny := mtlsdenylist.New()
	mtlsDenyProp := mtlsdenylistprop.New(mtlsDeny, securityBus, mtlsdenylistprop.WithErrorHandler(func(err error) {
		log.Printf("lenny-gateway: mTLS deny-list pub/sub publish failed: %v", err)
	}))

	// §4.9 credential deny list: the per-replica set of revoked
	// credential identities the LLM proxy checks before every upstream
	// call. The §4.9 LLM proxy below reads it directly on the hot path;
	// the credential-lease revocation propagator built next wraps it
	// with cross-replica Redis pub/sub fan-out, so the admin router's
	// §11.4 full_revoke fan-out and the emergency-revocation path revoke
	// a credential onto every replica's deny list.
	credDeny := denylist.New()

	// ----- §4.9 Proactive Lease Renewal worker -----
	// The credrenewal.Worker tracks each active credential lease by its
	// renewBefore deadline and issues a replacement before the original
	// expires, so a long-lived session never sees its LLM credential
	// lapse. credRenewal binds the worker to the credential-assignment
	// service that mints the replacement and to the warm-pod registry it
	// pushes the rotated credential to via the §4.7 RotateCredentials
	// RPC. credRenewal is nil when no credential pools are wired; a nil
	// receiver leaves every renewal hook a no-op.
	credRenewal := newCredRenewalWiring(credAssign, podRegistry)
	// credRenewalProp carries a §4.9 credential-lease revocation across
	// replicas: a Revoke updates the local deny list, drops the renewal
	// worker's tracked leases bound to the credential, and fans out over
	// the same Redis pub/sub channel the §4.9 credential-deny-list
	// propagator uses. The §11.4 full_revoke fan-out and the emergency-
	// revocation path route through it so a revoked credential lease
	// stops reaching the provider on every replica, and no replica
	// proactively renews a credential that is no longer trustworthy.
	var credRenewalWorker *credrenewal.Worker
	credRenewalProp := credrenewalprop.New(credDeny, nil, securityBus, credrenewalprop.WithErrorHandler(func(err error) {
		log.Printf("lenny-gateway: credential-lease revocation pub/sub publish failed: %v", err)
	}))
	if credRenewal != nil {
		credRenewalWorker = credrenewal.New(credRenewal, credrenewal.Options{
			// §4.9: a proactive renewal that rotates a lease onto a fresh
			// credential pushes it to the lease's pod via RotateCredentials.
			OnRenewed: credRenewal.onRenewed,
			// §4.9: a lease whose renewal cannot proceed falls through to
			// fault rotation. The worker drops it; onExhausted clears its
			// pool binding.
			OnExhausted: credRenewal.onExhausted,
			Clock:       clockinject.Now,
		})
		// Every §4.9 credential lease the assignment service mints — at
		// session start and at fault rotation — is tracked by the renewal
		// worker so its renewBefore deadline drives a proactive renewal.
		credAssign.OnAssigned(func(a credassign.LeaseAssignment) {
			credRenewal.track(credRenewalWorker, a.PoolName, string(a.Lease.Provider), a.Lease)
		})
		// Rebuild the propagator over the live worker so a peer replica's
		// credential-lease revocation also drops this replica's tracked
		// leases for the credential, not just its deny-list entry.
		credRenewalProp = credrenewalprop.New(credDeny, credRenewalWorker, securityBus,
			credrenewalprop.WithErrorHandler(func(err error) {
				log.Printf("lenny-gateway: credential-lease revocation pub/sub publish failed: %v", err)
			}))
	}

	// ----- Admin API -----
	var delegationPolicies delegationpolicystore.Store = delegationpolicystore.NewMemory()
	if pgPool != nil {
		delegationPolicies = delegationpolicypg.New(pgPool)
	}
	adminRouter := admin.NewRouter(tenants, admin.Options{Clock: clockinject.Now, Audit: auditSink, Metrics: gwMetrics}).
		WithRuntimes(runtimes).
		WithUsers(users).
		WithPools(pools).
		WithBreakers(breakers).
		WithConnectors(connectors).
		WithConnectorOAuth(connectorOAuth).
		WithDelegationPolicies(delegationPolicies).
		WithCredentialPools(credentialPools).
		WithCustomRoles(customRoles).
		WithTenantAccess(tenantAccess).
		WithSessions(sessions).
		WithInteractions(interactions).
		WithExperiments(experiments).
		WithEnvironments(environments).
		WithEvalResults(evals).
		WithRecommendations(recommendations.NewCapacityService(
			recommendations.NewWindowStore(7 * 24 * time.Hour),
		))
	adminRouter = adminRouter.
		WithEventBuffer(opsEmitter.Buffer()).
		WithEventEmitter(opsEmitter)
	if *elicitationFloor != "" {
		adminRouter = adminRouter.WithElicitationFloor(*elicitationFloor)
	}
	adminRouter = wireAudit(adminRouter)
	// §12.8 GDPR erasure: build the DeleteByUser orchestrator over the
	// wired stores and expose it behind the admin erasure endpoints.
	// Session-scoped stores (transcripts, artifacts) are erased per
	// session before the session-keyed user-scoped stores.
	{
		sessionScoped := []erasure.SessionEraser{}
		if te, ok := transcripts.(sessionArtifactDeleter); ok {
			sessionScoped = append(sessionScoped,
				erasure.SessionEraser{Name: "transcripts", DeleteBySession: te.DeleteBySession})
		}
		if be, ok := blobs.(sessionArtifactDeleter); ok {
			sessionScoped = append(sessionScoped,
				erasure.SessionEraser{Name: "artifacts", DeleteBySession: be.DeleteBySession})
		}
		sessionScoped = append(sessionScoped,
			erasure.SessionEraser{Name: "eval_results", DeleteBySession: evals.DeleteBySession})
		erasureOrch := erasure.New(erasure.Config{
			Sessions: func(ctx context.Context, tenantID, userID string) ([]string, error) {
				rows, err := sessions.List(ctx, tenantID, sessionstore.ListFilter{})
				if err != nil {
					return nil, err
				}
				ids := make([]string, 0, len(rows))
				for _, s := range rows {
					if s.UserID == userID {
						ids = append(ids, s.ID)
					}
				}
				return ids, nil
			},
			SessionScoped: sessionScoped,
			UserScoped: []erasure.Eraser{
				{Name: "interactions", DeleteByUser: interactions.DeleteByUser},
				{Name: "memory", DeleteByUser: func(ctx context.Context, tenantID, userID string) (int, error) {
					// §9.4 MemoryStore.DeleteByUser returns only an error;
					// the orchestrator's adapter reports the count it
					// cannot supply as 0.
					return 0, memories.DeleteByUser(ctx, tenantID, userID)
				}},
				{Name: "sessions", DeleteByUser: sessions.DeleteByUser},
			},
		})
		erasureJobs := erasurejob.NewMemory()
		erasureRunner := erasurejob.NewRunner(erasureJobs, erasureOrch, nil)
		// §12.8: billing events are append-only, so the erasure job
		// pseudonymizes them rather than deleting them. The Postgres
		// billing store's pseudonymize path is deferred (it needs an
		// UPDATE under the lenny_erasure role), so this attaches the
		// BillingEraser only when the in-memory billing store is wired.
		// The pseudonymize path operates on the durable ledger directly,
		// so it asserts against billingLedger, not the failover pipeline.
		if be, ok := billingLedger.(erasurejob.BillingErasureStore); ok {
			erasureRunner = erasureRunner.WithBilling(erasurejob.NewBillingEraser(be, tenants))
		}
		adminRouter = adminRouter.WithErasure(erasureRunner, erasureJobs)
	}
	if pgPool != nil {
		// §13.3 operator-initiated token revocation, durable in the
		// issued-token index and reflected in the revocation cache. The
		// admin endpoint routes through the propagator so a revocation
		// fans out to every replica's cache over Redis pub/sub, not just
		// the replica that served the request.
		adminRouter = adminRouter.WithIssuedTokens(issuedtokenstore.New(pgPool), revProp)
	}
	// §11.4 full_revoke fan-out. Each dependency is independently
	// optional: the pod terminator is wired only with warm-pod placement
	// (--agent-namespace), the lease revoker only when the lease store
	// exposes per-session lookup, and the token revoker only with a
	// Postgres-backed issued-token index. A minimal gateway wires none
	// of them and still soft/hard disables a user.
	{
		var (
			userPods   admin.UserPodTerminator
			userLeases admin.UserLeaseRevoker
			userTokens admin.UserTokenRevoker
		)
		if podRegistry != nil {
			userPods = &podTerminateFanOut{registry: podRegistry}
		}
		// llmLeases is the §4.9 credential-lease store; both the in-memory
		// and Postgres-backed implementations expose LeasesBySession, so
		// the assertion succeeds for either backend. The credential-lease
		// revocation propagator carries a revoked lease's credential
		// across replicas — onto every replica's deny list and renewal
		// worker — so the §11.4 full_revoke fan-out stops the lease
		// reaching the provider fleet-wide and no replica renews it.
		if ls, ok := llmLeases.(userLeaseStore); ok {
			userLeases = &userLeaseRevoker{leases: ls, denyList: credRenewalProp}
		}
		if pgPool != nil {
			userTokens = &userTokenRevoker{store: issuedtokenstore.New(pgPool)}
		}
		adminRouter = adminRouter.WithUserRevocation(userPods, userLeases, userTokens)
	}
	adminRouter = adminRouter.WithPlatformInfo(
		admin.PlatformInfo{
			Version:       buildVersion,
			GitCommit:     buildCommit,
			BuildDate:     buildDate,
			OpsServiceURL: *opsServiceURL,
		},
		map[string]string{
			"gateway.addr":          *addr,
			"gateway.multiTenant":   boolStr(*multiTenant),
			"gateway.devMode":       boolStr(*devMode),
			"gateway.runtimeBin":    *runtimeBin,
			"gateway.postgres":      boolStr(*postgresDSN != ""),
			"gateway.redis":         boolStr(*redisURL != ""),
			"gateway.replicaId":     replica,
			"gateway.opsServiceURL": *opsServiceURL,
		},
	)
	// §11.2.1 operator-initiated billing-correction workflow. The
	// correction endpoints write through the failover billing pipeline
	// and hold pending dual-control requests in the in-memory
	// correction registry.
	adminRouter = adminRouter.WithBillingCorrections(
		billing, correctionstore.NewMemory(), *billingDualControlThreshold,
	)

	// ----- Compose the mux -----
	mux := http.NewServeMux()
	mux.Handle("/v1/sessions", sessionSrv.Handler())
	mux.Handle("/v1/sessions/", sessionSrv.Handler())
	mux.Handle("/v1/blobs/", sessionSrv.Handler())
	// §9.1 / §15.1 runtime-discovery and model-list surfaces. The
	// sessionserver mux serves GET /v1/runtimes, /v1/runtimes/{name}/
	// meta/{key}, /v1/models, and the §5.1 internal meta-fetch path;
	// each is identity-filtered by §10.6 environment access.
	mux.Handle("/v1/runtimes", sessionSrv.Handler())
	mux.Handle("/v1/runtimes/", sessionSrv.Handler())
	mux.Handle("/v1/models", sessionSrv.Handler())
	mux.Handle("/internal/runtimes/", sessionSrv.Handler())
	mux.Handle("/v1/admin/", adminRouter.Handler())

	// §25.3 Platform Health API. Registered at the specific
	// /v1/admin/health* paths so Go's ServeMux routes them to the
	// health handler ahead of the /v1/admin/ admin catch-all.
	healthAgg := health.NewAggregator()
	healthAgg.Register(staticHealthy("gateway"))
	healthAgg.Register(staticHealthy("sessionstore"))
	healthAgg.Register(staticHealthy("blobstore"))
	healthAgg.Register(staticHealthy("executor"))
	// When a backing service is wired, the §25.3 health API reports
	// its real reachability instead of a static verdict.
	if pgPool != nil {
		healthAgg.Register(backends.Postgres(pgPool, "postgres"))
	}
	if redisClient != nil {
		healthAgg.Register(backends.Redis(redisClient, "redis"))
	}
	if breakerCache != nil {
		healthAgg.Register(backends.CircuitBreakerCache(breakerCache, "circuit-breaker-cache"))
	}
	// §25.3: emit a health_status_changed operational event when the
	// aggregate health verdict transitions.
	healthAgg.OnTransition(func(prev, curr health.Status) {
		data, _ := json.Marshal(map[string]any{
			"oldStatus": string(prev), "newStatus": string(curr),
		})
		opsEmitter.Emit(opsevents.OperationalEvent{
			Source:          "/v1/admin/health",
			Type:            opsevents.EventHealthStatusChanged.CloudEventsType(),
			Severity:        "warning",
			DataContentType: "application/json",
			Data:            data,
		})
	})
	healthHandler := health.Handler(healthAgg)
	mux.Handle("/v1/admin/health", healthHandler)
	mux.Handle("/v1/admin/health/", healthHandler)
	mux.Handle("/openapi.yaml", openapi.Handler())
	mux.Handle("/v1/openapi.json", openapi.Handler())
	mux.Handle("/v1/oauth/", tokSvc.Handler())

	// ----- §10.3 JWK Set publication -----
	// The JWKS endpoint advertises the kid/alg of every key the
	// §10.3 RotatingVerifier holds (the current key plus every
	// retained previous key during the §10.3 24h overlap window).
	// The endpoint is unauthenticated by design; the JWK Set carries
	// only public metadata, never private key material. Suppress with
	// --jwks-publish=false.
	if *jwksPublish {
		jwksHandler := jwt.NewJWKSHandler(rotatingVerifier)
		mux.Handle("/.well-known/jwks.json", jwksHandler)
		log.Printf("lenny-gateway: §10.3 JWKS published at /.well-known/jwks.json (current kid %s)",
			rotatingVerifier.CurrentKeyID())
	}
	mux.Handle("/v1/chat/completions", openaiHandler.Handler())
	mux.Handle("/v1/responses", responsesHandler.Handler())
	mux.Handle("/v1/responses/", responsesHandler.Handler())
	mux.Handle("/mcp", mcpSrv.Handler())
	mux.Handle("/v1/credentials", credServer.Handler())
	mux.Handle("/v1/credentials/", credServer.Handler())

	// ----- §27 web playground -----
	// The playground is feature-flag gated (§27.2). When
	// --playground-enabled is set the gateway serves the embedded SPA
	// bundle at /playground and the §27.3.1 auth gatekeepers; when
	// unset the /playground/* and /v1/playground/token routes are not
	// mounted at all and return 404. The playground is a client of the
	// public MCP and REST surface for session, chat, and discovery
	// traffic (§27.5); only the auth-gatekeeper endpoints are
	// playground-specific.
	if *playgroundEnabled {
		pgCfg := playground.Config{
			Enabled:            true,
			AuthMode:           playground.AuthMode(*playgroundAuthMode),
			DevTenantID:        *playgroundDevTenantID,
			AllowedRuntimes:    splitCSV(*playgroundAllowedRuntimes),
			MaxSessionMinutes:  *playgroundMaxSessionMinutes,
			MaxIdleTimeSeconds: *playgroundMaxIdleTimeSeconds,
			OIDCSessionTTL:     time.Duration(*playgroundOIDCSessionTTL) * time.Second,
			BearerTTL:          time.Duration(*playgroundBearerTTL) * time.Second,
			MultiTenant:        *multiTenant,
			GatewayHost:        *playgroundGatewayHost,
		}
		// §27.3 dev mode is permitted only under global devMode; the
		// chart rejects authMode=dev otherwise, and this is the
		// backstop for a deployment that bypassed Helm-validate.
		if pgCfg.AuthMode == playground.AuthModeDev && !*devMode {
			log.Fatalf("lenny-gateway: LENNY_PLAYGROUND_DEV_MODE_FORBIDDEN: playground.authMode=dev requires global.devMode=true (§27.3)")
		}
		// §27.2 layer-3 startup gate: a malformed playground config is
		// fatal (the schema and preflight layers are the primary
		// defenses; this is defense-in-depth).
		if err := pgCfg.Validate(); err != nil {
			log.Fatalf("lenny-gateway: %v", err)
		}
		// §27.3.1 session record + revocation backing store: Redis when
		// --redis-url is set so a logout on one replica revokes the
		// bearer fleet-wide, in-process otherwise (single-replica).
		var pgSessions playground.SessionStore
		if redisClient != nil {
			redisSessions := playground.NewRedisSessionStore(redisClient)
			pgSessions = redisSessions
			// §27.3.1 pub/sub: each replica subscribes to the
			// per-tenant revocation channel so the auth hot path can
			// short-circuit the Redis GET on a cache hit.
			for _, t := range playgroundSubscribeTenants(pgCfg) {
				go redisSessions.SubscribeRevocations(context.Background(), t)
			}
		} else {
			pgSessions = playground.NewMemorySessionStore()
		}
		// §27.8 playground metrics register against the same private
		// registry the gateway's /metrics scrape target serves.
		pgMetrics, err := playground.NewMetrics(gwMetrics.Registerer())
		if err != nil {
			log.Fatalf("lenny-gateway: §27.8 playground metrics: %v", err)
		}
		pg := playground.New(pgCfg, playground.Options{
			Signer:   jwtSigner,
			Verifier: jwtSigner,
			Tenants:  playgroundTenantRegistry{store: tenants},
			Sessions: pgSessions,
			Metrics:  pgMetrics,
		}).WithAuditEmitter(playgroundAuditEmitter{})
		mux.Handle("/playground", pg.PlaygroundRoutes())
		mux.Handle("/playground/", pg.PlaygroundRoutes())
		mux.Handle("/v1/playground/token", pg.TokenRoutes())
		log.Printf("lenny-gateway: §27 web playground served at /playground (authMode=%s)", pgCfg.AuthMode)
	}

	// ----- §16.1 Prometheus metrics -----
	mux.Handle("GET /metrics", gwMetrics.Handler())

	// ----- Healthz (unauthenticated) -----
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// ----- §12.5 drain-readiness endpoint (unauthenticated) -----
	// The lenny-drain-readiness webhook probes this before admitting a
	// node-drain pod eviction. blobProbe runs a real MinIO bucket check
	// when the artifact store is MinIO-backed, and an always-ready stub
	// for the process-local in-memory store.
	mux.Handle("GET /internal/drain-readiness", &drainreadiness.Handler{Prober: blobProbe})

	// ----- Middleware stack -----
	var handler http.Handler = mux

	// The §11.2 per-tenant concurrent-session quota is enforced inside
	// the session-creation handlers (sessionserver.requireSessionQuota)
	// against each tenant's configured MaxConcurrentSessions.

	// Idempotency next (after auth + circuit; needs the
	// authenticated tenant on the request to scope keys correctly).
	// The §11.5 key cache is durable under --postgres-dsn so an
	// idempotent retry replays across gateway replicas and restarts.
	var idemStore idemmw.Store = idemmw.NewMemoryStore()
	if pgPool != nil {
		idemStore = idempgstore.New(pgPool)
	}
	handler = idemmw.Wrap(handler, idemStore, idemmw.Options{})

	// Circuit breaker next: rejects requests when any open breaker
	// matches. The shared breakerstore.Memory satisfies cbmw.Registry
	// so the admin /v1/admin/circuit-breakers endpoints share state
	// with the request-path middleware.
	handler = cbmw.Wrap(handler, breakers, cbmw.Options{})

	// §11.1 rate limiting next — runs just after auth so the per-user
	// scope sees the authenticated principal. Limits default to zero
	// (disabled); operators set them via the rate-limit flags.
	handler = ratelimitmw.Wrap(handler, ratelimitmw.Options{
		Counter:          rateLimiter,
		GlobalPerMinute:  *rlGlobalPerMin,
		PerUserPerMinute: *rlPerUserPerMin,
	})

	// §10.6 transparent-filtering environment resolver — runs
	// immediately after auth so the caller's identity and OIDC groups
	// are resolved. It lists the caller tenant's environments, resolves
	// the noEnvironmentPolicy (per-tenant value over the platform-wide
	// resolvedNoEnvPolicy), and attaches a Resolution to the request
	// context. The §9.1 runtime-discovery surfaces and the
	// session-creation path read that Resolution and filter through it,
	// so a caller sees only the runtimes its environments authorize
	// without naming an environment. The resolver is a no-op when the
	// environment or tenant registry is not wired.
	handler = environmentmw.Wrap(handler, environmentmw.Options{
		Environments:               environments,
		Tenants:                    tenants,
		DefaultNoEnvironmentPolicy: resolvedNoEnvPolicy,
	})

	// Auth next-to-outermost. AllowDevRoles is only honoured when the
	// dev flag is set (LENNY_DEV_MODE=true or --dev-mode); production
	// deployments leave it off so X-Lenny-Roles cannot self-grant
	// platform-admin.
	//
	// The §10.2 Bearer path is verified with the bearer verifier built
	// above: the in-process Token Service signer, so a token minted by
	// POST /v1/oauth/token round-trips through the gateway, plus the
	// embedded OIDC provider's key when --bearer-trust-hmac-key-file is
	// set under §17.4 Embedded Mode. Production swaps in the OIDC JWKS
	// verifier.
	authOpts := authmw.Options{
		MultiTenant:     *multiTenant,
		AllowDevHeaders: true,
		AllowDevRoles:   *devMode,
		Verifier:        bearerVerifier,
		Revocations:     revProp,
	}
	if !*multiTenant {
		// Even in single-tenant mode, dev-header callers carry the
		// tenant header. Flip to multi-tenant with a permissive
		// registry so the header round-trips.
		authOpts.MultiTenant = true
	}
	authOpts.Registry = permissiveRegistry{}
	handler = authmw.Wrap(handler, authOpts)

	// §16.1 request metrics, outermost wrap so every request — including
	// auth rejections — is counted. The route label collapses
	// high-cardinality path segments to a stable template.
	handler = gwMetrics.Middleware(handler, routeTemplate)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// ----- §4.9 LLM reverse proxy -----
	// With --llm-proxy-addr the gateway serves the §4.9 LLM proxy for
	// proxy-mode agent pods on a listener separate from the REST API. It
	// resolves an inbound lease token against llmLeases — the same store
	// the credassign service records minted leases in — and the upstream
	// credential against credCache. The credential deny list credDeny is
	// built earlier and wrapped by the credential-lease revocation
	// propagator, so the admin router's §11.4 full_revoke fan-out and
	// the emergency-revocation path revoke a user's leases onto the same
	// list the proxy reads here.
	llmProxySrv := newLLMProxyServer(*llmProxyAddr, *anthropicVersion, llmLeases, credCache, credDeny)

	// ----- §8.6 GatewayControl gRPC server -----
	// With --grpc-addr the gateway serves the adapter→gateway control
	// surface — the inverse direction of the pod-facing Adapter service.
	// It currently hosts the §8.6 ExtendLease RPC: a pod's adapter calls
	// it when its LLM proxy rejects a request for budget exhaustion, and
	// the gateway computes the lease-extension grant.
	gatewayCtrlSrv, gatewayCtrlLis, err := newGatewayControlServer(*grpcAddr)
	if err != nil {
		log.Fatalf("lenny-gateway: §8.6 GatewayControl listen: %v", err)
	}

	// ----- §6.2 / §11.3 pre-running watchdog -----
	// Sweeps every 5 s; transitions stuck sessions to failed.
	// Tenants list is sourced from the in-memory store so newly
	// registered tenants are picked up on the next tick.
	wd := watchdog.New(sessions, tenantsLister{tenants}, watchdog.Config{}, nil).
		WithBilling(billing).
		WithTreeArchive(treeArchive)
	watchdogCtx, watchdogCancel := context.WithCancel(context.Background())
	defer watchdogCancel()
	go wd.Run(watchdogCtx, func(res watchdog.Result, err error) {
		if err != nil {
			log.Printf("lenny-gateway: watchdog sweep error: %v", err)
			return
		}
		if res.ForcedFailures > 0 {
			log.Printf("lenny-gateway: watchdog forced %d sessions to failed: %v",
				res.ForcedFailures, res.PerReason)
		}
		if res.Expirations > 0 {
			log.Printf("lenny-gateway: watchdog expired %d sessions past their §11.3 deadline",
				res.Expirations)
		}
	})

	// ----- §8.10 orphan-cleanup job -----
	orphanSweeper := orphancleanup.New(sessions, tenantsLister{tenants}, orphancleanup.Options{
		Archive: treeArchive,
		Clock:   clockinject.Now,
	})
	go orphanSweeper.Run(watchdogCtx, func(terminated int, err error) {
		if err != nil {
			log.Printf("lenny-gateway: orphan-cleanup sweep error: %v", err)
			return
		}
		if terminated > 0 {
			log.Printf("lenny-gateway: orphan-cleanup terminated %d sessions past the §8.10 cascade timeout",
				terminated)
		}
	})

	// ----- §7.1 artifact-retention GC -----
	// Collects the workspace snapshot, transcript, and blobs of every
	// terminal session past its retention TTL; a §12.8 legal hold
	// exempts the session.
	{
		var arts []retentiongc.Artifact
		if te, ok := transcripts.(sessionArtifactDeleter); ok {
			arts = append(arts, retentiongc.Artifact{Name: "transcripts", Delete: te.DeleteBySession})
		}
		if be, ok := blobs.(sessionArtifactDeleter); ok {
			arts = append(arts, retentiongc.Artifact{Name: "artifacts", Delete: be.DeleteBySession})
		}
		retGC := retentiongc.New(sessions, tenantsLister{tenants}, arts, retentiongc.Options{Clock: clockinject.Now})
		go retGC.Run(watchdogCtx, func(collected int, err error) {
			if err != nil {
				log.Printf("lenny-gateway: retention-GC sweep error: %v", err)
				return
			}
			if collected > 0 {
				log.Printf("lenny-gateway: retention-GC collected artifacts for %d sessions past their §7.1 retention TTL",
					collected)
			}
		})
	}

	// ----- §10.1 session-coordination lease sweeper -----
	// Active only with Redis: it renews this replica's lease on every
	// non-terminal session so a crashed replica's sessions free up.
	if coordinator != nil {
		go coordinator.Run(watchdogCtx)
	}

	// ----- §11.6 circuit-breaker cache refresh -----
	// Active only with Redis: keeps the local open-breaker snapshot
	// current via pub/sub and a periodic refresh.
	if breakerCache != nil {
		go breakerCache.Run(watchdogCtx)
	}

	// ----- §11.2.1 billing failover Tier 2 flusher -----
	// Drains the in-memory write-ahead buffer into the primary billing
	// ledger once Postgres connectivity is restored, preserving the
	// monotonic ordering guarantee. The Tier 1 Redis stream drains
	// itself through its own consumer-group flusher.
	go billingPipeline.RunFlusher(watchdogCtx)

	// ----- §4.9 / §10.3 / §13.3 security-cache pub/sub subscribers -----
	// Active only with Redis: each subscribe loop applies a peer
	// replica's revocations and deny-list mutations onto this replica's
	// local cache, so a §13.3 token revocation, a §10.3 mTLS
	// certificate revocation, and a §4.9 credential revocation each
	// converge fleet-wide. With no Redis the caches stay per-replica.
	//
	// The §4.9 credential-lease revocation runs the credrenewal
	// propagator's subscriber rather than the bare credential-deny-list
	// propagator's: both subscribe to the same channel and apply onto
	// the same deny list, and the credrenewal propagator additionally
	// drops the renewal worker's tracked leases for a revoked
	// credential. Running both would double-deliver onto one deny list
	// for no gain, so only the superset subscriber runs.
	if securityBus != nil {
		go revProp.Run(watchdogCtx)
		go mtlsDenyProp.Run(watchdogCtx)
		go credRenewalProp.Run(watchdogCtx)
	}

	// ----- §4.9 Proactive Lease Renewal sweep -----
	// Active only with credential pools wired: the worker sweeps tracked
	// leases on its interval, issues a replacement before each lease's
	// renewBefore deadline, and pushes the rotated credential to the
	// lease's pod via the §4.7 RotateCredentials RPC.
	if credRenewalWorker != nil {
		go credRenewalWorker.Run(watchdogCtx)
	}

	// ----- §4.4 periodic-checkpoint loop -----
	// Active only with --agent-namespace: snapshots every coordinated
	// session's workspace on the checkpoint cadence so the §7.1
	// WorkspaceSnapshot stays fresh against the §16.5 freshness SLO.
	// The same checkpointer backs the §7.1 seal-and-export on the
	// session-completion path.
	if checkpointSvc != nil {
		go checkpointSvc.Run(watchdogCtx)
	}

	// ----- §13.3 revocation-cache rehydration -----
	// Loads revoked-token jtis from the issued-token index so a
	// revocation survives a restart and propagates across replicas.
	if pgPool != nil {
		issued := issuedtokenstore.New(pgPool)
		lister := tenantsLister{tenants}
		if err := revCache.Rehydrate(context.Background(), lister, issued); err != nil {
			log.Printf("lenny-gateway: initial revocation rehydration failed: %v", err)
		}
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-watchdogCtx.Done():
					return
				case <-ticker.C:
					if err := revCache.Rehydrate(context.Background(), lister, issued); err != nil && watchdogCtx.Err() == nil {
						log.Printf("lenny-gateway: revocation rehydration failed: %v", err)
					}
				}
			}
		}()
	}

	// ----- §11.5 idempotency-key TTL garbage collection -----
	// Reclaims idempotency_keys rows past the 24-hour retention window
	// so the durable key cache stays bounded.
	if pgPool != nil {
		idemGC := idempgstore.New(pgPool)
		lister := tenantsLister{tenants}
		sweepIdempotencyKeys(context.Background(), idemGC, lister)
		go func() {
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-watchdogCtx.Done():
					return
				case <-ticker.C:
					sweepIdempotencyKeys(watchdogCtx, idemGC, lister)
				}
			}
		}()
	}

	// ----- §16.1 metrics export -----
	// Refreshes the gauge metrics (storage quota, circuit breakers)
	// that the §16.5 alerts read.
	exportGaugeMetrics := func(ctx context.Context) {
		exportStorageQuotaMetrics(ctx, tenants, storageCounter, gwMetrics)
		exportCircuitBreakerMetrics(ctx, breakers, breakerCache, gwMetrics)
	}
	exportGaugeMetrics(context.Background())
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-watchdogCtx.Done():
				return
			case <-ticker.C:
				exportGaugeMetrics(watchdogCtx)
			}
		}
	}()

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		log.Printf("lenny-gateway: listening on %s (dev_mode=%v multi_tenant=%v)",
			*addr, *devMode, *multiTenant)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("lenny-gateway: listen: %v", err)
		}
	}()
	if llmProxySrv != nil {
		go func() {
			log.Printf("lenny-gateway: §4.9 LLM proxy listening on %s", llmProxySrv.Addr)
			if err := llmProxySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("lenny-gateway: llm proxy listen: %v", err)
			}
		}()
	}
	if gatewayCtrlSrv != nil {
		go func() {
			log.Printf("lenny-gateway: §8.6 GatewayControl gRPC listening on %s", gatewayCtrlLis.Addr())
			if err := gatewayCtrlSrv.Serve(gatewayCtrlLis); err != nil && err != grpc.ErrServerStopped {
				log.Fatalf("lenny-gateway: GatewayControl listen: %v", err)
			}
		}()
	}

	<-stopCh
	log.Printf("lenny-gateway: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	if llmProxySrv != nil {
		_ = llmProxySrv.Shutdown(ctx)
	}
	if gatewayCtrlSrv != nil {
		gatewayCtrlSrv.GracefulStop()
	}
	if pgPool != nil {
		pgPool.Close()
	}
	if redisClient != nil {
		_ = redisClient.Close()
	}
}

// breakerRegistry is the breaker-store surface the gateway wires: the
// breakerstore.Store admin operations plus the cbmw.Registry snapshot
// the circuit-breaker middleware reads. Both the in-memory and the
// Redis-backed breaker stores satisfy it.
type breakerRegistry interface {
	breakerstore.Store
	cbmw.Registry
}

// newLLMProxyServer builds the §4.9 LLM reverse-proxy HTTP server,
// serving the Anthropic Messages endpoint at POST /llm-proxy/v1/messages.
// It returns nil when addr is empty, which disables the proxy listener.
// The credential-lease store and the credential cache start empty; the
// §4.9 credential-assignment path populates them, and a request that
// arrives before then is cleanly rejected. creds is the §4.9
// upstream-credential cache the binder's credential-assignment path
// populates, so the proxy resolves a lease's upstream credential from
// the same instance the assignment wrote it to. denyList is the
// per-replica credential deny list, owned by a propagator the caller
// drives so revocations converge across replicas.
func newLLMProxyServer(addr, anthropicVersion string, leases credleasestore.LeaseStore, creds *credcache.Cache, denyList *denylist.DenyList) *http.Server {
	if addr == "" {
		return nil
	}
	proxyMux := http.NewServeMux()
	proxyMux.Handle("POST /llm-proxy/v1/messages", &llmproxy.Handler{
		Leases:      leases,
		Translator:  &llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: anthropicVersion},
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: creds,
		DenyList:    denyList,
	})
	return &http.Server{
		Addr:              addr,
		Handler:           proxyMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// newGatewayControlServer builds the §8.6 GatewayControl gRPC server
// and binds its listener. It returns (nil, nil, nil) when addr is
// empty, which disables the GatewayControl listener. A non-empty addr
// that cannot be bound returns the error so the gateway fails fast.
//
// The server hosts the §8.6 ExtendLease RPC. Its budget state is held
// in a MemoryBudgetSource, which doubles as the TenantResolver. The
// §8.6 durability requirement — persisting the extension-denied flag
// and cool-off expiry to the delegation_tree_budget Postgres table so
// a coordinator handoff cannot bypass a user rejection — is met by
// swapping in a Postgres-backed leasecontrol.BudgetSource with the
// Wave 1 store-persistence work; leasecontrol.Service depends only on
// the interface.
func newGatewayControlServer(addr string) (*grpc.Server, net.Listener, error) {
	if addr == "" {
		return nil, nil, nil
	}
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets,
		Tenants: budgets,
		Clock:   clockinject.Now,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build GatewayControl service: %w", err)
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("bind GatewayControl listener on %s: %w", addr, err)
	}
	gs := grpc.NewServer()
	adapterv1.RegisterGatewayControlServer(gs, svc)
	return gs, lis, nil
}

// verifyPostgresSchema fails fast when the gateway is pointed at a
// database that has not had the migrations/ schema applied. It probes
// for the sessions table; the fuller §11.7 startup grant-verification
// check lands with the audit pipeline.
func verifyPostgresSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = 'sessions')`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("postgres: schema probe failed: %w", err)
	}
	if !exists {
		return fmt.Errorf("postgres: schema not migrated (the sessions table is absent); apply migrations/ before starting the gateway")
	}
	return nil
}

// resolveReplicaID returns this gateway replica's §10.1 coordination
// identity: the LENNY_REPLICA_ID override, or the hostname plus a
// random suffix so two replicas sharing a host still differ.
func resolveReplicaID() string {
	if id := os.Getenv("LENNY_REPLICA_ID"); id != "" {
		return id
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "gateway"
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%x", host, b)
}

// permissiveRegistry accepts every tenant. The minimal gateway uses
// this so dev-header transports can name an arbitrary tenant during
// integration tests without operator pre-provisioning. Production
// swaps in a Postgres-backed Registry (e.g., the in-memory
// tenantstore.Memory which also satisfies auth.TenantRegistry).
type permissiveRegistry struct{}

func (permissiveRegistry) IsRegistered(string) (bool, error) { return true, nil }

// sessionArtifactDeleter is implemented by session-scoped stores that
// expose the per-session DeleteBySession adapter — the transcript and
// blob stores. It backs both the §12.8 erasure orchestrator and the
// §7.1 retention GC.
type sessionArtifactDeleter interface {
	DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error)
}

// experimentRejectionReporter bridges a §10.7 ExperimentRouter
// fail-closed rejection to the §11.7 audit chain, the §16.1 metrics
// registry, and the §25.3 operational-event buffer: it records the
// `experiment.isolation_mismatch` event on all three and increments
// `lenny_experiment_isolation_rejections_total`.
type experimentRejectionReporter struct {
	audit   admin.AuditSink
	metrics *gatewaymetrics.Metrics
	emitter *opsevents.Emitter
}

func (e experimentRejectionReporter) ReportExperimentIsolationRejection(ctx context.Context, ev sessionserver.ExperimentIsolationRejection) {
	if e.metrics != nil {
		e.metrics.RecordExperimentIsolationRejection(ev.TenantID, ev.ExperimentID, ev.VariantID)
	}
	detail := map[string]any{
		"tenant_id":            ev.TenantID,
		"user_id":              ev.UserID,
		"experiment_id":        ev.ExperimentID,
		"variant_id":           ev.VariantID,
		"sessionMinIsolation":  ev.SessionMinIsolation,
		"variantPoolIsolation": ev.VariantPoolIsolation,
	}
	if e.audit != nil {
		e.audit.EmitAdminEvent(ctx, admin.AuditEvent{
			Type:           "experiment.isolation_mismatch",
			ActorTenantID:  ev.TenantID,
			TargetResource: ev.ExperimentID,
			Detail:         detail,
		})
	}
	// §16.6: the rejection is also an operational event — surface it on
	// the §25.3 event buffer so ops agents observe it without log scraping.
	if e.emitter != nil {
		data, _ := json.Marshal(detail)
		e.emitter.Emit(opsevents.OperationalEvent{
			Source:          "/v1/sessions",
			Type:            opsevents.EventExperimentIsolationMismatch.CloudEventsType(),
			Severity:        "warning",
			DataContentType: "application/json",
			Data:            data,
		})
	}
}

// mcpDelegationAuditor adapts the gateway audit sink to the
// mcptools.DelegationAuditor interface, drawing the §11.7 actor fields
// from the request principal on the context.
type mcpDelegationAuditor struct {
	sink admin.AuditSink
}

func (a mcpDelegationAuditor) EmitDelegationEvent(ctx context.Context, eventType string, detail map[string]any) {
	if a.sink == nil {
		return
	}
	ev := admin.AuditEvent{Type: eventType, Detail: detail, At: clockinject.Now().UTC()}
	if p, ok := authmw.FromContext(ctx); ok {
		ev.ActorSubject = p.Subject
		ev.ActorTenantID = p.TenantID
	}
	a.sink.EmitAdminEvent(ctx, ev)
}

// tenantsLister adapts a tenantstore.Store into a
// watchdog.TenantLister so the watchdog sweeps every registered
// tenant. In single-tenant deployments it also returns "default" so
// dev-mode sessions are bounded.
type tenantsLister struct {
	store tenantstore.Store
}

func (t tenantsLister) ListTenants(ctx context.Context) ([]string, error) {
	rows, err := t.store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows)+1)
	out = append(out, "default")
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out, nil
}

// playgroundTenantRegistry adapts a tenantstore.Store into the
// playground.TenantRegistry the §27.2 layer-4 Ready-gate consults. It
// reports a tenant as registered when the store returns a row that is
// not soft-deleted; the built-in "default" tenant is always
// registered so a dev-mode playground against the Embedded-Mode
// default tenant resolves without a Postgres row.
type playgroundTenantRegistry struct {
	store tenantstore.Store
}

func (r playgroundTenantRegistry) IsRegistered(tenantID string) (bool, error) {
	if tenantID == "default" {
		return true, nil
	}
	row, err := r.store.Get(context.Background(), tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.IsActive(), nil
}

// playgroundSubscribeTenants returns the tenant set whose §27.3.1
// playground revocation channel each gateway replica subscribes to.
// In dev mode that is the configured devTenantId; otherwise the
// built-in "default" tenant. A multi-tenant production deployment
// subscribes per-tenant as sessions are established; the initial set
// covers the common single-tenant case.
func playgroundSubscribeTenants(cfg playground.Config) []string {
	if cfg.AuthMode == playground.AuthModeDev {
		return []string{cfg.DevTenantID}
	}
	return []string{"default"}
}

// playgroundAuditEmitter bridges the playground's §27.3.1 audit
// events to the gateway log. A durable §11.7 audit sink is wired by
// the admin router; the playground emitter keeps a lightweight log
// record so a bearer mint and revoke are observable without coupling
// the playground package to the admin audit taxonomy.
type playgroundAuditEmitter struct{}

func (playgroundAuditEmitter) EmitPlaygroundEvent(_ context.Context, ev playground.AuditEvent) {
	log.Printf("lenny-gateway: §27 audit %s tenant=%s user=%s jti=%s", ev.Type, ev.TenantID, ev.UserID, ev.BearerJTI)
}

// splitCSV splits a comma-separated flag value into a trimmed,
// non-empty slice. An empty input yields a nil slice.
func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sweepIdempotencyKeys runs one §11.5 TTL garbage-collection pass,
// deleting idempotency_keys rows older than the 24-hour retention
// window. The sweep is per-tenant because the lenny_tenant_guard
// trigger fires for every DELETE.
func sweepIdempotencyKeys(ctx context.Context, gc *idempgstore.Store, lister tenantsLister) {
	tenants, err := lister.ListTenants(ctx)
	if err != nil {
		log.Printf("lenny-gateway: idempotency GC: listing tenants failed: %v", err)
		return
	}
	cutoff := clockinject.Now().Add(-idempotency.TTL)
	for _, tenant := range tenants {
		if _, err := gc.DeleteExpired(ctx, tenant, cutoff); err != nil && ctx.Err() == nil {
			log.Printf("lenny-gateway: idempotency GC: tenant %q sweep failed: %v", tenant, err)
		}
	}
}

// exportStorageQuotaMetrics refreshes the §16.1 per-tenant
// storage-quota gauges from the tenant registry and the storage
// counter. Only tenants with a configured quota are exported so the
// §16.5 StorageQuotaHigh alert does not divide by a zero limit.
func exportStorageQuotaMetrics(ctx context.Context, tenants tenantstore.Store, counter storagequota.Counter, m *gatewaymetrics.Metrics) {
	rows, err := tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		log.Printf("lenny-gateway: storage-quota metrics: listing tenants failed: %v", err)
		return
	}
	for _, t := range rows {
		if t.StorageQuotaBytes <= 0 {
			continue
		}
		used, err := counter.Used(ctx, t.ID)
		if err != nil {
			continue
		}
		m.SetStorageQuota(t.ID, used, t.StorageQuotaBytes)
	}
}

// exportCircuitBreakerMetrics refreshes the §16.1 circuit-breaker
// gauges: the per-breaker open state and the cache freshness. In
// in-memory mode there is no cache, so it reports the registry as
// always-current and initialized.
func exportCircuitBreakerMetrics(ctx context.Context, breakers breakerRegistry, cache *cachingstore.Store, m *gatewaymetrics.Metrics) {
	if rows, err := breakers.List(ctx); err == nil {
		for _, b := range rows {
			m.SetCircuitBreakerOpen(b.Name, b.State == circuitbreaker.StateOpen)
		}
	}
	if cache == nil {
		m.SetCircuitBreakerCache(0, true)
		return
	}
	last := cache.LastRefresh()
	if last.IsZero() {
		m.SetCircuitBreakerCache(0, false)
		return
	}
	m.SetCircuitBreakerCache(time.Since(last).Seconds(), true)
}

// staticHealthy returns a §25.3 health Checker that always reports
// the named component healthy. The minimal gateway uses these
// because every subsystem is an in-process in-memory store with no
// failure mode; production swaps in checkers that probe Postgres /
// Redis / MinIO connectivity.
func staticHealthy(name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(context.Context) health.Component {
			return health.Component{Name: name, Status: health.StatusHealthy}
		},
	}
}

// routeTemplate collapses a request path to a stable §16.1.1
// low-cardinality route label so the request metric does not
// explode into one series per session id / blob ref.
func routeTemplate(r *http.Request) string {
	p := r.URL.Path
	switch {
	case p == "/healthz", p == "/metrics", p == "/v1/sessions",
		p == "/v1/sessions/start", p == "/v1/chat/completions",
		p == "/v1/responses", p == "/mcp", p == "/openapi.yaml",
		p == "/v1/openapi.json":
		return p
	case strings.HasPrefix(p, "/v1/sessions/"):
		return "/v1/sessions/{id}/*"
	case strings.HasPrefix(p, "/v1/blobs/"):
		return "/v1/blobs/{ref}"
	case strings.HasPrefix(p, "/v1/responses/"):
		return "/v1/responses/{id}"
	case strings.HasPrefix(p, "/v1/admin/"):
		return "/v1/admin/*"
	case strings.HasPrefix(p, "/v1/oauth/"):
		return "/v1/oauth/*"
	default:
		return "other"
	}
}

// boolStr renders a bool as the lowercase string the §25.3
// platform-config endpoint surfaces.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// envFlag returns true when the env var name is set to a truthy
// value (1, true, yes — case-insensitive). Used to default the
// --dev-mode flag from LENNY_DEV_MODE.
func envFlag(name string) bool {
	v := os.Getenv(name)
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// envFlagDefault returns true / false from the env var name, or def
// when the var is unset. Used for flags that default on (e.g., the
// §10.3 --jwks-publish endpoint) where envFlag's always-false-default
// semantics do not match the spec posture.
func envFlagDefault(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// envFloat returns the env var name parsed as a float64, or def when
// the var is unset or does not parse. Used to default the
// --billing-dual-control-threshold flag from the environment.
func envFloat(name string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// envOr returns the env var name, or def when the var is unset or
// empty. Used to default the §27.2 playground string flags.
func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// envInt returns the env var name parsed as an int, or def when the
// var is unset or does not parse. Used to default the §27.2
// playground integer flags.
func envInt(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// splitAndTrim splits a comma-separated string and drops empty entries
// after trimming whitespace. Used to parse the --redis-sentinel-addrs
// list.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// dialTokenService dials lenny-token-service for the §4.3 credential
// materialization path. mTLS is required in production deployments —
// the gateway has a distinct client identity per replica per §4.3 —
// and certPath / keyPath / caPath name the project's mTLS material.
// With every TLS flag empty the dial falls through to plaintext for
// dev mode, which is the path the gateway-side bufconn tests exercise.
func dialTokenService(addr, certPath, keyPath, caPath string) (*grpc.ClientConn, error) {
	if addr == "" {
		return nil, fmt.Errorf("token service address is empty")
	}
	var transport grpc.DialOption
	switch {
	case certPath == "" && keyPath == "" && caPath == "":
		transport = grpc.WithTransportCredentials(insecure.NewCredentials())
	case certPath == "" || keyPath == "" || caPath == "":
		return nil, fmt.Errorf("token service mTLS requires --token-service-tls-cert, --token-service-tls-key, and --token-service-ca to all be set")
	default:
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load token-service client cert: %w", err)
		}
		caPEM, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read token-service CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("token-service CA bundle %q parsed no certificates", caPath)
		}
		transport = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      pool,
			MinVersion:   tls.VersionTLS13,
		}))
	}
	return grpc.NewClient(addr, transport)
}
