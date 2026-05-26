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
	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/cataloging"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/credential"
	gwadapter "github.com/lennylabs/lenny/pkg/gateway/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/adapterregistry"
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
	"github.com/lennylabs/lenny/pkg/gateway/checkpointretention"
	checkpointretentionpg "github.com/lennylabs/lenny/pkg/gateway/checkpointretention/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	connectorcredpg "github.com/lennylabs/lenny/pkg/gateway/connectorcredstore/pgstore"
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
	"github.com/lennylabs/lenny/pkg/gateway/extractionthreshold"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/gcpause"
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
	"github.com/lennylabs/lenny/pkg/gateway/legalholdreconciler"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpruntimes"
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
	"github.com/lennylabs/lenny/pkg/gateway/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/playground"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	poolpg "github.com/lennylabs/lenny/pkg/gateway/poolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/prestop"
	"github.com/lennylabs/lenny/pkg/gateway/proxycache"
	"github.com/lennylabs/lenny/pkg/gateway/pubsub"
	"github.com/lennylabs/lenny/pkg/gateway/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/ratelimit"
	ratelimitredis "github.com/lennylabs/lenny/pkg/gateway/ratelimit/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/recommendations"
	"github.com/lennylabs/lenny/pkg/gateway/createdsweeper"
	"github.com/lennylabs/lenny/pkg/gateway/retentiongc"
	"github.com/lennylabs/lenny/pkg/gateway/revocation"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/semanticcache"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionlogstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/slotcounter"
	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
	storagequotaredis "github.com/lennylabs/lenny/pkg/gateway/storagequota/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
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
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
	"github.com/lennylabs/lenny/pkg/kms/rekey"
	mtlsdenylist "github.com/lennylabs/lenny/pkg/mtls/denylist"
	mtlsdenylistprop "github.com/lennylabs/lenny/pkg/mtls/denylist/propagator"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/tenantkms"
	"github.com/lennylabs/lenny/pkg/tokensvcproxy"
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
		"Redis AUTH password applied to both direct and Sentinel modes. §12.4 requires AUTH; an empty password fails startup unless --redis-allow-insecure is set. Override via LENNY_REDIS_PASSWORD.")
	redisSentinelPassword := flag.String("redis-sentinel-password", os.Getenv("LENNY_REDIS_SENTINEL_PASSWORD"),
		"AUTH password for the sentinels themselves. Optional; sentinels typically run unauthenticated.")
	redisTLS := flag.Bool("redis-tls", envFlag("LENNY_REDIS_TLS"),
		"§12.4 request TLS on the Sentinel path. The direct-URL path derives TLS from the rediss:// scheme instead. TLS is mandatory unless --redis-allow-insecure is set, in which case this flag opts a dev Sentinel topology back into TLS. Override via LENNY_REDIS_TLS.")
	redisAllowInsecure := flag.Bool("redis-allow-insecure", envFlag("LENNY_REDIS_ALLOW_INSECURE"),
		"§12.4 opt out of the mandatory Redis AUTH-and-TLS startup invariant. The spec requires both on every Redis instance, so this defaults off and a missing password or plaintext connection fails startup. Set only for a dev or local Redis. Override via LENNY_REDIS_ALLOW_INSECURE.")
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
	// §4.8 line 1019: deployer-supplied external interceptors. Each
	// --external-interceptor value registers one §4 RequestInterceptor
	// service on the policy chain. Repeatable. Form:
	// name=<n>,endpoint=<host:port>,phase=<phase>[,priority=<n>]
	// [,failPolicy=fail-open|fail-closed][,timeout=<dur>].
	var externalInterceptors []string
	flag.Func("external-interceptor",
		"§4.8 external RequestInterceptor registration (repeatable): name=<n>,endpoint=<host:port>,phase=<phase>[,priority=<n>][,failPolicy=fail-open|fail-closed][,timeout=<dur>]",
		func(v string) error { externalInterceptors = append(externalInterceptors, v); return nil })
	externalInterceptorTLSCert := flag.String("external-interceptor-tls-cert", os.Getenv("LENNY_EXTERNAL_INTERCEPTOR_TLS_CERT"),
		"client certificate for mTLS to external interceptor services. When empty (with key/ca also empty) the gateway dials plaintext.")
	externalInterceptorTLSKey := flag.String("external-interceptor-tls-key", os.Getenv("LENNY_EXTERNAL_INTERCEPTOR_TLS_KEY"),
		"client private key for mTLS to external interceptor services.")
	externalInterceptorCA := flag.String("external-interceptor-ca", os.Getenv("LENNY_EXTERNAL_INTERCEPTOR_CA"),
		"CA bundle verifying external interceptor server certificates.")
	guardrailsClassifier := flag.String("guardrails-classifier", os.Getenv("LENNY_GUARDRAILS_CLASSIFIER"),
		"§4.8 GuardrailsInterceptor classifier registration (external RequestInterceptor spec: name=<n>,endpoint=<host:port>[,failPolicy=fail-open|fail-closed][,timeout=<dur>]). When empty the GuardrailsInterceptor is disabled. The priority is fixed at 400 and the phases at PreDelegation, PreLLMRequest, PostLLMResponse, and PostAgentOutput; any phase=/priority= in the spec is ignored.")
	interceptorFailOpenMax := flag.Int("interceptor-fail-open-max-consecutive", envInt("LENNY_INTERCEPTOR_FAIL_OPEN_MAX_CONSECUTIVE", 10),
		"§4.8 cumulative fail-open escalation ceiling: when a fail-open interceptor errors more than this many times in a rolling 5-minute window, the gateway auto-escalates it to fail-closed and emits interceptor.fail_open_escalated.")
	delegationMaxInputSize := flag.Int("delegation-max-input-size", envInt("LENNY_DELEGATION_MAX_INPUT_SIZE", delegationpolicystore.DefaultMaxInputSize),
		"§8.3 default contentPolicy.maxInputSize: the hard byte cap on TaskSpec.input the §4.8 DelegationPolicyEvaluator (PreDelegation, priority 250) enforces. A delegation exceeding it is rejected with INPUT_TOO_LARGE before pod allocation. Defaults to the §8.3 128 KiB. Override via LENNY_DELEGATION_MAX_INPUT_SIZE.")
	retryMaxRetries := flag.Int("retry-max-retries", envInt("LENNY_RETRY_MAX_RETRIES", policy.DefaultMaxRetries),
		"§7.3 default retryPolicy.maxRetries: the automatic-retry budget the §4.8 RetryPolicyEvaluator (PostRoute, priority 600) enforces. A session whose retryCount has reached this cap is rejected at routing (it is in awaiting_client_action and requires an explicit client resume). Defaults to the §7.3 example value of 2. Override via LENNY_RETRY_MAX_RETRIES.")
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
	tokenServiceHTTPURL := flag.String("token-service-http-url", os.Getenv("LENNY_TOKEN_SERVICE_HTTP_URL"),
		"§4.3 lenny-token-service HTTP token-exchange URL (scheme://host:port). When set, the gateway reverse-proxies /v1/oauth/* to the Token Service so the §13.3 canonical endpoint is served by the actual minter. Empty disables the /v1/oauth/ surface on the gateway entirely; deployments that wire a Token Service binary MUST set this to keep /v1/oauth/token reachable.")
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
	llmSemanticCache := flag.Bool("llm-semantic-cache", os.Getenv("LENNY_LLM_SEMANTIC_CACHE") == "1",
		"§4.9 enable the in-process semantic cache on the LLM proxy path. Caching stays disabled by default and is opt-in per pool via the pool's cachePolicy; this flag provisions the in-memory backend the per-pool policy draws on. The Redis-backed backend is wired separately.")
	anthropicVersion := flag.String("anthropic-version", os.Getenv("LENNY_ANTHROPIC_VERSION"),
		"default anthropic-version header the §4.9 LLM proxy injects when a request omits it. Empty rejects a request that omits the header.")
	// §4.9 lines 1525-1526: the proxy dispatches each lease to the
	// translator for its resolved provider. anthropic_direct and
	// openai_direct carry safe global defaults and are always
	// registered. aws_bedrock, vertex_ai, and azure_openai need
	// per-deployment region/project/endpoint config, so each registers
	// only when its config flag is set; a lease for an unconfigured
	// provider is rejected with UPSTREAM_PROVIDER_UNSUPPORTED.
	openaiBaseURL := flag.String("llm-openai-base-url", os.Getenv("LENNY_LLM_OPENAI_BASE_URL"),
		"§4.9 openai_direct upstream base URL the LLM proxy targets. Empty selects https://api.openai.com.")
	openaiOrg := flag.String("llm-openai-organization", os.Getenv("LENNY_LLM_OPENAI_ORGANIZATION"),
		"§4.9 optional OpenAI-Organization header the LLM proxy adds to openai_direct requests.")
	bedrockRegion := flag.String("llm-bedrock-region", os.Getenv("LENNY_LLM_BEDROCK_REGION"),
		"§4.9 AWS region for the aws_bedrock translator (e.g. us-east-1). Empty leaves aws_bedrock out of the proxy translator registry.")
	vertexRegion := flag.String("llm-vertex-region", os.Getenv("LENNY_LLM_VERTEX_REGION"),
		"§4.9 Vertex AI region for the vertex_ai translator (e.g. us-east5). Required with --llm-vertex-project to register vertex_ai.")
	vertexProject := flag.String("llm-vertex-project", os.Getenv("LENNY_LLM_VERTEX_PROJECT"),
		"§4.9 GCP project id for the vertex_ai translator. Required with --llm-vertex-region to register vertex_ai.")
	azureEndpoint := flag.String("llm-azure-endpoint", os.Getenv("LENNY_LLM_AZURE_ENDPOINT"),
		"§4.9 Azure OpenAI resource base URL for the azure_openai translator. Required with --llm-azure-api-version to register azure_openai.")
	azureAPIVersion := flag.String("llm-azure-api-version", os.Getenv("LENNY_LLM_AZURE_API_VERSION"),
		"§4.9 Azure OpenAI api-version query value. Required with --llm-azure-endpoint to register azure_openai.")
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
	checkpointInterval := flag.Duration("checkpoint-interval", 10*time.Minute,
		"§4.4 line 256 periodic-checkpoint cadence (`periodicCheckpointIntervalSeconds`). The gateway snapshots every coordinated session's workspace on this interval; active only with --agent-namespace. Default 10m (600s) matches the §4.4 spec value; the freshness SLO bounds workspace loss on eviction to ≤ one interval.")
	sessionArtifactRetentionSeconds := flag.Int("session-artifact-retention-seconds",
		envInt("LENNY_SESSION_ARTIFACT_RETENTION_SECONDS", int(sessionserver.DefaultArtifactRetention/time.Second)),
		"§7.1 line 77 default artifact-retention window in seconds. Session workspace snapshots, logs, and transcripts stay GC-eligible until this long after the session reaches a terminal state. Default 7 days (604800s); clients extend per-session via POST /v1/sessions/{id}/extend-retention. Override via LENNY_SESSION_ARTIFACT_RETENTION_SECONDS.")
	workspaceSealMaxDurationSeconds := flag.Int("workspace-seal-max-duration-seconds",
		envInt("LENNY_WORKSPACE_SEAL_MAX_DURATION_SECONDS", int(sessionserver.DefaultWorkspaceSealMaxDuration/time.Second)),
		"§7.1 line 112 maxWorkspaceSealDurationSeconds: the total wall-clock window the gateway retries seal-and-export (exponential backoff 5s→60s) before failing the session with workspace_seal_timeout and terminating the pod anyway. Default 300s. Override via LENNY_WORKSPACE_SEAL_MAX_DURATION_SECONDS.")
	idempotencyGCIntervalSeconds := flag.Int("idempotency-gc-interval-seconds",
		envInt("LENNY_IDEMPOTENCY_GC_INTERVAL_SECONDS", 3600),
		"§11.5 line 277 idempotency_keys TTL garbage-collection cadence. The sweeper iterates tenants and drops rows past the 24-hour retention window every interval. Default 3600s (one hour). Lower values reduce row backlog at the cost of more frequent Postgres scans; higher values keep expired rows up to the configured interval past TTL (read-time gate masks them from clients). Override via LENNY_IDEMPOTENCY_GC_INTERVAL_SECONDS.")
	checkpointJitterFraction := flag.Float64("checkpoint-jitter-fraction", envFloat("LENNY_CHECKPOINT_JITTER_FRACTION", checkpointer.DefaultJitterFraction),
		"§4.4 line 258 `periodicCheckpointJitterFraction`. Each session's first periodic checkpoint is scheduled at `checkpointInterval + random(0, checkpointInterval × jitterFraction)`, preventing thundering-herd checkpoint storms at Tier 3 scale. Range [0.0, 1.0]; default 0.2 spreads the first checkpoint uniformly across a 120-second window at the default 600-second interval. Override via LENNY_CHECKPOINT_JITTER_FRACTION.")
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
	maxSessionsPerReplica := flag.Int("max-sessions-per-replica", envInt("LENNY_MAX_SESSIONS_PER_REPLICA", 50),
		"§4.1 gateway.maxSessionsPerReplica: per-replica capacity ceiling used as the denominator of the GatewaySessionBudgetNearExhaustion alert (§16.5) and the §17.8.2 SCL-036 burst-absorption minReplicas formula. Provisional Tier defaults: 50 (Tier 1), 200 (Tier 2), 400 (Tier 3). Emitted at startup on the lenny_gateway_max_sessions_per_replica gauge. Override via LENNY_MAX_SESSIONS_PER_REPLICA.")
	// §4.1 / §16.5: scalar gauges read by the GatewayNoHealthyReplicas
	// and GatewayActiveStreamsHigh alert expressions in
	// pkg/alerting/rules/rules.go. The gateway emits them at startup so
	// the scalar(...) lookups in the alert rules resolve to a real
	// value instead of NaN.
	minReplicas := flag.Int("min-replicas", envInt("LENNY_MIN_REPLICAS", 1),
		"§4.1 / §16.5 gateway HPA minReplicas floor (§17.8.2 SCL-036). Emitted at startup on the lenny_gateway_min_replicas gauge so the GatewayNoHealthyReplicas alert (§16.5) can evaluate via scalar(lenny_gateway_min_replicas). Override via LENNY_MIN_REPLICAS.")
	streamCeiling := flag.Int("stream-ceiling", envInt("LENNY_STREAM_CEILING", 100),
		"§4.1 / §16.5 per-replica streaming-connection ceiling. Emitted at startup on the lenny_gateway_stream_ceiling gauge so the GatewayActiveStreamsHigh alert (§16.5) can evaluate via scalar(lenny_gateway_stream_ceiling). Override via LENNY_STREAM_CEILING.")
	// spec: §4.2 line 165 — LENNY_POOLER_MODE names the deployment
	// posture for the Postgres pooler. The gateway honours the value
	// at the application layer (logging it at startup so operators can
	// confirm the deployment posture); the load-bearing enforcement
	// is the migration 0057 lenny_tenant_guard trigger that rejects
	// the __all__ sentinel unless pgtenant.InAllTenants opts in via
	// the lenny.allow_all_sentinel session GUC.
	poolerMode := flag.String("pooler-mode", envOr("LENNY_POOLER_MODE", "transactional"),
		"§4.2 deployment posture for the Postgres pooler. `transactional` is the chart-managed in-cluster default; `external` names an out-of-process / managed pooler (RDS Proxy, Cloud SQL Auth Proxy with pgBouncer, etc.). The value is logged at startup; the underlying __all__ sentinel guard is enforced by the lenny_tenant_guard trigger via the lenny.allow_all_sentinel GUC.")
	// §4 / §17.5 KMS provider selector. The cloud adapters
	// (pkg/kms/{aws,gcp,azure}) reach the gateway through these
	// flags. spec: F-4.3.11 / F-10.2.11 / F-17.5.2.
	kmsOpts, kmsFinalize := providerflags.Bind(flag.CommandLine, os.Getenv,
		providerflags.Options{Provider: providerflags.ProviderLocal})
	flag.Parse()
	if err := kmsFinalize(); err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}

	// spec: §5.3 line 677 — in dev mode the default isolation profile
	// falls back to runc. Log the mandated warning once at startup so an
	// accidental production dev-mode install is visible in the logs.
	if *devMode {
		log.Printf("lenny-gateway: %s", isolation.DevModeIsolationWarning)
	}

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
	// chart with the default stripped fails closed at startup. See
	// resolveNoEnvironmentPolicy for the pure-function form the
	// §11.1 TestGatewayConfigValidation regression test exercises.
	resolvedNoEnvPolicy, err := resolveNoEnvironmentPolicy(*noEnvPolicy, *devMode)
	if err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}

	// spec: §4.2 line 165 — LENNY_POOLER_MODE must be one of the two
	// documented values. The trigger-level guard runs regardless of
	// this check; failing at startup keeps a misconfigured deploy
	// from running silently with the wrong assumed posture.
	switch *poolerMode {
	case "transactional", "external":
		log.Printf("lenny-gateway: pooler-mode=%s (§4.2 line 165)", *poolerMode)
	default:
		log.Fatalf("lenny-gateway: --pooler-mode must be `transactional` or `external`, got %q (§4.2 line 165)", *poolerMode)
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
	// §4.4 line 236 partial-manifest store: persists the recovery-aid
	// row written when an eviction checkpoint exceeds the preStop
	// tiered cap and the workspace upload is incomplete. The store is
	// always initialized; the writer is dormant until the §10.1
	// partial-upload pipeline is wired, but the resume-side cleanup
	// path is plumbed unconditionally so a row written by a
	// follow-on release is cleaned up correctly the first time a
	// session resumes.
	var partialManifests partialmanifeststore.Store
	if pgPool != nil {
		partialManifests = partialmanifestpg.New(pgPool, nil)
	} else {
		partialManifests = partialmanifeststore.NewMemoryStore(nil)
	}
	// §4.4 line 226 session-log store: persists runtime stderr to MinIO
	// when a session reaches a terminal state. The store is best-effort;
	// the Noop implementation drops the bytes and is sufficient for
	// in-memory deployments. A MinIO endpoint configured below
	// upgrades the wiring to MinIOStore so production retains the
	// observability artifact. The MinIO uploader integration is
	// deferred to the §4.5 wiring follow-on; today the close-hook
	// fires with an empty body so the session-completion path
	// exercises the contract without writing any object.
	// spec: §4.4 line 226.
	var sessionLogs sessionlogstore.Store = sessionlogstore.Noop{}
	// §4.4 line 234 / §12.5 latest-2 retention catalog. The Postgres-
	// backed store records every successful checkpoint and runs the
	// rotation in the same transaction; the in-memory store backs the
	// dev-mode deployment so the checkpointer can call Insert + Rotate
	// without a live database.
	// spec: §4.4 line 234.
	var checkpointRetention checkpointretention.Store
	if pgPool != nil {
		checkpointRetention = checkpointretentionpg.New(pgPool, nil)
	} else {
		checkpointRetention = checkpointretention.NewMemoryStore(nil)
	}
	// §4.5 artifact store: MinIO-backed when --minio-endpoint is set,
	// otherwise an in-memory store for the minimal gateway. blobProbe
	// is the §12.5 drain-readiness liveness probe — a real MinIO
	// bucket check with MinIO, an always-ready stub for the in-memory
	// store, which is process-local and cannot degrade.
	//
	// minioStore retains the concrete *miniostore.Store so the §12.5
	// ll. 303 fail-closed KMS-unavailable callback can be wired against
	// it once gwMetrics is constructed below. minioStore is nil when
	// the in-memory backend is selected (the in-memory path has no
	// per-tenant SSE-KMS surface).
	var blobs blobstore.Store = blobstore.NewMemoryStore(nil)
	var blobProbe drainreadiness.Prober = drainreadiness.ProberFunc(func(context.Context) error { return nil })
	var minioStore *miniostore.Store
	if *minioEndpoint != "" {
		// §12.5 ll. 297-303 SSEKeyResolver: the production gateway must
		// look up the writing tenant's workspaceTier on every Put and
		// hand MinIO the per-tenant SSE-KMS alias for T4 tenants.
		sseResolver := newSSEKeyResolver(tenants)
		ms, err := miniostore.New(miniostore.Config{
			Endpoint:       *minioEndpoint,
			AccessKey:      *minioAccessKey,
			SecretKey:      *minioSecretKey,
			Bucket:         *minioBucket,
			UseSSL:         *minioUseSSL,
			SSEKeyResolver: sseResolver,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: minio: %v", err)
		}
		minioStore = ms
		blobs = ms
		blobProbe = ms
		log.Printf("lenny-gateway: §4.5 artifact store is MinIO at %s (bucket %q); §12.5 SSEKeyResolver wired (T4 tenant-scoped SSE-KMS)",
			*minioEndpoint, *minioBucket)
	}

	// §12.5 ll. 309-321 artifact_store catalog. The Postgres-backed
	// catalog is the surface the §12.5 GC sweep, the §11.2 size
	// accounting, the §12.8 erasure orchestrator, and the §12.5 legal-
	// hold checks all read against. A nil pgPool yields a noop wrapper
	// — the in-memory deployment runs without the catalog, which is
	// the dev-mode posture: production paths require Postgres.
	var artifactCatalog artifactcatalog.Store
	var blobsCataloged *cataloging.Store
	if pgPool != nil {
		artifactCatalog = artifactcatalog.New(pgPool, nil)
		blobsCataloged = cataloging.New(blobs, artifactCatalog, cataloging.Options{
			LogOnCatalogFailure: func(uri string, err error) {
				log.Printf("lenny-gateway: §12.5 artifact_store catalog insert failed for %s: %v", uri, err)
			},
		})
		blobs = blobsCataloged
		log.Printf("lenny-gateway: §12.5 artifact_store catalog wired (Postgres-backed)")
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
			rcfg = redisconn.Config{URL: *redisURL, Password: *redisPassword, AllowInsecure: *redisAllowInsecure}
		default:
			rcfg = redisconn.Config{
				SentinelAddrs:    splitAndTrim(*redisSentinelAddrs),
				MasterName:       *redisSentinelMaster,
				Password:         *redisPassword,
				SentinelPassword: *redisSentinelPassword,
				TLS:              *redisTLS,
				AllowInsecure:    *redisAllowInsecure,
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

	// ----- §7.1 uploadToken KeyRing + rotator -----
	// The §7.1 line 67 contract requires the gateway to rotate signing
	// keys on a deployer-configurable schedule (default 24h) and keep
	// the previous key valid through a 5-minute overlap window so
	// tokens minted just before rotation continue to verify. The boot
	// key seeds the ring; the rotator goroutine (started below under
	// watchdogCtx) drives subsequent rotations and the overlap sweep.
	// spec: §7.1 line 67.
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		log.Fatalf("lenny-gateway: rand: %v", err)
	}
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "boot", Secret: seed[:]})
	uploadIssuer := uploadtoken.NewIssuer(ring, nil)
	uploadTracker := uploadtoken.NewMemoryTracker()
	uploadVerifier := uploadtoken.NewVerifier(ring, uploadTracker, nil)
	uploadRotator := uploadtoken.NewRotator(ring, uploadtoken.RotatorOptions{
		OnRotate: func(active, displaced uploadtoken.SigningKey) {
			log.Printf("lenny-gateway: §7.1 uploadToken signing key rotated; active=%s overlap=%s",
				active.KeyID, displaced.KeyID)
		},
		OnExpire: func(expired []string) {
			log.Printf("lenny-gateway: §7.1 uploadToken signing key(s) expired from overlap window: %v", expired)
		},
	})

	// ----- §4 KMS provider -----
	// The §4 / §12.9 envelope-encryption KEK seam. The gateway wraps
	// the §4.9 connector-credential DEKs through this provider; the
	// signing-key concern moved to the Token Service binary in
	// F-4.3.12. --kms-provider selects local | aws | gcp | azure;
	// `local` is rejected when --environment=prod.
	// spec: F-4.3.11, F-10.2.11, F-17.5.2.
	kmsProvider, err := providerflags.Resolve(context.Background(), *kmsOpts)
	if err != nil {
		log.Fatalf("lenny-gateway: kms provider: %v", err)
	}
	log.Printf("lenny-gateway: §4 KMS provider = %s (environment=%s)",
		kmsOpts.Provider, kmsOpts.Environment)

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
	// §13.3 canonical surface: the gateway does NOT mint tokens
	// in-process. The /v1/oauth/* HTTP path is reverse-proxied to
	// lenny-token-service per --token-service-http-url, so the
	// Token Service is the only component holding the signing key
	// and the only component writing token.exchanged audit rows.
	// spec: §4.3 line 193 "Canonical token endpoint" / F-4.3.12.

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
		credAssign       credassign.Assigner
		inProcessAssign  *credassign.Service
		tokenServiceConn *grpc.ClientConn
		// §4.9 admin-time RBAC live-probe. Set only when the Token
		// Service link is present; the probe is Token-Service-owned and
		// has no meaning without that link.
		secretProber admin.SecretAccessProber
	)
	// §4.3 line 211 per-subsystem circuit breaker for Token Service
	// calls. A degraded Token Service trips this breaker open after
	// consecutive transient failures; the credassign client returns
	// ErrTokenServiceUnavailable so the session-start path can surface
	// the §4.3 retryable error.
	tokenServiceSubsystem := &subsystem.Subsystem{
		Name:    "token_service",
		Breaker: &subsystem.Breaker{},
	}
	if *tokenServiceAddr != "" {
		conn, err := dialTokenService(*tokenServiceAddr, *tokenServiceCert, *tokenServiceKey, *tokenServiceCA)
		if err != nil {
			log.Fatalf("lenny-gateway: dial Token Service %q: %v", *tokenServiceAddr, err)
		}
		tokenServiceConn = conn
		credAssign = credassign.NewClient(credassign.ClientOptions{
			Stub:      tokensv1.NewTokenServiceClient(conn),
			Leases:    llmLeases,
			Creds:     credCache,
			TenantID:  *tokenServiceTenant,
			Subsystem: tokenServiceSubsystem,
		})
		// §4.9 line 1212: the admin credential-pool handlers probe Token
		// Service Secret-read access over this same mTLS link before
		// persisting a new secretRef.
		secretProber = &tokenServiceSecretProber{stub: tokensv1.NewTokenServiceClient(conn)}
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
		// §5.2 atomic slot counter. When Redis is wired, every
		// concurrent-mode slot reservation goes through the Redis Lua
		// GET-compare-INCR sequence so two gateway replicas racing on
		// the same pod cannot transiently exceed maxConcurrent. With
		// no Redis the SlotClaimer falls back to a race-prone SSA-only
		// path that is unsafe for the production envelope; the
		// fallback is retained only for tier-2 unit tests.
		var slotCounter *slotcounter.Counter
		if redisClient != nil {
			// §5.2 line 521: the SessionStore is the post-recovery
			// rehydration seed source. After a Redis restart the first
			// slot reservation on each concurrent-mode pod re-seeds the
			// pod's active_slots counter from GetActiveSlotsByPod before
			// any new slot is allowed, closing the over-commit race.
			slotCounter = slotcounter.New(redisClient, slotcounter.WithSlotSource(sessions))
		}
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
			// §5.2 atomic slot counter (Redis-backed); nil falls back
			// to the SSA-only path documented on SlotClaimer.
			SlotCounter: slotCounter,
		}
		// §4.6.1 Postgres-backed fallback claim: when Postgres is
		// configured the binder reads the agent_pod_state mirror to
		// claim a pod after the Kubernetes-API claim finds none. Without
		// Postgres the Fallback field stays nil and the no-idle-pod
		// result surfaces directly.
		if pgPool != nil {
			podBinder.Fallback = agentpodstatepg.New(pgPool)
			// §4.6.1 precondition 2: probe API-server reachability (GET
			// /readyz) before each fallback claim and skip when it fails.
			probe, err := podsession.NewReadyzProbe(cfg)
			if err != nil {
				log.Fatalf("lenny-gateway: build pod-claim fallback readyz probe: %v", err)
			}
			podBinder.APIServerReachable = probe
			log.Printf("lenny-gateway: §4.6.1 Postgres-backed pod-claim fallback enabled")
		}
		exec = executor.NewPodExecutor(podRegistry, podBinder)
		checkpointSvc = &checkpointer.Checkpointer{
			Sessions:       sessions,
			Registry:       podRegistry,
			Interval:       *checkpointInterval,
			JitterFraction: *checkpointJitterFraction,
			OnError: func(sessionID string, err error) {
				log.Printf("lenny-gateway: checkpoint of session %s failed: %v", sessionID, err)
			},
			// §4.4 line 234 / §12.5 — record the snapshot ref to the
			// retention catalog and Rotate so the table never holds
			// more than the latest-2 active rows per session. The
			// Metrics field is wired after gatewaymetrics.New() below
			// so the §4.4 line 254 duration histogram is emitted.
			Retention: checkpointRetention,
		}
		log.Printf("lenny-gateway: placing sessions on warm pods in namespace %q", *agentNamespace)
	}

	// §7.1 seal-and-export uses the same checkpointer; an untyped-nil
	// Sealer keeps seal-and-export disabled without --agent-namespace.
	var sessionSealer sessionserver.Sealer
	if checkpointSvc != nil {
		sessionSealer = checkpointSvc
	}

	eventBus := sessionevents.NewBus(0)
	// §4.4 line 225 / §12.3.7: when Redis is wired, attach the
	// cross-replica relay so a client reconnecting via Last-Event-ID
	// to a different replica sees prior events (the §15.1 streaming-
	// reconnect contract). Single-replica dev mode keeps the Bus's
	// in-memory-only behaviour.
	if redisClient != nil {
		eventBus = eventBus.WithRedisRelay(sessionevents.NewRedisRelay(redisClient))
		log.Printf("lenny-gateway: §4.4 session SSE event bus relay attached to Redis (cross-replica replay enabled)")
	}
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
	// §4.6.1: record fallback-claim skips on the gateway metrics registry.
	// Wired after gatewaymetrics.New() because the binder is constructed
	// earlier in the agent-namespace block.
	if podBinder != nil {
		podBinder.FallbackSkipped = gwMetrics.IncPodClaimFallbackSkipped
		// §5.2 line 519: record concurrent-mode slot-contention conflicts
		// on lenny_slot_assignment_conflict_total so operators can detect
		// pool under-sizing.
		podBinder.SlotConflict = gwMetrics.IncSlotAssignmentConflict
		// §5.2 line 12: record concurrent-workspace slot bind failures on
		// lenny_slot_failure_total (error_type, pool, k8s_pod_name).
		podBinder.SlotFailure = gwMetrics.IncSlotFailure
		// §5.2 line 521: record post-recovery slot-counter rehydration
		// events on lenny_slot_rehydration_total (pod, pool).
		podBinder.Rehydration = gwMetrics.IncSlotRehydration
	}
	// §16.1 lines 51, 53, 55: emit credential-lease assignment, lease
	// duration, and pool-utilization telemetry from the in-process
	// assignment service. The Token Service client path emits its own
	// §16.1 metrics on its registry.
	if inProcessAssign != nil {
		inProcessAssign.SetMetrics(gwMetrics)
	}
	// §4.1 / §16.1: emit the per-replica capacity ceiling as a startup-set
	// gauge so the §16.5 GatewaySessionBudgetNearExhaustion alert can
	// divide active sessions by it. The spec requires both delivery_mode
	// values be reported per replica; until a separate
	// maxSessionsPerReplicaProxyMode setting exists, both labels carry
	// the same configured value so capacity-planning dashboards have a
	// non-NaN value for either mode.
	if *maxSessionsPerReplica <= 0 {
		log.Fatalf("lenny-gateway: --max-sessions-per-replica must be > 0 (got %d) (§4.1)", *maxSessionsPerReplica)
	}
	gwMetrics.SetMaxSessionsPerReplica("direct", *maxSessionsPerReplica)
	gwMetrics.SetMaxSessionsPerReplica("proxy", *maxSessionsPerReplica)

	// §4.4 line 254 — late-binding the checkpointer's duration
	// histogram emitter to the freshly-constructed gateway metrics.
	// The checkpointer is constructed before gwMetrics so the Sealer
	// can flow into the session-server, so the Metrics field is wired
	// here once the registry is live.
	if checkpointSvc != nil {
		checkpointSvc.Metrics = gwMetrics
	}

	// §12.5 ll. 303 — wire the MinIO blob store's fail-closed T4
	// KMS-unavailable callback to the gateway metrics emitter. Every
	// ErrClassificationControlViolation the blob store raises bumps
	// `lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}`
	// so the CheckpointStorageUnavailable alert fires under the
	// outage. The handler also logs the rejection at INFO so operators
	// see the tenant id without spelunking through the bucket-side
	// access logs.
	if minioStore != nil {
		minioStore.SetOnKMSUnavailable(func(tenantID string) {
			gwMetrics.IncCheckpointKMSUnavailable()
			log.Printf("lenny-gateway: §12.5 ll. 303 CLASSIFICATION_CONTROL_VIOLATION: tenant=%s KMS key unavailable", tenantID)
		})
		// §12.5 line 282 — surface every retry-exhausted MinIO PUT
		// failure on lenny_artifact_upload_error_total and roll the
		// same signal into lenny_checkpoint_storage_failure_total so
		// the §16.5 MinIOUnavailable and CheckpointStorageUnavailable
		// alerts fire from one source of truth.
		minioStore.SetOnArtifactUploadError(func(tenantID, errorType string) {
			gwMetrics.IncArtifactUploadError(tenantID, errorType)
		})
		// §12.8 line 735 — wire the durable artifact_store catalog as
		// the legal-hold source of truth on DeleteBySession. The
		// in-memory legalHolds sync.Map remains a v1 fallback for the
		// catalog-less dev gateway; production reads the durable row.
		if artifactCatalog != nil {
			minioStore.SetCatalog(artifactCatalog)
			log.Printf("lenny-gateway: §12.8 line 735 durable legal-hold reader wired into MinIO blob store")
		}

		// §12.5 ll. 297 startup KMS probe: when at least one T4 tenant
		// is configured, probe a sample alias so a chronic
		// misconfiguration surfaces in startup logs. The gateway does
		// NOT fail startup — production may bring the gateway up before
		// every KMS alias is provisioned; the warning is the operator
		// signal.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			rows, err := tenants.List(ctx, tenantstore.ListFilter{})
			if err != nil {
				log.Printf("lenny-gateway: §12.5 startup T4 KMS probe: list tenants: %v", err)
				return
			}
			for _, t := range rows {
				if t.WorkspaceTier != tenantkms.WorkspaceTierT4 {
					continue
				}
				alias := tenantkms.AliasFor(t.ID)
				if _, perr := kmsProvider.CurrentKEKVersion(ctx, alias); perr != nil {
					log.Printf("lenny-gateway: §12.5 startup T4 KMS probe WARN: tenant=%s alias=%s unreachable: %v",
						t.ID, alias, perr)
				} else {
					log.Printf("lenny-gateway: §12.5 startup T4 KMS probe OK: tenant=%s alias=%s",
						t.ID, alias)
				}
				return // probe a single sample tenant only
			}
		}()
	}

	// §4.1 / §16.5: emit the configuration scalars the §16.5
	// GatewayNoHealthyReplicas and GatewayActiveStreamsHigh alert
	// expressions read via scalar(...). Each gauge is emitted per
	// replica; for replicaCount the spec recording rule sum() over
	// the fleet yields the fleet-wide ready-replica numerator.
	if *minReplicas <= 0 {
		log.Fatalf("lenny-gateway: --min-replicas must be > 0 (got %d) (§4.1 / §16.5)", *minReplicas)
	}
	if *streamCeiling <= 0 {
		log.Fatalf("lenny-gateway: --stream-ceiling must be > 0 (got %d) (§4.1 / §16.5)", *streamCeiling)
	}
	gwMetrics.SetMinReplicas(*minReplicas)
	gwMetrics.SetStreamCeiling(*streamCeiling)
	gwMetrics.SetReplicaCount(1)

	// §4.1 extractionThresholds: read the configured per-subsystem
	// thresholds from LENNY_EXTRACTION_THRESHOLD_* env vars (rendered
	// by charts/lenny/templates/gateway-deployment.yaml from
	// gateway.extractionThresholds Helm values) and emit each one
	// on the lenny_gateway_extraction_threshold gauge so the values
	// used for an extraction decision are auditable against /metrics.
	extractionthreshold.FromEnv().Emit(gwMetrics)

	// §4.1 per-subsystem metric family. Register the
	// lenny_gateway_subsystem_{request_duration_seconds, queue_depth,
	// circuit_state, errors_total} vectors against the gateway's
	// shared private registry so a Subsystem with its DoObserved path
	// wired surfaces samples on /metrics. The §4.1 alerts in §16.5
	// (GatewaySubsystemCircuitOpen, GatewayQueueDepthHigh,
	// GatewayLatencyHigh) read these vectors via the `subsystem` label.
	subsystemMetrics, err := subsystem.NewMetrics(gwMetrics.Registerer())
	if err != nil {
		log.Fatalf("lenny-gateway: subsystem metrics: %v", err)
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

	// §25.3 / §25.5 operational-event emitter, shared by the gateway
	// subsystems that emit and the admin event-buffer query endpoint.
	// Always keep a local buffer — the §25.3 buffer endpoint reads it
	// and the §25.5 fall-back path serves the same buffer when Redis is
	// unreachable. When Redis is wired, every emit also lands on the
	// §25.5 platform-scoped stream ops:events:stream so lenny-ops and
	// the controllers share the same logical event source.
	opsEventBuffer := events.NewEventBuffer(0)
	var opsEmitter events.EventEmitter = events.NewEmitter(opsEventBuffer, replica)
	if redisClient != nil {
		opsEmitter = events.NewStreamEmitter(events.StreamEmitterOptions{
			Client:    redisClient,
			Buffer:    opsEventBuffer,
			Source:    "//lenny.dev/gateway/" + replica,
			ReplicaID: replica,
		})
		log.Printf("lenny-gateway: §25.5 operational events streaming to Redis %s", events.DefaultStreamKey)
	}

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
	// §4.8 line 1030: arm cumulative fail-open escalation on every chain.
	// A fail-open interceptor that crosses the ceiling within the rolling
	// 5-minute window is auto-promoted to fail-closed; the transition
	// writes interceptor.fail_open_escalated to the tenant's audit chain.
	policyChain.SetFailOpenEscalation(*interceptorFailOpenMax, 0,
		policy.NewFailOpenObserver(auditAppender, nil), nil)
	// §4.8 line 972: AuthEvaluator (priority 100) is the sole built-in at
	// PreAuth. It runs in the auth middleware after the principal is
	// resolved as the fail-closed identity gate every later phase relies
	// on. "Always active" — registered unconditionally.
	if err := policyChain.Register(interceptor.PhasePreAuth, policy.NewAuthEvaluator()); err != nil {
		log.Fatalf("lenny-gateway: register AuthEvaluator: %v", err)
	}
	// §4.8 line 974: DelegationPolicyEvaluator (priority 250) fires at
	// PreDelegation only. It enforces the §8.3 contentPolicy.maxInputSize
	// cap on TaskSpec.input (INPUT_TOO_LARGE); the §8.3 depth/fan-out/
	// cycle/tag enforcement stays canonical in delegation.Service. The
	// default cap is operator-tunable via --delegation-max-input-size.
	if err := policyChain.Register(interceptor.PhasePreDelegation,
		policy.NewDelegationPolicyEvaluator(nil, *delegationMaxInputSize)); err != nil {
		log.Fatalf("lenny-gateway: register DelegationPolicyEvaluator: %v", err)
	}
	// §4.8 line 977: RetryPolicyEvaluator (priority 600) fires at PostRoute
	// only. It enforces the §7.3 automatic-retry budget — a session whose
	// retryCount has reached the effective retryPolicy.maxRetries is
	// rejected at routing rather than re-claiming a warm pod for an attempt
	// past its budget. The §7.3 resume-window timer stays canonical in the
	// watchdog. Backed by the session store; "always active" — registered
	// unconditionally.
	if err := policyChain.Register(interceptor.PhasePostRoute,
		policy.NewRetryPolicyEvaluator(sessionRetryLookup{sessions: sessions}, nil, *retryMaxRetries)); err != nil {
		log.Fatalf("lenny-gateway: register RetryPolicyEvaluator: %v", err)
	}
	log.Printf("lenny-gateway: §4.8 AuthEvaluator (PreAuth), DelegationPolicyEvaluator (PreDelegation, maxInputSize=%d), and RetryPolicyEvaluator (PostRoute, maxRetries=%d) registered", *delegationMaxInputSize, *retryMaxRetries)
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

	// §4.8 line 1019: register each deployer-supplied external
	// interceptor. The gateway dials the service's endpoint, builds the
	// generated RequestInterceptor client, and registers an External on
	// the named phase. Registration applies the reserved-priority
	// ceiling and the PreAuth restriction, so a misconfigured priority or
	// phase fails fast at startup rather than silently bypassing policy.
	for _, raw := range externalInterceptors {
		spec, err := interceptor.ParseExternalSpec(raw)
		if err != nil {
			log.Fatalf("lenny-gateway: --external-interceptor: %v", err)
		}
		conn, err := dialInterceptor(spec.Endpoint, *externalInterceptorTLSCert, *externalInterceptorTLSKey, *externalInterceptorCA)
		if err != nil {
			log.Fatalf("lenny-gateway: dial external interceptor %q at %s: %v", spec.Name, spec.Endpoint, err)
		}
		if _, err := policyChain.RegisterExternal(spec.Phase, interceptor.ExternalConfig{
			Name:       spec.Name,
			Endpoint:   spec.Endpoint,
			Priority:   spec.Priority,
			FailPolicy: spec.FailPolicy,
			Timeout:    spec.Timeout,
			Client:     interceptorv1.NewRequestInterceptorClient(conn),
		}); err != nil {
			log.Fatalf("lenny-gateway: register external interceptor %q on %s: %v", spec.Name, spec.Phase, err)
		}
		log.Printf("lenny-gateway: §4.8 external interceptor %q registered on %s (endpoint %s, priority %d)",
			spec.Name, spec.Phase, spec.Endpoint, spec.Priority)
	}

	// §4.8 line 1070: the GuardrailsInterceptor is the built-in hook for
	// a deployer-wired external content classifier, disabled by default.
	// When --guardrails-classifier is set the gateway dials the
	// classifier, wraps it at the fixed priority 400 across the guardrails
	// phases, and registers it; the spec's phase/priority fields are
	// ignored because §4.8 fixes them for this built-in.
	if *guardrailsClassifier != "" {
		spec, err := interceptor.ParseExternalSpec(*guardrailsClassifier + ",phase=" + string(interceptor.PhasePreLLMRequest))
		if err != nil {
			log.Fatalf("lenny-gateway: --guardrails-classifier: %v", err)
		}
		conn, err := dialInterceptor(spec.Endpoint, *externalInterceptorTLSCert, *externalInterceptorTLSKey, *externalInterceptorCA)
		if err != nil {
			log.Fatalf("lenny-gateway: dial guardrails classifier %q at %s: %v", spec.Name, spec.Endpoint, err)
		}
		classifier, err := interceptor.NewExternal(interceptor.ExternalConfig{
			Name:       spec.Name,
			Endpoint:   spec.Endpoint,
			Priority:   policy.GuardrailsInterceptorPriority,
			FailPolicy: spec.FailPolicy,
			Timeout:    spec.Timeout,
			Client:     interceptorv1.NewRequestInterceptorClient(conn),
		})
		if err != nil {
			log.Fatalf("lenny-gateway: build guardrails classifier %q: %v", spec.Name, err)
		}
		if err := policy.RegisterGuardrails(policyChain, policy.NewGuardrailsInterceptor(classifier)); err != nil {
			log.Fatalf("lenny-gateway: register GuardrailsInterceptor: %v", err)
		}
		log.Printf("lenny-gateway: §4.8 GuardrailsInterceptor enabled (classifier %q at %s, priority %d)",
			spec.Name, spec.Endpoint, policy.GuardrailsInterceptorPriority)
	}

	// §4.1 Upload Handler subsystem boundary. The Subsystem gates the
	// POST /v1/sessions/{id}/upload handler through a per-replica
	// breaker and concurrency semaphore so a saturated upload queue
	// cannot consume goroutines the Stream Proxy or MCP Fabric need.
	// The configured maxConcurrent matches the §4.1 extraction-
	// threshold default (uploadHandler.activeConcurrent: 200); the
	// breaker's FailureThreshold uses the package default until an
	// operator-tunable knob lands.
	uploadSubsystem := &subsystem.Subsystem{
		Name:    "upload_handler",
		Breaker: &subsystem.Breaker{},
		Limiter: &subsystem.Limiter{MaxConcurrent: int(extractionthreshold.FromEnv().UploadHandlerActiveConcurrent)},
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
		Usage:                   usage,
		Users:                   users,
		Billing:                 billing,
		Tenants:                 tenants,
		StorageQuota:            storageCounter,
		PodBinder:               podBinder,
		PodRegistry:             podRegistry,
		AgentNamespace:          *agentNamespace,
		DefaultIsolationProfile: isolation.Profile(*defaultIsolationProfile),
		DevMode:                 *devMode,
		Sealer:                  sessionSealer,
		// §4.4 line 236 — the resume path delegates partial-manifest
		// cleanup to this adapter. Deleter is nil for v1 (no chunk
		// uploader yet); when the §10.1 writer ships the chunk
		// deleter should be wired here.
		PartialManifestCleaner: &checkpointer.PartialCleaner{
			Store:   partialManifests,
			Metrics: gwMetrics,
		},
		// §4.4 line 226 session-log close-hook. Wired with the
		// per-deployment Store (Noop for in-memory, MinIOStore when
		// the §4.5 follow-on wiring lands a MinIO uploader). The
		// close-hook fires from the gateway's session-completion path;
		// the SessionLogStore drops or persists best-effort.
		SessionLogHook:  &sessionlogstore.CloseHook{Store: sessionLogs},
		TreeArchive:     treeArchive,
		Interceptors:    policyChain,
		PolicyAuditSink: policyAuditSink,
		// §7.1 / §16.6 — session lifecycle audit events to the §11.7
		// hash-chained log, written under the session's tenant.
		LifecycleAuditSink: sessionLifecycleAuditor{appender: auditAppender},
		// §7.1 line 77 — default artifact retention window.
		DefaultRetention: time.Duration(*sessionArtifactRetentionSeconds) * time.Second,
		// §7.1 line 112 — seal-and-export retry window + outcome histogram.
		WorkspaceSealMaxDuration:     time.Duration(*workspaceSealMaxDurationSeconds) * time.Second,
		ObserveWorkspaceSealDuration: gwMetrics.ObserveWorkspaceSealDuration,
		Clock:                        clockinject.Now,
		UploadSubsystem:              uploadSubsystem,
		// §4.9 line 1220 — the pre-claim availability check race metric.
		PreclaimMismatch: gwMetrics.IncCredentialPreclaimMismatch,
		// §6.3 lines 348, 372 — startup-latency histograms observed on
		// each successful pod-warm start.
		ObserveStartupDuration: gwMetrics.ObserveSessionStartupDuration,
		ObserveStartupPhase:    gwMetrics.ObserveSessionStartupPhase,
	})

	// ----- OpenAI Chat + Open Responses translators -----
	openaiHandler := translator.NewOpenAIChatHandler(sessions, exec, translator.OpenAIChatOptions{Clock: clockinject.Now})
	responsesHandler := translator.NewOpenResponsesHandler(sessions, exec, translator.OpenResponsesOptions{Clock: clockinject.Now})

	// ----- §4.9 end-user credential registry -----
	// The Postgres-backed store envelope-encrypts the §12.9 T4 secret
	// column under per-tenant KMS KEKs; the in-memory store keeps the
	// secret process-local and never persists it.
	var credentials credentialstore.Store = credentialstore.NewMemory(nil)
	// §4.9.1 re-encryption job: the envelope-backed stores re-key their
	// rows under the current KEK version after a rotation. Only the
	// Postgres stores have a KEK to rotate; the in-memory stores hold
	// plaintext.
	var credentialRekeyers []rekey.TenantRekeyer
	if pgPool != nil {
		pgCreds, perr := credentialpg.New(pgPool, kmsProvider)
		if perr != nil {
			log.Fatalf("lenny-gateway: credential store: %v", perr)
		}
		credentials = pgCreds
		credentialRekeyers = append(credentialRekeyers, pgCreds)
	}
	credServer := credentialserver.New(credentials).
		WithAudit(credentialAuditor{sink: auditSink})

	// ----- §9.3 connector OAuth 2.1 authorization-code flow -----
	// The connector-credential store holds the access/refresh tokens a
	// completed connector OAuth flow produces, keyed by the
	// (tenant, connector, user) triple. The in-memory store keeps the
	// tokens process-local; a Postgres-backed store envelope-encrypts
	// them under the same per-tenant KMS KEKs the credential store
	// uses. The flow is wired only when --connector-oauth-callback-url
	// is set: the OAuth provider needs an absolute redirect URI.
	// §4.3 line 200 / §13.3 connector OAuth tokens are T4 Restricted
	// and must be envelope-encrypted at rest. The Postgres-backed store
	// envelope-encrypts both access and refresh tokens under the
	// per-tenant KMS KEK; the in-memory store is for tests and the
	// minimal gateway. The §4.3 long-term trust-boundary tightening —
	// routing connector credential reads/writes through a Token Service
	// RPC so the gateway holds no KMS decrypt grant — is deferred (see
	// F-4.3.1 resolution note); today the gateway holds the same KMS
	// access for connector creds that it already holds for §4.9
	// user-credential rows.
	var connectorCreds connectorcredstore.Store = connectorcredstore.NewMemory(nil)
	if pgPool != nil {
		pgConnectorCreds, err := connectorcredpg.New(pgPool, kmsProvider, nil)
		if err != nil {
			log.Fatalf("lenny-gateway: connector-credential store: %v", err)
		}
		connectorCreds = pgConnectorCreds
		credentialRekeyers = append(credentialRekeyers, pgConnectorCreds)
		log.Printf("lenny-gateway: §4.3 connector credentials backed by Postgres (envelope-encrypted)")
	}
	// §4.9.1 KMS-key-rotation re-encryption job over every envelope-backed
	// credential store. Wired to the admin router below; absent in the
	// in-memory dev posture (no store to re-key).
	var credentialRekeyJob *rekey.Job
	if len(credentialRekeyers) > 0 {
		credentialRekeyJob = rekey.NewJob(credentialRekeyers...)
	}
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
	delegationSvc := delegation.NewService(sessions, delegation.Options{
		Experiments: experiments,
		Runtimes:    runtimes,
		Clock:       clockinject.Now,
		// §8.2 / §16.1: the delegation service emits
		// `lenny_delegation_depth` and
		// `lenny_delegation_would_have_blocked_total` through the
		// gateway metrics registry.
		Metrics: gwMetrics,
	})
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:                      sessions,
		Executor:                   exec,
		DevMode:                    *devMode,
		Delegation:                 delegationSvc,
		Runtimes:                   runtimes,
		Environments:               environments,
		Tenants:                    tenants,
		Pools:                      pools,
		Audit:                      mcpDelegationAuditor{sink: auditSink},
		DefaultNoEnvironmentPolicy: resolvedNoEnvPolicy,
		Interceptors:               policyChain,
		PolicyAudit:                policyAuditSink,
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
	credRenewal := newCredRenewalWiring(credAssign, podRegistry, opsEmitter)
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
	adminRouter := admin.NewRouter(tenants, admin.Options{Clock: clockinject.Now, Audit: auditSink, Metrics: gwMetrics, DevMode: *devMode}).
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
		WithEventBuffer(opsEventBuffer).
		WithEventEmitter(opsEmitter).
		WithOperationsInventory(operations.New())
	// §5.2 line 629: surface live poolCondition / idlePodCount on the
	// admin pool GET when the gateway has a Kubernetes client (an
	// agent-namespace deployment). The minimal Postgres-only posture
	// leaves the reader unwired and the fields are omitted.
	if podBinder != nil && podBinder.Client != nil {
		adminRouter = adminRouter.WithPoolStatusReader(podsession.PoolStatusLookup{
			Reader:    podBinder.Client,
			Namespace: podBinder.Namespace,
		})
	}
	if *elicitationFloor != "" {
		adminRouter = adminRouter.WithElicitationFloor(*elicitationFloor)
	}
	// §6.2 line 260 / §11.3 line 219: pin the admin runtime validator
	// to the same outer bound the finalizing-state watchdog enforces, so
	// the §15.1 POST/PUT /v1/admin/runtimes and POST /v1/admin/bootstrap
	// handlers reject a runtime whose setupPolicy.timeoutSeconds exceeds
	// gateway.maxFinalizingTimeoutSeconds.
	adminRouter = adminRouter.WithMaxFinalizingTimeoutSeconds(watchdog.DefaultMaxFinalizingStateSeconds)
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
	// §4.9 emergency credential revocation lease terminator. Wired when
	// the lease store exposes per-credential lookup (both backends do);
	// it reuses the credential-lease revocation propagator so a revoked
	// pool credential is denied on every replica, mirroring the §11.4
	// full_revoke fan-out's deny-list path.
	if ls, ok := llmLeases.(poolLeaseStore); ok {
		adminRouter = adminRouter.WithPoolCredentialRevocation(
			&poolCredentialRevoker{leases: ls, denyList: credRenewalProp})
	}
	// §4.9.1 KMS-key-rotation re-encryption admin surface. Registered
	// only when at least one envelope-backed store is wired (Postgres).
	if credentialRekeyJob != nil {
		adminRouter = adminRouter.WithCredentialRekey(credentialRekeyJob)
	}
	// §4.9 admin-time RBAC live-probe on credential-pool writes. Wired
	// only when the Token Service link is present.
	if secretProber != nil {
		adminRouter = adminRouter.WithSecretAccessProber(secretProber)
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
		_, _ = opsEmitter.Emit(context.Background(), events.OperationalEvent{
			Source:          "/v1/admin/health",
			Type:            events.EventHealthStatusChanged.CloudEventsType(),
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
	// §4.3 line 193 canonical token endpoint. The gateway reverse-
	// proxies /v1/oauth/* to lenny-token-service so the Token Service
	// is the actual minter for every Lenny bearer token. When the
	// flag is empty (dev path without a Token Service binary), the
	// /v1/oauth surface is unmounted; callers receive 404 from the
	// gateway. spec: F-4.3.12.
	if *tokenServiceHTTPURL != "" {
		proxy, err := tokensvcproxy.New(*tokenServiceHTTPURL)
		if err != nil {
			log.Fatalf("lenny-gateway: §4.3 token-service reverse proxy: %v", err)
		}
		mux.Handle("/v1/oauth/", proxy.Handler())
		log.Printf("lenny-gateway: §4.3 /v1/oauth/* reverse-proxied to %s", *tokenServiceHTTPURL)
	}

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
	// §15.0 ExternalAdapterRegistry. Each built-in adapter registers
	// through the registry and the registry mounts its HTTPHandler on
	// the shared mux. The §15.0 admin-API runtime-registration path
	// uses the same registry so a third-party adapter takes effect
	// without a gateway restart.
	adapterReg := adapterregistry.New()
	if err := adapterReg.Register(adapterregistry.NewSimpleAdapter(
		"openai-completions",
		openaiHandler.Handler(),
		gwadapter.Capabilities{PathPrefix: "/v1/chat/completions", Protocol: "openai-completions"},
	)); err != nil {
		log.Fatalf("lenny-gateway: §15.0 adapter registry: %v", err)
	}
	if err := adapterReg.Register(adapterregistry.NewSimpleAdapter(
		"open-responses",
		responsesHandler.Handler(),
		gwadapter.Capabilities{PathPrefix: "/v1/responses", Protocol: "open-responses"},
	)); err != nil {
		log.Fatalf("lenny-gateway: §15.0 adapter registry: %v", err)
	}
	if err := adapterReg.Register(adapterregistry.NewSimpleAdapter(
		"mcp",
		mcpSrv.Handler(),
		gwadapter.Capabilities{PathPrefix: "/mcp", Protocol: "mcp", SupportsElicitation: true},
	)); err != nil {
		log.Fatalf("lenny-gateway: §15.0 adapter registry: %v", err)
	}
	adapterReg.Mount(mux)
	// §4.1 long-lived interactive streams: the MCP WebSocket transport
	// is mounted alongside the POST /mcp single-shot handler so the
	// playground UI (pkg/gateway/playground/assets.go WSPath
	// /mcp/v1/ws) and any other MCP-over-WebSocket client land on the
	// same dispatch logic. The Streamable HTTP path remains on /mcp.
	mux.Handle("/mcp/v1/ws", mcpSrv.WebSocketHandler())
	// §4.1 dedicated MCP endpoints for type:mcp runtimes. The
	// dispatcher is nil in v1: every request that passes runtime
	// type validation surfaces RUNTIME_UNAVAILABLE per §15.2.1
	// while preserving the spec-required 404 / 400 error patterns
	// for unknown and non-mcp runtimes.
	mux.Handle("POST /mcp/runtimes/{name}", mcpruntimes.New(runtimes, nil))
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

	// ----- §12.5 line 291 node.drain.forced audit endpoint (unauthenticated) -----
	// The webhook POSTs here on a drain-force override admission so the
	// §16.7 audit event lands in the per-tenant §11.7 hash chain. The
	// endpoint sits on the gateway's internal port (same NetworkPolicy
	// scope as drain-readiness) and is unauthenticated at the HTTP layer
	// because the webhook's egress NetworkPolicy is the only path that
	// can reach it.
	if auditAppender != nil {
		mux.Handle("POST /internal/audit/node-drain-forced", &drainreadiness.ForcedDrainHandler{
			Appender:       auditAppender,
			Metrics:        gwMetrics,
			PlatformTenant: "platform",
		})
	}

	// ----- §4.4 / §10.1 preStop drain hook (unauthenticated) -----
	// Kubernetes invokes this via lifecycle.preStop.httpGet when a
	// gateway pod is scheduled for termination. The hook reads the
	// terminationGracePeriodSeconds budget, selects the §10.1 tiered
	// checkpoint cap per coordinated session, and triggers a
	// synchronous eviction checkpoint per session. Each cap selection
	// bumps lenny_prestop_cap_selection_total labeled by source so
	// the §16.5 PreStopCapFallbackRateHigh alert can fire.
	//
	// spec: §4.4 line 234, 263 / §10.1 preStop staged drain.
	var prestopCheckpointer prestop.CheckpointTrigger
	if checkpointSvc != nil {
		prestopCheckpointer = checkpointSvc
	}
	prestopHook := &prestop.Hook{
		Sessions: &prestop.RegistryEnumerator{
			Registry:    podRegistry,
			DefaultPool: "default",
		},
		Checkpoint:        prestop.CheckpointFnFor(prestopCheckpointer),
		Metrics:           gwMetrics,
		ServiceInstanceID: replica,
		GracePeriod:       parseTerminationGrace(),
		Logf:              func(format string, args ...any) { log.Printf(format, args...) },
	}
	mux.Handle("POST /internal/prestop", prestopHook)
	mux.Handle("GET /internal/prestop", prestopHook)

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
	// spec: §11.6 line 327 / §16.7 — on a breaker match the gate emits
	// the `admission.circuit_breaker_rejected` audit row carrying the
	// authenticated caller identity (resolved by the auth middleware,
	// which wraps outside this gate), sampled per-(tenant, circuit,
	// caller) over a 10s window per replica.
	cbAudit := cbmw.NewAuditReporter(auditAppender, gwMetrics, replica, nil)
	handler = cbmw.Wrap(handler, breakers, cbmw.Options{
		Audit: cbAudit,
		Snapshot: func(r *http.Request) cbmw.RejectionSnapshot {
			snap := cbmw.RejectionSnapshot{}
			if p, ok := authmw.FromContext(r.Context()); ok {
				snap.CallerSub = p.Subject
				snap.CallerTenantID = p.TenantID
				snap.SessionID = p.SessionID
			}
			return snap
		},
	})

	// §11.1 rate limiting next — runs just after auth so the per-user
	// scope sees the authenticated principal. Limits default to zero
	// (disabled); operators set them via the rate-limit flags. Metrics
	// wires the §11.1 line 7 rejection counter and the §16.5
	// RateLimitDegraded fail-open gauge.
	handler = ratelimitmw.Wrap(handler, ratelimitmw.Options{
		Counter:          rateLimiter,
		GlobalPerMinute:  *rlGlobalPerMin,
		PerUserPerMinute: *rlPerUserPerMin,
		Metrics:          gwMetrics,
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
		// §4.2 line 185: every tenant-claim rejection writes an
		// auth_failure audit row alongside the INFO log line.
		AuthFailureSink: authFailureAuditAdapter{sink: auditSink},
		// §4.8 line 1046: run the PreAuth chain (AuthEvaluator) after the
		// principal resolves and before the request reaches the handler.
		Interceptors: policyChain,
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
	llmTranslators := buildLLMTranslatorRegistry(llmTranslatorConfig{
		anthropicVersion: *anthropicVersion,
		openaiBaseURL:    *openaiBaseURL,
		openaiOrg:        *openaiOrg,
		bedrockRegion:    *bedrockRegion,
		vertexRegion:     *vertexRegion,
		vertexProject:    *vertexProject,
		azureEndpoint:    *azureEndpoint,
		azureAPIVersion:  *azureAPIVersion,
	})
	// §4.9 lines 1542-1556: the semantic cache is opt-in per pool. When
	// --llm-semantic-cache is set the gateway provisions the in-process
	// backend and the proxy consults it for any pool whose cachePolicy is
	// enabled; left off, the proxy path is uncached regardless of pool
	// policy. Per-user scope (the §4.9 default) keys on the session's
	// owning user, resolved from the session store.
	var llmCache llmproxy.ProxyCache
	if *llmSemanticCache {
		store := semanticcache.NewInMemory(nil, 0, 0, clockinject.Now)
		llmCache = proxycache.New(credentialPools, store, sessionUserLookup{sessions})
	}
	llmProxySrv := newLLMProxyServer(*llmProxyAddr, llmTranslators, llmLeases, credCache, credDeny, policyChain, llmCache, gwMetrics)

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
	// §5.2 line 519 / §6.2: a session forced terminal by background sweep
	// must run the full gateway-side terminal pipeline — workspace seal,
	// executor release (concurrent-mode slot release + pod drain), audit,
	// SSE, billing, archive — so the watchdog-driven path emits the same
	// signals exactly once as the REST-driven terminal path. Closes
	// F-5.2.26.
	wd := watchdog.New(sessions, tenantsLister{tenants}, watchdog.Config{}, nil).
		WithBilling(billing).
		WithTreeArchive(treeArchive).
		WithTerminalHook(sessionSrv)
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

	// §7.1 line 67 uploadToken signing-key rotator. The default
	// cadence rotates every 24h with a 5-minute overlap window; the
	// rotator both installs the new key and sweeps overlap keys whose
	// deadline has elapsed. spec: §7.1 line 67.
	go uploadRotator.Run(watchdogCtx)

	// ----- §7.1 abandoned `created`-state row sweep -----
	// Drops Session rows that stay in `created` past
	// maxCreatedStateTimeoutSeconds (default 300s). The §7.1 line 58
	// uploadToken TTL closes the upload window at that instant; without
	// this sweep the row itself lived forever, so abandoned creates
	// accumulated under repeated client retries.
	// spec: §7.1 line 58.
	createdGC := createdsweeper.New(sessions, tenantsLister{tenants}, createdsweeper.Options{
		Clock: clockinject.Now,
	})
	go createdGC.Run(watchdogCtx, func(dropped int, err error) {
		if err != nil {
			log.Printf("lenny-gateway: §7.1 created-state sweep error: %v", err)
			return
		}
		if dropped > 0 {
			log.Printf("lenny-gateway: §7.1 created-state sweep dropped %d abandoned rows past the upload-token deadline",
				dropped)
		}
	})

	// ----- §8.10 orphan-cleanup job -----
	orphanSweeper := orphancleanup.New(sessions, tenantsLister{tenants}, orphancleanup.Options{
		Archive: treeArchive,
		Clock:   clockinject.Now,
		// F-5.2.26: same terminal pipeline as the watchdog so an orphan
		// terminated by background sweep also releases its slot/pod.
		Terminal: sessionSrv,
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
	//
	// When the §12.5 artifact_store catalog is wired (Postgres path),
	// the blobs deleter transitions catalog rows to `soft_deleted` with
	// the §12.5 tombstone retention deadline rather than deleting them
	// outright. A background goroutine runs the §12.5 ll. 341 hard-prune
	// sweep on the same cadence so rows past their deadline (and their
	// matching bucket objects) are physically removed. In-memory dev
	// mode, where no catalog is wired, retains the legacy direct-delete
	// path.
	{
		var arts []retentiongc.Artifact
		if te, ok := transcripts.(sessionArtifactDeleter); ok {
			arts = append(arts, retentiongc.Artifact{Name: "transcripts", Delete: te.DeleteBySession})
		}
		if blobsCataloged != nil {
			// §12.5 ll. 311-313: soft-delete the catalog rows + bucket
			// objects through the cataloging decorator instead of
			// removing them outright. The hard-prune pass below runs on
			// the same Run loop and bumps lenny_gc_tombstones_pruned_total.
			tombstoneRetention := blobstore.DerivedSnapshotTTL // 7 days, matches §12.5 default
			arts = append(arts, retentiongc.Artifact{
				Name: "artifacts",
				Delete: func(ctx context.Context, tenantID, sessionID string) (int, error) {
					return blobsCataloged.SoftDeleteSession(ctx, tenantID, sessionID, tombstoneRetention)
				},
			})
		} else if be, ok := blobs.(sessionArtifactDeleter); ok {
			arts = append(arts, retentiongc.Artifact{Name: "artifacts", Delete: be.DeleteBySession})
		}
		retGC := retentiongc.New(sessions, tenantsLister{tenants}, arts, retentiongc.Options{
			Clock:   clockinject.Now,
			Metrics: gwMetrics,
		})
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

		// §12.5 ll. 341 hard-prune sweep: every retentiongc.DefaultSweepInterval
		// the catalog removes rows whose tombstone deadline has elapsed
		// and emits the count to lenny_gc_tombstones_pruned_total.
		// Production runs this under the §10.1 leader lease; the dev-
		// mode in-memory deployment has no Postgres catalog and skips
		// the sweep entirely.
		if blobsCataloged != nil {
			go func() {
				ticker := time.NewTicker(retentiongc.DefaultSweepInterval)
				defer ticker.Stop()
				for {
					select {
					case <-watchdogCtx.Done():
						return
					case <-ticker.C:
						count, err := blobsCataloged.HardPrune(watchdogCtx, clockinject.Now())
						if err != nil {
							log.Printf("lenny-gateway: §12.5 hard-prune sweep error: %v", err)
							continue
						}
						gwMetrics.AddGCTombstonesPruned(count)
						if count > 0 {
							log.Printf("lenny-gateway: §12.5 hard-prune removed %d tombstoned artifacts past retention",
								count)
						}
					}
				}
			}()
		}

		// §12.8 line 739 legal-hold reconciler: co-located with the
		// retention-GC sweep. On the same cadence it scans for
		// (tenant, session) pairs under legal_hold=true with one or
		// more checkpoints rotated, emits a
		// legal_hold.checkpoint_gap_detected audit event into the
		// per-tenant §11.7 chain, and bumps
		// lenny_legal_hold_checkpoint_gaps_total. The reconciler is
		// active only when the durable catalog and audit chain are
		// wired (Postgres mode); the in-memory dev gateway has no
		// catalog and skips it.
		if artifactCatalog != nil && auditAppender != nil {
			recon := legalholdreconciler.New(artifactCatalog, auditAppender, gwMetrics, legalholdreconciler.Options{
				Clock: clockinject.Now,
			})
			go recon.Run(watchdogCtx, func(emitted int, err error) {
				if err != nil {
					log.Printf("lenny-gateway: §12.8 legal-hold reconciler sweep error: %v", err)
					return
				}
				if emitted > 0 {
					log.Printf("lenny-gateway: §12.8 legal-hold reconciler emitted %d checkpoint-gap audit rows", emitted)
				}
			})
		}
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

	// ----- §4.4 freshness-reaper loop -----
	// Scans every active session and populates the per-(pool, level)
	// `lenny_checkpoint_stale_sessions` gauge so the §16.5
	// CheckpointStale alert can fire when any pool reports a non-zero
	// stale count for > 60 s. The reaper runs on every gateway
	// replica (not only the one with --agent-namespace) because the
	// freshness signal is platform-wide and the read path against the
	// session store is cheap.
	// spec: §4.4 line 256.
	freshnessReaper := &checkpointer.FreshnessReaper{
		Tenants:  tenantsLister{tenants},
		Sessions: sessions,
		Gauge:    gwMetrics,
		Interval: *checkpointInterval,
		OnError: func(tenantID string, err error) {
			if tenantID == "" {
				log.Printf("lenny-gateway: freshness reaper: list tenants: %v", err)
				return
			}
			log.Printf("lenny-gateway: freshness reaper: tenant %s: %v", tenantID, err)
		},
	}
	go freshnessReaper.Run(watchdogCtx)

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

	// ----- §4.9 credential deny-list startup rebuild -----
	// Seeds the per-replica credential deny list from the credential
	// stores' revoked entries, so a replica started immediately after a
	// pool-credential revocation denies that credential on the upstream
	// path even if it missed the original Redis pub/sub notification. The
	// rebuild is authoritative (Reset) and runs once at startup, where
	// the list is empty; it is not periodic because a periodic Reset
	// would drop the entries the live §11.4 revocation path adds for
	// pool credentials not yet reflected in the store query. The §4.9
	// union's user-credential term is vacuous today — no user-backed
	// lease is minted at session creation yet — so only the
	// pool-credential side is seeded.
	//
	// spec: §4.9 lines 1668-1673.
	{
		revoked, err := credentialPools.RevokedCredentials(context.Background())
		if err != nil {
			log.Printf("lenny-gateway: §4.9 credential deny-list startup rebuild failed: %v", err)
		} else {
			keys := make([]credential.CredentialKey, 0, len(revoked))
			for _, rc := range revoked {
				keys = append(keys, credential.CredentialKey{
					Source:       credential.SourcePool,
					PoolID:       rc.PoolName,
					CredentialID: rc.CredentialID,
				})
			}
			credDeny.Reset(keys)
			if len(keys) > 0 {
				log.Printf("lenny-gateway: §4.9 credential deny list rebuilt with %d revoked credential(s)", len(keys))
			}
		}
	}

	// ----- §11.5 idempotency-key TTL garbage collection -----
	// Reclaims idempotency_keys rows past the 24-hour retention window
	// so the durable key cache stays bounded. The cadence is operator
	// tunable via --idempotency-gc-interval-seconds (default 3600s).
	if pgPool != nil {
		idemGC := idempgstore.New(pgPool)
		lister := tenantsLister{tenants}
		gcInterval := time.Duration(*idempotencyGCIntervalSeconds) * time.Second
		if gcInterval <= 0 {
			gcInterval = time.Hour
		}
		sweepIdempotencyKeys(context.Background(), idemGC, lister)
		go func() {
			ticker := time.NewTicker(gcInterval)
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

	// §4.1 SCL-026 HPA scale-out gauges. Polled on a 5s cadence so the
	// custom-metrics pipeline (Prometheus Adapter / KEDA) observes
	// back-pressure quickly enough to scale before the saturation
	// threshold is reached. The primary trigger
	// (lenny_gateway_request_queue_depth) is the dominant signal here;
	// active streams and active sessions feed the secondary HPA metric
	// and the §16.5 GatewaySessionBudgetNearExhaustion alert.
	hpaTenantLister := tenantsLister{tenants}
	exportHPAGauges(context.Background(), sessions, hpaTenantLister, eventBus, gwMetrics)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-watchdogCtx.Done():
				return
			case <-ticker.C:
				exportHPAGauges(watchdogCtx, sessions, hpaTenantLister, eventBus, gwMetrics)
			}
		}
	}()

	// §4.1 process-level GC pause sampler. Reads runtime/debug.ReadGCStats
	// every gcpause.DefaultInterval seconds, maintains a sliding window
	// (gcpause.DefaultWindow), computes the p99 in milliseconds, and
	// pushes the value to the lenny_gateway_gc_pause_p99_ms gauge. The
	// §16.5 Tier3GCPressureHigh alert reads the fleet-wide aggregate
	// (`max(...)`) of this gauge to gate Tier 3 promotion.
	gcCollector := &gcpause.Collector{Gauge: gwMetrics}
	go gcCollector.Run(watchdogCtx)

	// §4.1 per-subsystem state publisher. Periodically reads the
	// queue depth, in-flight count, and circuit state from every
	// wired Subsystem and pushes the values to the
	// lenny_gateway_subsystem_{queue_depth, circuit_state} gauges
	// so the §16.5 alerts observe back-pressure even when the
	// handler path uses Breaker.Allow / Limiter.TryAcquire directly
	// (the DoObserved per-call path covers histograms / counters).
	subsystems := []*subsystem.Subsystem{uploadSubsystem, tokenServiceSubsystem}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		publish := func() {
			for _, s := range subsystems {
				s.PublishGauges(subsystemMetrics)
			}
			// §4.3 line 211: mirror the Token Service subsystem's
			// breaker state onto the dedicated
			// lenny_token_service_circuit_state gauge the §16.5
			// TokenServiceUnavailable alert reads. The §4.1
			// per-subsystem gauge already carries it; the dedicated
			// gauge keeps the alert expression cleanly named.
			gwMetrics.SetTokenServiceCircuitState(tokenServiceSubsystem.State().MetricValue())
		}
		publish()
		for {
			select {
			case <-watchdogCtx.Done():
				return
			case <-ticker.C:
				publish()
			}
		}
	}()

	// §4.0 / §25.13: the per-replica in-process alert tracker drives the
	// §16.5 catalog through inactive → pending → firing and emits
	// alert_fired / alert_resolved through the shared EventEmitter. With
	// no PromQL backend wired the tracker uses NoopExprEvaluator, which
	// keeps every rule inactive — the fall-back posture for a
	// Prometheus-less deployment. The wiring is unconditional so a
	// future commit that supplies a real ExprEvaluator only swaps the
	// backend, not the surface.
	alertEvaluator := evaluator.NewWithEmitter(
		rules.Catalog(),
		evaluator.NoopExprEvaluator{},
		evaluator.EventEmitOptions{
			Emitter: opsEmitter,
			Source:  "//lenny.dev/gateway/" + replica,
		},
	)
	go alertEvaluator.Run(watchdogCtx)

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
// sessionUserLookup adapts the session store to
// proxycache.SessionUserLookup so the §4.9 per-user semantic-cache scope
// resolves a session's owning user. A store miss, or a session with no
// recorded user, leaves the request uncached rather than keyed without a
// user id.
type sessionUserLookup struct{ sessions sessionstore.Store }

func (l sessionUserLookup) UserID(ctx context.Context, tenantID, sessionID string) (string, bool) {
	sess, err := l.sessions.Get(ctx, tenantID, sessionID)
	if err != nil || sess.UserID == "" {
		return "", false
	}
	return sess.UserID, true
}

// sessionRetryLookup adapts the §4.2 session store to the §4.8
// RetryPolicyEvaluator's RetryStateLookup: a missing session reads as
// not-found (ok == false, the request is admitted), and any other store
// fault surfaces as an error so the fail-closed evaluator rejects.
type sessionRetryLookup struct{ sessions sessionstore.Store }

func (l sessionRetryLookup) LookupRetryState(ctx context.Context, tenantID, sessionID string) (policy.RetryState, bool, error) {
	sess, err := l.sessions.Get(ctx, tenantID, sessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return policy.RetryState{}, false, nil
	}
	if err != nil {
		return policy.RetryState{}, false, err
	}
	return policy.RetryState{RetryCount: sess.RetryCount}, true, nil
}

func newLLMProxyServer(addr string, translators llmproxy.TranslatorRegistry, leases credleasestore.LeaseStore, creds *credcache.Cache, denyList *denylist.DenyList, chain *interceptor.Chain, cache llmproxy.ProxyCache, gwMetrics *gatewaymetrics.Metrics) *http.Server {
	if addr == "" {
		return nil
	}
	proxyMux := http.NewServeMux()
	proxyMux.Handle("POST /llm-proxy/v1/messages", &llmproxy.Handler{
		Leases:       leases,
		Translators:  translators,
		Forwarder:    &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials:  creds,
		DenyList:     denyList,
		Interceptors: chain,
		Cache:        cache,
		// §16.1 lines 97, 99, 100: active connections, translation
		// duration, and translation errors on the gateway registry.
		Metrics: gwMetrics,
	})
	return &http.Server{
		Addr:              addr,
		Handler:           proxyMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// llmTranslatorConfig carries the §4.9 per-provider translator config
// the gateway reads from flags. anthropic_direct and openai_direct
// register unconditionally with their defaults; the provider-config
// dependent translators register only when their required fields are
// set.
type llmTranslatorConfig struct {
	anthropicVersion string
	openaiBaseURL    string
	openaiOrg        string
	bedrockRegion    string
	vertexRegion     string
	vertexProject    string
	azureEndpoint    string
	azureAPIVersion  string
}

// buildLLMTranslatorRegistry assembles the §4.9 provider→translator
// registry the proxy dispatches on. spec: §4.9 lines 1525-1526.
func buildLLMTranslatorRegistry(c llmTranslatorConfig) llmproxy.TranslatorRegistry {
	translators := []llmproxy.Translator{
		&llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: c.anthropicVersion},
		&llmproxy.OpenAIDirectTranslator{BaseURL: c.openaiBaseURL, Organization: c.openaiOrg},
	}
	if c.bedrockRegion != "" {
		translators = append(translators, &llmproxy.AWSBedrockTranslator{Region: c.bedrockRegion})
	}
	if c.vertexRegion != "" && c.vertexProject != "" {
		translators = append(translators, &llmproxy.VertexAITranslator{Region: c.vertexRegion, Project: c.vertexProject})
	}
	if c.azureEndpoint != "" && c.azureAPIVersion != "" {
		translators = append(translators, &llmproxy.AzureOpenAITranslator{Endpoint: c.azureEndpoint, APIVersion: c.azureAPIVersion})
	}
	return llmproxy.NewTranslatorRegistry(translators...)
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

// resolveNoEnvironmentPolicy returns the resolved §10.6 / §11.1
// platform-wide noEnvironmentPolicy or a fatal-startup error. An
// empty value outside dev mode returns the
// "LENNY_CONFIG_MISSING config_key=noEnvironmentPolicy scope=platform"
// error §10.3's configuration validation table mandates. Dev mode
// derives allow-all for local convenience. Any value other than
// deny-all / allow-all returns a typed validation error. Extracted
// from main() so the §11.1 TestGatewayConfigValidation test can
// regression-cover the §10.3 contract. spec: §10.6 line 646;
// §11.1 line 13; §10.3 configuration validation table.
func resolveNoEnvironmentPolicy(value string, devMode bool) (string, error) {
	resolved := value
	if resolved == "" && devMode {
		resolved = tenantstore.NoEnvPolicyAllowAll
	}
	if resolved == "" {
		return "", fmt.Errorf("LENNY_CONFIG_MISSING config_key=noEnvironmentPolicy scope=platform: " +
			"set --no-environment-policy or LENNY_NO_ENVIRONMENT_POLICY to deny-all or allow-all (§10.6)")
	}
	if resolved != tenantstore.NoEnvPolicyDenyAll && resolved != tenantstore.NoEnvPolicyAllowAll {
		return "", fmt.Errorf("--no-environment-policy must be deny-all or allow-all, got %q", resolved)
	}
	return resolved, nil
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

// newSSEKeyResolver builds the §12.5 ll. 297-303 SSEKeyResolver the
// MinIO blob store calls on every Put. The closure:
//
//   - Returns (tenantkms.AliasFor(tenantID), true, nil) for a T4
//     tenant: MinIO MUST wrap under the per-tenant alias so the §12.5
//     cryptographic-erasure property holds.
//   - Returns ("", false, nil) for a non-T4 tenant: fall through to
//     the bucket-default SSE-S3 / SSE-KMS key.
//   - Returns ("", true, err) for a T4 tenant whose registry row is
//     unreachable: the blobstore maps it onto
//     CLASSIFICATION_CONTROL_VIOLATION and fires the KMS-unavailable
//     callback. Returning requireKey=true on a lookup failure is the
//     fail-closed posture: we cannot infer the tier from a missing
//     row, and a requireKey=false return would silently downgrade an
//     unknown tenant to the bucket-default key.
//
// spec: §12.5 ll. 297-303 — T4 SSE-KMS resolution and fail-closed
// rejection.
func newSSEKeyResolver(tenants tenantstore.Store) func(string) (string, bool, error) {
	return func(tenantID string) (string, bool, error) {
		row, err := tenants.Get(context.Background(), tenantID)
		if err != nil {
			return "", true, fmt.Errorf("lookup tenant %s: %w", tenantID, err)
		}
		if row.WorkspaceTier == tenantkms.WorkspaceTierT4 {
			return tenantkms.AliasFor(tenantID), true, nil
		}
		return "", false, nil
	}
}

// authFailureAuditAdapter bridges the §10.2 auth middleware to the
// §11.7 audit chain so every §4.2 line 185 tenant-claim rejection
// (TENANT_CLAIM_MISSING / TENANT_NOT_FOUND / TENANT_CLAIM_INVALID_FORMAT)
// produces an `auth_failure` audit row. Rejections that infer a
// tenant id from the JWT claim or dev header land on that inferred
// tenant's chain; the TENANT_CLAIM_MISSING case (no claim presented)
// falls back to the platform chain.
type authFailureAuditAdapter struct {
	sink admin.AuditSink
}

func (a authFailureAuditAdapter) EmitAuthFailure(ctx context.Context, ev authmw.AuthFailureEvent) {
	if a.sink == nil {
		return
	}
	actorTenant := ev.TenantID
	if actorTenant == "" {
		// §4.2: when no tenant could be inferred, land the row on the
		// platform chain (admin.NewChainAuditSink defaults the empty
		// ActorTenantID to "platform").
		actorTenant = ""
	}
	a.sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:          authmw.AuthFailureEventType,
		ActorSubject:  ev.UserID,
		ActorTenantID: actorTenant,
		Detail: map[string]any{
			"reason":    ev.Reason,
			"tenant_id": ev.TenantID,
			"user_id":   ev.UserID,
			"jti":       ev.JTI,
		},
		At: ev.At,
	})
}

// experimentRejectionReporter bridges a §10.7 ExperimentRouter
// fail-closed rejection to the §11.7 audit chain, the §16.1 metrics
// registry, and the §25.3 operational-event buffer: it records the
// `experiment.isolation_mismatch` event on all three and increments
// `lenny_experiment_isolation_rejections_total`.
type experimentRejectionReporter struct {
	audit   admin.AuditSink
	metrics *gatewaymetrics.Metrics
	emitter events.EventEmitter
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
		_, _ = e.emitter.Emit(ctx, events.OperationalEvent{
			Source:          "/v1/sessions",
			Type:            events.EventExperimentIsolationMismatch.CloudEventsType(),
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

// sessionLifecycleAuditor adapts the gateway audit appender to the
// sessionserver.LifecycleAuditSink interface. It writes the §7.1 /
// §16.6 session lifecycle events (session.created and the terminal
// session.{completed,failed,cancelled,expired}) to the §11.7
// hash-chained audit log under the session's own tenant partition. The
// tenant is taken from the session-derived event, satisfying the §11.7
// line 428 write-time tenant-validation rule. The OCSF mapping maps
// these event types to API Activity (6003).
type sessionLifecycleAuditor struct {
	appender policy.AuditAppender
}

func (a sessionLifecycleAuditor) EmitSessionLifecycle(ctx context.Context, ev sessionserver.SessionLifecycleEvent) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id": ev.SessionID,
		"user_sub":   ev.UserID,
		"runtime":    ev.RuntimeRef,
		"state":      ev.State,
	}
	if ev.FailureClass != "" {
		payload["failure_class"] = ev.FailureClass
	}
	if ev.Detail != "" {
		// spec: §7.1 line 112 — workspaceSealFailed records the last MinIO
		// export error in the detail field.
		payload["detail"] = ev.Detail
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	at := ev.At
	if at.IsZero() {
		at = clockinject.Now().UTC()
	}
	_, _ = a.appender.Append(ctx, ev.TenantID, ev.EventType, json.RawMessage(data), at)
}

// credentialAuditor adapts the gateway audit sink to the
// credentialserver.AuditSink interface, drawing the §11.7 actor fields
// from the request principal and the §4.9.2 credential_ref from the
// event detail so the audit query can target the affected credential.
type credentialAuditor struct {
	sink admin.AuditSink
}

func (a credentialAuditor) EmitCredentialEvent(ctx context.Context, eventType string, detail map[string]any) {
	if a.sink == nil {
		return
	}
	ev := admin.AuditEvent{Type: eventType, Detail: detail, At: clockinject.Now().UTC()}
	if p, ok := authmw.FromContext(ctx); ok {
		ev.ActorSubject = p.Subject
		ev.ActorTenantID = p.TenantID
	}
	if ref, ok := detail["credential_ref"].(string); ok {
		ev.TargetResource = ref
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

// tenantListerForHPA is the narrow interface exportHPAGauges
// requires. Both tenantsLister (production) and the test fake
// staticTenantLister satisfy it.
type tenantListerForHPA interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// exportHPAGauges refreshes the §4.1 / §16.1 horizontal-scaling
// gauges: the primary scale-out trigger (request queue depth, the
// in-flight HTTP request count on this replica), the secondary HPA
// metric (active streaming connections), and the capacity-ceiling
// numerator (non-terminal sessions tracked across tenants). Each
// gauge is set unconditionally on every poll so a transient store
// failure does not strand the gauge at a stale value — the next poll
// retries. Errors are logged but never bubble: the exporter is a
// best-effort signal and must not interrupt the watchdog loop.
//
// spec: §4.1 SCL-026 (HPA metric roles)
// spec: §16.1 (gauge metric definitions)
// spec: §16.5 GatewaySessionBudgetNearExhaustion (denominator gauge)
func exportHPAGauges(ctx context.Context, sessions sessionstore.Store, lister tenantListerForHPA, bus *sessionevents.Bus, m *gatewaymetrics.Metrics) {
	// Request queue depth — the §4.1 SCL-026 primary HPA scale-out
	// trigger. The metric is the count of HTTP requests the metrics
	// Middleware is currently servicing on this replica.
	m.SetRequestQueueDepth(m.InflightRequests())

	// Active streams — the §4.1 SCL-026 secondary HPA metric. Counts
	// in-flight SSE subscribers on this replica's sessionevents bus.
	if bus != nil {
		m.SetActiveStreams(bus.ActiveSubscribers())
	}

	// Active sessions — the §16.5 GatewaySessionBudgetNearExhaustion
	// alert numerator. Walks every tenant and counts non-terminal
	// sessions. Production scale will replace the per-tenant list
	// with a SessionStore.Count primitive; the per-tenant walk is
	// adequate for current tier sizes.
	tenants, err := lister.ListTenants(ctx)
	if err != nil {
		log.Printf("lenny-gateway: HPA gauge export: listing tenants failed: %v", err)
		return
	}
	var active int
	for _, tenant := range tenants {
		rows, err := sessions.List(ctx, tenant, sessionstore.ListFilter{})
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("lenny-gateway: HPA gauge export: tenant %q list failed: %v", tenant, err)
			}
			continue
		}
		for _, row := range rows {
			if !session.IsTerminal(row.State) {
				active++
			}
		}
	}
	m.SetActiveSessions(active)
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

// parseTerminationGrace returns the §4.4 line 263 termination grace
// period the preStop hook uses to bound the staged drain. It reads
// LENNY_TERMINATION_GRACE_SECONDS first; the chart-default 240s
// applies when the env is unset or invalid.
//
// spec: §17.8.2 — terminationGracePeriodSeconds: 240 default.
func parseTerminationGrace() time.Duration {
	seconds := envInt("LENNY_TERMINATION_GRACE_SECONDS", prestop.DefaultTerminationGraceSeconds)
	if seconds <= 0 {
		seconds = prestop.DefaultTerminationGraceSeconds
	}
	return time.Duration(seconds) * time.Second
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

// dialInterceptor dials a §4.8 external RequestInterceptor service. mTLS
// is used when cert/key/ca are all set; with all three empty the dial
// falls through to plaintext for dev mode. The §13.2 NET-058
// NetworkPolicy that scopes egress to the interceptor namespace is
// templated by the Helm chart; this dial assumes that egress is
// permitted.
func dialInterceptor(addr, certPath, keyPath, caPath string) (*grpc.ClientConn, error) {
	if addr == "" {
		return nil, fmt.Errorf("interceptor endpoint is empty")
	}
	var transport grpc.DialOption
	switch {
	case certPath == "" && keyPath == "" && caPath == "":
		transport = grpc.WithTransportCredentials(insecure.NewCredentials())
	case certPath == "" || keyPath == "" || caPath == "":
		return nil, fmt.Errorf("external interceptor mTLS requires --external-interceptor-tls-cert, --external-interceptor-tls-key, and --external-interceptor-ca to all be set")
	default:
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load external-interceptor client cert: %w", err)
		}
		caPEM, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read external-interceptor CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("external-interceptor CA bundle %q parsed no certificates", caPath)
		}
		transport = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      pool,
			MinVersion:   tls.VersionTLS13,
		}))
	}
	return grpc.NewClient(addr, transport)
}
