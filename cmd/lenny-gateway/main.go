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
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	"github.com/lennylabs/lenny/pkg/alerting/alertingmetrics"
	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/audit/siem"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/cataloging"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/connectoroauth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/delegation/recovery"
	"github.com/lennylabs/lenny/pkg/driftmonitor"
	"github.com/lennylabs/lenny/pkg/elicitation"
	gwadapter "github.com/lennylabs/lenny/pkg/gateway/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/adapterregistry"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/auditretention"
	"github.com/lennylabs/lenny/pkg/gateway/auditscope"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore/auditbatch"
	"github.com/lennylabs/lenny/pkg/gateway/billingretention"
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
	"github.com/lennylabs/lenny/pkg/gateway/connectorauthz"
	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	connectorcredpg "github.com/lennylabs/lenny/pkg/gateway/connectorcredstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/connectorsecret"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	connectorpg "github.com/lennylabs/lenny/pkg/gateway/connectorstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/correctionstore"
	correctionpg "github.com/lennylabs/lenny/pkg/gateway/correctionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/createdsweeper"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	credentialpoolpg "github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	credentialpg "github.com/lennylabs/lenny/pkg/gateway/credentialstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/credfallback"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	credleasepg "github.com/lennylabs/lenny/pkg/gateway/credleasestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/credrenewal"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	customrolepg "github.com/lennylabs/lenny/pkg/gateway/customrolestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/delegation/childtoken"
	"github.com/lennylabs/lenny/pkg/gateway/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/delegation/exportwire"
	"github.com/lennylabs/lenny/pkg/gateway/delegationbudget"
	delegationbudgetpg "github.com/lennylabs/lenny/pkg/gateway/delegationbudget/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	delegationpolicypg "github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/derivelock"
	"github.com/lennylabs/lenny/pkg/gateway/drainreadiness"
	"github.com/lennylabs/lenny/pkg/gateway/dualstore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	environmentpg "github.com/lennylabs/lenny/pkg/gateway/environmentstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	evalpg "github.com/lennylabs/lenny/pkg/gateway/evalstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/experimentsticky"
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
	leasepg "github.com/lennylabs/lenny/pkg/gateway/leasestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/legalholdreconciler"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpruntimes"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/memorystore"
	memorypg "github.com/lennylabs/lenny/pkg/gateway/memorystore/pgstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
	correlationmw "github.com/lennylabs/lenny/pkg/gateway/middleware/correlation"
	deprecationmw "github.com/lennylabs/lenny/pkg/gateway/middleware/deprecation"
	environmentmw "github.com/lennylabs/lenny/pkg/gateway/middleware/environment"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	idempgstore "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency/pgstore"
	ratelimitmw "github.com/lennylabs/lenny/pkg/gateway/middleware/ratelimit"
	recovermw "github.com/lennylabs/lenny/pkg/gateway/middleware/recover"
	"github.com/lennylabs/lenny/pkg/gateway/openapi"
	"github.com/lennylabs/lenny/pkg/gateway/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/pdbwatcher"
	"github.com/lennylabs/lenny/pkg/gateway/pgnotify"
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
	"github.com/lennylabs/lenny/pkg/gateway/redistopology"
	"github.com/lennylabs/lenny/pkg/gateway/retentiongc"
	"github.com/lennylabs/lenny/pkg/gateway/revocation"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/semanticcache"
	"github.com/lennylabs/lenny/pkg/gateway/sessionage"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessioninbox"
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
	"github.com/lennylabs/lenny/pkg/gateway/tlsprobe"
	"github.com/lennylabs/lenny/pkg/gateway/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	transcriptpg "github.com/lennylabs/lenny/pkg/gateway/transcriptstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/translator"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	treearchivepg "github.com/lennylabs/lenny/pkg/gateway/treearchive/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	usagepg "github.com/lennylabs/lenny/pkg/gateway/usagestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/watchdog"
	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
	"github.com/lennylabs/lenny/pkg/kms/rekey"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	mtlsdenylist "github.com/lennylabs/lenny/pkg/mtls/denylist"
	mtlsdenylistprop "github.com/lennylabs/lenny/pkg/mtls/denylist/propagator"
	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
	"github.com/lennylabs/lenny/pkg/observability/logging"
	"github.com/lennylabs/lenny/pkg/observability/slo"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/pgwritemetrics"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/schemamigrate"
	"github.com/lennylabs/lenny/pkg/storerouter"
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
	// spec: §16.4 lines 370-372 — install the structured JSON logger as the
	// process-wide slog default and route the stdlib log package through it,
	// so every gateway log line carries ts (RFC 3339 UTC), level, msg, and
	// component=gateway. F-16.4.1.
	logging.Setup(os.Stderr, "gateway")

	addr := flag.String("addr", ":8080", "address to bind (host:port)")
	multiTenant := flag.Bool("multi-tenant", false, "enable §10.2 multi-tenant claim extraction")
	tenantIDClaim := flag.String("tenant-id-claim", envOr("LENNY_TENANT_ID_CLAIM", "tenant_id"),
		"§10.2 line 212 OIDC claim name the gateway reads the tenant identifier from. Defaults to `tenant_id` (matches the canonical Lenny claim shape); set to e.g. `https://acme.example/tenant` when the upstream IdP stamps tenant identity under a different claim. Mirrors the `auth.tenantIdClaim` Helm value. F-10.2.9.")
	oidcIssuerURL := flag.String("oidc-issuer-url", os.Getenv("LENNY_OIDC_ISSUER_URL"),
		"§10.3 line 365 auth.oidc.issuerUrl: the OIDC issuer the gateway's token validation trusts. A §10.3 required platform key — outside --dev-mode an empty or non-absolute-URL value is a fatal startup misconfiguration (LENNY_CONFIG_MISSING config_key=auth.oidc.issuerUrl). Override via LENNY_OIDC_ISSUER_URL. F-10.3.14.")
	oidcClientID := flag.String("oidc-client-id", os.Getenv("LENNY_OIDC_CLIENT_ID"),
		"§10.3 line 366 auth.oidc.clientId: the OIDC client registration whose audience the gateway checks. A §10.3 required platform key — outside --dev-mode an empty value is a fatal startup misconfiguration (LENNY_CONFIG_MISSING config_key=auth.oidc.clientId). Override via LENNY_OIDC_CLIENT_ID. F-10.3.14.")
	devMode := flag.Bool("dev-mode", envFlag("LENNY_DEV_MODE"),
		"enable dev-mode auth shortcuts (X-Lenny-Roles dev-header). Override via LENNY_DEV_MODE.")
	sloValidated := flag.Bool("slo-validated", envFlag("LENNY_SLO_VALIDATED"),
		"§16.5 line 623 — set true once the Phase 14.5 benchmark gate has validated the §16.5 SLO targets. When false (the default), the gateway logs the provisional-SLO startup warning so an operator running unvalidated defaults cannot silently treat them as SLA commitments. Mirrors the slo.validated Helm value. Override via LENNY_SLO_VALIDATED.")
	bearerTrustHMACKeyFile := flag.String("bearer-trust-hmac-key-file", os.Getenv("LENNY_BEARER_TRUST_HMAC_KEY_FILE"),
		"path to an additional HMAC-SHA256 signing key the §10.2 Bearer path trusts, on top of the Token Service signer. Unset in a production install; §17.4 Embedded Mode sets it so the gateway accepts the embedded OIDC provider's tokens. Override via LENNY_BEARER_TRUST_HMAC_KEY_FILE.")
	bearerExpectedIssuer := flag.String("bearer-expected-issuer", os.Getenv("LENNY_BEARER_EXPECTED_ISSUER"),
		"§10.2 line 237 expected iss claim on every Bearer JWT. When set, a token whose iss differs is rejected with TOKEN_INVALID (reason=issuer_mismatch). Empty (default) skips the check, matching the existing wiring. Override via LENNY_BEARER_EXPECTED_ISSUER.")
	bearerExpectedAudiences := flag.String("bearer-expected-audiences", os.Getenv("LENNY_BEARER_EXPECTED_AUDIENCES"),
		"§10.2 line 237 comma-separated set of acceptable aud claims on Bearer JWTs. A token whose aud intersects this set is admitted; a token whose aud is disjoint is rejected with TOKEN_INVALID (reason=audience_mismatch). Empty (default) skips the check. Override via LENNY_BEARER_EXPECTED_AUDIENCES.")
	jwksPublish := flag.Bool("jwks-publish", envFlagDefault("LENNY_JWKS_PUBLISH", false),
		"§10.3 publish the gateway's JWT signing keys as a JWK Set at /.well-known/jwks.json. Defaults off (F-10.2.14): the v1 JWT backend is HMAC and the published entries carry `kty: oct` with no `k` field — verifiers cannot use them to validate signatures, so the endpoint advertises only the kid/alg of the current and previous keys. Set to true to opt into the metadata advertisement, or once an asymmetric signing backend lands (so the document carries usable public-key material). Override via LENNY_JWKS_PUBLISH.")
	runtimeBin := flag.String("runtime-bin", "",
		"path to a Basic-level runtime binary. When set, the gateway dispatches messages to a child process speaking the §15.4.1 adapter protocol instead of the in-process echo executor.")
	postgresDSN := flag.String("postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string. When set, sessions, transcripts, tenants, and runtimes are persisted to Postgres (the migrations/ schema must already be applied). When empty, in-memory stores are used.")
	// spec: §12.3 line 103 — optional dedicated Postgres instance for the
	// billing-event and audit-log write paths (the Tier-3 instance-
	// separation step at §12.3 line 130). When set, the §12.3 R-03
	// StoreRouter routes BillingShard/AuditShard/AllAuditShards to this
	// pool while every other write stays on the primary. Requires
	// --postgres-dsn; the schema (billing_events, audit_log) must already
	// be applied on the separate instance. F-12.3.5.
	billingAuditDSN := flag.String("postgres-billing-audit-dsn", os.Getenv("LENNY_PG_BILLING_AUDIT_DSN"),
		"§12.3 line 103 separate Postgres instance for billing/audit writes. When set (requires --postgres-dsn), billing-event and audit-log inserts route to this instance while all other writes stay on the primary. Empty keeps both paths on the primary. Override via LENNY_PG_BILLING_AUDIT_DSN.")
	scatterMaxConcurrency := flag.Int("scatter-max-concurrency", envInt("LENNY_SCATTER_MAX_CONCURRENCY", 16),
		"§12.6 line 556 storeRouter.maxScatterGatherConcurrency: at most this many shards are queried in parallel by a scatter-gather fan-out (list-sessions, GDPR erasure, tenant deletion). v1 is single-shard so the bound is inert; it becomes load-bearing under a multi-shard router. Override via LENNY_SCATTER_MAX_CONCURRENCY. F-12.6.18.")
	scatterPerShardTimeoutSeconds := flag.Int("scatter-per-shard-timeout-seconds", envInt("LENNY_SCATTER_PER_SHARD_TIMEOUT_SECONDS", 10),
		"§12.6 line 557 storeRouter.scatterGatherPerShardTimeoutSeconds: per-shard query deadline. A timed-out shard is dropped (reads, partial result) or retried twice (writes). Override via LENNY_SCATTER_PER_SHARD_TIMEOUT_SECONDS. F-12.6.18.")
	scatterAggregateTimeoutSeconds := flag.Int("scatter-aggregate-timeout-seconds", envInt("LENNY_SCATTER_AGGREGATE_TIMEOUT_SECONDS", 120),
		"§12.6 line 558 storeRouter.scatterGatherAggregateTimeoutSeconds: total scatter-gather deadline, capping worst-case latency when many shards are slow. Override via LENNY_SCATTER_AGGREGATE_TIMEOUT_SECONDS. F-12.6.18.")
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
	redisClusterAddrs := flag.String("redis-cluster-addrs", os.Getenv("LENNY_REDIS_CLUSTER_ADDRS"),
		"§12.4 comma-separated Redis Cluster seed nodes (host:port). When set the base Redis client is a CLUSTER KEYSLOT-aware go-redis ClusterClient — the Tier 2→3 migration topology for the Quota/Rate Limiting instance. Takes precedence over --redis-url and --redis-sentinel-addrs. Override via LENNY_REDIS_CLUSTER_ADDRS.")
	// spec: §12.4 lines 237-245 — "Logical separation of Redis concerns".
	// Each per-concern URL routes one §12.4 store role to a dedicated
	// Redis instance (separate connection string per role); an empty
	// value keeps that concern on the base --redis-url / Sentinel /
	// Cluster client, so the single Tier 1/2 topology needs none of these.
	redisCoordinationURL := flag.String("redis-coordination-url", os.Getenv("LENNY_REDIS_COORDINATION_URL"),
		"§12.4 dedicated Redis URL for the Coordination concern (session leases, derive locks, pod slot counters). Empty uses the base Redis client. Override via LENNY_REDIS_COORDINATION_URL.")
	redisQuotaURL := flag.String("redis-quota-url", os.Getenv("LENNY_REDIS_QUOTA_URL"),
		"§12.4 dedicated Redis URL for the Quota/Rate Limiting concern (token/rate counters, sliding windows, storage quota, billing stream). Empty uses the base Redis client. Override via LENNY_REDIS_QUOTA_URL.")
	redisCachePubSubURL := flag.String("redis-cache-pubsub-url", os.Getenv("LENNY_REDIS_CACHE_PUBSUB_URL"),
		"§12.4 dedicated Redis URL for the Cache/Pub-Sub concern (circuit breakers, event relay, semantic cache, connector state, security bus, playground). Empty uses the base Redis client. Override via LENNY_REDIS_CACHE_PUBSUB_URL.")
	redisSessionDataURL := flag.String("redis-session-data-url", os.Getenv("LENNY_REDIS_SESSION_DATA_URL"),
		"§12.4 dedicated Redis URL for the Session-data concern (durable inbox, DLQ). Empty uses the base Redis client. Override via LENNY_REDIS_SESSION_DATA_URL.")
	redisDelegationURL := flag.String("redis-delegation-url", os.Getenv("LENNY_REDIS_DELEGATION_URL"),
		"§12.4 dedicated Redis URL for the Delegation concern (tree budget keys {root_session_id}:dlg:*). Empty uses the base Redis client. Override via LENNY_REDIS_DELEGATION_URL.")
	// spec: §10.3 line 359 — gateway startup TLS probe. When an endpoint
	// host:port is set the gateway verifies a TLS handshake succeeds and a
	// plaintext connection is refused before it becomes ready, converting a
	// misconfigured backend (wrong port, missing cert) into a startup
	// failure. Empty disables the probe for that backend (dev / in-memory).
	// F-10.3.15.
	startupProbeRedisAddr := flag.String("startup-tls-probe-redis-addr", os.Getenv("LENNY_STARTUP_TLS_PROBE_REDIS_ADDR"),
		"§10.3 line 359 host:port of the Redis TLS listener the startup probe checks (TLS handshake must succeed; plaintext must be refused). Empty disables the Redis leg. Override via LENNY_STARTUP_TLS_PROBE_REDIS_ADDR. F-10.3.15.")
	startupProbePgBouncerAddr := flag.String("startup-tls-probe-pgbouncer-addr", os.Getenv("LENNY_STARTUP_TLS_PROBE_PGBOUNCER_ADDR"),
		"§10.3 line 359 host:port of the PgBouncer TLS listener the startup probe checks. Empty disables the PgBouncer leg. Override via LENNY_STARTUP_TLS_PROBE_PGBOUNCER_ADDR. F-10.3.15.")
	startupProbeCA := flag.String("startup-tls-probe-ca", os.Getenv("LENNY_STARTUP_TLS_PROBE_CA"),
		"§10.3 line 359 CA bundle that verifies the Redis/PgBouncer server certificates during the startup TLS probe. Empty uses the system trust store. Override via LENNY_STARTUP_TLS_PROBE_CA. F-10.3.15.")
	startupProbeCert := flag.String("startup-tls-probe-cert", os.Getenv("LENNY_STARTUP_TLS_PROBE_CERT"),
		"§10.3 line 359 client certificate presented during the startup TLS probe (Redis tls-auth-clients requires one). Empty presents no client certificate. Override via LENNY_STARTUP_TLS_PROBE_CERT. F-10.3.15.")
	startupProbeKey := flag.String("startup-tls-probe-key", os.Getenv("LENNY_STARTUP_TLS_PROBE_KEY"),
		"§10.3 line 359 private key for --startup-tls-probe-cert. Override via LENNY_STARTUP_TLS_PROBE_KEY. F-10.3.15.")
	coordInterval := flag.Duration("coordination-interval", 15*time.Second,
		"§10.1 session-coordination lease sweep interval. Each sweep renews this replica's lease on every non-terminal session. Only active when --redis-url is set.")
	dualStoreMaxSeconds := flag.Int("dual-store-unavailable-max-seconds",
		envInt("LENNY_DUAL_STORE_UNAVAILABLE_MAX_SECONDS", int(dualstore.DefaultMaxUnavailable/time.Second)),
		"§10.1 dualStoreUnavailableMaxSeconds: the per-replica window after which sessions with no successful store interaction become eligible for graceful termination once a store recovers. Default 60. The §10.1 dual-store monitor is active only when both --postgres-dsn and --redis-url are set. Override via LENNY_DUAL_STORE_UNAVAILABLE_MAX_SECONDS. F-10.1.3.")
	shutdownTimeout := flag.Duration("shutdown-timeout", 5*time.Second, "graceful shutdown timeout")
	// spec: §15.5 item 1 + docs/api/index.md line 124 — when a REST URL
	// version prefix enters its 6-month sunset window, the gateway adds
	// the `X-Lenny-Deprecated-Version` response header to every response
	// served under that prefix. The list defaults empty: v1 is current
	// and no /v2/ has shipped yet, so the middleware is inert. When the
	// first /v2/ surface lands, operators set
	// `gateway.deprecatedAPIVersions: [v1]` in the Helm values (rendered
	// as the flag below) and the middleware begins stamping the header
	// without further code changes. F-15.5.11.
	deprecatedAPIVersionsCSV := flag.String("deprecated-api-versions",
		os.Getenv("LENNY_DEPRECATED_API_VERSIONS"),
		"§15.5 item 1 / docs/api/index.md line 124 — comma-separated REST URL version prefixes currently in their 6-month sunset window. Each match stamps the `X-Lenny-Deprecated-Version` response header. Empty disables the header (v1 default). Override via LENNY_DEPRECATED_API_VERSIONS. F-15.5.11.")
	// spec: §25.3 lines 596, 604, 625 — operator knobs for the capacity
	// recommendations service. disabled-rules skips noisy rules across
	// every replica; window-overrides shrinks a rule's sliding window to
	// cut ring-buffer memory; disable-on-prometheus-outage fails closed
	// with RECOMMENDATIONS_UNAVAILABLE instead of computing from a
	// fallback reader. F-25.3.12.
	recommendationsDisabledRules := flag.String("recommendations-disabled-rules",
		os.Getenv("LENNY_RECOMMENDATIONS_DISABLED_RULES"),
		"§25.3 line 604 — comma-separated recommendation rule IDs to disable across all replicas. Override via LENNY_RECOMMENDATIONS_DISABLED_RULES. F-25.3.12.")
	recommendationsWindowOverrides := flag.String("recommendations-window-overrides",
		os.Getenv("LENNY_RECOMMENDATIONS_WINDOW_OVERRIDES"),
		"§25.3 line 596 — comma-separated per-category sliding-window overrides as category=duration (e.g. warm_pool_sizing=12h,credential_pool_sizing=72h). Override via LENNY_RECOMMENDATIONS_WINDOW_OVERRIDES. F-25.3.12.")
	recommendationsDisableOnOutage := flag.Bool("recommendations-disable-on-prometheus-outage",
		envFlag("LENNY_RECOMMENDATIONS_DISABLE_ON_PROMETHEUS_OUTAGE"),
		"§25.3 line 625 — return 503 RECOMMENDATIONS_UNAVAILABLE instead of computing from a fallback reader when the metric source is unreachable. Override via LENNY_RECOMMENDATIONS_DISABLE_ON_PROMETHEUS_OUTAGE. F-25.3.12.")
	rlGlobalPerMin := flag.Int("rate-limit-global-per-min", 0,
		"§11.1 global requests-per-minute admission limit. Zero disables the global rate limit.")
	rlPerUserPerMin := flag.Int("rate-limit-per-user-per-min", 0,
		"§11.1 per-user requests-per-minute admission limit. Zero disables the per-user rate limit.")
	rlPerTenantPerMin := flag.Int("rate-limit-per-tenant-per-min", 0,
		"§13.3 line 607 / §11.1 per-tenant requests-per-minute admission limit (fair-share brake across a tenant's users). Zero disables the per-tenant rate limit. F-11.1.8.")
	rlPerRuntimePerMin := flag.Int("rate-limit-per-runtime-per-min", 0,
		"§11.1 line 7 per-runtime session-creation requests-per-minute admission limit. Zero disables the per-runtime rate limit. F-11.1.2.")
	rlPerPoolPerMin := flag.Int("rate-limit-per-pool-per-min", 0,
		"§11.1 line 7 per-pool session-creation requests-per-minute admission limit (skipped when no warm pool resolves). Zero disables the per-pool rate limit. F-11.1.2.")
	maxConcSessGlobal := flag.Int("max-concurrent-sessions-global",
		envInt("LENNY_MAX_CONCURRENT_SESSIONS_GLOBAL", 0),
		"§11.1 line 8 global concurrent-session admission cap (live non-terminal sessions across every tenant). Zero disables the global scope. Override via LENNY_MAX_CONCURRENT_SESSIONS_GLOBAL. F-11.1.3.")
	maxConcSessPerUser := flag.Int("max-concurrent-sessions-per-user",
		envInt("LENNY_MAX_CONCURRENT_SESSIONS_PER_USER", 0),
		"§11.1 line 8 per-user concurrent-session admission cap (live non-terminal sessions a single user holds in their tenant). Zero disables the per-user scope. Override via LENNY_MAX_CONCURRENT_SESSIONS_PER_USER. F-11.1.3.")
	maxConcSessPerRuntime := flag.Int("max-concurrent-sessions-per-runtime",
		envInt("LENNY_MAX_CONCURRENT_SESSIONS_PER_RUNTIME", 0),
		"§11.1 line 8 per-runtime concurrent-session admission cap (live non-terminal sessions targeting a single runtime in a tenant). Zero disables the per-runtime scope. Override via LENNY_MAX_CONCURRENT_SESSIONS_PER_RUNTIME. F-11.1.3.")
	evalRLPerSessionPerMin := flag.Int("eval-rate-limit-per-session-per-min",
		envInt("LENNY_EVAL_RATE_LIMIT_PER_SESSION_PER_MIN", sessionserver.DefaultEvalPerSessionPerMin),
		"§10.7 line 938 evalRateLimit.perSessionPerMinute: per-session eval-submission requests-per-minute limit on POST /v1/sessions/{id}/eval. Default 100. Negative disables the per-session scope. Override via LENNY_EVAL_RATE_LIMIT_PER_SESSION_PER_MIN. F-10.7.4.")
	evalRLPerTenantPerMin := flag.Int("eval-rate-limit-per-tenant-per-min",
		envInt("LENNY_EVAL_RATE_LIMIT_PER_TENANT_PER_MIN", sessionserver.DefaultEvalPerTenantPerMin),
		"§10.7 line 938 evalRateLimit.perTenantPerMinute: per-tenant eval-submission requests-per-minute limit across all of a tenant's sessions. Default 10000. Negative disables the per-tenant scope. Override via LENNY_EVAL_RATE_LIMIT_PER_TENANT_PER_MIN. F-10.7.4.")
	// spec: §11.1 lines 10-11 — concurrent-upload and per-session
	// upload-size admission caps, distinct from the §4.1 upload-handler
	// back-pressure semaphore. Zero leaves each scope unlimited; operators
	// opt in. F-11.1.5, F-11.1.6.
	uploadMaxConcurrentPerSession := flag.Int("upload-max-concurrent-per-session",
		envInt("LENNY_UPLOAD_MAX_CONCURRENT_PER_SESSION", 0),
		"§11.1 line 10 per-session concurrent-upload admission cap. Excess in-flight uploads against one session are rejected with 429 RATE_LIMITED. Zero disables the per-session concurrency cap. Override via LENNY_UPLOAD_MAX_CONCURRENT_PER_SESSION. F-11.1.5.")
	uploadMaxConcurrentGlobal := flag.Int("upload-max-concurrent-global",
		envInt("LENNY_UPLOAD_MAX_CONCURRENT_GLOBAL", 0),
		"§11.1 line 10 global (per-replica) concurrent-upload admission cap. Excess in-flight uploads across all sessions are rejected with 429 RATE_LIMITED. Zero disables the global concurrency cap. Override via LENNY_UPLOAD_MAX_CONCURRENT_GLOBAL. F-11.1.5.")
	uploadMaxBytesPerSession := flag.Int64("upload-max-bytes-per-session",
		envInt64("LENNY_UPLOAD_MAX_BYTES_PER_SESSION", 0),
		"§11.1 line 11 per-session cumulative upload-size cap (bytes). The sum of all uploads in a session is rejected with 429 QUOTA_EXCEEDED past this value; the per-file cap is the separate 64 MiB body ceiling. Zero disables the per-session size cap. Override via LENNY_UPLOAD_MAX_BYTES_PER_SESSION. F-11.1.6.")
	// spec: §11.3 line 222 — rateLimitFailOpenMaxSeconds, operator-tunable.
	// Once a fail-open episode (counter-error window) has run past this
	// cap, the middleware switches to fail-closed and rejects requests
	// with 429 RATE_LIMITED until the counter recovers. F-11.3.22.
	rlFailOpenMaxSeconds := flag.Int("rate-limit-failopen-max-seconds",
		envInt("LENNY_RATE_LIMIT_FAILOPEN_MAX_SECONDS", int(ratelimitmw.DefaultFailOpenMaxSeconds/time.Second)),
		"§11.3 line 222 rateLimitFailOpenMaxSeconds: cap on a single fail-open episode in the §11.1 admission middleware. Negative disables the cap. Default 60s. Override via LENNY_RATE_LIMIT_FAILOPEN_MAX_SECONDS.")
	auditLockAcquireTimeoutMs := flag.Int("audit-lock-acquire-timeout-ms",
		envInt("LENNY_AUDIT_LOCK_ACQUIRE_TIMEOUT_MS", auditstore.DefaultLockConfig().AcquireTimeoutMs),
		"§11.7 item 3 audit.lock.acquireTimeoutMs: statement_timeout on the per-tenant audit advisory-lock acquisition. Default 5000ms. Override via LENNY_AUDIT_LOCK_ACQUIRE_TIMEOUT_MS.")
	auditLockMaxRetries := flag.Int("audit-lock-max-retries",
		envInt("LENNY_AUDIT_LOCK_MAX_RETRIES", auditstore.DefaultLockConfig().MaxRetries),
		"§11.7 item 3 audit.lock.maxRetries: same-replica retries after an audit lock timeout before returning 503 audit_unavailable. Default 3. Override via LENNY_AUDIT_LOCK_MAX_RETRIES.")
	auditLockRetryBaseMs := flag.Int("audit-lock-retry-base-ms",
		envInt("LENNY_AUDIT_LOCK_RETRY_BASE_MS", auditstore.DefaultLockConfig().RetryBaseMs),
		"§11.7 item 3 audit.lock.retryBaseMs: exponential-backoff base for audit lock retries, doubling per attempt and jittered ±20%. Default 20ms. Override via LENNY_AUDIT_LOCK_RETRY_BASE_MS.")
	globalTokenQuota := flag.Int64("global-token-quota-per-window", 0,
		"§11.2 platform-wide LLM-token budget per reset-period window, enforced by the §4.8 QuotaEvaluator at the global scope. Zero disables the global token cap. Only active when --redis-url is set.")
	userTokenQuota := flag.Int64("user-token-quota-per-window", 0,
		"§11.2 per-user LLM-token budget per reset-period window, enforced by the §4.8 QuotaEvaluator at the user scope. Zero disables the per-user token cap. Only active when --redis-url is set.")
	quotaRollingWindowSeconds := flag.Int("quota-rolling-window-seconds",
		envInt("LENNY_QUOTA_ROLLING_WINDOW_SECONDS", int(policy.DefaultRollingWindow.Seconds())),
		"§11.2 rolling-window length (seconds) applied when a tenant configures the `rolling` reset period. Default 3600 (1h). Override via LENNY_QUOTA_ROLLING_WINDOW_SECONDS.")
	quotaSyncIntervalSeconds := flag.Int("quota-sync-interval-seconds",
		envInt("LENNY_QUOTA_SYNC_INTERVAL_SECONDS", quota.DefaultSyncIntervalSeconds),
		"§11.2 line 44 quotaSyncIntervalSeconds: cadence (seconds) at which the gateway checkpoints Redis quota and delegation-budget counters to Postgres. Lower it (toward the 10s minimum) for high-throughput tenants to reduce crash-recovery overshoot; a value below the minimum is clamped up. Default 30s. Override via LENNY_QUOTA_SYNC_INTERVAL_SECONDS.")
	delegationNodeMemoryFootprintBytes := flag.Int64("delegation-node-memory-footprint-bytes",
		int64(envInt("LENNY_DELEGATION_NODE_MEMORY_FOOTPRINT_BYTES", int(delegationbudget.DefaultNodeMemoryFootprintBytes))),
		"§11.2 line 48 delegationNodeMemoryFootprintBytes: per-node in-memory footprint estimate the delegation-budget crash-recovery reconstruction multiplies by the live descendant count to derive liveMemoryBytes. Default 12288 (12 KB). Override via LENNY_DELEGATION_NODE_MEMORY_FOOTPRINT_BYTES.")
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
	delegationDefaultMaxDepth := flag.Int("delegation-default-max-depth", envInt("LENNY_DELEGATION_DEFAULT_MAX_DEPTH", delegation.DefaultMaxDepth),
		"§8.2.bis line 89 Helm fallback for delegationLease.maxDepth (gateway.delegation.defaultMaxDepth). Every effective delegation lease MUST carry a positive integer maxDepth; this value is consulted last in the precedence chain (client → preset → runtime default → policy ceiling → Helm fallback), so a delegation request that omits maxDepth still receives a bounded chain. Default 10. Override via LENNY_DELEGATION_DEFAULT_MAX_DEPTH.")
	delegationMaxActiveChildrenPerUser := flag.Int("delegation-max-active-children-per-user",
		envInt("LENNY_DELEGATION_MAX_ACTIVE_CHILDREN_PER_USER", 0),
		"§11.1 line 9 per-user active-delegated-children admission cap: the maximum count of live (non-terminal) delegated children a single user may hold across all their sessions and trees (the per-session breadth is bounded by the §8.2 lease/treebudget axes). Zero disables the per-user scope. Override via LENNY_DELEGATION_MAX_ACTIVE_CHILDREN_PER_USER. F-11.1.4.")
	gatewayAllowSelfRecursion := flag.Bool("gateway-allow-self-recursion", envFlag("LENNY_GATEWAY_ALLOW_SELF_RECURSION"),
		"§8.2 LayerPlatform input to the cycle-detection three-layer AND gate (Helm value gateway.allowSelfRecursion). A self-recursive delegation hop (same runtime+pool tuple appears earlier in the lineage) is admitted under mode=enforce iff this flag, the resolved Runtime.allowSelfRecursion, and the resolved DelegationPolicy.allowSelfRecursion are all true. Default false. Override via LENNY_GATEWAY_ALLOW_SELF_RECURSION.")
	interceptorWeakeningCooldownSeconds := flag.Int("interceptor-weakening-cooldown-seconds",
		envInt("LENNY_INTERCEPTOR_WEAKENING_COOLDOWN_SECONDS", int(delegation.DefaultInterceptorWeakeningCooldown/time.Second)),
		"§8.3 line 181 Helm value gateway.interceptorWeakeningCooldownSeconds: the cluster-scoped window during which delegate_task rejects every call whose effective DelegationPolicy is inside a `scanExportedFiles: true → false` weakening transition with INTERCEPTOR_WEAKENING_COOLDOWN (TRANSIENT, HTTP 503). Default 60s. Override via LENNY_INTERCEPTOR_WEAKENING_COOLDOWN_SECONDS. F-8.7.12 / F-13.5.7.")
	healthTrackerUseCompiledRules := flag.Bool("health-tracker-use-compiled-rules",
		envBool("LENNY_HEALTH_TRACKER_USE_COMPILED_RULES", true),
		"§25.13 line 4798 Helm value gateway.healthTracker.useCompiledRules: when true (default), the gateway's in-process §16.5 alert evaluator drives the per-replica health view. When false, the gateway suppresses the in-process alert tracker entirely and /v1/admin/health falls back to dependency probes and circuit breaker state only. Operators set this to false for strict consistency with operator-customized Prometheus rules. Override via LENNY_HEALTH_TRACKER_USE_COMPILED_RULES. F-25.13.4.")
	alertingBundleFormats := flag.String("alerting-bundle-formats",
		envOr("LENNY_ALERTING_BUNDLE_FORMATS", "prometheusrule"),
		"§25.13 line 4833 Helm value monitoring.format (closed-enum subset rendered by the chart): comma-separated list of the formats the chart bundled the §16.5 alert catalogue into. Stamps `lenny_alerting_rules_bundled{format}` so an operator can verify the bundling configuration. Override via LENNY_ALERTING_BUNDLE_FORMATS. F-25.13.3.")
	alertingOverrideCount := flag.Int("alerting-override-count",
		envInt("LENNY_ALERTING_OVERRIDE_COUNT", 0),
		"§25.13 line 4834 Helm value len(monitoring.alertOverrides): count of operator-customized §16.5 rules the chart rendered. Stamps `lenny_alerting_rule_overrides` so the §25.13 metrics surface shows how many rules diverged from the bundled defaults. Override via LENNY_ALERTING_OVERRIDE_COUNT. F-25.13.3.")
	gatewayQueueDepthThreshold := flag.Float64("gateway-queue-depth-threshold",
		envFloat("LENNY_GATEWAY_QUEUE_DEPTH_THRESHOLD", 20),
		"§25.13 line 4737 / §16.5 monitoring.alertThresholds.gatewayQueueDepthHigh.value: the per-subsystem queue-depth ceiling the GatewayQueueDepthHigh alert reads via scalar(lenny_gateway_queue_depth_threshold). Tier presets tighten this (Tier 2: 10, Tier 3: 5). Override via LENNY_GATEWAY_QUEUE_DEPTH_THRESHOLD. F-25.13.2.")
	gatewayLatencyThresholdSeconds := flag.Float64("gateway-latency-threshold-seconds",
		envFloat("LENNY_GATEWAY_LATENCY_THRESHOLD_SECONDS", 3.0),
		"§25.13 line 4737 / §16.5 monitoring.alertThresholds.gatewayLatencyHigh.p99Seconds: the per-subsystem p95 latency ceiling (seconds) the GatewayLatencyHigh alert reads via scalar(lenny_gateway_latency_threshold_seconds). Tier presets tighten this (Tier 2: 2.0, Tier 3: 1.0). Override via LENNY_GATEWAY_LATENCY_THRESHOLD_SECONDS. F-25.13.2.")
	credentialPoolLowThreshold := flag.Float64("credential-pool-low-threshold",
		envFloat("LENNY_CREDENTIAL_POOL_LOW_THRESHOLD", 0.80),
		"§25.13 line 4737 / §16.5 monitoring.alertThresholds.credentialPoolLow.utilizationThreshold: the per-pool utilisation fraction the CredentialPoolLow alert reads via scalar(lenny_credential_pool_low_threshold). Tier presets tighten this (Tier 2: 0.70, Tier 3: 0.60). Override via LENNY_CREDENTIAL_POOL_LOW_THRESHOLD. F-25.13.2.")
	billingFlushIntervalMs := flag.Int("billing-flush-interval-ms",
		envInt("LENNY_BILLING_FLUSH_INTERVAL_MS", int(failover.DefaultFlushInterval/time.Millisecond)),
		"§12.3 line 76 billingFlushIntervalMs: cadence (ms) at which the billing failover flusher drains the Tier 2 write-ahead buffer into Postgres in multi-row batches. Default 500. Override via LENNY_BILLING_FLUSH_INTERVAL_MS. F-12.3.13.")
	billingFlushBatchSize := flag.Int("billing-flush-batch-size",
		envInt("LENNY_BILLING_FLUSH_BATCH_SIZE", failover.DefaultFlushBatchSize),
		"§12.3 line 76 billingFlushBatchSize: maximum buffered billing events drained into Postgres per flush call (one multi-row INSERT batch). Default 50. Override via LENNY_BILLING_FLUSH_BATCH_SIZE. F-12.3.13.")
	billingFlushMaxPending := flag.Int("billing-flush-max-pending",
		envInt("LENNY_BILLING_FLUSH_MAX_PENDING", failover.DefaultFlushMaxPending),
		"§12.3 line 76 billingFlushMaxPending: once the Tier 2 write-ahead buffer grows past this many events, the gateway flushes immediately and emits the billing_flush_pressure metric. Default 500. Override via LENNY_BILLING_FLUSH_MAX_PENDING. F-12.3.13.")
	postgresWriteCeilingIops := flag.Float64("postgres-write-ceiling-iops",
		envFloat("LENNY_POSTGRES_WRITE_CEILING_IOPS", 200),
		"§12.3 line 123 postgres.writeCeilingIops: the measured sustained write-IOPS ceiling for the primary Postgres instance. Emitted unlabelled on lenny_postgres_write_ceiling_iops so the §16.5 PostgresWriteSaturation alert reads scalar(lenny_postgres_write_ceiling_iops). Tier presets set 200/600/1600. Override via LENNY_POSTGRES_WRITE_CEILING_IOPS. F-12.3.8.")
	postgresWriteIopsSampleSeconds := flag.Int("postgres-write-iops-sample-interval-seconds",
		envInt("LENNY_POSTGRES_WRITE_IOPS_SAMPLE_INTERVAL_SECONDS", 15),
		"§12.3 lines 115-125 cadence (seconds) at which the gateway samples pg_stat_database row-write deltas to publish the lenny_postgres_write_iops gauge feeding the §16.5 PostgresWriteSaturation alert. Default 15. Override via LENNY_POSTGRES_WRITE_IOPS_SAMPLE_INTERVAL_SECONDS. F-12.3.7.")
	auditStartupChainCheckEntries := flag.Int("audit-startup-chain-check-entries",
		envInt("LENNY_AUDIT_STARTUP_CHAIN_CHECK_ENTRIES", 1000),
		"§12.3 line 101 audit.startupChainCheckEntries: the most-recent N audit rows per tenant the startup chain-continuity check re-verifies. A non-positive value walks each chain in full. Default 1000. Override via LENNY_AUDIT_STARTUP_CHAIN_CHECK_ENTRIES. F-12.3.9.")
	auditGrantCheckIntervalSeconds := flag.Int("audit-grant-check-interval-seconds",
		envInt("LENNY_AUDIT_GRANT_CHECK_INTERVAL_SECONDS", 0),
		"§11.7 item 2 audit.grantCheckInterval: cadence of the periodic background integrity check that re-verifies append-only ledger grants/triggers/erasure guard and samples recent chain segments. 0 selects the profile default (regulated 60s, unregulated 300s). A value above the profile maximum (regulated 120s, unregulated 900s) is a fatal startup error. Override via LENNY_AUDIT_GRANT_CHECK_INTERVAL_SECONDS. F-11.7.3.")
	auditHardFailOnDrift := flag.Bool("audit-hard-fail-on-drift",
		envBool("LENNY_AUDIT_HARD_FAIL_ON_DRIFT", false),
		"§11.7 item 2 audit.hardFailOnDrift: when true, a drift detected by the periodic background integrity check initiates a graceful shutdown (in addition to the critical alert and lenny_audit_grant_drift_total increment). Default false. Override via LENNY_AUDIT_HARD_FAIL_ON_DRIFT. F-11.7.3.")
	auditSIEMEndpoint := flag.String("audit-siem-endpoint",
		os.Getenv("LENNY_AUDIT_SIEM_ENDPOINT"),
		"§11.7 audit.siem.endpoint: the external SIEM ingest endpoint. When empty, the §11.7 compliance gate rejects creating or updating a tenant to a regulated complianceProfile (soc2, fedramp, hipaa), and creating an environment under one, with COMPLIANCE_SIEM_REQUIRED; in production a regulated tenant with no endpoint is a fatal startup error. When set, the gateway validates SIEM connectivity at startup (a test event must be acknowledged or the gateway refuses to start) and runs the §11.7 OCSF translator → SIEM forwarder pipeline. Override via LENNY_AUDIT_SIEM_ENDPOINT. F-11.7.1 / F-11.7.2.")
	auditSIEMSecret := flag.String("audit-siem-secret",
		os.Getenv("LENNY_AUDIT_SIEM_SECRET"),
		"§11.7 SIEM HMAC shared secret. When set, the SIEM HTTP sink signs each OCSF batch with an HMAC-SHA256 X-Lenny-SIEM-Signature header so the receiver can authenticate the gateway. Override via LENNY_AUDIT_SIEM_SECRET. F-11.7.1.")
	auditSIEMFailureThresholdPercent := flag.Float64("audit-siem-failure-threshold-percent",
		envFloat("LENNY_AUDIT_SIEM_FAILURE_THRESHOLD_PERCENT", 5),
		"§11.7 item 4 audit.siem.failureThresholdPercent: when the SIEM delivery failure rate exceeds this percentage, the §25.3 health API reports the siem component degraded (default 5%). Override via LENNY_AUDIT_SIEM_FAILURE_THRESHOLD_PERCENT. F-11.7.16.")
	auditSIEMMaxDeliveryLagSeconds := flag.Int("audit-siem-max-delivery-lag-seconds",
		envInt("LENNY_AUDIT_SIEM_MAX_DELIVERY_LAG_SECONDS", 30),
		"§12.3 line 97 audit.siem.maxDeliveryLagSeconds: the threshold above which the §16.5 AuditSIEMDeliveryLag alert fires. Emitted on lenny_audit_siem_max_delivery_lag_seconds so the alert compares against an operator-tunable scalar. Default 30s. Override via LENNY_AUDIT_SIEM_MAX_DELIVERY_LAG_SECONDS. F-12.3.17.")
	auditSIEMPollIntervalSeconds := flag.Int("audit-siem-poll-interval-seconds",
		envInt("LENNY_AUDIT_SIEM_POLL_INTERVAL_SECONDS", 10),
		"§12.3 line 97 SIEM outbox forwarder poll interval: how often the forwarder tails the committed audit_log rows for newly committed events. Must stay well under audit.siem.maxDeliveryLagSeconds. Default 10s. Override via LENNY_AUDIT_SIEM_POLL_INTERVAL_SECONDS. F-12.3.6.")
	auditOCSFRetryIntervalSeconds := flag.Int("audit-ocsf-retry-interval-seconds",
		envInt("LENNY_AUDIT_OCSF_RETRY_INTERVAL_SECONDS", 30),
		"§11.7 audit.ocsf.retryInterval: cadence at which the OCSF translator re-drives pending / retry_pending audit rows toward succeeded | dead_lettered. Default 30s. Override via LENNY_AUDIT_OCSF_RETRY_INTERVAL_SECONDS. F-11.7.11.")
	auditOCSFMaxAttempts := flag.Int("audit-ocsf-max-attempts",
		envInt("LENNY_AUDIT_OCSF_MAX_ATTEMPTS", 10),
		"§11.7 audit.ocsf.maxAttempts: the per-row OCSF translation attempt budget; on the final failed attempt the row transitions to dead_lettered and a translation-failure receipt advances the SIEM delivery pointer. Default 10. Override via LENNY_AUDIT_OCSF_MAX_ATTEMPTS. F-11.7.11.")
	auditOCSFBatchSize := flag.Int("audit-ocsf-batch-size",
		envInt("LENNY_AUDIT_OCSF_BATCH_SIZE", 256),
		"§11.7 OCSF translator per-cycle batch size: the maximum number of pending audit rows one translation cycle drains. Default 256. Override via LENNY_AUDIT_OCSF_BATCH_SIZE. F-11.7.1.")
	auditSyncWritePoolSize := flag.Int("audit-sync-write-pool-size",
		envInt("LENNY_AUDIT_SYNC_WRITE_POOL_SIZE", 4),
		"§12.3 line 79 audit.syncWritePoolSize: the size of the dedicated audit sync write pool. Synchronous audit writes use this pool so they do not consume request-serving connections from the shared primary pool. Default 4. Override via LENNY_AUDIT_SYNC_WRITE_POOL_SIZE. F-12.3.14.")
	auditBatchingEnabled := flag.Bool("audit-batching-enabled",
		envBool("LENNY_AUDIT_BATCHING_ENABLED", false),
		"§12.3 line 81 audit.batchingEnabled: opt-in T2 (non-PII) audit-event batching. Disabled by default. When true, non-PII T2 operational audit events are buffered in memory and flushed in batches (accepting the documented data-loss risk on a crash); T3/T4 PII events always stay synchronous. Override via LENNY_AUDIT_BATCHING_ENABLED. F-12.3.14.")
	auditFlushIntervalMs := flag.Int("audit-flush-interval-ms",
		envInt("LENNY_AUDIT_FLUSH_INTERVAL_MS", 250),
		"§12.3 line 81 audit.flushIntervalMs: the maximum age of a buffered T2 audit event before it is flushed when batching is enabled. Default 250ms. Override via LENNY_AUDIT_FLUSH_INTERVAL_MS. F-12.3.14.")
	auditFlushBatchSize := flag.Int("audit-flush-batch-size",
		envInt("LENNY_AUDIT_FLUSH_BATCH_SIZE", 100),
		"§12.3 line 81 audit.flushBatchSize: the buffered T2 audit-event count that triggers an immediate flush when batching is enabled. Default 100. Override via LENNY_AUDIT_FLUSH_BATCH_SIZE. F-12.3.14.")
	retryMaxRetries := flag.Int("retry-max-retries", envInt("LENNY_RETRY_MAX_RETRIES", policy.DefaultMaxRetries),
		"§7.3 default retryPolicy.maxRetries: the automatic-retry budget the §4.8 RetryPolicyEvaluator (PostRoute, priority 600) enforces. A session whose retryCount has reached this cap is rejected at routing (it is in awaiting_client_action and requires an explicit client resume). Defaults to the §7.3 example value of 2. Override via LENNY_RETRY_MAX_RETRIES.")
	envVarBlocklistCSV := flag.String("env-var-blocklist", os.Getenv("LENNY_ENV_VAR_BLOCKLIST"),
		"§14 line 105 deployer extension to the env-var blocklist applied to a CreateSessionRequest's `env` field, a comma-separated list of exact names or `*` globs (e.g. AWS_SECRET_ACCESS_KEY,*_TOKEN). The platform default blocklist is always merged in first, so an operator can extend but not reduce it. Override via LENNY_ENV_VAR_BLOCKLIST.")
	maxResumePendingSeconds := flag.Int("max-resume-pending-seconds",
		envInt("LENNY_MAX_RESUME_PENDING_SECONDS", watchdog.DefaultMaxResumePendingSeconds),
		"§6.2 line 292 wall-clock cap on resume_pending: a session that has waited this long for a pod to become available transitions to awaiting_client_action. Mirrors the per-session retryPolicy.maxResumeWindowSeconds default; the per-session value tightens the platform cap. Default 900s. Override via LENNY_MAX_RESUME_PENDING_SECONDS.")
	maxResumingSeconds := flag.Int("max-resuming-seconds",
		envInt("LENNY_MAX_RESUMING_SECONDS", watchdog.DefaultMaxResumingSeconds),
		"§6.2 line 249 watchdog on resuming: a session whose resume has not completed within this window branches on retry budget (retries remain → resume_pending; exhausted → awaiting_client_action). Default 300s, matching the setup-command total timeout. Override via LENNY_MAX_RESUMING_SECONDS.")
	agentNamespace := flag.String("agent-namespace", os.Getenv("LENNY_AGENT_NAMESPACE"),
		"Kubernetes namespace the §5 warm pools and Sandboxes live in. When set, the gateway places each started session on a warm pod via the §4.7 adapter instead of the in-process executor.")
	clusterQPS := flag.Float64("cluster-qps", envFloat("LENNY_CLUSTER_QPS", 100),
		"client-go QPS for the cluster client the gateway uses to list/get/patch SandboxWarmPool / SandboxTemplate / Sandbox / SandboxClaim. The session-start path issues 5+ API calls per request, so client-go's default of 5 saturates at trivial load. The spec mandates explicit QPS values for the controller (§4.6.1) but leaves the gateway's client throttle to operator tuning; the kube-apiserver's own priority+fairness is the production-bounded gate. Override via LENNY_CLUSTER_QPS.")
	clusterBurst := flag.Int("cluster-burst", envInt("LENNY_CLUSTER_BURST", 200),
		"client-go burst (token-bucket size) for the cluster client. Pairs with --cluster-qps. Override via LENNY_CLUSTER_BURST.")
	defaultIsolationProfile := flag.String("default-isolation-profile", os.Getenv("LENNY_DEFAULT_ISOLATION_PROFILE"),
		"§5.3 isolation profile applied to a session that omits isolationProfile on the create body. Defaults to the chart's compiled-in fallback (`sandboxed`); the e2e overlay sets `standard` so every k6 scenario lands on the warm pool the agent-workload defines.")
	messagingDefaultScope := flag.String("messaging-default-scope", os.Getenv("LENNY_MESSAGING_DEFAULT_SCOPE"),
		"§7.2 deployment default messagingScope for lenny/send_message (`direct` | `siblings`). Empty resolves to the §7.2 default `direct` (siblings is opt-in). Override via LENNY_MESSAGING_DEFAULT_SCOPE.")
	messagingMaxScope := flag.String("messaging-max-scope", os.Getenv("LENNY_MESSAGING_MAX_SCOPE"),
		"§7.2 deployment messagingScope ceiling (`direct` | `siblings`); no tenant or runtime can widen beyond it. Empty imposes no ceiling beyond the enum; `direct` forbids sibling messaging tree-wide. Override via LENNY_MESSAGING_MAX_SCOPE.")
	messagingMaxPerMinute := flag.Int("messaging-max-per-minute", envInt("LENNY_MESSAGING_MAX_PER_MINUTE", 30),
		"§8.3 lenny/send_message per-sender outbound burst limit per minute. Override via LENNY_MESSAGING_MAX_PER_MINUTE.")
	messagingMaxPerSession := flag.Int("messaging-max-per-session", envInt("LENNY_MESSAGING_MAX_PER_SESSION", 200),
		"§8.3 lenny/send_message per-sender lifetime outbound cap. Override via LENNY_MESSAGING_MAX_PER_SESSION.")
	messagingMaxInboundPerMinute := flag.Int("messaging-max-inbound-per-minute", envInt("LENNY_MESSAGING_MAX_INBOUND_PER_MINUTE", 60),
		"§8.3 lenny/send_message per-target inbound aggregate limit per minute (the O(N²) sibling-storm brake). Override via LENNY_MESSAGING_MAX_INBOUND_PER_MINUTE.")
	messagingDurableInbox := flag.Bool("messaging-durable-inbox", envBool("LENNY_MESSAGING_DURABLE_INBOX", false),
		"§7.2 durableInbox: back the session inbox with a Redis list (t:{tenant}:session:{id}:inbox) so undelivered inter-session messages survive coordinator failover. Requires Redis. Default false (in-memory inbox). Override via LENNY_MESSAGING_DURABLE_INBOX.")
	messagingMaxInboxSize := flag.Int("messaging-max-inbox-size", envInt("LENNY_MESSAGING_MAX_INBOX_SIZE", 500),
		"§7.2 maxInboxSize: per-session inbox capacity before the oldest buffered message is evicted with a message_dropped(inbox_overflow) receipt. Override via LENNY_MESSAGING_MAX_INBOX_SIZE.")
	messagingMaxDLQSize := flag.Int("messaging-max-dlq-size", envInt("LENNY_MESSAGING_MAX_DLQ_SIZE", 500),
		"§7.2 maxDLQSize: per-session dead-letter-queue capacity before the oldest entry is evicted with a message_dropped(dlq_overflow) receipt. Override via LENNY_MESSAGING_MAX_DLQ_SIZE.")
	toolApprovalTimeout := flag.Duration("tool-approval-timeout", envDuration("LENNY_TOOL_APPROVAL_TIMEOUT", 0),
		"§7.2 tool-use approval wait: how long a blocked tool_call(approvalRequired) waits for a POST /tool-use/{id}/approve|deny before the gateway treats it as a denial. Zero (default) blocks until the user resolves it or the request context is cancelled. Override via LENNY_TOOL_APPROVAL_TIMEOUT.")
	treeArchiveCacheEntries := flag.Int("tree-archive-cache-entries", envInt("LENNY_TREE_ARCHIVE_CACHE_ENTRIES", 128),
		"§8.10 per-replica LRU cache size fronting the Postgres session_tree_archive (default 128 entries). Override via LENNY_TREE_ARCHIVE_CACHE_ENTRIES.")
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
	leaseAutoMaxPerMin := flag.Int("lease-extension-auto-max-per-min", 0,
		"§8.6 line 712 deployment-default autoModeRateLimit.maxAutoExtensionsPerMinute: the per-task-tree cap on auto-approved lease extensions per minute before the gateway pauses auto-approval and falls back to elicitation. Zero is the spec default (no limit). A tenant or runtime override (when registered) takes precedence. F-8.6.7.")
	spiffeTrustDomain := flag.String("spiffe-trust-domain", os.Getenv("LENNY_SPIFFE_TRUST_DOMAIN"),
		"§10.3 NET-060 SPIFFE trust domain (global.spiffeTrustDomain). When set together with --adapter-ca, the §8.6 GatewayControl listener validates each inbound pod certificate's spiffe://<trust-domain>/agent/{pool}/{pod} URI SAN at TLS handshake and rejects a foreign trust domain, a non-agent identity, or a revoked certificate with no gRPC response (logged pod_identity_mismatch). Empty disables SPIFFE peer validation (local development only).")
	saTokenAudience := flag.String("sa-token-audience", os.Getenv("LENNY_SA_TOKEN_AUDIENCE"),
		"§10.3 line 334 deployment-specific projected-SA-token audience (global.saTokenAudience, formatted lenny-gateway-<cluster-name>). When set, the §8.6 GatewayControl listener validates the audience claim of the projected SA token presented as the authorization bearer header on every pod→gateway request and rejects a token whose aud claim does not include this value (cross-deployment replay protection, the SA-token layer of the §10.3 defense-in-depth chain). Empty disables the SA-token audience check (local development only).")
	llmProxyAddr := flag.String("llm-proxy-addr", os.Getenv("LENNY_LLM_PROXY_ADDR"),
		"§4.9 LLM reverse-proxy listen address (host:port, e.g. :8443). When set, the gateway serves the proxy for proxy-mode agent pods on this address. Empty disables the LLM proxy listener.")
	llmSemanticCache := flag.Bool("llm-semantic-cache", os.Getenv("LENNY_LLM_SEMANTIC_CACHE") == "1",
		"§4.9 enable the in-process semantic cache on the LLM proxy path. Caching stays disabled by default and is opt-in per pool via the pool's cachePolicy; this flag provisions the in-memory backend the per-pool policy draws on. The Redis-backed backend is wired separately.")
	credentialFallbackMaxRotations := flag.Int("credential-fallback-max-rotations", envInt("LENNY_CREDENTIAL_FALLBACK_MAX_ROTATIONS", 3),
		"§4.9 credentialPolicy fallback.maxRotationsPerSession: the rotation budget shared across all providers in a session before the fallback chain is exhausted and the session terminates with CREDENTIAL_FALLBACK_EXHAUSTED. The spec default is 3; operator-tunable. Override via LENNY_CREDENTIAL_FALLBACK_MAX_ROTATIONS.")
	credentialFallbackCooldownSeconds := flag.Int("credential-fallback-cooldown-seconds", envInt("LENNY_CREDENTIAL_FALLBACK_COOLDOWN_SECONDS", 60),
		"§4.9 credentialPolicy fallback.cooldownOnRateLimit: seconds a faulted credential pool is held on cooldown before the fallback chain selects it again. The spec default is 60s; operator-tunable. Override via LENNY_CREDENTIAL_FALLBACK_COOLDOWN_SECONDS.")
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
	// spec: §12.5 line 279 — "MinIO connections MUST use TLS". TLS is on
	// by default; only §17.4 Embedded Mode (the chart's backends:embedded
	// posture) renders LENNY_MINIO_USE_SSL=false. The Helm chart fails the
	// render when tls.enabled is false on any non-embedded backend.
	minioUseSSL := flag.Bool("minio-use-ssl", envFlagDefault("LENNY_MINIO_USE_SSL", true),
		"connect to MinIO over HTTPS. Defaults to true per §12.5 line 279; only §17.4 Embedded Mode disables it. Override via LENNY_MINIO_USE_SSL.")
	checkpointInterval := flag.Duration("checkpoint-interval", 10*time.Minute,
		"§4.4 line 256 periodic-checkpoint cadence (`periodicCheckpointIntervalSeconds`). The gateway snapshots every coordinated session's workspace on this interval; active only with --agent-namespace. Default 10m (600s) matches the §4.4 spec value; the freshness SLO bounds workspace loss on eviction to ≤ one interval.")
	sessionArtifactRetentionSeconds := flag.Int("session-artifact-retention-seconds",
		envInt("LENNY_SESSION_ARTIFACT_RETENTION_SECONDS", int(sessionserver.DefaultArtifactRetention/time.Second)),
		"§7.1 line 77 default artifact-retention window in seconds. Session workspace snapshots, logs, and transcripts stay GC-eligible until this long after the session reaches a terminal state. Default 7 days (604800s); clients extend per-session via POST /v1/sessions/{id}/extend-retention. Override via LENNY_SESSION_ARTIFACT_RETENTION_SECONDS.")
	// spec: §12.5 line 317 — gc.cycleIntervalSeconds (default 900, min 60).
	// Drives both the §7.1 retention sweep and the §12.5 line 341 hard-prune
	// sweep cadence. A value below the floor is clamped up to 60s.
	gcCycleIntervalSeconds := flag.Int("gc-cycle-interval-seconds",
		envInt("LENNY_GC_CYCLE_INTERVAL_SECONDS", int(retentiongc.DefaultSweepInterval/time.Second)),
		"§12.5 line 317 gc.cycleIntervalSeconds: the leader-elected GC sweep cadence in seconds (default 900, minimum 60). Drives the §7.1 retention soft-delete sweep and the §12.5 line 341 tombstone hard-prune sweep. Override via LENNY_GC_CYCLE_INTERVAL_SECONDS.")
	// spec: §12.5 line 341 — gc.tombstoneRetentionSeconds (default 86400).
	// The soft-deleted artifact_store row's tombstone-retention window before
	// the hard-prune sweep physically removes it.
	gcTombstoneRetentionSeconds := flag.Int("gc-tombstone-retention-seconds",
		envInt("LENNY_GC_TOMBSTONE_RETENTION_SECONDS", int(retentiongc.DefaultTombstoneRetention/time.Second)),
		"§12.5 line 341 gc.tombstoneRetentionSeconds: how long a soft-deleted artifact_store row is retained before the hard-prune sweep removes it, in seconds (default 86400 / 24h). Operators may raise it without affecting GC correctness. Override via LENNY_GC_TOMBSTONE_RETENTION_SECONDS.")
	// spec: §12.5 line 307 (STO-021) — the leader-elected continuous T4
	// KMS availability probe. The cadence floor (60s) and the
	// token-bucket rate ceiling keep a large T4 fleet from bursting the
	// KMS backend; both are operator-tunable.
	t4KmsProbeIntervalSeconds := flag.Int("t4-kms-probe-interval-seconds",
		envInt("LENNY_T4_KMS_PROBE_INTERVAL_SECONDS", int(tenantkms.DefaultProbeInterval/time.Second)),
		"§12.5 line 307 storage.t4KmsProbeInterval: the leader-elected continuous T4 KMS availability probe cadence in seconds (default 300, minimum 60; a smaller value is clamped up to the floor). Override via LENNY_T4_KMS_PROBE_INTERVAL_SECONDS.")
	t4KmsProbeRateLimit := flag.Float64("t4-kms-probe-rate-limit",
		envFloat("LENNY_T4_KMS_PROBE_RATE_LIMIT", tenantkms.DefaultProbeRateLimit),
		"§12.5 line 307 storage.t4KmsProbeRateLimit: token-bucket ceiling on T4 KMS probe issuance in probes/sec (default 10). A non-positive value disables rate limiting. Override via LENNY_T4_KMS_PROBE_RATE_LIMIT.")
	maxCreatedStateTimeoutSeconds := flag.Int("max-created-state-timeout-seconds",
		envInt("LENNY_MAX_CREATED_STATE_TIMEOUT_SECONDS", watchdog.DefaultMaxCreatedStateSeconds),
		"§7.1 line 58 maxCreatedStateTimeoutSeconds: the deadline on the `created` pre-running state. Threaded uniformly into the §7.1 uploadToken TTL, the watchdog's `created`-state budget, and the createdsweeper's abandoned-row timeout so the three deadlines never drift. Default 300s. Override via LENNY_MAX_CREATED_STATE_TIMEOUT_SECONDS.")
	// spec: §11.3 line 219-221 — gateway.max{Finalizing,Ready,Starting}TimeoutSeconds
	// and the platform-wide cap on §6.2 awaiting_client_action /
	// maxSessionAge. Each is operator-tunable; the watchdog applied the
	// constructed defaults silently before. F-11.3.11.
	maxFinalizingTimeoutSeconds := flag.Int("max-finalizing-state-timeout-seconds",
		envInt("LENNY_MAX_FINALIZING_STATE_TIMEOUT_SECONDS", watchdog.DefaultMaxFinalizingStateSeconds),
		"§11.3 line 219 maxFinalizingTimeoutSeconds: the deadline on the `finalizing` pre-running state. A session stuck longer than this wall-clock window is transitioned to `failed`. The §6.2 line 260 invariant `maxFinalizingTimeoutSeconds ≥ runtime.setupTimeoutSeconds` is enforced at admin registration; raising this flag also raises the gateway-side cap admin uses when validating new runtimes. Default 600s. Override via LENNY_MAX_FINALIZING_STATE_TIMEOUT_SECONDS.")
	maxReadyStateTimeoutSeconds := flag.Int("max-ready-state-timeout-seconds",
		envInt("LENNY_MAX_READY_STATE_TIMEOUT_SECONDS", watchdog.DefaultMaxReadyStateSeconds),
		"§11.3 line 220 maxReadyTimeoutSeconds: the deadline on the `ready` pre-running state. A session stuck longer than this is transitioned to `failed`. Default 300s. Override via LENNY_MAX_READY_STATE_TIMEOUT_SECONDS.")
	maxStartingStateTimeoutSeconds := flag.Int("max-starting-state-timeout-seconds",
		envInt("LENNY_MAX_STARTING_STATE_TIMEOUT_SECONDS", watchdog.DefaultMaxStartingStateSeconds),
		"§11.3 line 221 maxStartingTimeoutSeconds: the deadline on the `starting` pre-running state. A session stuck longer than this is transitioned to `failed`. Default 120s. Override via LENNY_MAX_STARTING_STATE_TIMEOUT_SECONDS.")
	maxSessionAgeSeconds := flag.Int("max-session-age-seconds",
		envInt("LENNY_MAX_SESSION_AGE_SECONDS", watchdog.DefaultMaxSessionAgeSeconds),
		"§11.3 line 198 / §6.2 line 240 platform-wide maxSessionAgeSeconds: the total non-terminal lifetime cap of a session, measured from its creation. The per-runtime `runtime.maxSessionAgeSeconds` (and per-session retryPolicy.maxSessionAgeSeconds) tighten this cap; this flag is the floor a runtime without an override inherits. Default 7200s (2h). Override via LENNY_MAX_SESSION_AGE_SECONDS.")
	maxAwaitingClientActionSeconds := flag.Int("max-awaiting-client-action-seconds",
		envInt("LENNY_MAX_AWAITING_CLIENT_ACTION_SECONDS", watchdog.DefaultMaxAwaitingClientActionSeconds),
		"§11.3 line 199 maxAwaitingClientActionSeconds: the deadline on the `awaiting_client_action` state. A session that has waited this long for client action is transitioned to `expired`. Default 900s. Override via LENNY_MAX_AWAITING_CLIENT_ACTION_SECONDS.")
	maxSuspendedPodHoldSeconds := flag.Int("max-suspended-pod-hold-seconds",
		envInt("LENNY_MAX_SUSPENDED_POD_HOLD_SECONDS", watchdog.DefaultMaxSuspendedPodHoldSeconds),
		"§11.3 line 233 maxSuspendedPodHoldSeconds: the wall-clock window a `suspended` session may hold its pod before the watchdog transitions it to `expired`. Both the deploy-wide cap (this flag) and a per-tenant cap apply; the more restrictive value wins. Default 900s. Override via LENNY_MAX_SUSPENDED_POD_HOLD_SECONDS.")
	// spec: §11.3 line 205-206 — grpc.keepaliveTime{,out}Ms on the
	// adapter client (gateway → pod), operator-tunable. The library
	// default is no keepalive on the client side, so the §11.3 5s timeout
	// is unenforced without this. F-11.3.12.
	adapterKeepaliveTimeMs := flag.Int("adapter-keepalive-time-ms",
		envInt("LENNY_ADAPTER_KEEPALIVE_TIME_MS", 10_000),
		"§11.3 line 205 grpc.keepaliveTimeMs: the interval at which the gateway sends a keepalive ping on an idle adapter connection. Default 10000ms (10s). Override via LENNY_ADAPTER_KEEPALIVE_TIME_MS.")
	adapterKeepaliveTimeoutMs := flag.Int("adapter-keepalive-timeout-ms",
		envInt("LENNY_ADAPTER_KEEPALIVE_TIMEOUT_MS", 5_000),
		"§11.3 line 206 grpc.keepaliveTimeoutMs: how long the gateway waits for an adapter keepalive-ping reply before closing the connection. Default 5000ms (5s). Override via LENNY_ADAPTER_KEEPALIVE_TIMEOUT_MS.")
	// spec: §11.3 line 224 — delegation.usageQuiescenceTimeoutSeconds,
	// operator-tunable. The §8.10 tree-recovery path waits this long after
	// the last usage report before declaring the tree quiescent and
	// progressing to drain. F-11.3.19.
	delegationUsageQuiescenceTimeoutSeconds := flag.Int("delegation-usage-quiescence-timeout-seconds",
		envInt("LENNY_DELEGATION_USAGE_QUIESCENCE_TIMEOUT_SECONDS", 5),
		"§11.3 line 224 delegation.usageQuiescenceTimeoutSeconds: the wall-clock window the §8.10 tree-recovery path waits after the last child usage report before declaring the delegation tree quiescent. Default 5s. Override via LENNY_DELEGATION_USAGE_QUIESCENCE_TIMEOUT_SECONDS.")
	// spec: §8.10 lines 1022-1023 / 1042 — operator-tunable per-level and
	// whole-tree recovery deadlines. The default (120s / 600s) matches
	// `maxLevelRecoverySeconds` / `maxTreeRecoverySeconds`. Deployers
	// running deep trees apply the §8.10 line 1032 formula to raise the
	// tree cap. F-8.10.6.
	delegationMaxLevelRecoverySeconds := flag.Int("delegation-max-level-recovery-seconds",
		envInt("LENNY_DELEGATION_MAX_LEVEL_RECOVERY_SECONDS", int(recovery.DefaultLevelTimeout/time.Second)),
		"§8.10 line 1022 delegation.maxLevelRecoverySeconds: maximum time the gateway waits for all nodes at a single tree depth to complete recovery before marking the unrecovered ones as terminally failed. Default 120s. Override via LENNY_DELEGATION_MAX_LEVEL_RECOVERY_SECONDS.")
	delegationMaxTreeRecoverySeconds := flag.Int("delegation-max-tree-recovery-seconds",
		envInt("LENNY_DELEGATION_MAX_TREE_RECOVERY_SECONDS", int(recovery.DefaultTreeTimeout/time.Second)),
		"§8.10 line 1023 / line 1042 delegation.maxTreeRecoverySeconds: total wall-clock bound for recovering the full delegation tree; overrides per-level budgets. Default 600s. Deployers running deep trees should apply the §8.10 line 1032 formula. Override via LENNY_DELEGATION_MAX_TREE_RECOVERY_SECONDS.")
	// spec: §8.10 line 1078 — cascadeTimeoutSeconds is the deployer-tuned
	// wall-clock bound an `await_completion` child may run after parent
	// failure and how long a `detach` orphan persists before cleanup.
	// F-8.10.9.
	delegationCascadeTimeoutSeconds := flag.Int("delegation-cascade-timeout-seconds",
		envInt("LENNY_DELEGATION_CASCADE_TIMEOUT_SECONDS", int(orphancleanup.DefaultCascadeTimeout/time.Second)),
		"§8.10 line 1078 delegation.cascadeTimeoutSeconds: deployer-wide cap on how long an `await_completion` child may run after parent failure and how long a `detach` orphan persists before §8.10 cleanup. Default 3600s (1h). Override via LENNY_DELEGATION_CASCADE_TIMEOUT_SECONDS.")
	// spec: §8.10 line 1103 — maxOrphanTasksPerTenant caps a tenant's
	// active orphan tasks; when exceeded, the `detach` cascade falls back
	// to `cancel_all`. The §16.5 OrphanTasksPerTenantHigh alert reads
	// `scalar(lenny_max_orphan_tasks_per_tenant)` as the denominator;
	// publishing the configured value at startup makes the alert evaluate
	// against the live cap. F-8.10.10.
	delegationMaxOrphanTasksPerTenant := flag.Int("delegation-max-orphan-tasks-per-tenant",
		envInt("LENNY_DELEGATION_MAX_ORPHAN_TASKS_PER_TENANT", sessionserver.DefaultMaxOrphanTasksPerTenant),
		"§8.10 line 1103 delegation.maxOrphanTasksPerTenant: per-tenant cap on active orphan tasks; when the count would exceed this, the gateway falls back from `detach` to `cancel_all`. Default 100. Override via LENNY_DELEGATION_MAX_ORPHAN_TASKS_PER_TENANT.")
	// spec: §11.3 line 215 — credentials.expiryWarningLeadSeconds,
	// operator-tunable. Each tracked credential lease fires a structured
	// expiry-warning log line once when now is within this window of the
	// lease's ExpiresAt, so deployers see impending expiry before the
	// §4.9 fault-rotation path is consumed. F-11.3.20.
	credentialsExpiryWarningLeadSeconds := flag.Int("credentials-expiry-warning-lead-seconds",
		envInt("LENNY_CREDENTIALS_EXPIRY_WARNING_LEAD_SECONDS", int(credrenewal.DefaultExpiryWarningLead/time.Second)),
		"§11.3 line 215 credentials.expiryWarningLeadSeconds: how long before a credential lease's ExpiresAt the gateway fires a structured warning log. Set to 0 to disable. Default 3600 (1h). Override via LENNY_CREDENTIALS_EXPIRY_WARNING_LEAD_SECONDS.")
	workspaceSealMaxDurationSeconds := flag.Int("workspace-seal-max-duration-seconds",
		envInt("LENNY_WORKSPACE_SEAL_MAX_DURATION_SECONDS", int(sessionserver.DefaultWorkspaceSealMaxDuration/time.Second)),
		"§7.1 line 112 maxWorkspaceSealDurationSeconds: the total wall-clock window the gateway retries seal-and-export (exponential backoff 5s→60s) before failing the session with workspace_seal_timeout and terminating the pod anyway. Default 300s. Override via LENNY_WORKSPACE_SEAL_MAX_DURATION_SECONDS.")
	idempotencyGCIntervalSeconds := flag.Int("idempotency-gc-interval-seconds",
		envInt("LENNY_IDEMPOTENCY_GC_INTERVAL_SECONDS", 3600),
		"§11.5 line 277 idempotency_keys TTL garbage-collection cadence. The sweeper iterates tenants and drops rows past the 24-hour retention window every interval. Default 3600s (one hour). Lower values reduce row backlog at the cost of more frequent Postgres scans; higher values keep expired rows up to the configured interval past TTL (read-time gate masks them from clients). Override via LENNY_IDEMPOTENCY_GC_INTERVAL_SECONDS.")
	idempotencyMaxBodyBytes := flag.Int64("idempotency-max-body-bytes",
		envInt64("LENNY_IDEMPOTENCY_MAX_BODY_BYTES", 8<<20),
		"§11.5 line 277 cap on the request body the idempotency middleware buffers and hashes. A request larger than this is rejected with 413 BODY_TOO_LARGE before reaching the inner handler. Default 8 MiB covers the §11.5 critical operations (CreateSession ~10KB, FinalizeWorkspace and StartSession ~KB, Resume body that may carry a TaskResult). Operators raise it when their delegation payloads (taskInput) or replay/derive bodies exceed the default. Override via LENNY_IDEMPOTENCY_MAX_BODY_BYTES.")
	checkpointJitterFraction := flag.Float64("checkpoint-jitter-fraction", envFloat("LENNY_CHECKPOINT_JITTER_FRACTION", checkpointer.DefaultJitterFraction),
		"§4.4 line 258 `periodicCheckpointJitterFraction`. Each session's first periodic checkpoint is scheduled at `checkpointInterval + random(0, checkpointInterval × jitterFraction)`, preventing thundering-herd checkpoint storms at Tier 3 scale. Range [0.0, 1.0]; default 0.2 spreads the first checkpoint uniformly across a 120-second window at the default 600-second interval. Override via LENNY_CHECKPOINT_JITTER_FRACTION.")
	noEnvPolicy := flag.String("no-environment-policy", os.Getenv("LENNY_NO_ENVIRONMENT_POLICY"),
		"§10.6 platform-wide noEnvironmentPolicy (deny-all or allow-all). Required outside --dev-mode.")
	connectorOAuthCallbackURL := flag.String("connector-oauth-callback-url", os.Getenv("LENNY_CONNECTOR_OAUTH_CALLBACK_URL"),
		"§9.3 absolute URL the connector OAuth provider redirects back to (the gateway's GET /v1/admin/connectors/oauth/callback). Wiring the connector OAuth 2.1 flow requires it. Override via LENNY_CONNECTOR_OAUTH_CALLBACK_URL.")
	connectorOAuthCA := flag.String("connector-oauth-ca", os.Getenv("LENNY_CONNECTOR_OAUTH_CA"),
		"path to a CA bundle that verifies the §9.3 connector OAuth provider's token-endpoint TLS certificate. Empty uses the system trust store. Set this for a provider behind a private CA. Override via LENNY_CONNECTOR_OAUTH_CA.")
	connectorOAuthClientSecretKey := flag.String("connector-oauth-client-secret-key", envOr("LENNY_CONNECTOR_OAUTH_CLIENT_SECRET_KEY", connectorsecret.DefaultSecretKey),
		"§9.3 Kubernetes Secret data key the confidential-client resolver reads when a connector's auth.clientSecretRef names only namespace/name. A three-segment namespace/name/key reference overrides it per connector. Override via LENNY_CONNECTOR_OAUTH_CLIENT_SECRET_KEY.")
	opsServiceURL := flag.String("ops-service-url", os.Getenv("LENNY_OPS_SERVICE_URL"),
		"§25.14 public URL of the lenny-ops service (the ops.ingress.host Helm value). Advertised in GET /v1/admin/platform/version so lenny-ctl auto-discovers the ops endpoint. Override via LENNY_OPS_SERVICE_URL.")
	billingDualControlThreshold := flag.Float64("billing-dual-control-threshold", envFloat("LENNY_BILLING_DUAL_CONTROL_THRESHOLD", 0),
		"§11.2.1 billing.dualControlThreshold: an operator-initiated billing correction whose absolute adjustment value exceeds this requires a second platform-admin's approval. The default of 0 makes every correction dual-control. Override via LENNY_BILLING_DUAL_CONTROL_THRESHOLD.")
	billingCorrectionRateThreshold := flag.Float64("billing-correction-rate-threshold", envFloat("LENNY_BILLING_CORRECTION_RATE_THRESHOLD", 0.05),
		"§11.2.1 line 187 billing.correctionRateThreshold: BillingCorrectionRateHigh alert threshold as a fraction (0.05 = 5%). Emitted at startup on the lenny_billing_correction_rate_threshold gauge so the §16.5 alert can evaluate via scalar(lenny_billing_correction_rate_threshold). Override via LENNY_BILLING_CORRECTION_RATE_THRESHOLD.")
	billingRetentionDays := flag.Int("billing-retention-days", envInt("LENNY_BILLING_RETENTION_DAYS", billingretention.DefaultRetentionDays),
		"§11.2.1 line 151 billing.retentionDays: how long billing events are retained before the periodic retention pruner deletes them (default 395). The gateway rejects a value below the compliance floor of any tenant's regulated complianceProfile at startup (hipaa 2190, soc2 365, fedramp 365). Override via LENNY_BILLING_RETENTION_DAYS.")
	gdprRetentionDays := flag.Int("audit-gdpr-retention-days", envInt("LENNY_AUDIT_GDPR_RETENTION_DAYS", audit.GDPRRetentionDefaultDays),
		"§12.8 line 839 audit.gdprRetentionDays: how long gdpr.* audit rows (erasure receipts, legal-hold ledger events) are retained, on a window separate from audit.retentionDays (default 2555 / 7 years). The gateway rejects a value below 2190 (6 years) when any tenant has a regulated complianceProfile (soc2, fedramp, hipaa) at startup. Override via LENNY_AUDIT_GDPR_RETENTION_DAYS.")
	auditRetentionPreset := flag.String("audit-retention-preset", envOr("LENNY_AUDIT_RETENTION_PRESET", string(audit.PresetSOC2)),
		"§16.4 audit.retentionPreset: the compliance-aware retention bundle for non-gdpr audit rows (soc2, fedramp-high, hipaa, nis2-dora, custom). A named preset fixes the retention window (soc2 365, fedramp-high 1095, hipaa 2190, nis2-dora 1825); custom uses --audit-retention-days. The gateway warns at startup when the preset is incompatible with an active tenant's complianceProfile. Override via LENNY_AUDIT_RETENTION_PRESET.")
	auditRetentionDays := flag.Int("audit-retention-days", envInt("LENNY_AUDIT_RETENTION_DAYS", audit.PresetSOC2.PresetDays()),
		"§16.4 audit.retentionDays: the general (non-gdpr) Postgres audit-log retention window in days, used when --audit-retention-preset is custom. Emitted at startup on the lenny_audit_retention_days gauge so the §16.5 AuditRetentionLow alert can evaluate. Override via LENNY_AUDIT_RETENTION_DAYS.")
	auditRetentionPruneIntervalSeconds := flag.Int("audit-retention-prune-interval-seconds", envInt("LENNY_AUDIT_RETENTION_PRUNE_INTERVAL_SECONDS", 3600),
		"§16.4 line 378 audit-retention sweep cadence in seconds: how often the leader-elected pruner deletes audit rows past audit.retentionDays (gdpr.* rows held under audit.gdprRetentionDays, undelivered rows held by the SIEM delivery guard). Clamped up to a 60s floor. Override via LENNY_AUDIT_RETENTION_PRUNE_INTERVAL_SECONDS.")
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
	playgroundSessionLabels := flag.String("playground-session-labels", os.Getenv("LENNY_PLAYGROUND_SESSION_LABELS"),
		"§27.2 line 41 playground.sessionLabels: comma-separated key=value pairs stamped on every playground session record and audit event. Empty applies the default {origin: \"playground\"}; the load-bearing origin entry is re-stamped at startup regardless of the supplied value. Override via LENNY_PLAYGROUND_SESSION_LABELS.")
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
	// spec: §10.4 line 385 / §16.5 PDBBlockedEvictions — the §10.4 PDB
	// status poller addresses the gateway's PodDisruptionBudget object
	// by namespace+name. The chart sets --gateway-namespace from the
	// release namespace and --gateway-pdb-name to `lenny-gateway`.
	// F-10.4.4.
	gatewayNamespace := flag.String("gateway-namespace", envOr("LENNY_GATEWAY_NAMESPACE", os.Getenv("POD_NAMESPACE")),
		"§10.4 namespace holding the gateway PodDisruptionBudget for the periodic poller (defaults to POD_NAMESPACE). Override via LENNY_GATEWAY_NAMESPACE.")
	gatewayPDBName := flag.String("gateway-pdb-name", envOr("LENNY_GATEWAY_PDB_NAME", "lenny-gateway"),
		"§10.4 name of the gateway PodDisruptionBudget object for the periodic poller. Override via LENNY_GATEWAY_PDB_NAME.")
	// spec: §10.4 line 389 — operator-tunable SSE replay-buffer depth.
	// Default 512 events matches the §10.4 reconnect-window assumption
	// (60s at 10 events/s). The 64..4096 envelope is the spec-mandated
	// range. F-10.4.5.
	sessionEventReplayBufferDepth := flag.Int("session-event-replay-buffer-depth", envInt("LENNY_SESSION_EVENT_REPLAY_BUFFER_DEPTH", 512),
		"§10.4 line 389 per-session SSE replay buffer depth (events). Default 512 matches the §10.4 60s reconnect window at 10 events/s; accepted range 64..4096. Override via LENNY_SESSION_EVENT_REPLAY_BUFFER_DEPTH.")
	// spec: §9.4 line 202 — `memory.maxMemoriesPerUser` (default
	// 10,000) bounds the per-user record count; a Write that exceeds
	// it evicts the oldest by created_at. F-9.4.5.
	memoryMaxPerUser := flag.Int("memory-max-per-user", envInt("LENNY_MEMORY_MAX_PER_USER", memorystore.DefaultMaxMemoriesPerUser),
		"§9.4 line 202 per-user memory cap before oldest-first eviction. Override via LENNY_MEMORY_MAX_PER_USER.")
	// spec: §9.4 line 196 / §12.8 line 746 — `memory.enabled=false`
	// is the escape hatch that disables the MemoryStore entirely;
	// the lenny/memory_write and lenny/memory_query MCP tools are
	// not registered, the §12.8 erasure preflight is skipped, and
	// no `agent_memory` rows are written. Default true. F-9.4.7.
	memoryEnabled := flag.Bool("memory-enabled", envFlagDefault("LENNY_MEMORY_ENABLED", true),
		"§9.4 / §12.8 line 746 MemoryStore feature flag. false disables the lenny/memory_* MCP tools and skips the preflight. Override via LENNY_MEMORY_ENABLED.")
	// spec: §9.4 line 202 / §16.1 line 153 — periodic sampler for the
	// `lenny_memory_store_record_count` gauge. The store-specific
	// implementation walks tenants and emits the per-tenant count.
	// Default 60s aligns with the §16.5 alert windows; zero disables.
	memoryRecordCountInterval := flag.Duration("memory-record-count-interval", envDuration("LENNY_MEMORY_RECORD_COUNT_INTERVAL", 60*time.Second),
		"§9.4 line 202 / §16.1 line 153 periodic sampler interval for the MemoryStore record-count gauge. 0 disables. Override via LENNY_MEMORY_RECORD_COUNT_INTERVAL.")
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

	// spec: §16.3 line 359 — install the process-wide OpenTelemetry
	// TracerProvider and W3C trace-context propagator so the §16.3 span
	// catalog has a real exporter behind it instead of the global no-op
	// provider. The gateway emits 100% (head) and an OTLP/HTTP exporter
	// ships spans to OTEL_EXPORTER_OTLP_ENDPOINT; with no endpoint (or in
	// dev mode) a stdout exporter covers `make run`. F-16.3.2 / F-16.3.8.
	traceShutdown, err := tracing.InitProvider(context.Background(), tracing.ProviderConfig{
		ServiceName:  "lenny-gateway",
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		DevMode:      *devMode,
	})
	if err != nil {
		log.Fatalf("lenny-gateway: tracing init: %v", err)
	}

	// spec: §16.5 lines 609, 623 — surface the provisional-SLO startup
	// warning unless the Phase 14.5 benchmark gate has set slo.validated,
	// so a deployment running the unvalidated defaults cannot silently
	// treat them as customer SLA commitments.
	if msg, emit := slo.StartupWarning(*sloValidated); emit {
		log.Printf("lenny-gateway: %s", msg)
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

	// spec: §10.3 lines 361-371 — startup configuration validation. Each
	// missing required platform key emits a structured LENNY_CONFIG_MISSING
	// log entry (config_key/scope/remediation fields) and the gateway
	// refuses to become ready by exiting non-zero so Kubernetes surfaces
	// CrashLoopBackOff rather than the replica silently serving with
	// undefined semantics. noEnvironmentPolicy is gated just above;
	// playground.devTenantId by playground.Config.Validate; this covers the
	// OIDC and session-duration keys. The OIDC keys are exempt in dev mode
	// (§10.3 line 373 / §17.4). F-10.3.14.
	if missing := validatePlatformConfig(*devMode, *oidcIssuerURL, *oidcClientID, *maxSessionAgeSeconds); len(missing) > 0 {
		for _, m := range missing {
			slog.Error("LENNY_CONFIG_MISSING", "config_key", m.configKey, "scope", m.scope, "remediation", m.remediation)
		}
		log.Fatalf("lenny-gateway: %d required platform configuration key(s) missing or invalid (§10.3); see the LENNY_CONFIG_MISSING entries above", len(missing))
	}

	// spec: §10.3 line 359 — gateway startup TLS probe. Before the replica
	// is marked ready, verify a TLS handshake to Redis and PgBouncer
	// succeeds and that a plaintext connection is refused, so a
	// misconfigured backend (wrong port, missing cert) fails startup
	// rather than degrading silently at runtime. Dev mode is exempt (the
	// line 373 dev-mode symmetry / §17.4) and an unset endpoint is skipped.
	// F-10.3.15.
	if !*devMode && (*startupProbeRedisAddr != "" || *startupProbePgBouncerAddr != "") {
		probeTLS, perr := buildStartupProbeTLSConfig(*startupProbeCA, *startupProbeCert, *startupProbeKey)
		if perr != nil {
			log.Fatalf("lenny-gateway: §10.3 startup TLS probe configuration: %v", perr)
		}
		if err := tlsprobe.Probe(context.Background(), tlsprobe.Config{TLSConfig: probeTLS},
			tlsprobe.Target{Backend: tlsprobe.BackendRedis, Addr: *startupProbeRedisAddr},
			tlsprobe.Target{Backend: tlsprobe.BackendPgBouncer, Addr: *startupProbePgBouncerAddr},
		); err != nil {
			log.Fatalf("lenny-gateway: §10.3 startup TLS probe failed: %v", err)
		}
		log.Printf("lenny-gateway: §10.3 startup TLS probe passed (redis=%q pgbouncer=%q)", *startupProbeRedisAddr, *startupProbePgBouncerAddr)
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

	// spec: §11.3 line 224 — surface the effective
	// `delegation.usageQuiescenceTimeoutSeconds` at startup so operators
	// see the value the §8.10 tree-recovery orchestrator will consume.
	// The recovery package exposes the Config field; the gateway-side
	// orchestrator threads `delegationQuiescenceCfg` to keep one source
	// of truth. F-11.3.19.
	//
	// spec: §8.10 lines 1022-1023 / 1042 — extend the same Config to
	// carry `maxLevelRecoverySeconds` / `maxTreeRecoverySeconds` so the
	// recovery package consumes the live operator overrides. F-8.10.6.
	delegationQuiescenceCfg := recovery.Config{
		LevelTimeout:           time.Duration(*delegationMaxLevelRecoverySeconds) * time.Second,
		TreeTimeout:            time.Duration(*delegationMaxTreeRecoverySeconds) * time.Second,
		UsageQuiescenceTimeout: time.Duration(*delegationUsageQuiescenceTimeoutSeconds) * time.Second,
	}
	log.Printf("lenny-gateway: delegation.usageQuiescenceTimeoutSeconds=%ds (§11.3 line 224)",
		int(delegationQuiescenceCfg.QuiescenceDeadline(time.Time{}).Sub(time.Time{}).Seconds()))
	log.Printf("lenny-gateway: delegation.maxLevelRecoverySeconds=%ds delegation.maxTreeRecoverySeconds=%ds (§8.10 lines 1022-1023)",
		*delegationMaxLevelRecoverySeconds, *delegationMaxTreeRecoverySeconds)
	log.Printf("lenny-gateway: delegation.cascadeTimeoutSeconds=%ds delegation.maxOrphanTasksPerTenant=%d (§8.10 lines 1078, 1103)",
		*delegationCascadeTimeoutSeconds, *delegationMaxOrphanTasksPerTenant)
	_ = delegationQuiescenceCfg

	// ----- Stores -----
	// session, transcript, tenant, and runtime state is persisted to
	// Postgres when --postgres-dsn is set, and held in memory
	// otherwise. The remaining stores are in-memory pending their
	// Redis (circuit breakers, quota) or Postgres backings.
	var (
		sessions         sessionstore.Store
		tenants          tenantstore.Store
		runtimes         runtimestore.Store
		transcripts      transcriptstore.Store
		users            userstore.Store
		connectors       connectorstore.Store
		billing          billingstore.Store
		pgPool           *pgxpool.Pool
		billingAuditPool *pgxpool.Pool
		auditSyncPool    *pgxpool.Pool
	)
	if *postgresDSN != "" {
		pool, err := pgxpool.New(context.Background(), *postgresDSN)
		if err != nil {
			log.Fatalf("lenny-gateway: postgres: %v", err)
		}
		if err := verifyPostgresSchema(context.Background(), pool); err != nil {
			log.Fatalf("lenny-gateway: %v", err)
		}
		// spec: §12.3 lines 49-56 — cloud-managed pooler defense. Under
		// LENNY_POOLER_MODE=external the managed proxy cannot run the
		// connect_query __unset__ sentinel, so the per-transaction
		// lenny_tenant_guard trigger is the load-bearing RLS defense.
		// Refuse to start when the trigger is absent from any
		// tenant-scoped table, independent of the §17.6 preflight Job so a
		// post-install migration rollback is also caught. The fatal fires
		// regardless of LENNY_ENV because external pooler mode is an
		// explicit production posture. F-12.3.1 / F-12.2.14 / F-17.9.2.
		if err := integrity.VerifyCloudManagedPoolerDefense(context.Background(), pool, *poolerMode); err != nil {
			log.Fatalf("lenny-gateway: %v", err)
		}
		pgPool = pool
		// §12.3 line 103 — when the separate billing/audit instance is
		// configured, open and verify its pool here so the §12.3 R-03
		// StoreRouter below can route billing/audit writes to it. The
		// separate instance carries the migrations/ schema for the
		// append-only ledgers (billing_events, audit_log); the
		// cloud-managed-pooler defense is primary-only (the separate
		// instance is operator-provisioned for write isolation, not a
		// tenant-facing RLS surface), so only the schema check runs here.
		// F-12.3.5.
		if *billingAuditDSN != "" {
			bapool, err := pgxpool.New(context.Background(), *billingAuditDSN)
			if err != nil {
				log.Fatalf("lenny-gateway: billing/audit postgres: %v", err)
			}
			if err := verifyPostgresSchema(context.Background(), bapool); err != nil {
				log.Fatalf("lenny-gateway: billing/audit postgres: %v", err)
			}
			billingAuditPool = bapool
			log.Printf("lenny-gateway: §12.3 routing billing-event and audit-log writes to the separate LENNY_PG_BILLING_AUDIT_DSN instance")
		}
		// §12.3 line 79: a dedicated, small audit sync write pool so the
		// synchronous audit hash-chain writes do not consume the shared
		// request pool's connections. It targets the instance where the
		// audit ledger physically lives (the separate billing/audit
		// instance when configured, otherwise the primary), sized by
		// audit.syncWritePoolSize. A non-positive size keeps audit writes
		// on the router pool. F-12.3.14.
		if *auditSyncWritePoolSize > 0 {
			auditDSN := *postgresDSN
			if *billingAuditDSN != "" {
				auditDSN = *billingAuditDSN
			}
			syncCfg, err := pgxpool.ParseConfig(auditDSN)
			if err != nil {
				log.Fatalf("lenny-gateway: §12.3 audit sync write pool config: %v", err)
			}
			syncCfg.MaxConns = int32(*auditSyncWritePoolSize)
			sp, err := pgxpool.NewWithConfig(context.Background(), syncCfg)
			if err != nil {
				log.Fatalf("lenny-gateway: §12.3 audit sync write pool: %v", err)
			}
			auditSyncPool = sp
			log.Printf("lenny-gateway: §12.3 dedicated audit sync write pool active (max_conns=%d)", *auditSyncWritePoolSize)
		}
		// §11.7 startup integrity check: the append-only ledgers must
		// keep their grants, triggers, and erasure guard intact.
		// Production refuses to start on a violation; other
		// environments log a warning and continue. The check runs against
		// the instance where the ledgers physically live — the separate
		// billing/audit pool when configured, otherwise the primary.
		// F-12.3.5.
		ledgerPool := pool
		if billingAuditPool != nil {
			ledgerPool = billingAuditPool
		}
		if err := integrity.Verify(context.Background(), ledgerPool); err != nil {
			if os.Getenv("LENNY_ENV") == "production" {
				log.Fatalf("lenny-gateway: audit integrity check failed: %v", err)
			}
			log.Printf("lenny-gateway: WARNING: audit integrity check failed (non-production, continuing): %v", err)
		}
		sessions = sessionpg.New(pool)
		tenants = tenantpg.New(pool)
		runtimes = runtimepg.New(pool)
		transcripts = transcriptpg.New(pool)
		users = userpg.New(pool)
		connectors = connectorpg.New(pool)
		// billing is constructed below via the §12.3 R-03 StoreRouter,
		// once the Redis client (if any) is resolved, so the ledger never
		// holds a raw pool. F-12.3.4 / F-12.6.1 / F-12.2.13 / F-12.7.1.
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
		redisClient    redis.UniversalClient
		concernRedis   *redistopology.Clients
		coordinator    *coordination.Sweeper
		storageCounter storagequota.Counter = storagequota.NewMemory()
		rateLimiter    ratelimit.Counter    = ratelimit.NewMemory()

		// storageRecoveryReconciler drives the §12.4 line 210 write-back of
		// storage-quota counters to Redis on a Redis-recovery edge. Set only
		// when both Redis and the Postgres artifact catalog are wired.
		storageRecoveryReconciler *storagequota.RecoveryReconciler

		// delegationBudgetReconciler drives the §11.2 periodic Postgres
		// checkpoint of the §8.2 delegation tree budget counters and their
		// §11.2 line 48 two-source reconstruction on a Redis-recovery edge.
		// Set only when the delegation Redis counters, the Postgres pool,
		// and the SessionStore are all wired. F-11.2.5 / F-12.4.8.
		delegationBudgetReconciler *delegationbudget.Reconciler
	)
	if *redisURL != "" || *redisSentinelAddrs != "" || *redisClusterAddrs != "" {
		if *redisURL != "" && *redisSentinelAddrs != "" {
			log.Fatalf("lenny-gateway: --redis-url and --redis-sentinel-addrs are mutually exclusive")
		}
		var rcfg redisconn.Config
		switch {
		case *redisClusterAddrs != "":
			// §12.4 lines 260-264: Cluster mode is the CLUSTER KEYSLOT-aware
			// topology and takes precedence over the direct/Sentinel fields.
			rcfg = redisconn.Config{
				ClusterAddrs:  splitAndTrim(*redisClusterAddrs),
				Password:      *redisPassword,
				TLS:           *redisTLS,
				AllowInsecure: *redisAllowInsecure,
			}
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
		client, err := redisconn.NewUniversalClient(rcfg)
		if err != nil {
			log.Fatalf("lenny-gateway: redis client: %v", err)
		}
		redisClient = client
		if err := redisconn.PingWithTimeout(redisClient, 5*time.Second); err != nil {
			log.Fatalf("lenny-gateway: redis: %v", err)
		}
		// §12.4 lines 237-245: build the per-concern client split. A
		// concern with no dedicated URL falls back to the base client, so
		// the single Tier 1/2 topology resolves every concern to
		// redisClient unchanged. The auth/TLS template carries the base
		// password and allow-insecure posture to each per-concern URL.
		concernRedis, err = redistopology.Build(redisClient, map[storerouter.RedisConcern]string{
			storerouter.RedisConcernCoordination: *redisCoordinationURL,
			storerouter.RedisConcernQuota:        *redisQuotaURL,
			storerouter.RedisConcernCachePubSub:  *redisCachePubSubURL,
			storerouter.RedisConcernSessionData:  *redisSessionDataURL,
			storerouter.RedisConcernDelegation:   *redisDelegationURL,
		}, redisconn.Config{Password: *redisPassword, AllowInsecure: *redisAllowInsecure})
		if err != nil {
			log.Fatalf("lenny-gateway: redis concern split: %v", err)
		}
		// The §11.6 breaker registry lives in Redis (Cache/Pub-Sub
		// concern); the cachingstore keeps a local open-breaker snapshot
		// so the request-path check never round-trips to Redis and
		// survives a Redis outage.
		cacheClient := concernRedis.For(storerouter.RedisConcernCachePubSub)
		breakerCache = cachingstore.New(redisstore.New(cacheClient), cacheClient)
		breakers = breakerCache
		// §12.4 Coordination concern: session leases. The Redis-backed
		// store is the primary; with Postgres also wired the failover
		// wrapper routes lease operations to the §12.4 line 206 Postgres
		// advisory-lock fallback during a Redis outage, so coordination
		// degrades to higher latency rather than breaking lease
		// acquisition outright.
		var leaseStore leasestore.LeaseStore = leasestore.New(concernRedis.For(storerouter.RedisConcernCoordination))
		if pgPool != nil {
			leaseStore = leasestore.NewFailover(leaseStore, leasepg.New(pgPool), nil)
		}
		coordinator = coordination.NewSweeper(
			tenantsLister{tenants}, sessions, leaseStore,
			coordination.Options{ReplicaID: replica, Interval: *coordInterval},
		)
		// §12.4 Quota/Rate Limiting concern: the storage-quota counter
		// lives in Redis so the quota holds across replicas; its reserve
		// is Lua-atomic.
		storageRedis := storagequotaredis.New(concernRedis.For(storerouter.RedisConcernQuota))
		storageCounter = storageRedis
		// §12.4 line 210: when the durable artifact catalog is wired,
		// front the Redis counter with the Postgres-fallback failover so a
		// Redis outage degrades upload pre-checks to the authoritative
		// SUM(artifact_size_bytes) instead of breaking uploads, and a
		// simultaneous Postgres outage fails closed (ErrUnavailable → 503).
		// On Redis recovery the reconciler below writes the sum back so the
		// Lua fast path resumes. Without a catalog (dev/in-memory) the bare
		// Redis store keeps the prior fail-on-Redis-error behavior.
		if artifactCatalog != nil {
			storageCounter = storagequota.NewFailover(storageRedis, artifactCatalog.SumLiveBytes, nil)
			storageRecoveryReconciler = &storagequota.RecoveryReconciler{
				Probe: func(ctx context.Context) bool {
					return redisconn.PingWithTimeout(redisClient, 2*time.Second) == nil
				},
				Primary: storageRedis,
				Tenants: (tenantsLister{tenants}).ListTenants,
				SizeOf:  artifactCatalog.SumLiveBytes,
				Logf:    log.Printf,
			}
		}
		// §12.4 Quota/Rate Limiting concern: the §11.1 rate-limit counter
		// is Redis-backed so requests-per-minute limits hold across replicas.
		rateLimiter = ratelimitredis.New(concernRedis.For(storerouter.RedisConcernQuota))
		switch {
		case *redisClusterAddrs != "":
			log.Printf("lenny-gateway: Redis via Cluster nodes=%d split=%t; coordination replica %s",
				len(splitAndTrim(*redisClusterAddrs)), concernRedis.Split(), replica)
		case *redisSentinelAddrs != "":
			log.Printf("lenny-gateway: Redis via Sentinel master=%q sentinels=%d split=%t; coordination replica %s",
				*redisSentinelMaster, len(splitAndTrim(*redisSentinelAddrs)), concernRedis.Split(), replica)
		default:
			log.Printf("lenny-gateway: circuit-breaker state in Redis split=%t; coordination replica %s", concernRedis.Split(), replica)
		}
	} else {
		breakers = breakerstore.NewMemory()
	}

	// §12.3 R-03 line 144: billing and audit writes route through the
	// StoreRouter so a future Tier-3 shard split is a router swap with no
	// billing/audit call-site changes. v1 wires the single-shard router;
	// it runs in Postgres-only mode when Redis is unconfigured because
	// the billing/audit paths route only Postgres shards. The router is
	// built only with a real Postgres pool; in-memory deployments keep
	// the billingstore.NewMemory ledger set above and leave storeRouter
	// nil (the audit chain below is likewise Postgres-only). The redis
	// client is passed through the UniversalClient interface only when it
	// is a real *redis.Client — a typed-nil would defeat the nil check in
	// NewSingleShardRouter, matching the securityBus guard below.
	// F-12.3.4 / F-12.6.1 / F-12.2.13 / F-12.6.2 / F-12.7.1.
	// §12.6 lines 556-558 scatter-gather execution bounds, resolved from
	// the storeRouter.* Helm values. The single-shard v1 router satisfies
	// them trivially; the bounds and the §12.6 line 560 metrics are wired
	// now so a later multi-shard split needs no retrofit. F-12.6.18.
	scatterCfg := storerouter.ScatterConfig{
		MaxConcurrency:   *scatterMaxConcurrency,
		PerShardTimeout:  time.Duration(*scatterPerShardTimeoutSeconds) * time.Second,
		AggregateTimeout: time.Duration(*scatterAggregateTimeoutSeconds) * time.Second,
	}
	var scatterRouter *storerouter.SingleShardRouter
	var storeRouter storerouter.StoreRouter
	if pgPool != nil {
		// §12.4 lines 237-245: the router resolves each RedisConcern to
		// its own client when an operator has split concerns onto separate
		// instances; concernRedis.ByConcern() is nil for the single Tier
		// 1/2 topology, so RedisShard falls back to the base client.
		// PlatformRedis (pod slot counters, circuit breakers) rides on the
		// Coordination instance per the §12.4 table.
		r, err := storerouter.NewSingleShardRouter(storerouter.Config{
			Postgres:             pgPool,
			BillingAuditPostgres: billingAuditPool,
			Redis:                redisClient,
			RedisByConcern:       concernRedis.ByConcern(),
			PlatformRedisClient:  concernRedis.For(storerouter.RedisConcernCoordination),
			Scatter:              scatterCfg,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: store router: %v", err)
		}
		storeRouter = r
		scatterRouter = r
		billing = billingpg.New(r)
	}

	// §11 line 37 storage-quota release. The cataloging decorator
	// decrements the per-tenant storage counter by a deleted artifact's
	// size after its catalog row commits with `deleted_at` set, so a
	// terminal session's GC-collected artifacts free their reserved
	// bytes and a tenant near its cap can upload again. The decorator is
	// built before storageCounter is resolved (the Redis-backed counter
	// is wired above), so the releaser is installed here through the
	// setter. F-11.2.9.
	if blobsCataloged != nil {
		blobsCataloged.SetQuotaReleaser(storageCounter)
		// §11 line 37 Redis-restart rehydration: reconstruct each tenant's
		// storage counter from the authoritative sum of live artifact bytes
		// in Postgres (same recovery path as the token quota counters). A
		// per-tenant fault is logged and skipped so one tenant cannot block
		// startup. Runs before the HTTP listener accepts traffic, so no
		// reservation races the absolute Set.
		if artifactCatalog != nil {
			rehydrateCtx, cancelRehydrate := context.WithTimeout(context.Background(), 30*time.Second)
			if ids, lerr := (tenantsLister{tenants}).ListTenants(rehydrateCtx); lerr != nil {
				log.Printf("lenny-gateway: §11 storage-quota rehydration: list tenants: %v", lerr)
			} else if rerr := storagequota.Rehydrate(rehydrateCtx, storageCounter, ids, artifactCatalog.SumLiveBytes); rerr != nil {
				log.Printf("lenny-gateway: §11 storage-quota rehydration: %v", rerr)
			} else if len(ids) > 0 {
				log.Printf("lenny-gateway: §11 storage-quota counters rehydrated from artifact_store for %d tenants", len(ids))
			}
			cancelRehydrate()
		}
	}

	// §4.9 / §10.3 / §13.3 security-cache pub/sub substrate. The
	// gateway's revocation cache and the two deny lists are per-replica
	// in-memory sets; the Bus fans a local mutation out to peer replicas
	// over Redis pub/sub so a revocation takes effect fleet-wide. With
	// no Redis the Bus stays nil, which the propagators treat as the
	// single-replica mode: every cache stays local and nothing is
	// published. redisClient is a redis.UniversalClient set only on the
	// Redis-configured path, so its nil is a genuine interface nil the
	// guard below detects; the per-concern client resolves through the
	// same nil base when no split is configured.
	var securityBus *pubsub.Bus
	if redisClient != nil {
		// §12.4 Cache/Pub-Sub concern: revocation fan-out is event pub/sub.
		securityBus = pubsub.New(concernRedis.For(storerouter.RedisConcernCachePubSub))
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
	var billingTier *redisstream.Tier
	if pgStore, ok := billing.(*billingpg.Store); ok && redisClient != nil {
		tier, err := redisstream.New(redisstream.Options{
			// §12.4 Quota/Rate Limiting concern: billing stream.
			Client:       concernRedis.For(storerouter.RedisConcernQuota),
			ConsumerName: replica,
			Inserter:     pgStore,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: billing failover stream: %v", err)
		}
		billingStream = tier
		billingTier = tier
		log.Printf("lenny-gateway: §11.2.1 billing failover Tier 1 backed by the Redis stream (consumer %s)", replica)
	} else {
		billingStream = failover.NewMemStream()
	}
	billingPipeline := failover.New(failover.Options{
		Primary: billing,
		Stream:  billingStream,
		// §12.3 line 76 billingFlushIntervalMs / billingFlushBatchSize /
		// billingFlushMaxPending. OnFlushPressure is wired after
		// gatewaymetrics.New() below. F-12.3.13.
		FlushInterval: time.Duration(*billingFlushIntervalMs) * time.Millisecond,
		BatchSize:     *billingFlushBatchSize,
		MaxPending:    *billingFlushMaxPending,
		Clock:         clockinject.Now,
	})
	// The pipeline is a billingstore.Store, so it replaces the bare
	// ledger everywhere downstream — billing emission, the metering API,
	// and the billing-correction workflow all write through the failover
	// path. billingLedger keeps a handle to the un-wrapped store for the
	// erasure job's pseudonymize path, which operates on the durable
	// store directly.
	billingLedger := billing
	billing = billingPipeline

	// spec: §11.2.1 line 151 / §12.8 line 839 — reject a retention window
	// below the compliance floor of any tenant's regulated
	// complianceProfile at startup. billing.retentionDays floors at the
	// per-profile billing floor (hipaa 2190, soc2/fedramp 365);
	// audit.gdprRetentionDays floors at 2190 (6 years) under any regulated
	// profile so gdpr.* erasure receipts outlive the erased user's data
	// and any subsequent tenant deletion. A transient tenant-list failure
	// degrades to a warning rather than crashing the boot. F-11.2.15,
	// F-12.8.16.
	// §16.4 audit.retentionPreset: resolve the compliance-aware retention
	// bundle for non-gdpr audit rows. A preset typo is a fatal config
	// error (the closed §16.4 enum); a valid preset fixes the retention
	// window, and `custom` uses --audit-retention-days. The resolved
	// window is emitted on lenny_audit_retention_days below so the §16.5
	// AuditRetentionLow alert can evaluate. F-16.4.10.
	auditRetentionPresetValue := audit.RetentionPreset(*auditRetentionPreset)
	if !auditRetentionPresetValue.IsValid() {
		log.Fatalf("lenny-gateway: §16.4 audit.retentionPreset %q is not a valid preset (soc2, fedramp-high, hipaa, nis2-dora, custom)", *auditRetentionPreset)
	}
	effectiveAuditRetentionDays := audit.ResolveRetentionDays(auditRetentionPresetValue, *auditRetentionDays)

	if profiles, err := activeComplianceProfiles(context.Background(), tenants); err != nil {
		log.Printf("lenny-gateway: WARNING: retention-days compliance-floor preflight could not list tenants: %v", err)
	} else {
		if err := billingretention.ValidateRetentionDays(*billingRetentionDays, profiles); err != nil {
			log.Fatalf("lenny-gateway: %v", err)
		}
		if err := audit.ValidateGDPRRetentionDays(*gdprRetentionDays, profiles); err != nil {
			log.Fatalf("lenny-gateway: %v", err)
		}
		// §16.4 preset × compliance-profile pairing matrix. A mismatch is
		// a diagnostic warning rather than a fatal error: the resolved
		// window still satisfies the compliance minimum (a stricter preset
		// only lengthens retention), and a single global preset cannot
		// satisfy a deployment that mixes incompatible regulated profiles.
		// The warning names the compatible presets so an operator can
		// align the configuration. F-16.4.10.
		for _, p := range profiles {
			if err := audit.ValidatePairing(auditRetentionPresetValue, audit.ComplianceProfile(p)); err != nil {
				log.Printf("lenny-gateway: WARNING: §16.4 %v", err)
			}
		}
	}
	log.Printf("lenny-gateway: §12.8 audit.gdprRetentionDays floor active (gdpr.* retention %d days)", *gdprRetentionDays)
	log.Printf("lenny-gateway: §16.4 audit.retentionPreset=%s (non-gdpr retention %d days)", auditRetentionPresetValue, effectiveAuditRetentionDays)

	// spec: §11.7 item 2 lines 357-359 — resolve the periodic background
	// integrity-check cadence against the active compliance posture. Any
	// tenant with a regulated complianceProfile (soc2, fedramp, hipaa)
	// tightens both the default (60s) and the maximum (120s); an
	// unregulated deployment defaults to 300s with a 900s ceiling. A
	// configured value above the profile maximum is a fatal startup
	// error. The periodic goroutine is started under watchdogCtx below.
	// F-11.7.3.
	grantCheckRegulated := false
	if profiles, err := activeComplianceProfiles(context.Background(), tenants); err != nil {
		log.Printf("lenny-gateway: WARNING: §11.7 grant-check cadence preflight could not list tenants: %v", err)
	} else {
		for _, p := range profiles {
			if audit.ComplianceProfile(p).IsRegulated() {
				grantCheckRegulated = true
				break
			}
		}
	}
	resolvedGrantCheckInterval, err := integrity.ResolveGrantCheckInterval(
		time.Duration(*auditGrantCheckIntervalSeconds)*time.Second, grantCheckRegulated)
	if err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}

	// spec: §11.7 line 450 — a regulated-profile tenant with no configured
	// audit.siem.endpoint is a fatal startup error in production mode; a
	// non-production deployment logs a warning and continues. This catches
	// a SIEM endpoint accidentally removed from Helm values from silently
	// invalidating a live compliance posture. F-11.7.2.
	if err := admin.ValidateSIEMForRegulatedTenants(context.Background(), tenants, *auditSIEMEndpoint != ""); err != nil {
		if err.Error() == admin.SIEMStartupFatalMessage {
			if os.Getenv("LENNY_ENV") == "production" {
				log.Fatalf("lenny-gateway: %s", err.Error())
			}
			log.Printf("lenny-gateway: WARNING: %s (non-production, continuing)", err.Error())
		} else {
			log.Printf("lenny-gateway: WARNING: §11.7 SIEM compliance preflight could not list tenants: %v", err)
		}
	}

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
	kmsBackedSigner, err := jwt.NewKMSSigner(context.Background(), kmsProvider, jwt.TokenServiceKEKAlias, "boot")
	if err != nil {
		log.Fatalf("lenny-gateway: kms-backed jwt signer: %v", err)
	}
	// spec: §10.2 line 225 — wrap the KMS-backed signer in the
	// JWTSigner circuit breaker. More than 3 consecutive Sign failures
	// inside a 30s window trips the breaker open; subsequent Sign calls
	// short-circuit to ErrSigningUnavailable until the cooldown elapses.
	// The Token Service handler maps the sentinel to 503
	// KMS_SIGNING_UNAVAILABLE with retryable: true. The Observer is
	// wired after gatewaymetrics.New() below so the breaker can push the
	// signing-error counter and circuit-state gauge. F-10.2.6.
	kmsBreakerObs := &kmsBreakerObserver{}
	jwtSigner := &jwt.BreakerSigner{
		Inner:    kmsBackedSigner,
		Observer: kmsBreakerObs,
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
	// Verifier uses kmsBackedSigner directly: verification is local
	// memory and doesn't reach KMS, so it must not gate on the §10.2
	// signing breaker. F-10.2.6.
	rotatingVerifier := jwt.NewRotatingVerifier(kmsBackedSigner, jwt.DefaultOverlapWindow)

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
		// spec: §10.2 line 195. The bare HMAC signer is the dev-mode
		// backend; the spec is explicit that it "must never be used in
		// production deployments". --bearer-trust-hmac-key-file is the
		// §17.4 Embedded Mode hook that trusts the bundled OIDC
		// provider's HMAC key; it has no production use case. Refuse
		// to load it when --dev-mode is off so a misconfigured chart
		// fails closed at startup instead of silently widening the
		// trust set. F-10.2.13.
		if !*devMode {
			log.Fatalf("lenny-gateway: --bearer-trust-hmac-key-file requires --dev-mode (§10.2 line 195: the dev HMAC backend must never be used in production)")
		}
		trusted, err := jwt.LoadHMACKeyFile(*bearerTrustHMACKeyFile)
		if err != nil {
			log.Fatalf("lenny-gateway: --bearer-trust-hmac-key-file: %v", err)
		}
		bearerVerifier = jwt.NewMultiVerifier(rotatingVerifier, trusted)
		log.Printf("lenny-gateway: trusting an additional HMAC bearer key from %s (kid %s)",
			*bearerTrustHMACKeyFile, trusted.KeyID())
	}
	// spec: §10.2 line 237 — wrap the verifier so the standard auth
	// chain enforces iss / aud alongside signature / exp / nbf when an
	// operator configures the expected values. An unset flag skips
	// the corresponding check so dev deployments retain their existing
	// posture.
	expectedAuds := splitCSV(*bearerExpectedAudiences)
	if *bearerExpectedIssuer != "" || len(expectedAuds) > 0 {
		bearerVerifier = jwt.NewClaimChecker(bearerVerifier, jwt.ExpectedClaims{
			Issuer:    *bearerExpectedIssuer,
			Audiences: expectedAuds,
		})
		log.Printf("lenny-gateway: §10.2 bearer iss/aud enforced iss=%q audiences=%v",
			*bearerExpectedIssuer, expectedAuds)
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
		// clusterClient is the controller-runtime client used by the
		// session-start path and by the §10.4 PDB poller (F-10.4.4). It
		// stays nil when --agent-namespace is unset (single-process dev).
		clusterClient client.Client
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
		clusterClient = k8sClient
		dialOpt, err := adapter.TLSClientOption(*adapterTLSCert, *adapterTLSKey, *adapterCA)
		if err != nil {
			log.Fatalf("lenny-gateway: adapter TLS: %v", err)
		}
		// spec: §11.3 lines 205-206 — gateway→pod keepalive: 10s interval
		// and 5s timeout. Without these the gRPC client library default
		// (no keepalive) leaves a half-open TCP connection holding
		// adapter state past the §11.3 timeout. F-11.3.12.
		keepaliveOpt := grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                time.Duration(*adapterKeepaliveTimeMs) * time.Millisecond,
			Timeout:             time.Duration(*adapterKeepaliveTimeoutMs) * time.Millisecond,
			PermitWithoutStream: true,
		})
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
			slotCounter = slotcounter.New(concernRedis.For(storerouter.RedisConcernCoordination), slotcounter.WithSlotSource(sessions))
		}
		podBinder = &podsession.Binder{
			Client:           k8sClient,
			Namespace:        *agentNamespace,
			AdapterPort:      adapterGRPCPort,
			AcceptedVersions: []string{adapter.ProtocolVersionV1},
			DialAdapter: func(addr string) (*adapterclient.Client, error) {
				return adapterclient.Dial(addr, dialOpt, keepaliveOpt)
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

	// spec: §10.4 line 389 — replay buffer depth is operator-tunable
	// via gateway.sessionEventReplayBufferDepth. F-10.4.5.
	eventBus := sessionevents.NewBus(*sessionEventReplayBufferDepth)
	// §4.4 line 225 / §12.3.7: when Redis is wired, attach the
	// cross-replica relay so a client reconnecting via Last-Event-ID
	// to a different replica sees prior events (the §15.1 streaming-
	// reconnect contract). Single-replica dev mode keeps the Bus's
	// in-memory-only behaviour.
	if redisClient != nil {
		// §12.4 Cache/Pub-Sub concern: session-event relay.
		eventBus = eventBus.WithRedisRelay(sessionevents.NewRedisRelay(concernRedis.For(storerouter.RedisConcernCachePubSub)))
		log.Printf("lenny-gateway: §4.4 session SSE event bus relay attached to Redis (cross-replica replay enabled)")
	}
	// spec: §7.3 line 397 — wire the durable last_seq writer + the
	// coordinator-handoff seed loader so per-session SeqNum survives
	// replica restart and handoff without rewinds. The sessions
	// store carries the persisted last_seq column; both hooks are
	// best-effort (a Postgres outage degrades to the local counter).
	// F-7.3.3.
	if sessions != nil {
		lastSeq := lastSeqStore{sessions: sessions}
		eventBus = eventBus.WithLastSeqPersister(lastSeq).WithLastSeqLoader(lastSeq)
	}
	// spec: §7.2 lines 274-343 — the session inbox + DLQ coordinator.
	// The DLQ is a Redis sorted set, so durability requires Redis; the
	// dev / no-Redis posture leaves messagingCoord nil and the gateway
	// no-ops the inbox-to-DLQ migration and the terminal DLQ drain. The
	// inbox is in-memory by default and Redis-list-backed when
	// durableInbox is set (§7.2 line 286). F-7.2.4, F-12.4.6, F-7.3.12.
	var messagingCoord *sessioninbox.Coordinator
	if redisClient != nil {
		// §12.4 Session-data concern: durable inbox + DLQ.
		sessionDataClient := concernRedis.For(storerouter.RedisConcernSessionData)
		var inbox sessioninbox.Inbox
		if *messagingDurableInbox {
			inbox = sessioninbox.NewRedisInbox(sessionDataClient, *messagingMaxInboxSize)
		} else {
			inbox = sessioninbox.NewMemoryInbox(*messagingMaxInboxSize)
		}
		messagingCoord = sessioninbox.NewCoordinator(sessioninbox.Config{
			Inbox:   inbox,
			DLQ:     sessioninbox.NewDLQ(sessionDataClient, *messagingMaxDLQSize),
			Emitter: sessionserver.NewBusEmitter(eventBus, clockinject.Now),
			Now:     clockinject.Now,
			Durable: *messagingDurableInbox,
		})
	}
	// One §8.10 tree archive shared by the sessionserver (which archives
	// children on terminal transitions) and the platform MCP tools. In
	// production the durable record lives in Postgres (migration 0100);
	// a per-replica LRU cache (§8.10 line 129, default 128 entries)
	// fronts it so a parent re-reading a child's result does not hit
	// Postgres every time. Developer mode keeps the in-memory archive,
	// which is durable for the lifetime of the single replica.
	var treeArchive treearchive.Store = treearchive.NewMemory()
	if pgPool != nil {
		treeArchive = treearchive.NewCached(treearchivepg.New(pgPool, nil), *treeArchiveCacheEntries)
	}
	// §8.2 lines 57, 127 / §12.4 lines 193, 213: the Redis-backed
	// per-tree delegation budget counters gate every admission and are
	// decremented when a child settles (the §8.2 line 130 completed-
	// subtree offload). Shared by the delegation service (admission
	// Reserve) and the sessionserver (terminal-state Return). When Redis
	// is not configured the reserver stays nil and only the static
	// ValidateChildSlice ceiling is enforced. F-8.2.18 / F-8.2.12 / F-8.2.13.
	var treeBudgetReserver delegation.TreeBudgetReserver
	// hwmReader is the concrete *treebudget.Reserver kept alongside the
	// interface so the §8.3 line 379 high-watermark read (not part of the
	// narrow Reserve/Return interfaces) is reachable from the
	// sessionserver. Nil when Redis is not configured, which leaves the
	// nil interface genuinely nil so the typed-nil-in-interface trap is
	// avoided. F-8.9.6.
	var hwmReader sessionserver.DelegationHighWatermarkReader
	// treeBudgetConcrete keeps the concrete *treebudget.Reserver so the
	// §11.2 delegation-budget checkpoint/reconstruction reconciler can
	// call Snapshot/Restore (not part of the narrow Reserve/Return
	// interfaces). Nil when Redis is not configured. F-11.2.5.
	var treeBudgetConcrete *treebudget.Reserver
	if redisClient != nil {
		// §12.4 Delegation concern: tree-budget keys {root_session_id}:dlg:*.
		r := treebudget.New(concernRedis.For(storerouter.RedisConcernDelegation), 0)
		treeBudgetReserver = r
		hwmReader = r
		treeBudgetConcrete = r
	}
	// One §9.2 interaction store shared by the sessionserver (which
	// serves the respond/dismiss endpoints) and the platform MCP tools
	// (lenny/request_elicitation), so an elicitation a tool records is
	// resolvable through the REST surface.
	var interactions interactionstore.Store = interactionstore.NewMemory()
	if pgPool != nil {
		interactions = interactionpg.New(pgPool)
	}
	// spec: §7.2 lines 124-134 — the tool-use approval loop. When a
	// runtime emits a tool_call(approvalRequired) over the §4.7 Attach
	// stream the pod executor consults the gate, which records the
	// KindToolUse interaction, publishes the tool_use_requested SSE
	// event, and blocks until the §15.1 approve/deny endpoint delivers
	// the verdict onto this shared waiter registry. The pod executor is
	// the only producer of approval-required frames, so the gate is wired
	// only when exec is a *PodExecutor (the echo / subprocess dev posture
	// never blocks on approval). F-7.2.9, F-7.2.18.
	toolApprovalWaits := toolapproval.NewRegistry()
	if pe, ok := exec.(*executor.PodExecutor); ok {
		pe.SetApprovalGate(sessionserver.NewToolApprovalGate(
			sessions, interactions, eventBus, toolApprovalWaits, clockinject.Now, *toolApprovalTimeout))
	}
	var evals evalstore.Store = evalstore.NewMemory(0, nil)
	if pgPool != nil {
		evals = evalpg.New(pgPool)
	}
	var experiments experimentstore.Store = experimentstore.NewMemory()
	if pgPool != nil {
		experiments = experimentpg.New(pgPool)
	}
	// spec: §9.4 line 196 / §12.8 line 746 — when the feature flag
	// disables the MemoryStore, construct no store. The MCP tools
	// short-circuit on a nil Memory in mcptools.Deps. F-9.4.7.
	var memories memorystore.Store
	var memoryBackendLabel string
	if *memoryEnabled {
		// spec: §9.4 line 198 — the Embedder seam advertised for custom
		// providers is preflighted at startup so a misconfigured
		// dimension width (e.g., a provider that returns 1536-wide
		// vectors against the migration-0044 vector(256) column) fails
		// at boot rather than corrupting every Write with an unfriendly
		// pgvector dimension-mismatch error. The built-in
		// HashingEmbedder is correct by construction; the preflight is
		// still cheap and pins the contract for future seam swaps.
		// F-9.4.8.
		if err := memorystore.ValidateEmbedder(memorystore.NewHashingEmbedder()); err != nil {
			log.Fatalf("lenny-gateway: memorystore embedder preflight failed (§9.4 line 198): %v", err)
		}
		if pgPool != nil {
			pgmem := memorypg.NewWithMaxPerUser(pgPool, *memoryMaxPerUser)
			memories = pgmem
			memoryBackendLabel = "postgres"
		} else {
			inMem := memorystore.NewInMemory(*memoryMaxPerUser, nil)
			memories = inMem
			memoryBackendLabel = "memory"
		}
	}

	// ----- §16.1 Prometheus metrics -----
	gwMetrics, err := gatewaymetrics.New()
	if err != nil {
		log.Fatalf("lenny-gateway: metrics: %v", err)
	}
	// spec: §10.2 line 225 — back-fill the JWTSigner breaker observer
	// with the freshly-built metrics so signing failures and circuit
	// transitions land on `lenny_gateway_kms_signing_errors_total` and
	// `lenny_gateway_kms_signing_circuit_state`. F-10.2.6.
	kmsBreakerObs.SetMetrics(gwMetrics)
	// spec: §12.6 line 560 — register the scatter-gather duration histogram
	// and shard-count gauge and attach them to the store router so the §16
	// ScatterGatherSlowQuery alert has a series. The router is built before
	// the metrics registerer, so the collector is wired here. F-12.6.18.
	if scatterRouter != nil {
		scatterMetrics, err := storerouter.NewScatterMetrics(gwMetrics.Registerer())
		if err != nil {
			log.Fatalf("lenny-gateway: scatter-gather metrics: %v", err)
		}
		scatterRouter.SetScatterMetrics(scatterMetrics)
	}
	// spec: §13.3 line 595 — NTP drift self-monitor. The source returns
	// the clockinject-injected offset for v1 (zero in production unless
	// an operator wires a real adjtimex/chrony probe). /healthz and any
	// downstream consumer (currently the embedded TokenService path,
	// which lives in lenny-token-service) consult driftMonitor.Degraded.
	// F-13.3.5.
	driftMonitor := driftmonitor.New(func() time.Duration {
		off, _ := clockinject.Offset()
		return off
	}, gwMetrics)
	// spec: §10.1 — dual-store degraded-mode monitor. It is active only
	// when both coordination stores are wired; an in-memory / single-store
	// dev posture has no "both down" condition to detect. The monitor
	// probes Postgres and Redis on a short cadence and, on detecting both
	// unreachable, pins lenny_dual_store_unavailable=1, broadcasts a
	// PLATFORM_DEGRADED SSE event to every active client stream, and gates
	// session.create with 503 + Retry-After: 10 (via DualStore on the
	// session-server Options). The per-replica dualStoreUnavailableMaxSeconds
	// countdown is anchored at detection. F-10.1.3.
	var dsMonitor *dualstore.Monitor
	if pgPool != nil && redisClient != nil {
		pgPoolRef := pgPool
		redisRef := redisClient
		dsMonitor = &dualstore.Monitor{
			PostgresProbe: func(ctx context.Context) bool {
				pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				return pgPoolRef.Ping(pctx) == nil
			},
			RedisProbe: func(context.Context) bool {
				return redisconn.PingWithTimeout(redisRef, 2*time.Second) == nil
			},
			Gauge:          gwMetrics,
			Streams:        eventBus,
			MaxUnavailable: time.Duration(*dualStoreMaxSeconds) * time.Second,
			Logf:           func(format string, args ...any) { log.Printf(format, args...) },
		}
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
		// §6.3 line 352 / §16.1 line 122: emit lenny_warmpool_claims_total
		// on each idle→claimed transition so deployers can read the
		// denominator of the SDK-warm demotion-rate ratio.
		podBinder.ClaimAccepted = gwMetrics.IncWarmpoolClaim
	}
	// §16.1 lines 51, 53, 55: emit credential-lease assignment, lease
	// duration, and pool-utilization telemetry from the in-process
	// assignment service. The Token Service client path emits its own
	// §16.1 metrics on its registry.
	if inProcessAssign != nil {
		inProcessAssign.SetMetrics(gwMetrics)
	}
	// spec: §9.4 line 200 / §16.1 lines 151-154 — wire the MemoryStore
	// Observer once gatewaymetrics is ready. The §16.1 `backend` label
	// is the bound implementation tag (`postgres` for the pgvector
	// backend, `memory` for the in-process test backend). F-9.4.1 /
	// F-9.4.6.
	if memories != nil {
		obs := memoryStoreObserver{metrics: gwMetrics, backend: memoryBackendLabel}
		switch s := memories.(type) {
		case *memorystore.InMemory:
			s.SetObserver(obs)
		case *memorypg.Store:
			s.SetObserver(obs)
		}
	}
	// spec: §12.8 lines 743-758 — MemoryStore erasure preflight (stub
	// detection, defense-in-depth layer 2). Before serving traffic, seed a
	// probe memory under the reserved (__preflight__, __preflight_user__)
	// scope, erase it, and assert it does not survive. A backend whose
	// DeleteByUser / DeleteByTenant satisfies the interface but silently
	// no-ops makes the gateway refuse to start, so a GDPR erasure can never
	// report success while memories persist. Skipped when memory.enabled is
	// false (no store wired). F-9.4.3 / F-12.1.4 / F-12.2.10 / F-12.8.9.
	if memories != nil {
		preflightCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		preflightErr := memorystore.ValidateMemoryStoreErasure(preflightCtx, memories)
		cancel()
		if preflightErr != nil {
			log.Fatalf("FATAL: MemoryStore preflight failed — configured backend (%s) does not honor DeleteByUser; GDPR erasure would silently succeed while leaving memories in place (Section 12.8): %v", memoryBackendLabel, preflightErr)
		}
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

	// spec: §8.10 line 1103 + §16.5 OrphanTasksPerTenantHigh — publish the
	// configured maxOrphanTasksPerTenant cap so the alert's
	// `scalar(lenny_max_orphan_tasks_per_tenant)` denominator resolves to
	// the live ceiling. The Helm-driven flag is the source of truth; the
	// sessionserver constructor also receives the same value via the
	// Options.MaxOrphanTasksPerTenant field so the detach-cascade fallback
	// path and the alert evaluate against one shared cap. F-8.10.10 /
	// F-8.10.13.
	gwMetrics.SetMaxOrphanTasksPerTenant(*delegationMaxOrphanTasksPerTenant)

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
	// spec: §10.4 line 389 — accepted range 64..4096 events.
	if *sessionEventReplayBufferDepth < 64 || *sessionEventReplayBufferDepth > 4096 {
		log.Fatalf("lenny-gateway: --session-event-replay-buffer-depth must be in [64, 4096] (got %d) (§10.4 line 389)", *sessionEventReplayBufferDepth)
	}
	gwMetrics.SetMinReplicas(*minReplicas)
	gwMetrics.SetStreamCeiling(*streamCeiling)
	gwMetrics.SetReplicaCount(1)
	// §16.4 / §16.5: emit the audit alert-support scalars so the
	// AuditSIEMNotConfigured and AuditRetentionLow expressions resolve.
	// siemConfigured is the suppression term (1 when audit.siem.endpoint
	// is set), retentionDays is the resolved §16.4 window, and
	// envProduction gates both alerts to production. F-16.4.9; F-16.4.10.
	gwMetrics.SetAuditSIEMConfigured(*auditSIEMEndpoint != "")
	gwMetrics.SetAuditRetentionDays(effectiveAuditRetentionDays)
	gwMetrics.SetEnvProduction(os.Getenv("LENNY_ENV") == "production")
	// spec: §12.3 line 99 — in production with T2 audit batching enabled
	// but no SIEM endpoint, there is no external durable copy to recover
	// buffered T2 events from on a crash. Warn at startup and emit the
	// AuditBatchingNoSIEM counter. F-12.3.15.
	if auditBatchingNoSIEM(os.Getenv("LENNY_ENV"), *auditBatchingEnabled, *auditSIEMEndpoint != "") {
		log.Printf("lenny-gateway: WARNING: Audit batching is enabled for T2 events but no SIEM is configured — buffered T2 audit events will be lost on gateway crash")
		gwMetrics.IncAuditBatchingNoSIEM()
	}
	// spec: §11.2.1 line 187 — emit the configured BillingCorrectionRateHigh
	// threshold as a startup-set scalar gauge so the alert expression in
	// pkg/alerting/rules can read it via scalar(lenny_billing_correction_rate_threshold).
	if *billingCorrectionRateThreshold < 0 || *billingCorrectionRateThreshold > 1 {
		log.Fatalf("lenny-gateway: --billing-correction-rate-threshold must be in [0, 1] (got %v) (§11.2.1 / §16.5)", *billingCorrectionRateThreshold)
	}
	gwMetrics.SetBillingCorrectionRateThreshold(*billingCorrectionRateThreshold)

	// §12.3 line 76 — wire the billing_flush_pressure callback now that
	// the metric registry exists (the billing Pipeline was constructed
	// earlier). F-12.3.13.
	billingPipeline.SetFlushPressureHook(gwMetrics.IncBillingFlushPressure)
	// §12.3 line 123 — emit the configured Postgres sustained write-IOPS
	// ceiling so the §16.5 PostgresWriteSaturation alert resolves
	// scalar(lenny_postgres_write_ceiling_iops). F-12.3.8.
	if *postgresWriteCeilingIops <= 0 {
		log.Fatalf("lenny-gateway: --postgres-write-ceiling-iops must be > 0 (got %v) (§12.3 line 123)", *postgresWriteCeilingIops)
	}
	gwMetrics.SetPostgresWriteCeilingIops(*postgresWriteCeilingIops)
	// §12.3 line 101 — startup chain-continuity check. After Postgres is
	// reachable the gateway re-verifies the most recent
	// audit.startupChainCheckEntries rows of each tenant's hash chain,
	// emits lenny_audit_chain_integrity_total per tenant, and logs a WARN
	// per detected gap. It never refuses to start — a gap is a compliance
	// signal. The check reads audit_log from the instance it lives on —
	// the separate §12.3 billing/audit pool when configured. F-12.3.9 /
	// F-12.3.5.
	if pgPool != nil {
		chainPool := pgPool
		if billingAuditPool != nil {
			chainPool = billingAuditPool
		}
		runStartupChainContinuityCheck(context.Background(), chainPool, *auditStartupChainCheckEntries, gwMetrics)
	}

	// spec: §25.13 line 4737 / §16.5 — emit the configured Tier preset
	// for the §25.13 tier-dependent thresholds (gateway queue depth,
	// gateway p95 latency, credential pool utilisation). The
	// corresponding alert expressions read each value via scalar(...),
	// so a tier preset tightening the threshold flows through to the
	// bundled manifest without re-rendering the rule body. F-25.13.2.
	if *gatewayQueueDepthThreshold < 0 {
		log.Fatalf("lenny-gateway: --gateway-queue-depth-threshold must be >= 0 (got %v) (§25.13 line 4737)", *gatewayQueueDepthThreshold)
	}
	gwMetrics.SetGatewayQueueDepthThreshold(*gatewayQueueDepthThreshold)
	if *gatewayLatencyThresholdSeconds < 0 {
		log.Fatalf("lenny-gateway: --gateway-latency-threshold-seconds must be >= 0 (got %v) (§25.13 line 4737)", *gatewayLatencyThresholdSeconds)
	}
	gwMetrics.SetGatewayLatencyThresholdSeconds(*gatewayLatencyThresholdSeconds)
	if *credentialPoolLowThreshold < 0 || *credentialPoolLowThreshold > 1 {
		log.Fatalf("lenny-gateway: --credential-pool-low-threshold must be in [0, 1] (got %v) (§25.13 line 4737)", *credentialPoolLowThreshold)
	}
	gwMetrics.SetCredentialPoolLowThreshold(*credentialPoolLowThreshold)

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
		auditSink            admin.AuditSink
		wireAudit            func(*admin.Router) *admin.Router
		auditAppender        policy.AuditAppender
		auditValidator       *auditscope.Validator
		ocsfTranslationStore ocsf.TranslationStore
		siemDeliveryStore    siem.DeliveryStore
		auditBatchBuffer     *auditbatch.Buffer
		auditPruner          *auditretention.Pruner
	)
	if pgPool != nil {
		// spec: §11.7 item 3 line 368 — bound the per-tenant audit
		// advisory-lock acquisition with the operator-tunable
		// statement_timeout + jittered retry budget, and emit the
		// lenny_audit_lock_acquire_seconds / _concurrency_timeout_total
		// series the AuditLockContention alert reads.
		auditLockMetrics, err := auditstore.NewLockMetrics(gwMetrics.Registerer())
		if err != nil {
			log.Fatalf("lenny-gateway: audit lock metrics: %v", err)
		}
		// §12.3 R-03: the audit chain routes through the same StoreRouter
		// built above (non-nil whenever pgPool is). F-12.3.4 / F-12.6.1.
		pgAudit := auditstore.New(storeRouter,
			auditstore.WithLockConfig(auditstore.LockConfig{
				AcquireTimeoutMs: *auditLockAcquireTimeoutMs,
				MaxRetries:       *auditLockMaxRetries,
				RetryBaseMs:      *auditLockRetryBaseMs,
			}),
			auditstore.WithLockMetrics(auditLockMetrics),
			// §12.3 line 79: route synchronous audit writes onto the
			// dedicated sync write pool when one was opened. F-12.3.14.
			auditstore.WithSyncWritePool(auditSyncPool))
		// §12.3 line 81: opt-in T2 audit-event batching. When enabled, the
		// non-PII cross_tenant_read worker receipts are buffered and
		// flushed in batches through the dedicated sync write pool instead
		// of one synchronous write each; T3/T4 PII audit events stay
		// synchronous. F-12.3.14.
		if *auditBatchingEnabled {
			auditBatchBuffer = auditbatch.New(pgAudit.AppendBatch, auditbatch.Config{
				FlushInterval: time.Duration(*auditFlushIntervalMs) * time.Millisecond,
				BatchSize:     *auditFlushBatchSize,
			}, nil)
			pgAudit.SetBatchBuffer(auditBatchBuffer)
		}
		// spec: §11.7 line 428 — guard the caller-driven audit-write
		// boundaries (the admin sink and the §4.8 policy-rejection sink)
		// with the write-time tenant-scope validator so a forged-tenant
		// row cannot be injected. Reads stay on the raw chain.
		auditValidator = auditscope.New(pgAudit, nil)
		auditSink = admin.NewAuditLogSink(auditValidator, nil)
		wireAudit = func(rt *admin.Router) *admin.Router { return rt.WithAuditLog(pgAudit) }
		// The §11.7 `interceptor.rejected` policy-rejection rows share
		// the durable Postgres-backed per-tenant hash chain.
		auditAppender = pgAudit
		// The auditstore drives the §11.7 OCSF translation state machine
		// (ocsf_translation_state). Hoisted so the OCSF translator wired
		// below reads pending rows from the durable chain. F-11.7.1.
		ocsfTranslationStore = pgAudit
		// The same durable chain backs the §12.3 SIEM outbox forwarder:
		// it tails committed audit_log rows past the per-tenant delivery
		// high-water mark in siem_delivery_state and checkpoints each
		// SIEM-acknowledged row. F-12.3.6.
		siemDeliveryStore = pgAudit
		// §16.4 lines 378-382 audit-retention pruner: a leader-elected
		// sweep deletes audit rows past audit.retentionDays, holding
		// gdpr.* erasure receipts under the longer audit.gdprRetentionDays
		// floor and any SIEM-undelivered row behind the delivery guard.
		// The forced-drop override records audit.partition_drop_forced on
		// the platform chain through pgAudit.Append. F-11.7.17.
		auditPruner = auditretention.New(
			pgAudit,
			auditPruneTenants{tenants},
			func(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) error {
				_, err := pgAudit.Append(ctx, tenantID, eventType, payload, at)
				return err
			},
			auditretention.Options{
				RetentionDays:     effectiveAuditRetentionDays,
				GDPRRetentionDays: *gdprRetentionDays,
				SIEMConfigured:    *auditSIEMEndpoint != "",
				Interval:          time.Duration(*auditRetentionPruneIntervalSeconds) * time.Second,
				Clock:             clockinject.Now,
			})
	} else {
		auditChains := audit.NewChainSet()
		auditValidator = auditscope.New(auditscope.NewChainSetChain(auditChains, nil), nil)
		auditSink = admin.NewAuditLogSink(auditValidator, nil)
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

	// spec: §11.7 item 4 + Wire Format — wire the OCSF translator and
	// SIEM forwarder into the gateway binary. The translator drains the
	// auditstore's ocsf_translation_state rows, serializes each to the
	// canonical OCSF v1.1.0 wire form, and multicasts to its sink; the
	// SIEM forwarder is that sink (it implements ocsf.Sink). When a SIEM
	// endpoint is configured the gateway validates connectivity at startup
	// and refuses to start until a test event is acknowledged; at runtime
	// the §25.3 health API reports the `siem` component degraded once the
	// delivery failure rate crosses the threshold. With no SIEM the
	// translator still advances the per-row state machine so audit rows do
	// not pin in `pending`. F-11.7.1 / F-11.7.11 / F-11.7.16.
	var (
		ocsfTranslator    *ocsf.Translator
		ocsfOutbox        *siem.Outbox
		siemHealthChecker health.Checker
	)
	ocsfCfg := ocsf.TranslationConfig{
		RetryInterval: time.Duration(*auditOCSFRetryIntervalSeconds) * time.Second,
		MaxAttempts:   *auditOCSFMaxAttempts,
		BatchSize:     *auditOCSFBatchSize,
	}
	// §12.3 line 97: emit the configured SIEM delivery-lag threshold so
	// AuditSIEMDeliveryLag compares lenny_audit_siem_delivery_lag_seconds
	// against an operator-tunable scalar rather than a literal. F-12.3.17.
	gwMetrics.SetSIEMMaxDeliveryLagSeconds(float64(*auditSIEMMaxDeliveryLagSeconds))
	if *auditSIEMEndpoint != "" {
		siemMetrics := siem.NewCountingMetrics()
		forwarder := siem.NewForwarder(
			siem.NewHTTPSink(siem.HTTPSinkOptions{
				Endpoint: *auditSIEMEndpoint,
				Secret:   *auditSIEMSecret,
			}),
			siem.ForwarderConfig{},
			siemMetrics,
		)
		validateCtx, cancelValidate := context.WithTimeout(context.Background(), 10*time.Second)
		if err := forwarder.ValidateConnectivity(validateCtx); err != nil {
			cancelValidate()
			log.Fatalf("lenny-gateway: §11.7 SIEM startup connectivity validation failed (the gateway refuses to start until the SIEM endpoint acknowledges a test event): %v", err)
		}
		cancelValidate()
		siemHealthChecker = backends.SIEM(forwarder, siemMetrics, *auditSIEMFailureThresholdPercent, "siem")
		if siemDeliveryStore != nil {
			// §12.3 line 97: durable Postgres chain → the SIEM egress is
			// the outbox / CDC forwarder. It tails committed audit_log
			// rows past the siem_delivery_state high-water mark and
			// advances the mark only after the SIEM acknowledges each
			// record, so a crash after a Postgres commit but before SIEM
			// delivery replays the row instead of losing it. The OCSF
			// translator no longer pushes to the SIEM (sink = nil) — it
			// only advances ocsf_translation_state — so the two paths do
			// not double-deliver. F-12.3.6.
			ocsfOutbox = siem.NewOutbox(siemDeliveryStore, forwarder,
				siem.OutboxConfig{PollInterval: time.Duration(*auditSIEMPollIntervalSeconds) * time.Second},
				gwMetrics)
			log.Printf("lenny-gateway: §12.3 SIEM outbox forwarder validated connectivity to %s; tailing committed audit rows (poll %ds)", *auditSIEMEndpoint, *auditSIEMPollIntervalSeconds)
			if ocsfTranslationStore != nil {
				ocsfTranslator = ocsf.NewTranslator(ocsfTranslationStore, nil, ocsfCfg, ocsfMetricsAdapter{metrics: gwMetrics})
			}
		} else if ocsfTranslationStore != nil {
			// No durable chain (in-memory, minimal gateway): there is no
			// audit_log table to tail, so fall back to the push-based
			// translator → SIEM forwarder path. F-11.7.1.
			log.Printf("lenny-gateway: §11.7 SIEM forwarder validated connectivity to %s; OCSF audit egress active (push mode, no durable chain)", *auditSIEMEndpoint)
			ocsfTranslator = ocsf.NewTranslator(ocsfTranslationStore, forwarder, ocsfCfg, ocsfMetricsAdapter{metrics: gwMetrics})
		}
	} else if ocsfTranslationStore != nil {
		ocsfTranslator = ocsf.NewTranslator(ocsfTranslationStore, nil, ocsfCfg, ocsfMetricsAdapter{metrics: gwMetrics})
	}

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
			// §12.4 Cache/Pub-Sub concern: ops event stream fan-out.
			Client:    concernRedis.For(storerouter.RedisConcernCachePubSub),
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
	// resolver reads the effective DelegationPolicy's per-policy
	// maxInputSize (filled in once delegationSvc exists, below); a
	// runtime that names no policy falls back to the operator-tunable
	// default cap (--delegation-max-input-size). F-13.5.1 / F-8.2.9.
	maxInputResolver := &maxInputSizeResolverHolder{}
	if err := policyChain.Register(interceptor.PhasePreDelegation,
		policy.NewDelegationPolicyEvaluator(maxInputResolver, *delegationMaxInputSize)); err != nil {
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
	// quotaCounter / tenantLimits are hoisted out of the redis-only block
	// so the §4.9 proxy usage recorder (built later) can advance the same
	// §11.2 hierarchical token counter QuotaEvaluator reads. Both stay nil
	// when --redis-url is unset, which leaves quota recording disabled.
	var quotaCounter *quotastore.Counter
	var tenantLimits *policy.TenantStoreLimits
	if redisClient != nil {
		quotaCounter = quotastore.New(concernRedis.For(storerouter.RedisConcernQuota))
		tenantLimits = policy.NewTenantStoreLimits(tenants, policy.TenantStoreLimitsOptions{
			GlobalTokenQuotaPerWindow: *globalTokenQuota,
			UserTokenQuotaPerWindow:   *userTokenQuota,
			RollingWindow:             time.Duration(*quotaRollingWindowSeconds) * time.Second,
		})
		quotaEval := policy.NewQuotaEvaluator(tenantLimits, quotaCounter, nil)
		if err := policyChain.Register(interceptor.PhasePostAuth, quotaEval); err != nil {
			log.Fatalf("lenny-gateway: register QuotaEvaluator: %v", err)
		}
		// spec: §11.7 line 428 — route policy-rejection audit rows through
		// the write-time tenant-scope validator alongside the admin sink.
		policyAuditSink = policy.NewAuditSink(auditValidator, nil)
		// spec: §11.2 line 44 — quotaSyncIntervalSeconds is the cadence
		// at which Redis quota and delegation-budget counters checkpoint
		// to Postgres. Clamp the operator-supplied value up to the 10s
		// floor so a misconfiguration cannot drive a busy-loop, and log
		// the effective cadence. F-11.2.16.
		quotaSyncSeconds := quota.ClampSyncIntervalSeconds(*quotaSyncIntervalSeconds)
		if quotaSyncSeconds != *quotaSyncIntervalSeconds && *quotaSyncIntervalSeconds > 0 {
			log.Printf("lenny-gateway: §11.2 line 44 quotaSyncIntervalSeconds=%d below the %ds floor; clamping to the minimum",
				*quotaSyncIntervalSeconds, quota.MinSyncIntervalSeconds)
		}
		log.Printf("lenny-gateway: §4.8 QuotaEvaluator enforcing §11.2 token budgets on the PostAuth chain (quota checkpoint cadence %ds)", quotaSyncSeconds)
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
	// §16.1: the upload-handler-specific byte-count and queue-depth
	// metrics (lenny_upload_bytes_total, lenny_upload_queue_depth) that
	// the unified per-subsystem family does not carry under their
	// catalogued names. F-13.4.12.
	uploadMetrics, err := sessionserver.NewUploadMetrics(gwMetrics.Registerer())
	if err != nil {
		log.Fatalf("lenny-gateway: upload metrics: %v", err)
	}

	// §8.5 lenny/request_input pending-call registry. Shared across the
	// sessionserver REST surface and the MCP tools so a REST
	// POST /v1/sessions/{id}/messages with `inReplyTo` resolves the
	// blocked tool call the MCP `lenny/request_input` registered.
	// spec: §7.2 line 317 (path 1); F-7.2.14.
	inputWaits := inputwait.NewRegistry()

	// spec: §10.7 lines 831, 1096 / §12.4 (`t:{tenant}:exp:{exp}:sticky:*`) —
	// the `sticky: user` variant-assignment cache. Backed by the cache/pubsub
	// Redis concern; nil without Redis, in which case the ExperimentRouter
	// re-evaluates every experiment fresh (the §12.4 fail-open path) and the
	// PATCH flush is a no-op. F-12.4.7 / F-10.7.6.
	var (
		sessionStickyCache sessionserver.StickyCache
		adminStickyFlusher admin.StickyFlusher
	)
	if redisClient != nil {
		stickyCache := experimentsticky.NewRedis(
			concernRedis.For(storerouter.RedisConcernCachePubSub),
			experimentsticky.WithInvalidationRecorder(gwMetrics),
		)
		// Assign to the interface variables only when constructed so the
		// nil-Redis posture leaves a genuine nil interface (not a typed-nil
		// *RedisCache the consumers would call methods on).
		sessionStickyCache = stickyCache
		adminStickyFlusher = stickyCache
	}

	sessionSrv := sessionserver.New(sessions, sessionserver.Options{
		// spec: §8.10 line 1103 — operator-tunable per-tenant orphan cap.
		// The default (100) flows through the constructor when the flag
		// is unset; an override surfaces both on the sessionserver
		// detach-cascade fallback and on the §16.5
		// OrphanTasksPerTenantHigh alert (the scalar() denominator below
		// is re-emitted from the same flag). F-8.10.10.
		MaxOrphanTasksPerTenant: *delegationMaxOrphanTasksPerTenant,
		UploadTokenIssuer:       uploadIssuer,
		UploadTokenVerifier:     uploadVerifier,
		// F-7.4.7: §7.1 line 58 TTL = maxCreatedStateTimeoutSeconds.
		UploadTokenTTL: time.Duration(*maxCreatedStateTimeoutSeconds) * time.Second,
		Blobs:          blobs,
		Executor:       exec,
		Transcripts:    transcripts,
		// spec: §8.8 lines 888-896 — the §8.10 archive materialization
		// lists a settled child's catalogued artifacts to populate
		// TaskResult.output.artifactRefs. Nil in the in-memory posture.
		// F-8.8.2.
		Artifacts: artifactCatalog,
		Events:    eventBus,
		// spec: §10.1 item 2 — gate session.create with 503 + Retry-After
		// while both coordination stores are unreachable. Nil monitor
		// (single-store / in-memory posture) leaves the gate open. F-10.1.3.
		DualStore:                  dsMonitor,
		Messaging:                  messagingCoord,
		Interactions:               interactions,
		ToolApprovalWaits:          toolApprovalWaits,
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
		StickyCache:    sessionStickyCache,
		Usage:          usage,
		Users:          users,
		Billing:        billing,
		Tenants:        tenants,
		StorageQuota:   storageCounter,
		PodBinder:      podBinder,
		PodRegistry:    podRegistry,
		AgentNamespace: *agentNamespace,
		// spec: §11.1 line 7 — per-runtime and per-pool admission rate
		// limits, enforced at session creation where the runtime/pool are
		// known. Shares the same per-minute counter the §11.1 HTTP
		// middleware uses for global/per-user/per-tenant. F-11.1.2.
		AdmissionRateLimitCounter: rateLimiter,
		PerRuntimePerMinute:       *rlPerRuntimePerMin,
		PerPoolPerMinute:          *rlPerPoolPerMin,
		RateLimitMetrics:          gwMetrics,
		// spec: §11.1 line 8 — global, per-user, and per-runtime
		// concurrent-session admission caps (live non-terminal session
		// counts). Zero leaves a scope unlimited. F-11.1.3.
		MaxConcurrentSessionsGlobal:     *maxConcSessGlobal,
		MaxConcurrentSessionsPerUser:    *maxConcSessPerUser,
		MaxConcurrentSessionsPerRuntime: *maxConcSessPerRuntime,
		// §10.7 line 938 — the eval-submission rate limit shares the same
		// §11.1 per-minute counter (Redis-backed across replicas) keyed by
		// session_id and tenant_id. F-10.7.4 / F-11.2.19.
		EvalRateLimitCounter:    rateLimiter,
		EvalPerSessionPerMinute: *evalRLPerSessionPerMin,
		EvalPerTenantPerMinute:  *evalRLPerTenantPerMin,
		DefaultIsolationProfile: isolation.Profile(*defaultIsolationProfile),
		DevMode:                 *devMode,
		// spec: §10.2 lines 256–264. F-10.2.4. Multi-tenant deployments
		// fail closed on a no-role principal at the session RBAC gate.
		MultiTenant: *multiTenant,
		Sealer:      sessionSealer,
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
		SessionLogHook:        &sessionlogstore.CloseHook{Store: sessionLogs},
		TreeArchive:           treeArchive,
		TreeBudgetReturner:    treeBudgetReserver,
		HighWatermarkReader:   hwmReader,
		HighWatermarkObserver: gwMetrics,
		Interceptors:          policyChain,
		PolicyAuditSink:       policyAuditSink,
		// §7.1 / §16.6 — session lifecycle audit events to the §11.7
		// hash-chained log, written under the session's tenant.
		LifecycleAuditSink: sessionLifecycleAuditor{appender: auditAppender},
		// §7.2 lines 124-127 / §11.7 / §16.7 — interaction-resolution
		// audit events (tool-use approve/deny, elicitation
		// respond/dismiss) to the §11.7 hash-chained log, written under
		// the session's tenant. F-7.2.8.
		InteractionAuditSink: interactionResolutionAuditor{appender: auditAppender},
		// §8.9 line 1003 / §11.7 / §16.1 — tree-walker defensive cycle
		// observer for REST /v1/sessions/{id}/tree. Emits the
		// delegation.tree_cycle_detected audit row + increments the
		// lenny_delegation_tree_cycle_detected_total counter when the
		// walker hits a repeated node. F-8.9.10.
		TreeCycleObserver: sessionserverTreeCycleObserver{emitter: treeCycleEmitter{metrics: gwMetrics}},
		// §7.2 line 317 — shared inputwait registry so REST inReplyTo
		// resolves the same pending `lenny/request_input` MCP registers.
		// F-7.2.14.
		InputWaits: inputWaits,
		// §7.1 line 92 — per-source-session advisory lock that serializes
		// concurrent /derive calls across replicas. Wired with Redis when
		// available (cross-replica serialization); a process-local
		// derivelock.Memory backs the minimal-gateway and single-replica
		// posture. F-7.1.12.
		DeriveLock: defaultDeriveLock(concernRedis.For(storerouter.RedisConcernCoordination)),
		// §7.1 line 77 — default artifact retention window.
		DefaultRetention: time.Duration(*sessionArtifactRetentionSeconds) * time.Second,
		// §7.1 line 112 — seal-and-export retry window + outcome histogram.
		WorkspaceSealMaxDuration:     time.Duration(*workspaceSealMaxDurationSeconds) * time.Second,
		ObserveWorkspaceSealDuration: gwMetrics.ObserveWorkspaceSealDuration,
		// §10.7 lines 1120-1132, §16.1 lines 161-164 — variant-labelled
		// rollback-trigger metric family emitted at terminal session
		// transition and at each built-in eval submission.
		RecordSessionTerminal: gwMetrics.RecordSessionTerminal,
		ObserveEvalScore:      gwMetrics.ObserveEvalScore,
		// §10.7 lines 835-844 (SCL-023) — the per-tenant targeting
		// circuit-breaker open/closed gauge.
		SetExperimentTargetingCircuitOpen: gwMetrics.SetExperimentTargetingCircuitOpen,
		Clock:                             clockinject.Now,
		UploadSubsystem:                   uploadSubsystem,
		UploadMetrics:                     uploadMetrics,
		// spec: §11.1 lines 10-11 — concurrent-upload + per-session
		// upload-size admission caps. F-11.1.5, F-11.1.6.
		MaxConcurrentUploadsPerSession: *uploadMaxConcurrentPerSession,
		MaxConcurrentUploadsGlobal:     *uploadMaxConcurrentGlobal,
		MaxUploadBytesPerSession:       *uploadMaxBytesPerSession,
		// §4.9 line 1220 — the pre-claim availability check race metric.
		PreclaimMismatch: gwMetrics.IncCredentialPreclaimMismatch,
		// §5.2 — whole-pod replacement counter, incremented when the
		// concurrent-workspace slot retry policy drains an unhealthy pod
		// (ceil(maxConcurrent/2) slots failed or leaked in the window).
		SlotReplacement: gwMetrics.IncSlotPodReplacement,
		// §6.3 lines 348, 372 — startup-latency histograms observed on
		// each successful pod-warm start.
		ObserveStartupDuration: gwMetrics.ObserveSessionStartupDuration,
		ObserveStartupPhase:    gwMetrics.ObserveSessionStartupPhase,
		// §6.3 line 356, §16.1 line 15 — TTFT histogram observed on
		// the first agent-streamed response event per session.
		ObserveTimeToFirstToken: gwMetrics.ObserveSessionTimeToFirstToken,
		// §7.3 lines 377-393 — clamp the client-supplied retry policy
		// against the deployer caps so the per-session value can never
		// exceed the watchdog's platform-wide bounds. F-7.3.1 /
		// F-7.3.24.
		RetryPolicyCaps: session.RetryPolicyCaps{
			MaxRetries:             *retryMaxRetries,
			MaxSessionAgeSeconds:   watchdog.DefaultMaxSessionAgeSeconds,
			MaxResumeWindowSeconds: *maxResumePendingSeconds,
		},
		// §14 line 105 — deployer extension to the platform env-var
		// blocklist; the platform default is always merged in first.
		// F-14.1.12.
		EnvVarBlocklist: splitCSV(*envVarBlocklistCSV),
		// §7.3 / §16.1 — retry + resume metric emitters. The
		// watchdog/coordinator path bumps retryCount on each pod
		// recovery (the v1 retry path); the explicit /resume endpoint
		// counts every attempt with its outcome. F-7.3.10.
		IncSessionResumeAttempt: gwMetrics.IncSessionResumeAttempt,
		IncSessionRetry:         gwMetrics.IncSessionRetry,
		// spec: §16.1 line 124, §7.3 line 387 — F-7.5.9. Increment the
		// lenny_warmpool_warmup_failure_total{error_type=setup_command_failed}
		// counter when a §7.5 setup command fails on the warm-pool side
		// of a bind.
		IncWarmpoolWarmupFailure: gwMetrics.IncWarmpoolWarmupFailure,
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
	var connectorStateStore *connectoroauth.MemoryStateStore
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
		// spec: §9.3 line 157 — pending state binds to (session, connector)
		// with TTL=10min. Production binds it to Redis so the flow survives
		// a gateway restart and a callback resolves on any replica
		// (F-9.3.5); the in-memory store is the single-process fallback and
		// alone needs the periodic Sweep scheduled below in the watchdog
		// group (Redis relies on native key expiry). F-9.3.16.
		var connectorStateBacking connectoroauth.StateStore
		if redisClient != nil {
			connectorStateBacking = connectoroauth.NewRedisStateStore(concernRedis.For(storerouter.RedisConcernCachePubSub))
			log.Printf("lenny-gateway: §9.3 connector OAuth state backed by Redis (TTL=10m, cross-replica)")
		} else {
			connectorStateStore = connectoroauth.NewMemoryStateStore()
			connectorStateBacking = connectorStateStore
		}
		connectorOAuth = &admin.ConnectorOAuth{
			StateSigner: stateSigner,
			StateStore:  connectorStateBacking,
			Credentials: connectorCreds,
			CallbackURL: *connectorOAuthCallbackURL,
		}
		// spec: §9.3 line 129 — resolve a confidential connector's client
		// secret from its auth.clientSecretRef Kubernetes Secret at
		// token-exchange time. Wired whenever the gateway holds a cluster
		// client (the production --agent-namespace path); without it a
		// confidential-client callback returns a clear "no client-secret
		// resolver is wired" error instead of failing on exchange. F-9.3.4.
		if clusterClient != nil {
			connectorOAuth.ClientSecrets = connectorsecret.NewKubeResolver(clusterClient, *connectorOAuthClientSecretKey)
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
	// spec: §8.3 — the DelegationPolicy registry feeds both the
	// admin CRUD surface (below) and the delegation admission gate so
	// the §8.2 LayerPolicy AllowSelfRecursion input and the §8.2.bis
	// policy ceiling can both consult the same store. Construct it
	// here so delegationSvc can read it; the admin router below shares
	// the same handle.
	var delegationPolicies delegationpolicystore.Store = delegationpolicystore.NewMemory()
	if pgPool != nil {
		delegationPolicies = delegationpolicypg.New(pgPool)
	}
	// §8.7 / §8.2 steps 3, 4: the file-export materializer pulls declared
	// fileExport sets from the running parent pod (over the pod-session
	// registry's adapter client), validates + persists them to the §4.5
	// blob store, and stamps the §14 child WorkspacePlan. It is wired only
	// when the pod registry exists; a delegation that declares fileExport
	// without it fails closed with EXPORT_NOT_CONFIGURED. The §8.3
	// scanExportedFiles interceptor resolver is not yet wired, so a policy
	// that mandates the export scan fails closed with
	// EXPORT_FILE_SCAN_UNAVAILABLE until the interceptor registry lands.
	// F-8.7.1.
	var exportMaterializer delegation.ExportMaterializer
	if podRegistry != nil {
		exportMaterializer = export.NewMaterializer(
			exportwire.NewPodExporter(podRegistry),
			exportwire.NewBlobSink(blobs, 0),
			mcpDelegationAuditor{sink: auditSink},
		)
	}
	// §13.3 revocation cache: the auth middleware rejects a token whose
	// jti is in this set, and the §8.2 line 61 child-token exchange reads
	// the parent (actor) token's jti against it inside the minting step.
	// Constructed here so both the delegation child-token minter and the
	// revocation propagator (below) share the one cache instance. F-8.1.2.
	revCache := revocation.NewCache()
	// §8.2 line 59 / §13.3: the in-process child-token minter the
	// delegation service runs after admission. It narrows scope, builds
	// the act chain, fixes delegation_depth at parent + 1, caps exp, and
	// fails closed with DELEGATION_PARENT_REVOKED when the parent jti is
	// revoked. F-8.1.2 / F-8.2.7.
	childTokenMinter := childtoken.NewMinter(childtoken.Options{
		Revocations: revCache,
		Clock:       clockinject.Now,
	})
	delegationSvc := delegation.NewService(sessions, delegation.Options{
		Experiments:        experiments,
		Runtimes:           runtimes,
		Policies:           delegationPolicies,
		Clock:              clockinject.Now,
		ExportMaterializer: exportMaterializer,
		TreeBudgetReserver: treeBudgetReserver,
		ChildTokenMinter:   childTokenMinter,
		// §11.1 line 9 — per-user active-delegated-children admission cap.
		// Zero leaves the scope unlimited. F-11.1.4.
		MaxActiveChildrenPerUser: *delegationMaxActiveChildrenPerUser,
		// §8.2 LayerPlatform — Helm value gateway.allowSelfRecursion.
		PlatformAllowSelfRecursion: *gatewayAllowSelfRecursion,
		// §8.2.bis line 89 — Helm value gateway.delegation.defaultMaxDepth.
		DefaultMaxDepth: *delegationDefaultMaxDepth,
		// §8.3 line 181 — Helm value gateway.interceptorWeakeningCooldownSeconds.
		// F-8.7.12 / F-13.5.7.
		InterceptorWeakeningCooldown: time.Duration(*interceptorWeakeningCooldownSeconds) * time.Second,
		// §8.2 / §16.1: the delegation service emits
		// `lenny_delegation_depth` and
		// `lenny_delegation_would_have_blocked_total` through the
		// gateway metrics registry.
		Metrics: gwMetrics,
		// spec: §11.7 line 62 / §16.7 — wire the §11.7 audit sink so
		// the service emits `delegation.spawned`,
		// `delegation.self_recursion_allowed`, and `delegation.cycle_warning`.
		// F-8.5.8 / F-8.5.9.
		Auditor: mcpDelegationAuditor{sink: auditSink},
		// spec: §8.2 line 90 / §10.7 — `independent` propagation
		// routes the child afresh through the same ExperimentRouter
		// the top-level session-creation path uses. Wired as a
		// pointer to *sessionserver.Server, which implements
		// delegation.ExperimentRouter via ApplyExperimentRouting.
		ExperimentRouter: sessionSrv,
	})
	// §8.3 line 157 / §4.8 line 974: now that delegationSvc exists, fill
	// in the holder so the DelegationPolicyEvaluator measures TaskSpec.input
	// against the parent runtime's effective contentPolicy.maxInputSize
	// rather than the cluster default alone. F-13.5.1 / F-8.2.9.
	maxInputResolver.inner = delegationSvc
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:                      sessions,
		Executor:                   exec,
		DevMode:                    *devMode,
		Delegation:                 delegationSvc,
		Users:                      users,
		Runtimes:                   runtimes,
		Environments:               environments,
		Tenants:                    tenants,
		Pools:                      pools,
		Audit:                      mcpDelegationAuditor{sink: auditSink},
		DefaultNoEnvironmentPolicy: resolvedNoEnvPolicy,
		Interceptors:               policyChain,
		PolicyAudit:                policyAuditSink,
		Events:                     eventBus,
		InputWaits:                 inputWaits,
		TreeArchive:                treeArchive,
		Interactions:               interactions,
		Memory:                     memories,
		ElicitationMetrics:         gwMetrics,
		// spec: §16.1 lines 60–63; §16.5 line 458. F-9.2.14 — the
		// §9.2 dispatcher emits admit/terminal lifecycle samples that
		// drive the ElicitationBacklogHigh alert and the operator
		// roundtrip / timeout / suppressed dashboards.
		ElicitationLifecycleMetrics: gwMetrics,
		// spec: §16.1 line 64; §9.2 line 60 — the §9.2 chain walker
		// reports a content-tamper detection through this recorder, which
		// increments lenny_elicitation_content_tamper_detected_total
		// {origin_pod, tampering_pod, enforcement_mode}. Without it the
		// dispatcher's tamper branch is a no-op and the §16.5
		// ElicitationContentTamperDetected alert can never fire. F-9.2.4.
		ElicitationTamperMetrics: gwMetrics,
		// spec: §9.2 lines 58–64 — resolve the per-tenant effective
		// content-integrity enforcement mode (max of the platform floor
		// and the tenant stored mode) on the elicitation dispatch path so
		// an operator's enforce / detect-only / off setting takes effect:
		// off skips the integrity check, detect-only records a divergence
		// but forwards as received, enforce drops the divergent forward.
		// A lookup error fails safe to the enforce default. F-9.2.2.
		ElicitationModeResolver: func(ctx context.Context, tenantID string) elicitation.EnforcementMode {
			stored := ""
			if t, err := tenants.Get(ctx, tenantID); err == nil {
				stored = t.ElicitationContentIntegrity
			}
			return elicitation.ResolveEffectiveWithDefaults(*elicitationFloor, stored)
		},
		// spec: §9.2 / §16.1 / §15.2 line 1335 — Deps.TenantID is the
		// fallback for transports without an authenticated principal
		// (tests, the dev-headers path). Every production handler
		// re-resolves the per-request tenant from the auth middleware's
		// principal via callerTenantID, so a multi-tenant deployment
		// scopes session creation, the elicitation budget, the §9.2
		// chain lookup, the §16.7 audit emission, and the §16.1 tamper
		// metric to the right tenant. F-9.2.13 / F-15.2.15.
		TenantID: "default",
		// spec: §7.2 lines 236-272; §8.3 lines 269-272 — deployment
		// messagingScope (default + ceiling) and the per-session
		// send_message rate limits. The same cross-replica rate counter
		// the §11.1 admission limits use backs the per-minute messaging
		// windows. F-7.2.6.
		MessagingDefaultScope: session.MessagingScope(*messagingDefaultScope),
		MessagingMaxScope:     session.MessagingScope(*messagingMaxScope),
		MessagingRateLimit: mcptools.MessagingRateLimit{
			MaxPerMinute:        *messagingMaxPerMinute,
			MaxPerSession:       *messagingMaxPerSession,
			MaxInboundPerMinute: *messagingMaxInboundPerMinute,
		},
		MessagingRateCounter: rateLimiter,
		Clock:                clockinject.Now,
		// §8.9 line 1003 / §11.7 / §16.1 — same tree-walker cycle
		// observer the REST /tree handler uses, so the audit row +
		// counter fire regardless of which surface walked the tree.
		// F-8.9.10.
		TreeCycleObserver: mcpToolsTreeCycleObserver{emitter: treeCycleEmitter{metrics: gwMetrics}},
	})

	// §13.3 revocation cache: the auth middleware rejects a token
	// whose jti is in this set. It is rehydrated from the Postgres
	// issued-token index below. The propagator wraps the cache with
	// Redis pub/sub fan-out so a revocation on any replica reaches every
	// replica within pub/sub latency; with no Redis the propagator is a
	// local-only pass-through. revCache stays the read primitive the
	// auth middleware and the rehydration loop use directly. revCache is
	// constructed above (shared with the §8.2 child-token minter).
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
	// §4.9 line 1647: the credential deny-list revocation propagates via
	// Redis pub/sub with Postgres LISTEN/NOTIFY as fallback. The Postgres
	// half is wired only when Postgres is configured (the option is
	// omitted otherwise so a no-Postgres dev gateway keeps a true-nil
	// fallback); it carries a revocation when Redis is down or disabled
	// and feeds the LISTEN subscribe loop so a peer's revocation still
	// converges. F-13.3.8.
	credDenyPropOpts := []credrenewalprop.Option{
		credrenewalprop.WithErrorHandler(func(err error) {
			log.Printf("lenny-gateway: credential-lease revocation pub/sub publish failed: %v", err)
		}),
	}
	if pgPool != nil {
		credDenyPropOpts = append(credDenyPropOpts, credrenewalprop.WithFallback(pgnotify.New(pgPool)))
	}
	var credRenewalWorker *credrenewal.Worker
	credRenewalProp := credrenewalprop.New(credDeny, nil, securityBus, credDenyPropOpts...)
	if credRenewal != nil {
		// spec: §11.3 line 215 — credentials.expiryWarningLeadSeconds.
		// 0 disables warnings; -1 keeps the package default; any other
		// non-negative value is the explicit operator override.
		expiryWarningLead := time.Duration(*credentialsExpiryWarningLeadSeconds) * time.Second
		credRenewalWorker = credrenewal.New(credRenewal, credrenewal.Options{
			// §4.9: a proactive renewal that rotates a lease onto a fresh
			// credential pushes it to the lease's pod via RotateCredentials.
			OnRenewed: credRenewal.onRenewed,
			// §4.9: a lease whose renewal cannot proceed falls through to
			// fault rotation. The worker drops it; onExhausted clears its
			// pool binding.
			OnExhausted: credRenewal.onExhausted,
			Clock:       clockinject.Now,
			// spec: §11.3 line 215 — operator-tunable expiry-warning lead.
			// F-11.3.20.
			ExpiryWarningLead: expiryWarningLead,
			OnExpiryWarning:   logCredentialExpiryWarning,
		})
		log.Printf("lenny-gateway: §11.3 line 215 credentials.expiryWarningLeadSeconds=%ds", int(expiryWarningLead/time.Second))
		// Every §4.9 credential lease the assignment service mints — at
		// session start and at fault rotation — is tracked by the renewal
		// worker so its renewBefore deadline drives a proactive renewal.
		credAssign.OnAssigned(func(a credassign.LeaseAssignment) {
			credRenewal.track(credRenewalWorker, a.PoolName, string(a.Lease.Provider), a.Lease)
		})
		// Rebuild the propagator over the live worker so a peer replica's
		// credential-lease revocation also drops this replica's tracked
		// leases for the credential, not just its deny-list entry.
		credRenewalProp = credrenewalprop.New(credDeny, credRenewalWorker, securityBus, credDenyPropOpts...)
	}

	// ----- Admin API -----
	// delegationPolicies was constructed above so the delegation
	// admission gate (§8.2 LayerPolicy) and the admin CRUD share one
	// store handle.
	// §8.6 GatewayControl lease-extension budget state. Created here, when
	// the GatewayControl listener is enabled via --grpc-addr, so the same
	// per-tree denial flags are shared between the ExtendLease handler and
	// the §15.1 line 868 admin extension-denial clear endpoint — the admin
	// handler must mutate the very state the handler reads. F-8.6.8.
	var leaseBudgets *leasecontrol.MemoryBudgetSource
	if *grpcAddr != "" {
		leaseBudgets = leasecontrol.NewMemoryBudgetSource()
	}

	// spec: §12.5 line 301 / line 307 — the §12.5 T4 KMS availability
	// probe Lifecycle, backed by the resolved §4 kms.Provider so the
	// zero-byte encrypt/decrypt round-trip uses the same provider
	// credentials a T4 artifact write would. One Lifecycle instance is
	// shared by the admin-time probe (WithKMSProbe below, F-12.5.3) and
	// the leader-elected continuous probe (the Prober wired with the GC
	// loops, F-12.5.4) so the `t4KmsLastProbeSuccessAt` admin field and
	// the `lenny_t4_kms_probe_last_success_timestamp` gauge read the
	// same recorded last-success time.
	kmsProbeLifecycle := tenantkms.NewProviderProbeLifecycle(kmsProvider, clockinject.Now)

	// §9.3 outbound MCP transport shared by the connector live test and
	// the §9.3 line 136 capability refresh. The §9.3 line 164
	// connector-access authorizer resolves the calling session's effective
	// delegation policy (runtime-level + §10.6 environment default) so a
	// session cannot invoke a connector its policy does not permit. The
	// client carries a bounded egress timeout because it dials untrusted
	// external endpoints.
	connectorMCPClient := connectorinvoke.New(&http.Client{Timeout: 15 * time.Second})
	connectorAuthorizer := connectorauthz.New(delegationSvc, sessions, environments)
	connectorInvoker := connectorinvoke.NewInvoker(connectors, connectorCreds, connectorMCPClient, nil, connectorAuthorizer).
		WithClock(clockinject.Now).
		// spec: §10.6 line 607 — enforce the calling session's environment
		// connectorSelector capability filter on each connector tools/call.
		// F-10.6.2.
		WithEnvironments(environments)

	adminRouter := admin.NewRouter(tenants, admin.Options{Clock: clockinject.Now, Audit: auditSink, Metrics: gwMetrics, DevMode: *devMode}).
		WithKMSProbe(kmsProbeLifecycle).
		WithRuntimes(runtimes).
		WithUsers(users).
		WithPools(pools).
		WithBreakers(breakers).
		WithConnectors(connectors).
		WithConnectorOAuth(connectorOAuth).
		// §15.1 connector live-connectivity test. The outbound MCP
		// client dials untrusted external endpoints, so it carries a
		// bounded egress timeout; the per-connector limiter enforces the
		// §15.1 line 1180 10/min cap.
		WithConnectorTest(
			connectorinvoke.NewTester(connectorMCPClient),
			connectorCreds,
			ratelimit.NewMemory(),
		).
		// §9.3 line 136 connector capability inference on the sanctioned
		// outbound path. Carries the same per-connector 10/min cap as the
		// live test since it also dials the external endpoint.
		WithConnectorRefresh(connectorInvoker, ratelimit.NewMemory()).
		WithDelegationPolicies(delegationPolicies).
		WithCredentialPools(credentialPools).
		WithCustomRoles(customRoles).
		WithTenantAccess(tenantAccess).
		WithSessions(sessions).
		WithInteractions(interactions).
		WithExperiments(experiments).
		WithStickyFlusher(adminStickyFlusher).
		WithEnvironments(environments).
		WithEvalResults(evals).
		WithRecommendations(recommendations.NewCapacityServiceWithConfig(
			recommendations.NewWindowStore(7*24*time.Hour),
			recommendations.Config{
				DisabledRules:             splitCSV(*recommendationsDisabledRules),
				WindowOverrides:           parseWindowOverrides(*recommendationsWindowOverrides),
				DisableOnPrometheusOutage: *recommendationsDisableOnOutage,
			},
		))
	adminRouter = adminRouter.
		WithEventBuffer(opsEventBuffer).
		WithEventEmitter(opsEmitter).
		WithOperationsInventory(operations.New())
	if artifactCatalog != nil {
		// §12.8 line 735 / 794(b): the durable artifact_store catalog backs
		// artifact-scoped legal holds (POST /v1/admin/legal-hold with
		// artifactId) and the artifact half of the GDPR-erasure legal-hold
		// preflight.
		adminRouter = adminRouter.WithArtifactLegalHold(artifactCatalog)
	}
	if leaseBudgets != nil {
		// §15.1 line 868: expose DELETE …/extension-denial backed by the
		// same leasecontrol budget source the GatewayControl handler reads.
		adminRouter = adminRouter.WithLeaseDenials(leaseBudgets)
	}
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
	adminRouter = adminRouter.WithMaxFinalizingTimeoutSeconds(*maxFinalizingTimeoutSeconds)
	// spec: §11.7 lines 445-451 — record whether audit.siem.endpoint is
	// configured so the admin compliance gate can reject regulated-profile
	// tenant create/update and environment creation with
	// COMPLIANCE_SIEM_REQUIRED when SIEM is absent. F-11.7.2.
	adminRouter = adminRouter.WithSIEMConfigured(*auditSIEMEndpoint != "")
	// §15.1 lines 891-892 / §24.13 lines 150-151: wire the
	// schema-migration management endpoints when a Postgres DSN is set
	// (the migrations the runner applies live in the same database).
	if *postgresDSN != "" {
		if mig, err := schemamigrate.New(*postgresDSN); err != nil {
			log.Printf("lenny-gateway: schema-migration manager disabled: %v", err)
		} else {
			adminRouter = adminRouter.WithMigrationManager(mig)
		}
	}
	adminRouter = wireAudit(adminRouter)
	if auditPruner != nil {
		// §16.4 line 378 force-drop override surface. F-11.7.17.
		adminRouter = adminRouter.WithAuditPruner(auditPruner)
	}
	// §12.5 line 317: the erasure-completion → tenant-scoped GC sweep
	// trigger. The erasure runner is built here but the retention-GC
	// collector is constructed later, so the runner closes over this
	// indirection and the GC block below assigns the real sweep once the
	// collector exists. Nil during the startup window before the GC block
	// runs (no erasure job can complete that early).
	var immediateGCSweep func(ctx context.Context, tenantID string)
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
		erasureCfg := erasure.Config{
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
				// §12.8: MemoryStore (step 8) precedes the session-keyed
				// interaction rows; both precede SessionStore (step 17).
				{Name: "memory", DeleteByUser: func(ctx context.Context, tenantID, userID string) (int, error) {
					// §9.4 MemoryStore.DeleteByUser returns only an error;
					// the orchestrator's adapter reports the count it
					// cannot supply as 0.
					return 0, memories.DeleteByUser(ctx, tenantID, userID)
				}},
				{Name: "interactions", DeleteByUser: interactions.DeleteByUser},
				{Name: "sessions", DeleteByUser: sessions.DeleteByUser},
			},
		}
		// spec: §12.8 lines 792-836 — fail closed if the wired erasure
		// stores violate the dependency order (a foreign-key child erased
		// after its parent would leave orphan rows or violate a constraint).
		if err := erasure.ValidateOrder(erasureCfg); err != nil {
			log.Fatalf("FATAL: §12.8 erasure store ordering invalid: %v", err)
		}
		erasureOrch := erasure.New(erasureCfg)
		erasureJobs := erasurejob.NewMemory()
		erasureRunner := erasurejob.NewRunner(erasureJobs, erasureOrch, nil).
			WithFailureObserver(gwMetrics.IncErasureJobFailed).
			// spec: §12.8 line 768 — emit the in-progress gauge and
			// per-job duration histogram for erasure-throughput / SLA
			// monitoring.
			WithLifecycleMetrics(gwMetrics).
			// spec: §12.8 line 768 / §12.9 — record the tier-specific SLA
			// deadline on each job (T4 Restricted 1h, otherwise 72h).
			WithDeadlineResolver(func(ctx context.Context, tenantID string) time.Duration {
				if t, err := tenants.Get(ctx, tenantID); err == nil && t.WorkspaceTier == tenantkms.WorkspaceTierT4 {
					return time.Hour
				}
				return 72 * time.Hour
			}).
			// §12.5 line 317: on completion, trigger an immediate
			// tenant-scoped GC sweep for a `gcPriority: high` tenant.
			WithCompletionHook(func(ctx context.Context, tenantID, _ string) {
				if immediateGCSweep != nil {
					immediateGCSweep(ctx, tenantID)
				}
			})
		// spec: §12.8 lines 743-758 (layer 3) — re-run the MemoryStore
		// erasure preflight at the start of every job so a backend that
		// regressed after the startup check (e.g. a rolling upgrade of an
		// external vector DB) aborts the job as memory_store_preflight_failed
		// before any deletion, leaving processing_restricted set and
		// incrementing lenny_erasure_job_failed_total{failure_phase=
		// memory_store_preflight}. F-9.4.3 / F-12.2.10.
		if memories != nil {
			memstore := memories
			erasureRunner = erasureRunner.WithMemoryPreflight(func(ctx context.Context) error {
				return memorystore.ValidateMemoryStoreErasure(ctx, memstore)
			})
		}
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
		// spec: §12.8 line 768 — publish the erasure-SLA gauges
		// (lenny_erasure_job_age_seconds, lenny_erasure_job_deadline_seconds)
		// on a periodic tick so the §16.5 ErasureJobOverdue alert can detect
		// a stalled job before its deadline breaches. The Runner cannot
		// advance a job's age while blocked inside a slow DeleteByUser, so a
		// separate sampler reads the registry. Default deadline is the §12.9
		// T3 72h bound the alert's scalar() compares against.
		erasureSampler := erasurejob.NewSampler(erasureJobs, gwMetrics, 72*time.Hour, clockinject.Now)
		go erasureSampler.Run(context.Background(), 30*time.Second)
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
	// correction endpoints write through the failover billing pipeline.
	// Pending dual-control requests are held in the durable Postgres
	// registry when Postgres is wired, so a gateway restart does not lose
	// a pending request or its four-eyes audit trail (the spec rules out
	// restart-fragility for financial controls); the in-memory registry
	// backs the Postgres-less minimal gateway. F-11.2.11.
	var corrections correctionstore.Store = correctionstore.NewMemory()
	if pgPool != nil {
		corrections = correctionpg.New(pgPool)
	}
	adminRouter = adminRouter.WithBillingCorrections(
		billing, corrections, *billingDualControlThreshold,
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
	//
	// readinessDeps lists the hard backend dependencies whose
	// unreachability the §10.4 /readyz probe gates on. Only Postgres
	// (the externalized session-truth store, §10.4 mechanism 1) is
	// included: Redis loss has the §12.4 advisory-lock lease fallback,
	// and the SIEM-delivery checker is §11.7-non-gating, so neither
	// should pull a replica out of the Service on its own. F-10.4.6.
	var readinessDeps []string
	if pgPool != nil {
		healthAgg.Register(backends.Postgres(pgPool, "postgres"))
		readinessDeps = append(readinessDeps, "postgres")
	}
	if redisClient != nil {
		healthAgg.Register(backends.Redis(redisClient, "redis"))
	}
	if breakerCache != nil {
		healthAgg.Register(backends.CircuitBreakerCache(breakerCache, "circuit-breaker-cache"))
	}
	// spec: §11.7 item 4 line 372 — the SIEM delivery health check feeds
	// the §25.3 health verdict. Registered only when a SIEM endpoint is
	// configured; a degraded verdict reports the external audit copy is
	// lagging while the durable Postgres chain stays intact. The gateway
	// deliberately does not gate /healthz or /readyz on this component so a
	// shared-SIEM outage cannot pull every replica out of the Service.
	// F-11.7.16.
	if siemHealthChecker != nil {
		healthAgg.Register(siemHealthChecker)
	}
	// §25.3: emit a health_status_changed operational event when the
	// aggregate health verdict transitions.
	healthAgg.OnTransition(func(prev, curr health.Status) {
		data, _ := json.Marshal(map[string]any{
			"oldStatus": string(prev), "newStatus": string(curr),
		})
		_ = opsEmitter.Emit(context.Background(), events.OperationalEvent{
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
	// spec: §15.1 line 589 — served `info.version` must reflect the
	// gateway's release version, not the embedded default.
	openapiHandler := openapi.HandlerWithVersion(buildVersion)
	mux.Handle("/openapi.yaml", openapiHandler)
	// spec: §15.1 line 589 — `/openapi.json` is the canonical
	// gateway-side JSON mount; `/v1/openapi.json` is retained for
	// the §18 build-sequence reference and §25.4 lenny-ops parity.
	// F-15.1.17.
	mux.Handle("/openapi.json", openapiHandler)
	mux.Handle("/v1/openapi.json", openapiHandler)
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
		// spec: §10.2 line 195 / F-10.2.14. The v1 signer is HMAC; the
		// published JWKS entries carry `kty: oct` with no `k` field, so
		// the document advertises kid/alg only. Log a notice when the
		// endpoint is mounted on top of an HMAC-only key set so
		// operators understand that JWKS verification of the actual
		// signature is not possible against the published document
		// (the secret must be obtained out of band).
		log.Printf("lenny-gateway: §10.3 JWKS published at /.well-known/jwks.json (current kid %s)",
			rotatingVerifier.CurrentKeyID())
		if !jwksAdvertisesAsymmetric(jwksHandler.Document()) {
			log.Printf("lenny-gateway: §10.2 line 195 / F-10.2.14: published JWKS contains only `kty: oct` entries (HMAC). The document advertises kid/alg only; verifiers cannot validate signatures against it.")
		}
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
	// spec: §10.6 line 557 — the explicit-environment MCP surface. A
	// client opts into a named environment scope by speaking MCP to
	// /mcp/environments/{name}; the dispatch is identical to POST /mcp
	// with the environment attached to the request context so
	// session-creation and discovery default to that scope. F-10.6.11.
	mux.Handle("POST /mcp/environments/{name}", mcpSrv.EnvironmentHandler())
	// §4.1 dedicated MCP endpoints for type:mcp runtimes. The
	// dispatcher is nil in v1: every request that passes runtime
	// type validation surfaces RUNTIME_UNAVAILABLE per §15.2.1
	// while preserving the spec-required 404 / 400 error patterns
	// for unknown and non-mcp runtimes.
	// spec: §10.6 line 607 — the per-runtime MCP dispatcher gates a
	// tools/call scoped to an environment (`?environment=<name>`) by that
	// environment's mcpRuntimeFilters capability filter. F-10.6.2.
	mux.Handle("POST /mcp/runtimes/{name}", mcpruntimes.New(runtimes, nil).WithEnvironments(environments))
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
	//
	// §27.6 line 204 — the playground handler's authoritative per-request
	// revocation check is hoisted here so the auth middleware (built
	// below) can consult it for every origin=playground bearer. It stays
	// nil when the playground is disabled, leaving the auth hot path
	// unchanged. F-27.6.3 / F-27.3.1.
	var playgroundRevocations authmw.PlaygroundRevocationChecker
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
			SessionLabels:      parseKeyValueCSV(*playgroundSessionLabels),
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
		// §27.8 playground metrics register against the same private
		// registry the gateway's /metrics scrape target serves. Created
		// before the session store so the Redis-backed store can record
		// §27.8 propagation samples from its subscribe loop. F-27.6.6.
		pgMetrics, err := playground.NewMetrics(gwMetrics.Registerer())
		if err != nil {
			log.Fatalf("lenny-gateway: §27.8 playground metrics: %v", err)
		}
		// §27.3.1 session record + revocation backing store: Redis when
		// --redis-url is set so a logout on one replica revokes the
		// bearer fleet-wide, in-process otherwise (single-replica).
		var pgSessions playground.SessionStore
		if redisClient != nil {
			redisSessions := playground.NewRedisSessionStore(concernRedis.For(storerouter.RedisConcernCachePubSub)).WithMetrics(pgMetrics)
			pgSessions = redisSessions
			// §27.3.1 / §27.6 line 204 pub/sub: a single PSUBSCRIBE on
			// t:*:pg:revocations subscribes this replica to every tenant's
			// revocation channel — including tenants provisioned after
			// gateway start — so the auth hot path can short-circuit the
			// Redis GET on a cache hit and the §27.8 propagation histogram
			// captures cross-replica visibility for all tenants. F-27.6.6,
			// F-27.6.7.
			go redisSessions.SubscribeAllRevocations(context.Background())
		} else {
			pgSessions = playground.NewMemorySessionStore()
			// spec: §27.6 line 204 — Logout endpoints MUST NOT return
			// 200 to the browser until the revocation writes have
			// committed to Redis. The in-memory store has no cross-
			// replica fan-out and loses every revocation marker on
			// restart, so a bearer minted before restart and not yet
			// expired becomes honourable again. Surface the durability
			// gap at startup so a single-replica dev/embedded operator
			// knows the §27.6 commit-before-200 guarantee is unbacked.
			// F-27.6.9.
			log.Printf("lenny-gateway: §27.6 playground SessionStore is in-memory (no --redis-url): bearer revocations are not durable across gateway restarts and do not propagate across replicas; production deployments MUST set --redis-url")
		}
		pg := playground.New(pgCfg, playground.Options{
			// spec: §10.2 line 225 — Sign goes through the breaker so
			// KMS outages convert to retryable KMS_SIGNING_UNAVAILABLE;
			// Verify uses the inner signer (verification is local memory
			// and never reaches KMS). F-10.2.6.
			Signer:   jwtSigner,
			Verifier: kmsBackedSigner,
			Tenants:  playgroundTenantRegistry{store: tenants},
			Sessions: pgSessions,
			Metrics:  pgMetrics,
		}).WithAuditEmitter(playgroundAuditEmitter{sink: auditSink})
		// spec: §27.6 lines 200-201 — wire the playground idle/duration caps
		// into the session-creation path so an origin=playground session is
		// bounded by min(runtime, playground) on both axes, and record the
		// §27.8 lenny_playground_sessions_created_total counter once the claim
		// is read. pg.Config() is the §27.2-normalized config (defaults
		// applied), so its EffectiveIdleSeconds / EffectiveSessionMinutes carry
		// the resolved caps. F-27.3.3 / F-27.6.1 / F-27.6.2 / F-27.6.11.
		sessionSrv.SetPlaygroundCaps(pg.Config(), pgMetrics.SessionCreated)
		// §27.6 line 204 — expose the per-request revocation check to the
		// auth middleware so an origin=playground bearer is rejected on
		// every replica once its session is revoked. F-27.6.3 / F-27.3.1.
		playgroundRevocations = pg
		// §27.5.4 — wire the same revocation check into the MCP WebSocket
		// transport so an origin=playground bearer revoked mid-stream
		// (logout / idle / admin / user.invalidated) closes the in-flight
		// connection with WebSocket code 4401. The principal is read from
		// the auth-middleware context the upgrade request already carries;
		// non-playground bearers (no origin claim) are not watched.
		// F-27.5.4.
		mcpSrv.SetWebSocketAuth(func(r *http.Request) (mcp.WSPrincipal, bool) {
			p, ok := authmw.FromContext(r.Context())
			if !ok {
				return mcp.WSPrincipal{}, false
			}
			return mcp.WSPrincipal{Tenant: p.TenantID, JTI: p.JTI, Origin: p.Origin}, true
		}, pg, 0)
		// §11.4 / §27.6 line 204 — drive the §11.4 user-invalidation
		// fan-out into the playground revocation primitive so an OIDC
		// principal invalidation revokes the user's playground sessions.
		// adminRouter is a pointer the mounted handlers read at request
		// time, so wiring it here (after the admin mux is built) takes
		// effect on the next invalidate call. F-27.6.4 / F-27.3.2.
		adminRouter = adminRouter.WithPlaygroundRevocation(pg)
		mux.Handle("/playground", pg.PlaygroundRoutes())
		mux.Handle("/playground/", pg.PlaygroundRoutes())
		mux.Handle("/v1/playground/token", pg.TokenRoutes())
		log.Printf("lenny-gateway: §27 web playground served at /playground (authMode=%s)", pgCfg.AuthMode)
	}

	// ----- §16.1 Prometheus metrics -----
	mux.Handle("GET /metrics", gwMetrics.Handler())

	// ----- Healthz (unauthenticated) -----
	// spec: §13.3 line 595 — when this replica's NTP drift exceeds the
	// 5s ceiling, /healthz reports degraded (503). Kubernetes responds
	// by removing the pod from the Service endpoints, so traffic stops
	// reaching a replica whose clock cannot be trusted for `exp`
	// validation. F-13.3.5.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if driftMonitor.Degraded() {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("clock_drift_exceeded\n"))
			return
		}
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
			Sessions:    sessions,
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

	// ----- Readiness (unauthenticated) -----
	// spec: §10.1 — the preStop staged drain's Stage 1 is a readiness
	// flip: once the preStop hook fires, /readyz reports 503 so the
	// Endpoints controller removes this pod from the Service and the
	// load balancer stops routing new requests *before* the
	// eviction-checkpoint drain begins. Liveness stays on /healthz so a
	// draining pod is not also killed by the kubelet mid-drain. The
	// readiness probe also fails on NTP drift so a clock-untrustworthy
	// replica is removed from the endpoints (the §13.3.5 behaviour that
	// previously rode on /healthz serving double duty). F-10.1.6.
	//
	// spec: §10.4 line 386 — readiness additionally reflects the hard
	// backend dependency (Postgres session truth) so a replica whose
	// store connection is broken is removed from traffic rather than
	// serving until the process crashes. The dual-store-both-down case
	// is exempted so the replica stays ready to answer the §10.1 503
	// PLATFORM_DEGRADED. F-10.4.6.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		dualStoreDown := dsMonitor != nil && dsMonitor.Unavailable()
		probe := func(ctx context.Context) health.Status {
			return healthAgg.HardDependencyStatus(ctx, readinessDeps...)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		res := readinessVerdict(ctx, prestopHook.Draining(), driftMonitor.Degraded(), dualStoreDown, probe)
		w.WriteHeader(res.Code)
		if res.Body != "" {
			_, _ = w.Write([]byte(res.Body))
		}
	})

	// ----- Middleware stack -----
	var handler http.Handler = mux

	// The §11.2 per-tenant concurrent-session quota is enforced inside
	// the session-creation handlers (sessionserver.requireSessionQuota)
	// against each tenant's configured MaxConcurrentSessions.

	// Idempotency next (after auth + circuit; needs the
	// authenticated tenant on the request to scope keys correctly).
	// The §11.5 key cache is durable under --postgres-dsn so an
	// idempotent retry replays across gateway replicas and restarts.
	// AllowedPaths restricts the cache to the §11.5-listed critical
	// operations so a stray Idempotency-Key on an unrelated route
	// cannot trap a non-mutating response for 24 hours. spec: §11.5
	// line 268; F-11.5.7.
	var idemStore idemmw.Store = idemmw.NewMemoryStore()
	if pgPool != nil {
		idemStore = idempgstore.New(pgPool)
	}
	// spec: §11.5 line 277 — when an MCP client supplies idempotencyKey
	// inside a tool's arguments (lenny/create_session,
	// lenny/delegate_task), the MCP server runs the call through the
	// same §11.5 Store with per-tool key namespacing so retries collapse
	// to one execution. The Tools allow-list keeps the field opt-in per
	// tool; the TenantFromRequest resolver mirrors the REST middleware's
	// auth-set header. spec: F-11.5.1, F-11.5.6.
	mcpSrv.SetIdempotency(mcp.IdempotencyConfig{
		Store: idemStore,
		TenantFromRequest: func(r *http.Request) string {
			return r.Header.Get("X-Lenny-Tenant-ID")
		},
		Tools: map[string]bool{
			"lenny/create_session": true,
			"lenny/delegate_task":  true,
		},
		Now: clockinject.Now,
	})
	handler = idemmw.Wrap(handler, idemStore, idemmw.Options{
		Metrics: gwMetrics,
		// spec: §11.5 line 277 — the middleware buffers the request body
		// to hash and replay it. The default 8 MiB cap covers the
		// critical-operation payloads and is operator-tunable via the
		// flag for deployments that carry larger bodies (e.g. resume
		// with a verbose TaskResult). F-11.5.8.
		MaxBodyBytes: *idempotencyMaxBodyBytes,
		// spec: §11.5 line 268 — the six "critical operations"
		// (CreateSession, FinalizeWorkspace, StartSession, SpawnChild,
		// Approve/DenyDelegation, Resume) bound the §11.5 cache. The
		// middleware also defaults to POST-only (Options.AllowedMethods),
		// so a misplaced Idempotency-Key on a GET or unrelated POST
		// passes through without being trapped for 24 hours. F-11.5.7.
		AllowedPaths: []string{
			"/v1/sessions",                     // CreateSession
			"/v1/sessions/start",               // create-and-start
			"/v1/environments/{name}/sessions", // environment-scoped CreateSession
			"/v1/sessions/{id}/finalize",       // FinalizeWorkspace
			"/v1/sessions/{id}/start",          // StartSession
			"/v1/sessions/{id}/resume",         // Resume
			"/v1/sessions/{id}/derive",         // fork (Resume-adjacent)
			"/v1/sessions/{id}/tool-use/...",   // Approve/DenyDelegation tree
			"/mcp",                             // SpawnChild via JSON-RPC POST
		},
	})

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
		// spec: §13.3 line 607 / §11.1 — per-tenant fair-share brake.
		// F-11.1.8.
		PerTenantPerMinute: *rlPerTenantPerMin,
		Metrics:            gwMetrics,
		// spec: §11.3 line 222 / §12.4 line 220 — operator-tunable cap on
		// the fail-open episode. F-11.3.22.
		FailOpenMax: time.Duration(*rlFailOpenMaxSeconds) * time.Second,
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
		MultiTenant: *multiTenant,
		// spec: §10.2 line 212 — operator-configurable OIDC tenant claim
		// name (`auth.tenantIdClaim` Helm value). F-10.2.9.
		TenantClaimName: *tenantIDClaim,
		AllowDevHeaders: true,
		AllowDevRoles:   *devMode,
		Verifier:        bearerVerifier,
		Revocations:     revProp,
		// §27.6 line 204 / §27.3.1 lines 95-97 — authoritative per-request
		// revocation check for origin=playground bearers; nil (and thus a
		// no-op) when the playground is disabled. F-27.6.3 / F-27.3.1.
		PlaygroundRevocations: playgroundRevocations,
		// §4.2 line 185: every tenant-claim rejection writes an
		// auth_failure audit row alongside the INFO log line.
		AuthFailureSink: authFailureAuditAdapter{sink: auditSink},
		// §4.8 line 1046: run the PreAuth chain (AuthEvaluator) after the
		// principal resolves and before the request reaches the handler.
		Interceptors: policyChain,
		// spec: §10.2 line 294 — platform-managed user→role mapping
		// overrides OIDC-derived roles. The userstore-backed adapter
		// returns the user's stored Roles when the row exists; missing
		// rows fall through to the JWT claim. F-10.2.3.
		PlatformRoles: userstorePlatformRoles{store: users},
	}
	// §13.3 line 601 — fail-closed token validation. Only when Postgres
	// backs the revocation rehydration (below) is the staleness gate
	// meaningful: a replica that cannot reach Postgres for longer than the
	// freshness window refuses to validate (503 token_validation_unavailable)
	// rather than honor a possibly-revoked token from its stale cache. The
	// in-memory dev path leaves it nil so a no-Postgres deployment does not
	// fail closed. F-13.3.4.
	if pgPool != nil {
		authOpts.RevocationFreshness = revCache
	}
	if !*multiTenant {
		// Even in single-tenant mode, dev-header callers carry the
		// tenant header. Flip to multi-tenant with a permissive
		// registry so the header round-trips.
		authOpts.MultiTenant = true
		authOpts.Registry = permissiveRegistry{}
	} else {
		// spec: §10.2 lines 219-221 — in multi-tenant mode, an
		// unregistered `tenant_id` claim is a hard 403 TENANT_NOT_FOUND.
		// The bearer chain consults the real tenantstore so unprovisioned
		// callers cannot ride a signature-valid token into the platform.
		// F-10.2.1.
		authOpts.Registry = bearerTenantRegistry{store: tenants}
	}
	handler = authmw.Wrap(handler, authOpts)

	// §16.1 request metrics, wrapped before recovery so panics still
	// surface in the request_total / request_duration histograms with
	// the resulting 500 status. The route label collapses
	// high-cardinality path segments to a stable template.
	handler = gwMetrics.Middleware(handler, routeTemplate)

	// spec: §15.5 item 1 + docs/api/index.md line 124 — stamp the
	// `X-Lenny-Deprecated-Version` response header onto every response
	// served under a deprecated URL version prefix. The wrapper is a
	// no-op when --deprecated-api-versions is empty, which is the v1
	// default. Inserted ahead of the recovery wrapper so the header
	// also rides the 500 surface emitted when an inner handler
	// panics. F-15.5.11.
	handler = deprecationmw.Wrap(handler, splitCSV(*deprecatedAPIVersionsCSV)...)

	// spec: §10.4 line 377 — handler-goroutine panic must surface as
	// an explicit 500 response and a structured log line rather than
	// the net/http default of silent recover + truncated response. The
	// recovery wrapper is the outermost middleware so it catches a
	// panic from any inner handler (auth, rate limit, business
	// handlers, SSE writers). F-10.4.9.
	handler = recovermw.Middleware(handler)

	// spec: §16.4 lines 371-372 — outermost middleware. It reads the inbound
	// correlation headers (X-Lenny-Operation-ID, X-Lenny-Agent-Name,
	// traceparent, X-Lenny-Session-ID, X-Lenny-Tenant-ID) and attaches them
	// to the request context so the §16.4 logging handler projects them onto
	// every downstream log line, then emits one structured request-completion
	// line per request carrying those fields. Probe endpoints are skipped so
	// liveness/readiness/metrics scrapes do not flood the access log.
	// F-16.4.2, F-16.4.3.
	handler = correlationmw.Wrap(handler, correlationmw.Options{
		SkipPaths: map[string]bool{
			"/healthz":                  true,
			"/readyz":                   true,
			"/metrics":                  true,
			"/internal/drain-readiness": true,
		},
	})

	// spec: §27.3.1 line 142 — outermost wrapper. The §27.5 MCP WebSocket
	// bearer carrier promotes `Sec-WebSocket-Protocol: lenny.bearer.<token>`
	// (the browser fallback for upgrades that cannot set an Authorization
	// header) to a standard `Authorization: Bearer` header and strips the
	// credential entry from the sub-protocol header. It runs ahead of the
	// correlation/access-log and auth middleware so the bearer is never
	// logged or emitted in audit traces and is validated on the standard
	// bearer path. It is a no-op for every request without a
	// `lenny.bearer.` carrier. F-27.3.4 / F-27.5.2.
	handler = mcp.WebSocketBearerCarrier(handler)

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
	// spec: §4.9 line 1468 — wire the §15.1 / §11.2 usage recorder so
	// proxy-extracted (authoritative) counts are persisted as the
	// quota-accounting record. Pod-reported counts are filtered at the
	// adapterclient ReportUsage boundary (see §11.2 usage path).
	llmProxyUsage := newProxyUsageRecorder(usage, sessions, quotaCounter, tenantLimits)
	// spec: §4.9 lines 1383-1411 — the credentialPolicy Fallback Flow.
	// The Controller holds each session's rotation budget and per-provider
	// fallback chain; the rotator mints a replacement from the chain's
	// next pool and pushes it via the §4.7 RotateCredentials RPC, and the
	// audit sink emits credential.fallback_exhausted on exhaustion.
	llmFallback := credfallback.NewController(*credentialFallbackMaxRotations,
		time.Duration(*credentialFallbackCooldownSeconds)*time.Second)
	llmFallbackDeps := llmFallbackWiring{
		controller: llmFallback,
		rotator:    proxyFallbackRotator{assign: credAssign, registry: podRegistry},
		audit:      proxyFallbackAudit{sink: auditSink},
		metrics:    gwMetrics,
	}
	llmProxySrv := newLLMProxyServer(*llmProxyAddr, llmTranslators, llmLeases, credCache, credDeny, policyChain, llmCache, gwMetrics, llmProxyUsage, llmFallbackDeps)

	// ----- §8.6 GatewayControl gRPC server -----
	// With --grpc-addr the gateway serves the adapter→gateway control
	// surface — the inverse direction of the pod-facing Adapter service.
	// It currently hosts the §8.6 ExtendLease RPC: a pod's adapter calls
	// it when its LLM proxy rejects a request for budget exhaustion, and
	// the gateway computes the lease-extension grant. gwMetrics satisfies
	// leasecontrol.MetricEmitter so every grant drives the §16
	// lenny_delegation_lease_extension_total counter. F-8.6.13.
	// spec: §8.6 line 743 / §11.7 — the leasecontrol auditor adapts the
	// gateway audit appender so every ExtendLease decision (granted,
	// capped, denied) lands as a `delegation.lease_extended` row on the
	// hash-chained §11.7 audit log. The recorder pulls the requesting
	// session's tenant and the live actor sub from the §10.6
	// principal-bound context to satisfy §11.7 line 428 actor-tenant
	// validation. F-8.6.10.
	leaseExtensionAuditor := leaseExtensionAuditAdapter{appender: auditAppender}
	// §8.6 line 718 — the production Elicitor presents the generic budget
	// elicitation on the requesting session's client stream over the §9.2
	// interaction store and blocks for the user's decision. Built only when
	// the GatewayControl listener is enabled. F-8.6.2.
	var leaseElicit leasecontrol.Elicitor
	if *grpcAddr != "" {
		leaseElicit = &leaseElicitor{
			sessions:     sessions,
			interactions: interactions,
			publish:      func(s, et, d string, n time.Time) { eventBus.Publish(s, et, d, n) },
			clock:        clockinject.Now,
			idgen:        func() string { var b [16]byte; _, _ = rand.Read(b[:]); return fmt.Sprintf("lease-elicit-%x", b[:]) },
		}
	}
	gatewayCtrlSrv, gatewayCtrlLis, err := newGatewayControlServer(*grpcAddr, leaseBudgets, gwMetrics, leaseExtensionAuditor, leaseElicit, rateLimiter, *leaseAutoMaxPerMin, replica, *adapterTLSCert, *adapterTLSKey, *adapterCA, *spiffeTrustDomain, *saTokenAudience, mtlsDeny)
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
	// F-7.4.7: thread the configured maxCreatedStateTimeoutSeconds into
	// the watchdog so its `created`-state budget matches the
	// uploadToken TTL and the createdsweeper deadline.
	// spec: §7.1 line 58.
	// F-6.2.14: thread the §6.2 line 249 resuming watchdog and §6.2 line
	// 292 resume_pending wall-clock cap into the gateway watchdog. The
	// resume_pending cap defaults to maxResumeWindowSeconds (which the
	// per-session retryPolicy.maxResumeWindowSeconds tightens); resuming
	// is the §6.2 fixed 300s budget. MaxRetries falls through to the
	// same deployer flag the §4.8 RetryPolicyEvaluator uses so the
	// resuming → resume_pending retry counts against the same budget.
	// spec: §11.3 line 219-221 — every operator-tunable watchdog budget
	// flows through `Config`. The flag surface above defaults each to the
	// §11.3 spec value, so `Config{}` is now constructed with the
	// effective value (after env/flag resolution) rather than the
	// zero-value the watchdog used to backfill silently. F-11.3.11.
	wd := watchdog.New(sessions, tenantsLister{tenants}, watchdog.Config{
		MaxCreatedSeconds:              *maxCreatedStateTimeoutSeconds,
		MaxFinalizingSeconds:           *maxFinalizingTimeoutSeconds,
		MaxReadySeconds:                *maxReadyStateTimeoutSeconds,
		MaxStartingSeconds:             *maxStartingStateTimeoutSeconds,
		MaxSessionAgeSeconds:           *maxSessionAgeSeconds,
		MaxAwaitingClientActionSeconds: *maxAwaitingClientActionSeconds,
		MaxSuspendedPodHoldSeconds:     *maxSuspendedPodHoldSeconds,
		MaxResumePendingSeconds:        *maxResumePendingSeconds,
		MaxResumingSeconds:             *maxResumingSeconds,
		MaxRetries:                     *retryMaxRetries,
	}, nil).
		WithBilling(billing).
		WithTreeArchive(treeArchive).
		WithTerminalHook(sessionSrv).
		// spec: §11.3 line 198 — the maxSessionAge sweep honours a
		// deployer's per-runtime `limits.maxSessionAgeSeconds` / per-pool
		// `maxSessionAgeSeconds` (most-restrictive-wins below the platform
		// default) instead of expiring every session at the single baked
		// default. F-11.3.3.
		WithSessionAgeResolver(sessionage.New(runtimes, pools))
	// spec: §7.2 lines 294, 341 — wire the DLQ TTL sweeper only when the
	// messaging coordinator exists (Redis present). Passing a nil
	// *Coordinator would create a typed-nil interface that defeats the
	// watchdog's nil guard and force wasted List calls every tick.
	if messagingCoord != nil {
		wd = wd.WithMessaging(messagingCoord)
	}
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
		if res.ResumePendingTimeouts > 0 {
			log.Printf("lenny-gateway: watchdog transitioned %d resume_pending sessions to awaiting_client_action per §6.2 line 292",
				res.ResumePendingTimeouts)
		}
		if res.ResumingTimeouts > 0 {
			log.Printf("lenny-gateway: watchdog fired §6.2 line 249 resuming watchdog on %d sessions: %v",
				res.ResumingTimeouts, res.PerResumingOutcome)
		}
	})

	// §7.1 line 67 uploadToken signing-key rotator. The default
	// cadence rotates every 24h with a 5-minute overlap window; the
	// rotator both installs the new key and sweeps overlap keys whose
	// deadline has elapsed. spec: §7.1 line 67.
	go uploadRotator.Run(watchdogCtx)

	// F-7.4.9: §7.1 single-use upload-token tracker sweep. The memory
	// tracker holds one entry per consumed token's digest until its
	// expiry timestamp; without a periodic Sweep the map grows
	// monotonically with the gateway's process lifetime. The cadence is
	// the watchdog tick interval — cheap enough to run frequently and
	// short enough that a sweep happens well within one
	// maxCreatedStateTimeoutSeconds window. spec: §7.1 line 60.
	go func() {
		tick := time.NewTicker(uploadtoken.DefaultRotationInterval / 96) // 15m at the default 24h
		if tick == nil {
			return
		}
		defer tick.Stop()
		for {
			select {
			case <-watchdogCtx.Done():
				return
			case now := <-tick.C:
				if n := uploadTracker.Sweep(now.UTC()); n > 0 {
					log.Printf("lenny-gateway: §7.1 upload-token tracker swept %d expired digests", n)
				}
			}
		}
	}()

	// ----- §9.3 connector OAuth state-store sweep -----
	// F-9.3.16: the in-memory MemoryStateStore is bound by the
	// state-TTL (10 min per §9.3 line 157) plus a `consumed` flag for
	// single-use enforcement. Without a periodic Sweep the entries
	// accumulate until process restart. The cadence is well inside one
	// TTL window so consumed/expired entries are reclaimed promptly.
	// A Redis-backed store relies on native key expiry instead — this
	// goroutine runs only when the in-memory store is wired (the
	// `--connector-oauth-callback-url` opt-in path).
	// spec: §9.3 line 157.
	if connectorStateStore != nil {
		go func(store *connectoroauth.MemoryStateStore) {
			tick := time.NewTicker(1 * time.Minute)
			if tick == nil {
				return
			}
			defer tick.Stop()
			for {
				select {
				case <-watchdogCtx.Done():
					return
				case now := <-tick.C:
					if n := store.Sweep(now.UTC()); n > 0 {
						log.Printf("lenny-gateway: §9.3 connector OAuth state store swept %d expired/consumed entries", n)
					}
				}
			}
		}(connectorStateStore)
	}

	// ----- §7.1 abandoned `created`-state row sweep -----
	// Drops Session rows that stay in `created` past
	// maxCreatedStateTimeoutSeconds (default 300s). The §7.1 line 58
	// uploadToken TTL closes the upload window at that instant; without
	// this sweep the row itself lived forever, so abandoned creates
	// accumulated under repeated client retries.
	// spec: §7.1 line 58.
	createdGC := createdsweeper.New(sessions, tenantsLister{tenants}, createdsweeper.Options{
		// F-7.4.7: pinned to the same maxCreatedStateTimeoutSeconds the
		// watchdog and the uploadToken issuer use.
		// spec: §7.1 line 58.
		Timeout: time.Duration(*maxCreatedStateTimeoutSeconds) * time.Second,
		Clock:   clockinject.Now,
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
		// spec: §8.10 line 1078 — operator-tunable cascade timeout. The
		// per-deploy cap is the Sweeper's wall-clock window an orphan may
		// persist past its root's terminal state. F-8.10.9.
		CascadeTimeout: time.Duration(*delegationCascadeTimeoutSeconds) * time.Second,
		// F-5.2.26: same terminal pipeline as the watchdog so an orphan
		// terminated by background sweep also releases its slot/pod.
		Terminal: sessionSrv,
		// spec: §8.10 lines 1091, 1093-1101, 1103; §16.1 lines 146-149 —
		// publish the cleanup-runs counter, the cumulative terminated
		// counter, the fleet-wide active gauge, and the per-tenant active
		// gauge so the §16.5 OrphanTasksPerTenantHigh alert evaluates.
		// F-8.10.7.
		Metrics: gwMetrics,
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

	// ----- §10.1 dual-store degraded-mode monitor -----
	// Probes Postgres + Redis on a short cadence; on detecting both
	// unreachable it pins lenny_dual_store_unavailable=1, broadcasts
	// PLATFORM_DEGRADED to active SSE streams, and gates session.create
	// (via DualStore on the session-server). Active only when both stores
	// are wired. F-10.1.3.
	if dsMonitor != nil {
		go dsMonitor.Run(watchdogCtx)
	}

	// ----- §12.4 line 210 storage-quota recovery reconciler -----
	// Probes Redis reachability; on a recovery edge it writes each
	// tenant's storage_bytes_used counter back to Redis from the
	// authoritative SUM(artifact_size_bytes) in Postgres so the Lua fast
	// path resumes enforcing against the correct value rather than a
	// stale-zero counter left by a Redis restart. Active only when both
	// Redis and the Postgres artifact catalog are wired. F-12.4.11.
	if storageRecoveryReconciler != nil {
		go storageRecoveryReconciler.Run(watchdogCtx)
	}

	// ----- §11.2 delegation tree budget checkpoint + reconstruction -----
	// On the quotaSyncIntervalSeconds cadence the reconciler persists each
	// active tree's Redis dlg:* counters to the delegation_tree_budget
	// table (§11.2 line 44); on a Redis-recovery edge it reconstructs each
	// checkpointed tree's counters to max(postgres_checkpoint, live) per
	// axis before new delegations resume (§11.2 line 48 / §12.4 line 218),
	// moving a tree whose checkpoint is stale and whose live state cannot
	// be enumerated to awaiting_client_action. Active only when the
	// delegation Redis counters, the Postgres pool, and the SessionStore
	// are all wired. F-11.2.5 / F-12.4.8.
	if treeBudgetConcrete != nil && pgPool != nil && sessions != nil {
		delegationBudgetReconciler = &delegationbudget.Reconciler{
			Probe: func(ctx context.Context) bool {
				return redisconn.PingWithTimeout(redisClient, 2*time.Second) == nil
			},
			Counters:        delegationbudget.CounterAdapter{Reserver: treeBudgetConcrete},
			Trees:           delegationbudget.SessionTreeLister{Sessions: sessions, Tenants: (tenantsLister{tenants}).ListTenants},
			Store:           delegationbudgetpg.New(pgPool),
			Live:            delegationbudget.SessionEnumerator{Sessions: sessions},
			Marker:          delegationbudget.SessionUnrecoverableMarker{Sessions: sessions},
			Metrics:         gwMetrics,
			Interval:        time.Duration(quota.ClampSyncIntervalSeconds(*quotaSyncIntervalSeconds)) * time.Second,
			NodeMemoryBytes: *delegationNodeMemoryFootprintBytes,
			Now:             clockinject.Now,
			Logf:            log.Printf,
		}
		log.Printf("lenny-gateway: §11.2 delegation tree budget checkpoint cadence %s (node footprint %d bytes)",
			time.Duration(quota.ClampSyncIntervalSeconds(*quotaSyncIntervalSeconds))*time.Second, *delegationNodeMemoryFootprintBytes)
		go delegationBudgetReconciler.Run(watchdogCtx)
	}

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
		// §12.5 line 317 gc.cycleIntervalSeconds: the GC sweep cadence,
		// clamped up to the spec's 60s floor so a misconfigured chart
		// value cannot make the leader-elected sweep busy-loop.
		rawGCInterval := time.Duration(*gcCycleIntervalSeconds) * time.Second
		gcInterval := retentiongc.ClampSweepInterval(rawGCInterval)
		if gcInterval != rawGCInterval && rawGCInterval > 0 {
			log.Printf("lenny-gateway: §12.5 line 317 gc.cycleIntervalSeconds=%d below the %ds floor; clamping to the minimum",
				*gcCycleIntervalSeconds, int(retentiongc.MinSweepInterval/time.Second))
		}
		// §12.5 line 341 gc.tombstoneRetentionSeconds: the window a
		// soft-deleted artifact_store row is retained before hard-prune.
		// This is distinct from the §7.1 artifact-retention TTL (when a
		// terminal session's artifacts become soft-delete-eligible).
		gcTombstoneRetention := time.Duration(*gcTombstoneRetentionSeconds) * time.Second
		var arts []retentiongc.Artifact
		if te, ok := transcripts.(sessionArtifactDeleter); ok {
			arts = append(arts, retentiongc.Artifact{Name: "transcripts", Delete: te.DeleteBySession})
		}
		if blobsCataloged != nil {
			// §12.5 ll. 311-313: soft-delete the catalog rows + bucket
			// objects through the cataloging decorator instead of
			// removing them outright. The hard-prune pass below runs on
			// the same Run loop and bumps lenny_gc_tombstones_pruned_total.
			// The tombstone deadline is now + gc.tombstoneRetentionSeconds.
			arts = append(arts, retentiongc.Artifact{
				Name: "artifacts",
				Delete: func(ctx context.Context, tenantID, sessionID string) (int, error) {
					return blobsCataloged.SoftDeleteSession(ctx, tenantID, sessionID, gcTombstoneRetention)
				},
			})
		} else if be, ok := blobs.(sessionArtifactDeleter); ok {
			arts = append(arts, retentiongc.Artifact{Name: "artifacts", Delete: be.DeleteBySession})
		}
		retGC := retentiongc.New(sessions, tenantsLister{tenants}, arts, retentiongc.Options{
			Interval: gcInterval,
			Clock:    clockinject.Now,
			Metrics:  gwMetrics,
		})
		log.Printf("lenny-gateway: §12.5 GC sweep cadence %s (gc.cycleIntervalSeconds=%d); tombstone retention %s (gc.tombstoneRetentionSeconds=%d)",
			gcInterval, int(gcInterval/time.Second), gcTombstoneRetention, int(gcTombstoneRetention/time.Second))
		// §12.5 line 317: bind the erasure-completion → immediate-sweep
		// trigger now that the collector exists. A `gcPriority: high`
		// tenant's expired artifacts are reclaimed the moment one of its
		// erasure jobs completes, independent of the global cycle. A normal
		// tenant takes no extra sweep. Best-effort: a lookup or sweep error
		// is logged but never propagated back into the completed job.
		immediateGCSweep = func(ctx context.Context, tenantID string) {
			t, err := tenants.Get(ctx, tenantID)
			if err != nil {
				log.Printf("lenny-gateway: §12.5 gcPriority lookup for tenant %q failed: %v", tenantID, err)
				return
			}
			if !t.TriggersImmediateGC() {
				return
			}
			collected, err := retGC.SweepTenant(ctx, tenantID, clockinject.Now())
			if err != nil {
				log.Printf("lenny-gateway: §12.5 gcPriority=high immediate sweep for tenant %q failed: %v", tenantID, err)
				return
			}
			log.Printf("lenny-gateway: §12.5 gcPriority=high immediate sweep for tenant %q collected %d session(s)", tenantID, collected)
		}
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

		// §16.4 lines 378-382 audit-retention pruner: only constructed
		// when a durable Postgres audit chain exists (the in-memory
		// gateway has nothing to prune). Production runs it under the
		// §10.1 leader lease alongside the artifact GC above. F-11.7.17.
		if auditPruner != nil {
			log.Printf("lenny-gateway: §16.4 audit-retention sweep cadence %s (retention %d days, gdpr.* %d days)",
				time.Duration(*auditRetentionPruneIntervalSeconds)*time.Second, effectiveAuditRetentionDays, *gdprRetentionDays)
			go auditPruner.Run(watchdogCtx, func(pruned int, err error) {
				if err != nil {
					log.Printf("lenny-gateway: §16.4 audit-retention sweep error: %v", err)
					return
				}
				if pruned > 0 {
					log.Printf("lenny-gateway: §16.4 audit-retention sweep pruned %d audit rows past their retention window", pruned)
				}
			})
		}

		// §12.5 ll. 341 hard-prune sweep: every gc.cycleIntervalSeconds
		// the catalog removes rows whose tombstone deadline has elapsed
		// and emits the count to lenny_gc_tombstones_pruned_total.
		// Production runs this under the §10.1 leader lease; the dev-
		// mode in-memory deployment has no Postgres catalog and skips
		// the sweep entirely.
		if blobsCataloged != nil {
			go func() {
				ticker := time.NewTicker(gcInterval)
				defer ticker.Stop()
				for {
					select {
					case <-watchdogCtx.Done():
						return
					case <-ticker.C:
						// §12.5 ll. 341: the single hard-prune pass sweeps
						// both GC-managed row classes — artifact_store
						// catalog rows and partial-checkpoint manifest rows
						// — on the same deleted_at retention predicate.
						count, err := blobsCataloged.HardPrune(watchdogCtx, clockinject.Now())
						if err != nil {
							log.Printf("lenny-gateway: §12.5 hard-prune sweep error: %v", err)
						} else {
							gwMetrics.AddGCTombstonesPruned("artifact_store", count)
							if count > 0 {
								log.Printf("lenny-gateway: §12.5 hard-prune removed %d tombstoned artifact_store rows past retention",
									count)
							}
						}
						// §12.5 ll. 316, 341: partial-manifest rows live in
						// the checkpoint metadata table and follow the
						// identical post-soft-delete lifecycle. Prune those
						// whose soft-delete tombstone predates
						// now - gc.tombstoneRetentionSeconds.
						pmCutoff := clockinject.Now().Add(-gcTombstoneRetention)
						pmCount, pmErr := hardPrunePartialManifests(watchdogCtx, partialManifests, pmCutoff)
						if pmErr != nil {
							log.Printf("lenny-gateway: §12.5 partial-manifest hard-prune sweep error: %v", pmErr)
							continue
						}
						gwMetrics.AddGCTombstonesPruned("partial_manifest", pmCount)
						if pmCount > 0 {
							log.Printf("lenny-gateway: §12.5 hard-prune removed %d tombstoned partial_manifest rows past retention",
								pmCount)
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

	// ----- §12.5 line 307 continuous T4 KMS availability probe (STO-021) -----
	// A leader-elected background goroutine, co-located with the GC
	// sweeps under the same gateway lease (the leader-election lease is
	// the §10.1 model the GC loops above already run under). On each
	// cadence it enumerates T4 tenants, re-runs the zero-byte
	// encrypt/decrypt round-trip against every tenant:{tenant_id} key,
	// and updates lenny_t4_kms_probe_last_success_timestamp /
	// lenny_t4_kms_probe_result_total so the T4KmsKeyUnusable alert and
	// the admin t4KmsLastProbeSuccessAt field observe silent
	// post-provisioning key drift. The token-bucket rate ceiling caps
	// KMS API spend; the cadence floor is enforced inside Prober.Start.
	{
		probeMetrics, err := tenantkms.NewProbeMetrics(gwMetrics.Registerer())
		if err != nil {
			log.Fatalf("lenny-gateway: §12.5 T4 KMS probe metrics: %v", err)
		}
		prober := &tenantkms.Prober{
			Lifecycle: kmsProbeLifecycle,
			Tenants:   t4TenantSource{tenants},
			Metrics:   probeMetrics,
			Interval:  time.Duration(*t4KmsProbeIntervalSeconds) * time.Second,
			RateLimit: *t4KmsProbeRateLimit,
			Now:       clockinject.Now,
		}
		log.Printf("lenny-gateway: §12.5 continuous T4 KMS probe interval=%ds rate=%.1f/s",
			*t4KmsProbeIntervalSeconds, *t4KmsProbeRateLimit)
		go func() {
			if err := prober.Start(watchdogCtx); err != nil {
				log.Printf("lenny-gateway: §12.5 T4 KMS probe loop exited: %v", err)
			}
		}()
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
	// monotonic ordering guarantee.
	go billingPipeline.RunFlusher(watchdogCtx)

	// ----- §11 line 144 billing failover Tier 1 per-tenant flusher -----
	// When the Tier 1 stream is Redis-backed, a per-tenant flusher
	// goroutine drains each tenant's billing stream back into Postgres
	// after a transient Postgres outage and runs the startup
	// fast-recovery XAUTOCLAIM that claims entries a predecessor replica
	// left. Without it the stream accumulates until billingStreamTTLSeconds
	// and the events are lost. The manager reconciles the per-tenant
	// goroutine set against the tenant store on its own interval.
	// F-11.2.8.
	if billingTier != nil {
		flushInterval := time.Duration(*billingFlushIntervalMs) * time.Millisecond
		mgr := billingTier.NewFlusherManager(tenantsLister{tenants}, flushInterval, 0)
		go mgr.Run(watchdogCtx)
		log.Printf("lenny-gateway: §11.2.1 billing failover Tier 1 per-tenant flusher started (flush every %s)", flushInterval)
	}

	// ----- §12.3 lines 115-125 Postgres write-IOPS sampler -----
	// Periodically differentiates the pg_stat_database row-write total
	// into a sustained write-IOPS rate and publishes
	// lenny_postgres_write_iops so the §16.5 PostgresWriteSaturation
	// alert has a numerator. Only the Postgres-backed deployment has a
	// pool to sample. F-12.3.7.
	if pgPool != nil {
		pool := pgPool
		sampler := pgwritemetrics.New(func(ctx context.Context) (uint64, error) {
			// §12.3 line 62 write sources are row-level inserts/updates/
			// deletes; pg_stat_database aggregates them per database.
			var n int64
			if err := pool.QueryRow(ctx,
				`SELECT COALESCE(SUM(tup_inserted + tup_updated + tup_deleted), 0)::bigint
				 FROM pg_stat_database WHERE datname = current_database()`).Scan(&n); err != nil {
				return 0, err
			}
			if n < 0 {
				n = 0
			}
			return uint64(n), nil
		}, gwMetrics, clockinject.Now)
		go sampler.Start(watchdogCtx, time.Duration(*postgresWriteIopsSampleSeconds)*time.Second)
	}

	// ----- §11.2.1 billing retention pruner -----
	// Periodically deletes billing events past the configured
	// billing.retentionDays window across every registered tenant. The
	// DELETE is idempotent, so running it on every replica is safe (a
	// replica that loses the race prunes zero rows). Best-effort: a
	// per-tenant failure is logged and the sweep continues.
	// spec: §11.2.1 line 151. F-11.2.15.
	billingPruner := billingretention.New(billing, tenantsLister{tenants}, billingretention.Options{
		RetentionDays: *billingRetentionDays,
		Clock:         clockinject.Now,
	})
	log.Printf("lenny-gateway: §11.2.1 billing retention pruner active (retention %d days)", billingPruner.RetentionDays())
	go billingPruner.Run(watchdogCtx, func(pruned int, err error) {
		if err != nil {
			log.Printf("lenny-gateway: §11.2.1 billing retention sweep error: %v", err)
			return
		}
		if pruned > 0 {
			log.Printf("lenny-gateway: §11.2.1 billing retention pruned %d events past the %d-day window",
				pruned, billingPruner.RetentionDays())
		}
	})

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
		// §16.5 line 460 — the standing ElicitationContentIntegrityWeakened
		// alert reads a gauge that must reflect the live tenant posture, so
		// refresh it on the same 30s cadence as the other gauge exporters.
		// F-9.2.5.
		exportElicitationIntegrityWeakened(ctx, tenants, *elicitationFloor, gwMetrics)
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

	// spec: §13.3 line 595 — NTP drift sampler. Samples the clockinject
	// offset on the configured cadence, publishes the
	// lenny_time_drift_seconds gauge, and gates /healthz at the 5s
	// degraded threshold. F-13.3.5.
	go driftMonitor.Start(watchdogCtx, 30*time.Second)

	// spec: §9.4 line 202 / §16.1 line 153 — periodic per-tenant
	// MemoryStore record-count sampler. Walks the store's tenants and
	// emits `lenny_memory_store_record_count{tenant_id}` on the
	// configured interval (default 60s); 0 disables. The contract is a
	// best-effort approximate gauge sampled periodically. F-9.4.1.
	if memories != nil && *memoryRecordCountInterval > 0 {
		if counter, ok := memories.(interface {
			TenantRecordCounts(context.Context) (map[string]int, error)
		}); ok {
			interval := *memoryRecordCountInterval
			go func() {
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				sample := func() {
					ctx, cancel := context.WithTimeout(watchdogCtx, 30*time.Second)
					defer cancel()
					counts, err := counter.TenantRecordCounts(ctx)
					if err != nil {
						log.Printf("lenny-gateway: §9.4 record-count sampler: %v", err)
						return
					}
					for tenantID, n := range counts {
						gwMetrics.SetMemoryStoreRecordCount(tenantID, n)
					}
				}
				sample()
				for {
					select {
					case <-watchdogCtx.Done():
						return
					case <-ticker.C:
						sample()
					}
				}
			}()
			log.Printf("lenny-gateway: §9.4 record-count sampler interval=%s backend=%s",
				interval, memoryBackendLabel)
		}
	}

	// spec: §10.4 line 385 / §16.5 PDBBlockedEvictions — periodic PDB
	// status poller. Each cycle that observes Status.DisruptionsAllowed
	// == 0 on the gateway PDB increments the
	// lenny_pdb_blocked_evictions_total counter so the §16.5 alert can
	// fire when the PDB sustains blocking. The poller activates only
	// when the cluster client is wired (production install with
	// --agent-namespace) and when --gateway-namespace is set so the
	// poller can address the right PDB object. F-10.4.4.
	if clusterClient != nil && *gatewayNamespace != "" {
		watcher := pdbwatcher.New(pdbwatcher.Config{
			Client:    clusterClient,
			Namespace: *gatewayNamespace,
			PDBName:   *gatewayPDBName,
			Sink:      gwMetrics,
		})
		go watcher.Run(watchdogCtx)
		log.Printf("lenny-gateway: §10.4 PDB poller watching %s/%s",
			*gatewayNamespace, *gatewayPDBName)
	}

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

	// §16.1 line 713 / §25.13 lines 4833–4835 — register the bundled-
	// alerting observability surface so an operator's chart inputs and
	// per-rule in-process eval latency become visible on /metrics.
	// F-25.13.3.
	alertingMx, err := alertingmetrics.New(nil)
	if err != nil {
		log.Fatalf("lenny-gateway: §25.13 alerting metrics: %v", err)
	}
	{
		var formats []string
		for _, f := range strings.Split(*alertingBundleFormats, ",") {
			if t := strings.TrimSpace(f); t != "" {
				formats = append(formats, t)
			}
		}
		alertingMx.SetBundledFormats(formats...)
	}
	if *alertingOverrideCount < 0 {
		log.Fatalf("lenny-gateway: --alerting-override-count must be >= 0 (got %d) (§25.13 line 4834)", *alertingOverrideCount)
	}
	alertingMx.SetOverrideCount(*alertingOverrideCount)

	// §4.0 / §25.13: the per-replica in-process alert tracker drives the
	// §16.5 catalog through inactive → pending → firing and emits
	// alert_fired / alert_resolved through the shared EventEmitter. With
	// no PromQL backend wired the tracker uses NoopExprEvaluator, which
	// keeps every rule inactive — the fall-back posture for a
	// Prometheus-less deployment. The wiring is unconditional so a
	// future commit that supplies a real ExprEvaluator only swaps the
	// backend, not the surface.
	//
	// spec: §25.13 line 4798 — operators can suppress the in-process
	// tracker entirely via `gateway.healthTracker.useCompiledRules:
	// false`. In that posture the per-replica health view falls back to
	// dependency probes and circuit breaker state only. F-25.13.4.
	if *healthTrackerUseCompiledRules {
		alertEvaluator := evaluator.NewWithEmitter(
			rules.Catalog(),
			evaluator.NoopExprEvaluator{},
			evaluator.EventEmitOptions{
				Emitter:            opsEmitter,
				Source:             "//lenny.dev/gateway/" + replica,
				OnRuleEvalDuration: alertingMx.ObserveRuleEvalDuration,
			},
		)
		go alertEvaluator.Run(watchdogCtx)
	} else {
		log.Printf("lenny-gateway: §25.13 in-process alert tracker disabled (gateway.healthTracker.useCompiledRules=false); /v1/admin/health falls back to dependency probes + circuit breaker state only")
	}

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTERM, syscall.SIGINT)

	// spec: §11.7 item 2 lines 356-359 — periodic background integrity
	// check. After the signal handler is installed, run the grant /
	// trigger / erasure-guard re-verification and recent-chain sample on
	// the resolved cadence. A detected drift logs a critical line and
	// increments lenny_audit_grant_drift_total; with audit.hardFailOnDrift
	// it self-signals SIGTERM so the existing graceful-shutdown path
	// drains in-flight work. Postgres-only: the in-memory chain has no
	// grant surface to drift. F-11.7.3.
	if pgPool != nil {
		periodic := &integrity.PeriodicCheck{
			DB: pgPool,
			Cfg: integrity.PeriodicConfig{
				Interval:        resolvedGrantCheckInterval,
				HardFailOnDrift: *auditHardFailOnDrift,
				ChainSampleN:    *auditStartupChainCheckEntries,
			},
			OnGrantDrift: gwMetrics.IncAuditGrantDrift,
			OnChainState: gwMetrics.IncAuditChainIntegrity,
			Logf:         func(format string, args ...any) { log.Printf("lenny-gateway: "+format, args...) },
			Shutdown: func(string) {
				if proc, perr := os.FindProcess(os.Getpid()); perr == nil {
					_ = proc.Signal(syscall.SIGTERM)
				}
			},
		}
		log.Printf("lenny-gateway: §11.7 item 2 periodic audit integrity check active (interval=%s regulated=%v hardFailOnDrift=%v)",
			resolvedGrantCheckInterval, grantCheckRegulated, *auditHardFailOnDrift)
		go periodic.Run(watchdogCtx)
	}

	// spec: §11.7 Wire Format — start the OCSF translator's background
	// retry loop. It drains pending audit rows into OCSF records and
	// multicasts them to the SIEM forwarder (when configured), advancing
	// each row's ocsf_translation_state on the resolved cadence.
	// F-11.7.1 / F-11.7.11.
	if ocsfTranslator != nil {
		log.Printf("lenny-gateway: §11.7 OCSF translator active (siem=%v retryInterval=%ds maxAttempts=%d batchSize=%d)",
			*auditSIEMEndpoint != "", *auditOCSFRetryIntervalSeconds, *auditOCSFMaxAttempts, *auditOCSFBatchSize)
		go ocsfTranslator.Run(watchdogCtx)
	}

	// spec: §12.3 line 97 — start the SIEM outbox forwarder's background
	// loop. It tails committed audit_log rows, delivers each to the SIEM
	// after Postgres commits it, checkpoints the per-tenant delivery
	// high-water mark in siem_delivery_state, and emits
	// lenny_audit_siem_delivery_lag_seconds. F-12.3.6 / F-12.3.17.
	if ocsfOutbox != nil {
		log.Printf("lenny-gateway: §12.3 SIEM outbox forwarder active (pollInterval=%ds maxDeliveryLag=%ds)",
			*auditSIEMPollIntervalSeconds, *auditSIEMMaxDeliveryLagSeconds)
		go ocsfOutbox.Run(watchdogCtx)
	}

	// spec: §12.3 line 81 — drive the opt-in T2 audit batch buffer's
	// flush loop. It flushes buffered non-PII T2 audit events every
	// flushIntervalMs or when the buffer reaches flushBatchSize, and
	// flushes the remainder on shutdown. F-12.3.14.
	if auditBatchBuffer != nil {
		log.Printf("lenny-gateway: §12.3 T2 audit batching enabled (flushInterval=%dms flushBatchSize=%d)",
			*auditFlushIntervalMs, *auditFlushBatchSize)
		go auditBatchBuffer.Run(watchdogCtx)
	}

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
	// Flush buffered spans before the process exits (§16.3 batch processor).
	_ = traceShutdown(ctx)
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
	// §12.4: close the per-concern split clients (no-op when no split is
	// configured; the base client closed above is left untouched).
	_ = concernRedis.Close()
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

// lastSeqStore adapts the §4.2 session store to the §7.3 line 397
// sessions.last_seq durability hooks on the session event bus. It
// satisfies both sessionevents.LastSeqPersister (advance on every
// publish) and sessionevents.LastSeqLoader (seed the in-memory
// counter on first publish for the session). Both methods are
// best-effort — a Postgres outage degrades to the local counter
// without dropping events. F-7.3.3.
type lastSeqStore struct{ sessions sessionstore.Store }

// LoadLastSeq returns the persisted §7.3 line 397 sessions.last_seq
// counter so the Bus seeds its local counter on the first publish for
// the session (the coordinator-handoff "primed from Postgres at
// handoff step 0" contract). A missing row reads as zero so a fresh
// session starts at 1.
func (l lastSeqStore) LoadLastSeq(ctx context.Context, tenantID, sessionID string) (int64, error) {
	sess, err := l.sessions.Get(ctx, tenantID, sessionID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return sess.LastSeq, nil
}

// AdvanceLastSeq persists the new per-session SeqNum to Postgres. The
// store's Update mutate-callback applies the new value and the
// pgstore's GREATEST floor in updateSQL keeps the persisted value
// monotonic against late writers from sibling replicas.
func (l lastSeqStore) AdvanceLastSeq(ctx context.Context, tenantID, sessionID string, seq int64) error {
	_, err := l.sessions.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		if seq > row.LastSeq {
			row.LastSeq = seq
		}
		return nil
	})
	if errors.Is(err, sessionstore.ErrNotFound) {
		return nil
	}
	return err
}

// sessionRetryLookup adapts the §4.2 session store to the §4.8
// RetryPolicyEvaluator's RetryStateLookup: a missing session reads as
// not-found (ok == false, the request is admitted), and any other store
// fault surfaces as an error so the fail-closed evaluator rejects.
// maxInputSizeResolverHolder lets the §4.8 DelegationPolicyEvaluator be
// registered into the policy chain before delegationSvc is constructed:
// the inner resolver is filled in once the service exists. Until then
// (and whenever inner is nil) it reports "no policy", so the evaluator
// falls back to the operator-configured default maxInputSize. The holder
// is read on the request path after wiring completes, so the deferred
// assignment is safe. spec: §4.8 line 974; §8.3 line 157. F-13.5.1 / F-8.2.9.
type maxInputSizeResolverHolder struct {
	inner policy.MaxInputSizeResolver
}

func (h *maxInputSizeResolverHolder) ResolveMaxInputSize(ctx context.Context, tenantID, parentSessionID string) (int, bool) {
	if h.inner == nil {
		return 0, false
	}
	return h.inner.ResolveMaxInputSize(ctx, tenantID, parentSessionID)
}

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

// llmFallbackWiring bundles the §4.9 Fallback Flow dependencies the LLM
// proxy handler drives on an upstream credential fault. A zero value (or
// nil controller) leaves the proxy on its pre-fallback behavior.
type llmFallbackWiring struct {
	controller *credfallback.Controller
	rotator    llmproxy.FallbackRotator
	audit      llmproxy.FallbackAuditSink
	metrics    llmproxy.FallbackMetrics
}

func newLLMProxyServer(addr string, translators llmproxy.TranslatorRegistry, leases credleasestore.LeaseStore, creds *credcache.Cache, denyList *denylist.DenyList, chain *interceptor.Chain, cache llmproxy.ProxyCache, gwMetrics *gatewaymetrics.Metrics, usage llmproxy.UsageRecorder, fallback llmFallbackWiring) *http.Server {
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
		// spec: §4.9 line 1468 — proxy-extracted counts feed the §15.1 /
		// §11.2 usage record. A nil Usage discards the counts.
		Usage: usage,
		// §16.1 lines 97, 99, 100: active connections, translation
		// duration, and translation errors on the gateway registry.
		Metrics: gwMetrics,
		// spec: §4.9 lines 1383-1411 — the credentialPolicy Fallback Flow.
		Fallback:        fallback.controller,
		FallbackRotator: fallback.rotator,
		FallbackAudit:   fallback.audit,
		FallbackMetrics: fallback.metrics,
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
// The server hosts the §8.6 ExtendLease RPC. Its budget state is the
// caller-supplied MemoryBudgetSource (shared with the §15.1 admin
// extension-denial clear endpoint so both mutate one set of per-tree
// denial flags), which doubles as the TenantResolver; a nil budgets
// argument falls back to a fresh source. The §8.6 durability
// requirement — persisting the extension-denied flag and cool-off
// expiry to the delegation_tree_budget Postgres table so a coordinator
// handoff cannot bypass a user rejection — is met by swapping in a
// Postgres-backed leasecontrol.BudgetSource with the Wave 1
// store-persistence work; leasecontrol.Service depends only on the
// interface.
//
// tlsCert/tlsKey/clientCA carry the §4.7 mesh credentials (the gateway's
// own --adapter-tls-* material). When clientCA is set the listener
// requires and verifies the pod adapter's client certificate, and the
// RequireVerifiedPeerInterceptor fails any call lacking a verified
// chain; all three empty selects the local-development plaintext path.
// F-8.6.4 / F-15.3.1.
//
// metrics may be nil for the no-metrics test path; in production the
// gatewaymetrics.Metrics implements leasecontrol.MetricEmitter so
// every ExtendLease decision drives the §16 line 66
// `lenny_delegation_lease_extension_total` counter. F-8.6.13.
//
// trustDomain and denyList wire the §10.3 NET-060 inbound peer
// validation: when both clientCA and trustDomain are set, the listener
// installs a SPIFFE VerifyPeerCertificate callback that validates each
// inbound pod certificate's `spiffe://<trust-domain>/agent/{pool}/{pod}`
// URI SAN at handshake (spec line 321) and rejects a certificate on the
// §10.3 revocation deny list (spec line 352). A rejection aborts the
// handshake with no gRPC frame and emits the spec's `pod_identity_mismatch`
// log. trustDomain empty leaves CA-only verification in place (the
// local-development path). F-10.3.1 / F-10.3.7 / F-10.3.13.
func newGatewayControlServer(addr string, budgets *leasecontrol.MemoryBudgetSource, metrics leasecontrol.MetricEmitter, auditor leasecontrol.Auditor, elicitor leasecontrol.Elicitor, autoCounter ratelimit.Counter, defaultAutoMaxPerMin int, replicaID, tlsCert, tlsKey, clientCA, trustDomain, saTokenAudience string, denyList spiffe.DenyChecker) (*grpc.Server, net.Listener, error) {
	if addr == "" {
		return nil, nil, nil
	}
	if budgets == nil {
		budgets = leasecontrol.NewMemoryBudgetSource()
	}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets:           budgets,
		Tenants:           budgets,
		Metrics:           metrics,
		Auditing:          auditor,
		ServiceInstanceID: replicaID,
		Clock:             clockinject.Now,
		// §8.6 line 714 — wire the elicitation path so elicitation-mode
		// trees solicit the user's consent instead of auto-granting.
		// F-8.6.2.
		Elicitor: elicitor,
		// §8.6 line 712 — the auto-mode rate-limit counter (reuses the
		// §11.1 request-rate counter, Redis-backed when configured) and
		// the deployment-default cap. F-8.6.7.
		AutoExtensionCounter:    autoCounter,
		DefaultAutoMaxPerMinute: defaultAutoMaxPerMin,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build GatewayControl service: %w", err)
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("bind GatewayControl listener on %s: %w", addr, err)
	}
	// spec: §4.7 line 616 / §15.3 — the adapter↔gateway channel is mTLS.
	// The pod adapter is the client of this listener, so the gateway
	// presents its mesh server cert (--adapter-tls-cert/key, its §4.7
	// identity) and requires + verifies the adapter's client cert against
	// the mesh CA (--adapter-ca). The same adapter.TLSServerOption helper
	// the pod-facing Adapter service uses builds the credentials, so both
	// directions of the channel share one mTLS configuration. When no
	// cert material is configured the option is nil and the listener
	// serves plaintext — the documented local-development path only.
	// F-8.6.4 / F-15.3.1.
	// spec: §10.3 line 321 (NET-060) — the gateway validates the pod's
	// SPIFFE URI on every inbound handshake. The verifier runs as a
	// VerifyPeerCertificate callback on top of CA chain verification, so
	// possession of a cluster-CA cert is necessary but never sufficient
	// (spec line 324). It also consults the §10.3 revocation deny list
	// (spec line 352) so a cert revoked between rotations is rejected at
	// handshake. Only installed when client-cert verification is active
	// (clientCA set) and a trust domain is configured; otherwise the
	// local-development plaintext/CA-only path is preserved.
	var tlsMods []adapter.TLSConfigMod
	if clientCA != "" && trustDomain != "" {
		verifier := spiffe.AgentPeerVerifier{
			TrustDomain: trustDomain,
			DenyList:    denyList,
			OnMismatch: func(reason spiffe.MismatchReason, uri string, mErr error) {
				slog.Warn("pod_identity_mismatch",
					"net_rule", "NET-060",
					"reason", string(reason),
					"spiffe_uri", uri,
					"error", mErr.Error())
			},
		}
		tlsMods = append(tlsMods, func(c *tls.Config) {
			c.VerifyPeerCertificate = verifier.VerifyPeerCertificate
		})
	}
	tlsOpt, err := adapter.TLSServerOption(tlsCert, tlsKey, clientCA, tlsMods...)
	if err != nil {
		return nil, nil, fmt.Errorf("§8.6 GatewayControl mTLS credentials: %w", err)
	}
	var opts []grpc.ServerOption
	if tlsOpt != nil {
		opts = append(opts, tlsOpt)
	}
	// The interceptor fails closed when client-cert verification is
	// active (clientCA set): every ExtendLease call must arrive over a
	// verified mTLS chain, since the handler trusts the session_id in the
	// request body and has no other proof of the caller's identity.
	// F-8.6.4 / F-15.3.1.
	//
	// spec: §10.3 line 334 — the gateway validates the projected SA
	// token's deployment-specific audience claim on every pod→gateway
	// request, the SA-token layer of the §10.3 defense-in-depth chain.
	// The interceptor is a no-op when no audience is configured (the
	// local-development path), so it composes with the mTLS gate above
	// without disturbing dev runs. F-10.3.20.
	opts = append(opts, grpc.ChainUnaryInterceptor(
		leasecontrol.RequireVerifiedPeerInterceptor(clientCA != ""),
		leasecontrol.RequireSATokenAudienceInterceptor(saTokenAudience),
	))
	// spec: §16.3 line 328 ("Pod → Gateway (delegation tool calls carry
	// parent trace context)") — extract the inbound traceparent from gRPC
	// metadata so the gateway's GatewayControl spans continue the pod's
	// trace. F-16.3.3.
	opts = append(opts, grpc.StatsHandler(otelgrpc.NewServerHandler()))
	gs := grpc.NewServer(opts...)
	adapterv1.RegisterGatewayControlServer(gs, svc)
	return gs, lis, nil
}

// leaseExtensionAuditAdapter implements leasecontrol.Auditor, turning
// each ExtensionAudit record into a §11.7 hash-chained audit row
// keyed on the request tenant. The event type is the spec-listed
// `delegation.lease_extended`; the payload carries every §8.6 line
// 743 field so a forensic reconstruction can identify the requesting
// session, the approval mode and approver, the per-batch grouping,
// the issuing replica, and the client originator. F-8.6.10.
// spec: §8.6 line 743
type leaseExtensionAuditAdapter struct {
	appender policy.AuditAppender
}

func (a leaseExtensionAuditAdapter) RecordExtension(ctx context.Context, e leasecontrol.ExtensionAudit) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id":      e.RequestSessionID,
		"root_session_id": e.RootSessionID,
		// §8.6 line 643 requested/granted amounts across every extendable
		// dimension, not just tokens. F-8.6.1.
		"requested_tokens":            e.Requested.Tokens,
		"granted_tokens":              e.Granted.Tokens,
		"requested_seconds":           e.Requested.Seconds,
		"granted_seconds":             e.Granted.Seconds,
		"requested_children":          e.Requested.Children,
		"granted_children":            e.Granted.Children,
		"requested_parallel_children": e.Requested.ParallelChildren,
		"granted_parallel_children":   e.Granted.ParallelChildren,
		"requested_tree_size":         e.Requested.TreeSize,
		"granted_tree_size":           e.Granted.TreeSize,
		"requested_file_export_files": e.Requested.FileExportFiles,
		"granted_file_export_files":   e.Granted.FileExportFiles,
		"requested_file_export_bytes": e.Requested.FileExportBytes,
		"granted_file_export_bytes":   e.Granted.FileExportBytes,
		"effective_max":               e.EffectiveMax,
		"outcome":                     string(e.Outcome),
		"approval_mode":               string(e.ApprovalMode),
		"approver":                    e.Approver,
		"batch_id":                    e.BatchID,
		"service_instance_id":         e.ServiceInstanceID,
		"client_ip":                   e.ClientIP,
		"new_limits": map[string]any{
			"token_budget":      e.NewLimits.Tokens,
			"max_age_seconds":   e.NewLimits.Seconds,
			"children":          e.NewLimits.Children,
			"parallel_children": e.NewLimits.ParallelChildren,
			"tree_size":         e.NewLimits.TreeSize,
			"file_export_files": e.NewLimits.FileExportFiles,
			"file_export_bytes": e.NewLimits.FileExportBytes,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = a.appender.Append(ctx, e.TenantID, "delegation.lease_extended", json.RawMessage(data), clockinject.Now().UTC())
}

// RecordAutoRateLimitExceeded emits the §8.6 line 712
// `delegation.lease_extension_auto_rate_limit_exceeded` audit row when an
// auto-mode extension request trips the tree's maxAutoExtensionsPerMinute
// and the gateway falls back to elicitation for the remainder of the
// window. F-8.6.7.
// spec: §8.6 line 712
func (a leaseExtensionAuditAdapter) RecordAutoRateLimitExceeded(ctx context.Context, e leasecontrol.AutoRateLimitAudit) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id":          e.RequestSessionID,
		"root_session_id":     e.RootSessionID,
		"max_per_minute":      e.MaxPerMinute,
		"service_instance_id": e.ServiceInstanceID,
		"client_ip":           e.ClientIP,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = a.appender.Append(ctx, e.TenantID, "delegation.lease_extension_auto_rate_limit_exceeded", json.RawMessage(data), clockinject.Now().UTC())
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

// platformConfigMissing is one §10.3 line 361 LENNY_CONFIG_MISSING
// violation: a required platform configuration key that is absent or
// invalid. The fields mirror the structured-log fields §10.3 line 371
// mandates (config_key, scope, remediation).
type platformConfigMissing struct {
	configKey   string
	scope       string
	remediation string
}

// validatePlatformConfig returns the §10.3 line 361 required-key
// violations for the platform keys gated at this point in gateway
// startup: the OIDC issuer URL and client ID (both exempt in dev mode
// per the line 373 dev-mode symmetry and §17.4), and the
// defaultMaxSessionDuration (always required to be a positive duration).
// The remaining required keys fail closed elsewhere so each key is
// gated before the replica is marked ready: noEnvironmentPolicy by
// resolveNoEnvironmentPolicy, playground.devTenantId by
// playground.Config.Validate. Extracted from main() so the
// TestGatewayConfigValidation regression test can cover the §10.3
// contract without booting a gateway. spec: §10.3 lines 361-373;
// §17.4 dev mode.
func validatePlatformConfig(devMode bool, oidcIssuerURL, oidcClientID string, defaultMaxSessionSeconds int) []platformConfigMissing {
	var missing []platformConfigMissing
	if !devMode {
		switch issuer := strings.TrimSpace(oidcIssuerURL); {
		case issuer == "":
			missing = append(missing, platformConfigMissing{
				configKey:   "auth.oidc.issuerUrl",
				scope:       "platform",
				remediation: "set auth.oidc.issuerUrl (Helm) / --oidc-issuer-url / LENNY_OIDC_ISSUER_URL to the OIDC issuer URL, or run with LENNY_DEV_MODE=true",
			})
		case !isAbsoluteURL(issuer):
			missing = append(missing, platformConfigMissing{
				configKey:   "auth.oidc.issuerUrl",
				scope:       "platform",
				remediation: "auth.oidc.issuerUrl must be an absolute URL (scheme://host); fix --oidc-issuer-url / LENNY_OIDC_ISSUER_URL",
			})
		}
		if strings.TrimSpace(oidcClientID) == "" {
			missing = append(missing, platformConfigMissing{
				configKey:   "auth.oidc.clientId",
				scope:       "platform",
				remediation: "set auth.oidc.clientId (Helm) / --oidc-client-id / LENNY_OIDC_CLIENT_ID, or run with LENNY_DEV_MODE=true",
			})
		}
	}
	if defaultMaxSessionSeconds <= 0 {
		missing = append(missing, platformConfigMissing{
			configKey:   "defaultMaxSessionDuration",
			scope:       "platform",
			remediation: "set gateway.maxSessionAgeSeconds (Helm) / --max-session-age-seconds / LENNY_MAX_SESSION_AGE_SECONDS to a positive number of seconds",
		})
	}
	return missing
}

// isAbsoluteURL reports whether s parses as an absolute URL with a
// scheme and host — the §10.3 line 365 "Non-empty URL" acceptance
// criterion for auth.oidc.issuerUrl.
func isAbsoluteURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && u.IsAbs() && u.Host != ""
}

// buildStartupProbeTLSConfig assembles the §10.3 line 359 startup TLS
// probe's client config from the optional CA bundle and client
// certificate. An empty CA uses the system trust store; an empty
// cert/key presents no client certificate. spec: §10.3 line 359.
func buildStartupProbeTLSConfig(caFile, certFile, keyFile string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read --startup-tls-probe-ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("--startup-tls-probe-ca %s contains no PEM certificates", caFile)
		}
		cfg.RootCAs = pool
	}
	if certFile != "" || keyFile != "" {
		crt, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load --startup-tls-probe-cert/--startup-tls-probe-key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{crt}
	}
	return cfg, nil
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
// this in single-tenant mode (where the §10.2 dev-header transport
// flips to MultiTenant=true to round-trip the tenant header).
// Multi-tenant production deployments use bearerTenantRegistry
// instead, which consults the real tenantstore.
type permissiveRegistry struct{}

func (permissiveRegistry) IsRegistered(string) (bool, error) { return true, nil }

// kmsBreakerObserver routes the §10.2 line 225 JWTSigner breaker
// transitions and signing failures onto gatewaymetrics so the §16.5
// KMSSigningUnavailable alert reads them. The metrics pointer is wired
// in after gatewaymetrics.New() returns; pre-wire calls are no-ops.
// spec: §10.2 line 225. F-10.2.6.
type kmsBreakerObserver struct {
	mu sync.Mutex
	m  *gatewaymetrics.Metrics
}

func (o *kmsBreakerObserver) SetMetrics(m *gatewaymetrics.Metrics) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.m = m
}

func (o *kmsBreakerObserver) metrics() *gatewaymetrics.Metrics {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.m
}

// hardPrunePartialManifests runs the §12.5 ll. 341 tombstone hard-prune
// pass over the partial-checkpoint manifest table: it physically removes
// every row whose soft-delete tombstone predates cutoff
// (now - gc.tombstoneRetentionSeconds). The pass is the sibling of the
// artifact_store hard-prune and runs on the same GC cycle so partial
// manifests follow the identical post-soft-delete lifecycle. Per-row
// HardDelete failures are logged and skipped — the next cycle retries
// them; a list failure returns the error with no rows pruned. Returns
// the number of rows physically removed.
//
// spec: §12.5 ll. 316, 341 — partial-manifest rows are swept by the same
// hard-prune pass on the same deleted_at retention predicate.
func hardPrunePartialManifests(ctx context.Context, store partialmanifeststore.Store, cutoff time.Time) (int, error) {
	expired, err := store.ListSoftDeletedBefore(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	pruned := 0
	for _, r := range expired {
		if derr := store.HardDelete(ctx, r.TenantID, r.SessionID, r.Generation); derr != nil {
			log.Printf("lenny-gateway: §12.5 partial-manifest hard-prune row %s/%s gen=%d: %v",
				r.TenantID, r.SessionID, r.Generation, derr)
			continue
		}
		pruned++
	}
	return pruned, nil
}

// runStartupChainContinuityCheck implements the §12.3 line 101 startup
// chain-continuity check: it re-verifies the most recent lastN audit
// rows of every tenant's hash chain, increments
// lenny_audit_chain_integrity_total per tenant by §11.7 state, and logs
// the spec WARN message for each detected gap. A broken chain fires the
// §16.5 AuditChainGap alert through the metric; the gateway does not
// refuse to start. spec: §12.3 line 101. F-12.3.9.
func runStartupChainContinuityCheck(ctx context.Context, db integrity.Querier, lastN int, m *gatewaymetrics.Metrics) {
	results, err := integrity.CheckChainContinuityRecent(ctx, db, lastN)
	if err != nil {
		log.Printf("lenny-gateway: WARNING: §12.3 startup audit chain-continuity check could not run: %v", err)
		return
	}
	for _, r := range results {
		m.IncAuditChainIntegrity(string(r.Result.Integrity))
		if !r.Broken() {
			continue
		}
		if r.GapHighSeq() > 0 {
			log.Printf("Audit chain gap detected for tenant %s: gap between sequence %d and %d (~%s to %s). This indicates T2 audit events were lost from the in-memory batch buffer during a previous gateway crash. T3/T4 events are synchronous and will not appear in chain gaps.",
				r.TenantID, r.GapLowSeq(), r.GapHighSeq(),
				r.GapStart().Format(time.RFC3339), r.GapEnd().Format(time.RFC3339))
			continue
		}
		log.Printf("lenny-gateway: WARNING: §12.3 audit chain broken for tenant %s at sequence %d: %s",
			r.TenantID, r.Result.BreakSeq, r.Result.Detail)
	}
}

func (o *kmsBreakerObserver) OnSigningFailure() {
	if m := o.metrics(); m != nil {
		m.RecordKMSSigningError("inner")
	}
}

func (o *kmsBreakerObserver) OnRejected() {
	if m := o.metrics(); m != nil {
		m.RecordKMSSigningError("rejected")
	}
}

func (o *kmsBreakerObserver) OnCircuitOpen() {
	if m := o.metrics(); m != nil {
		m.SetKMSSigningCircuitState(2)
	}
}

func (o *kmsBreakerObserver) OnCircuitClosed() {
	if m := o.metrics(); m != nil {
		m.SetKMSSigningCircuitState(0)
	}
}

// memoryStoreObserver adapts the gatewaymetrics emitters into the §9.4
// MemoryStore Observer contract so the in-memory and Postgres
// backends route their per-operation metrics through one bound
// `backend` label. spec: §9.4 line 200 / §16.1 line 151–154. F-9.4.1.
type memoryStoreObserver struct {
	metrics *gatewaymetrics.Metrics
	backend string
}

func (o memoryStoreObserver) ObserveOperation(op string, seconds float64) {
	o.metrics.ObserveMemoryStoreOperation(op, o.backend, seconds)
}

func (o memoryStoreObserver) IncError(op, errorType string) {
	o.metrics.IncMemoryStoreError(op, o.backend, errorType)
}

func (o memoryStoreObserver) SetRecordCount(tenantID string, count int) {
	o.metrics.SetMemoryStoreRecordCount(tenantID, count)
}

func (o memoryStoreObserver) IncUserOverThreshold(tenantID string) {
	o.metrics.IncMemoryStoreUserOverThreshold(tenantID, o.backend)
}

// ocsfMetricsAdapter bridges the §11.7 OCSF translator's metric surface
// onto the gateway's Prometheus registry: a per-row translation failure
// advances lenny_audit_ocsf_translation_failed_total labeled by event
// type and ocsf.ErrorClass. Success and dead-letter counts stay on the
// translator's in-memory CountingMetrics (no dedicated Prometheus series
// exists for them in the §16.1 catalog). F-11.7.1 / F-11.7.15.
type ocsfMetricsAdapter struct{ metrics *gatewaymetrics.Metrics }

func (a ocsfMetricsAdapter) TranslationFailed(eventType string, class ocsf.ErrorClass) {
	a.metrics.IncAuditOCSFTranslationFailed(eventType, string(class))
}

func (a ocsfMetricsAdapter) TranslationSucceeded(string) {}

func (a ocsfMetricsAdapter) DeadLettered(string) {}

// userstorePlatformRoles adapts a userstore.Store into the §10.2 line
// 294 platform-managed role resolver consulted by the auth middleware.
// When a row exists for (tenantID, subject) — including a row whose
// Roles slice is empty — its Roles fully replace the OIDC claim, so
// tenant-admins can downgrade a user with an over-broad OIDC claim by
// recording an explicit (possibly empty) assignment. A row not found
// leaves the JWT claim authoritative.
// spec: §10.2 line 294. F-10.2.3.
type userstorePlatformRoles struct {
	store userstore.Store
}

func (r userstorePlatformRoles) ResolveRoles(ctx context.Context, tenantID, subject string) ([]auth.Role, bool, error) {
	if r.store == nil {
		return nil, false, nil
	}
	row, err := r.store.Get(ctx, tenantID, subject)
	if errors.Is(err, userstore.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return append([]auth.Role(nil), row.Roles...), true, nil
}

// bearerTenantRegistry is the §10.2 line 219 multi-tenant bearer-chain
// adapter. It consults the wired tenantstore so a Bearer JWT whose
// `tenant_id` claim names a tenant that is not provisioned (or is
// soft-deleted) is rejected with TENANT_NOT_FOUND. The built-in
// `default` tenant is admitted unconditionally so the Embedded-Mode
// quickstart (which seeds the default row via the bootstrap Job) works
// even before the row is persisted; once the row exists, the active
// flag (IsActive) governs.
// spec: §10.2 lines 219-221. F-10.2.1.
type bearerTenantRegistry struct {
	store tenantstore.Store
}

func (r bearerTenantRegistry) IsRegistered(tenantID string) (bool, error) {
	if tenantID == auth.DefaultTenantID {
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
		_ = e.emitter.Emit(ctx, events.OperationalEvent{
			Source:          "/v1/sessions",
			Type:            events.EventExperimentIsolationMismatch.CloudEventsType(),
			Severity:        "warning",
			DataContentType: "application/json",
			Data:            data,
		})
	}
}

// ObserveTargetingDuration records the §16.1 line 156
// lenny_experiment_targeting_duration_seconds histogram.
func (e experimentRejectionReporter) ObserveTargetingDuration(_ context.Context, provider string, seconds float64) {
	if e.metrics != nil {
		e.metrics.ObserveExperimentTargetingDuration(provider, seconds)
	}
}

// RecordTargetingError increments the §16.1 line 157
// lenny_experiment_targeting_error_total counter.
func (e experimentRejectionReporter) RecordTargetingError(_ context.Context, provider, errorType string) {
	if e.metrics != nil {
		e.metrics.RecordExperimentTargetingError(provider, errorType)
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
	if ev.Outcome != "" {
		// spec: §13.4; §11.7 — the §16.6 session.upload boundary records
		// accepted/rejected so the SIEM stream carries the upload-rejection
		// class; the rejected row pairs the outcome with a sub-code reason.
		// F-13.4.8.
		payload["outcome"] = ev.Outcome
	}
	if ev.Reason != "" {
		payload["reason"] = ev.Reason
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

// interactionResolutionAuditor adapts the gateway audit appender to
// the sessionserver.InteractionAuditSink interface. It writes the
// §7.2 / §11.7 / §16.7 tool-use approve/deny and elicitation
// respond/dismiss events to the hash-chained audit log under the
// session's own tenant partition. The tenant is taken from the
// session-derived event, satisfying the §11.7 line 428 write-time
// tenant-validation rule. The OCSF mapping maps these event types to
// API Activity (6003). spec: §7.2 lines 124-127. F-7.2.8.
type interactionResolutionAuditor struct {
	appender policy.AuditAppender
}

func (a interactionResolutionAuditor) EmitInteractionResolution(ctx context.Context, ev sessionserver.InteractionResolutionEvent) {
	if a.appender == nil {
		return
	}
	payload := map[string]any{
		"session_id":     ev.SessionID,
		"user_sub":       ev.UserID,
		"interaction_id": ev.InteractionID,
		"phase":          ev.Phase,
	}
	if ev.Reason != "" {
		// §15.1 deny body — the optional dismissal reason recorded so
		// the post-incident reconstruction can show why a tool call was
		// denied.
		payload["reason"] = ev.Reason
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

// treeCycleEmitter increments the
// `lenny_delegation_tree_cycle_detected_total` counter when a §8.9
// tree walker hits a cycle in the §8.2 ParentSessionID lineage. Each
// tree-walker surface (REST `/v1/sessions/{id}/tree`, MCP
// `lenny/get_task_tree`) wraps this emitter in a per-package adapter
// that matches the package's TreeCycleObserver interface; both
// adapters fan into the same metric so the corruption surfaces
// regardless of which transport walked the tree. The audit-row half
// of the §8.9 finding (a `delegation.tree_cycle_detected` row) is
// not yet emitted: §16.7 is a closed catalog of spec-listed events
// and the new event type requires a spec change. spec: §8.9 line
// 1003; F-8.9.10 (metric half closed, audit half deferred to a
// future spec addition).
type treeCycleEmitter struct {
	metrics *gatewaymetrics.Metrics
}

func (e treeCycleEmitter) emit(_ context.Context, tenantID, _, _, source string) {
	if e.metrics != nil {
		e.metrics.IncDelegationTreeCycleDetected(tenantID, source)
	}
	// The cycle-detected metric is the operator-visible signal in v1.
	// A `delegation.tree_cycle_detected` audit row is the cleaner long-
	// run answer but lands with the §16.7 catalog extension.
}

// sessionserverTreeCycleObserver adapts treeCycleEmitter to
// sessionserver.TreeCycleObserver for the REST /v1/sessions/{id}/tree
// walker. spec: §8.9 line 1003; F-8.9.10.
type sessionserverTreeCycleObserver struct {
	emitter treeCycleEmitter
}

func (o sessionserverTreeCycleObserver) OnTreeCycle(ctx context.Context, ev sessionserver.TreeCycleEvent) {
	o.emitter.emit(ctx, ev.TenantID, ev.RootSessionID, ev.CycleNodeID, ev.Source)
}

// mcpToolsTreeCycleObserver adapts treeCycleEmitter to
// mcptools.TreeCycleObserver for the lenny/get_task_tree platform-
// tool walker. spec: §8.9 line 1003; F-8.9.10.
type mcpToolsTreeCycleObserver struct {
	emitter treeCycleEmitter
}

func (o mcpToolsTreeCycleObserver) OnTreeCycle(ctx context.Context, ev mcptools.TreeCycleEvent) {
	o.emitter.emit(ctx, ev.TenantID, ev.RootSessionID, ev.CycleNodeID, ev.Source)
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

// auditPruneTenants enumerates the audit chains the §16.4 retention
// sweep covers: every registered tenant plus the "platform"
// pseudo-tenant, which carries platform-admin audit rows (e.g.
// compliance.profile_decommissioned) that are not keyed to a registered
// tenant row but still age past the retention window. F-11.7.17.
type auditPruneTenants struct {
	store tenantstore.Store
}

func (a auditPruneTenants) ListTenants(ctx context.Context) ([]string, error) {
	rows, err := a.store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows)+1)
	out = append(out, "platform")
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out, nil
}

// t4TenantSource adapts a tenantstore.Store into a
// tenantkms.TenantSource so the §12.5 line 307 continuous probe
// enumerates exactly the active tenants at workspaceTier T4 — the only
// tenants holding a tenant-scoped KMS key. Soft-deleted tenants are
// dropped (their key is destroyed in §12.8 Phase 4a, so probing it is
// pointless and would flatline the gauge for a tenant that is gone).
type t4TenantSource struct {
	store tenantstore.Store
}

func (t t4TenantSource) T4Tenants(ctx context.Context) ([]string, error) {
	rows, err := t.store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.IsActive() && row.WorkspaceTier == tenantkms.WorkspaceTierT4 {
			out = append(out, row.ID)
		}
	}
	return out, nil
}

// activeComplianceProfiles returns the distinct, non-empty
// complianceProfile values across the registered tenants. It backs the
// §11.2.1 billing.retentionDays compliance-floor preflight: a profile
// active on any tenant raises the deployment's retention floor.
// spec: §11.2.1 line 151. F-11.2.15.
func activeComplianceProfiles(ctx context.Context, store tenantstore.Store) ([]string, error) {
	rows, err := store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var profiles []string
	for _, row := range rows {
		if row.ComplianceProfile == "" || seen[row.ComplianceProfile] {
			continue
		}
		seen[row.ComplianceProfile] = true
		profiles = append(profiles, row.ComplianceProfile)
	}
	return profiles, nil
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

// playgroundAuditEmitter bridges the playground's §27.3.1 / §10.2
// audit events into the durable §11.7 audit chain. spec: §27.3.1 step
// 6 (line 156) — playground.bearer_minted and playground.bearer_revoked
// "share the taxonomy and redaction rules of other auth events in
// §11.7", so they are committed to the principal's per-tenant hash
// chain, not just logged. It keeps the lightweight log line so a mint
// and revoke remain observable in the gateway log stream, and falls
// back to log-only when no durable sink is wired. F-27.3.5.
type playgroundAuditEmitter struct {
	sink admin.AuditSink
}

func (e playgroundAuditEmitter) EmitPlaygroundEvent(ctx context.Context, ev playground.AuditEvent) {
	log.Printf("lenny-gateway: §27 audit %s tenant=%s user=%s jti=%s", ev.Type, ev.TenantID, ev.UserID, ev.BearerJTI)
	if e.sink == nil {
		return
	}
	detail := map[string]any{
		"session_cookie_id": ev.SessionCookieID,
		"bearer_jti":        ev.BearerJTI,
		"origin":            ev.Origin,
	}
	if ev.BearerTTLSeconds > 0 {
		detail["bearer_ttl_seconds"] = ev.BearerTTLSeconds
	}
	for k, v := range ev.Labels {
		detail["label_"+k] = v
	}
	// The event lands on the principal's tenant chain (§11.7 is
	// tenant-scoped); ActorSubject is the playground user.
	e.sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:           ev.Type,
		ActorSubject:   ev.UserID,
		ActorTenantID:  ev.TenantID,
		TargetResource: ev.SessionCookieID,
		Detail:         detail,
		At:             ev.At,
	})
}

// EmitMintRejected routes the §10.2 line 243
// playground.bearer_mint_rejected event to the durable §11.7 sink and
// logs it alongside the metric increment. A rejection that fires before
// tenant extraction carries an empty tenant; the sink commits it to the
// platform chain. F-27.3.5.
func (e playgroundAuditEmitter) EmitMintRejected(ctx context.Context, ev playground.MintRejectedEvent) {
	log.Printf("lenny-gateway: §10.2 audit playground.bearer_mint_rejected tenant=%s subject_jti=%s subject_typ=%s invariant=%s ingress=%s",
		ev.TenantID, ev.SubjectJTI, ev.SubjectTyp, ev.InvariantViolated, ev.IngressPath)
	if e.sink == nil {
		return
	}
	e.sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:          "playground.bearer_mint_rejected",
		ActorTenantID: ev.TenantID,
		Detail: map[string]any{
			"subject_jti":        ev.SubjectJTI,
			"subject_typ":        ev.SubjectTyp,
			"invariant_violated": ev.InvariantViolated,
			"ingress_path":       ev.IngressPath,
		},
		At: ev.At,
	})
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

// parseKeyValueCSV splits a comma-separated key=value flag value into
// a map. Trimmed empty entries and entries without `=` are skipped.
// An empty input yields a nil map. The §27.2 line 41
// playground.sessionLabels flag uses this encoding so a Helm value
// like `{origin: playground, env: stage}` renders to
// `--playground-session-labels=origin=playground,env=stage`.
func parseKeyValueCSV(raw string) map[string]string {
	var out map[string]string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[k] = v
	}
	return out
}

// jwksAdvertisesAsymmetric reports whether doc contains at least one
// asymmetric (`kty: RSA` or `kty: EC`) entry. The HMAC-only case
// produces only `kty: oct` entries with no `k` field, so the document
// advertises kid/alg metadata that a verifier cannot use to validate a
// signature. F-10.2.14 keys the §10.3 publication notice on this check
// so an operator who opts into --jwks-publish on top of the v1 HMAC
// signer is told that the JWKS document is metadata-only.
// spec: §10.2 line 195. F-10.2.14.
func jwksAdvertisesAsymmetric(doc jwt.JWKSet) bool {
	for _, k := range doc.Keys {
		if k.Kty != "" && k.Kty != "oct" {
			return true
		}
	}
	return false
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

// exportElicitationIntegrityWeakened refreshes the §16.5 line 460
// ElicitationContentIntegrityWeakened standing-alert gauge: the count
// of active tenants whose §9.2 effective elicitation content-integrity
// mode (max(platformFloor, tenantStored)) is weaker than enforce. The
// gauge keeps the standing warning alert firing while any tenant runs a
// reduced-integrity posture and resolves it to zero once every active
// tenant resolves to enforce. List with the zero filter already drops
// soft-deleted rows, so the count reflects active tenants only. Errors
// are logged but never bubble — the exporter is a best-effort signal
// and must not interrupt the gauge-refresh loop.
//
// spec: §16.5 line 460 (standing-alert numerator)
// spec: §9.2 lines 60, 64 (effective-mode resolution + defaults)
func exportElicitationIntegrityWeakened(ctx context.Context, tenants tenantstore.Store, floor string, m *gatewaymetrics.Metrics) {
	rows, err := tenants.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("lenny-gateway: elicitation-integrity weakened gauge: listing tenants failed: %v", err)
		}
		return
	}
	var weakened int
	for _, t := range rows {
		eff := elicitation.ResolveEffectiveWithDefaults(floor, t.ElicitationContentIntegrity)
		if !eff.AtLeast(elicitation.ModeEnforce) {
			weakened++
		}
	}
	m.SetElicitationIntegrityWeakened(weakened)
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
		// spec: §10.4 line 389 / §16 catalog — sample the worst
		// per-session SSE replay buffer utilization so the
		// lenny_event_bus_replay_buffer_utilization gauge tracks the
		// pressure on the §10.4 reconnect-window assumption. F-10.4.11.
		m.SetReplayBufferUtilization(bus.MaxReplayBufferUtilization())
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
		p == "/openapi.json", p == "/v1/openapi.json":
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

// envDuration mirrors envInt for time.Duration-valued flags. Accepts
// any value time.ParseDuration parses ("60s", "5m", "1h"); returns def
// on missing or unparseable env vars.
func envDuration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// envBool returns the env var parsed as a bool, or def when the var is
// unset or does not parse. Accepts the strconv.ParseBool truth values
// ("1", "true", "TRUE", "0", "false", ...).
func envBool(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// auditBatchingNoSIEM reports the §12.3 line 99 AuditBatchingNoSIEM
// condition: production mode has T2 audit batching enabled but no SIEM
// endpoint, so buffered T2 audit events would be lost on a crash with
// no external durable copy to recover from. F-12.3.15.
func auditBatchingNoSIEM(env string, batchingEnabled, siemConfigured bool) bool {
	return env == "production" && batchingEnabled && !siemConfigured
}

// envInt64 mirrors envInt for int64-valued flags (idempotency body
// cap, size limits). Returns def on missing or unparseable env vars.
func envInt64(name string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
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

// parseWindowOverrides parses the §25.3 recommendations window-override
// flag (comma-separated category=duration pairs, e.g.
// "warm_pool_sizing=12h,credential_pool_sizing=72h") into the map the
// recommendations.Config expects. Malformed pairs and unparseable
// durations are skipped so one bad entry does not drop the rest.
// spec: §25.3 line 596. F-25.3.12.
func parseWindowOverrides(raw string) map[string]time.Duration {
	out := map[string]time.Duration{}
	for _, pair := range splitAndTrim(raw) {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil || d <= 0 {
			continue
		}
		out[strings.TrimSpace(k)] = d
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

// defaultDeriveLock picks the §7.1 line 92 derive-lock implementation.
// Redis-backed serialization is mandatory across replicas; the in-
// process Memory fallback is correct for the minimal-gateway and
// single-replica deployments (the in-memory store mutex inside
// derive.go is the only other serialization path in v1, and it
// serializes by accident — not by spec). F-7.1.12.
func defaultDeriveLock(client redis.UniversalClient) derivelock.Lock {
	if client != nil {
		return derivelock.NewRedis(client)
	}
	return derivelock.NewMemory(derivelock.DefaultWait)
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
		// spec: §10.3 line 338 — present the gateway leaf via a
		// filesystem-watching GetClientCertificate callback so a
		// cert-manager renewal is picked up on the next dial without a
		// gateway restart.
		reloader, err := certreload.New(certPath, keyPath)
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
			GetClientCertificate: reloader.GetClientCertificate,
			RootCAs:              pool,
			MinVersion:           tls.VersionTLS13,
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
		// spec: §10.3 line 338 — present the gateway leaf via a
		// filesystem-watching GetClientCertificate callback so a
		// cert-manager renewal is picked up on the next dial without a
		// gateway restart.
		reloader, err := certreload.New(certPath, keyPath)
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
			GetClientCertificate: reloader.GetClientCertificate,
			RootCAs:              pool,
			MinVersion:           tls.VersionTLS13,
		}))
	}
	return grpc.NewClient(addr, transport)
}
