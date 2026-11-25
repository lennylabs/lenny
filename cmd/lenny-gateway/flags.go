// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/billing/billingretention"
	"github.com/lennylabs/lenny/pkg/billing/billingstore/failover"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/delegation/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/delegation/recovery"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/api/admintoken"
	deprecationmw "github.com/lennylabs/lenny/pkg/gateway/api/deprecation"
	ratelimitmw "github.com/lennylabs/lenny/pkg/gateway/api/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/api/serviceidentity"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorsecret"
	"github.com/lennylabs/lenny/pkg/gateway/core/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/core/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/core/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/core/coordination/dualstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/leaderjobs"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/budgetstate"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/slotcounter"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/slotreaper"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/drain/prestop"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/metering"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/delegationbudget"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/elicitationfloor"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/playground"
	"github.com/lennylabs/lenny/pkg/gateway/replica/gatewayleader"
	"github.com/lennylabs/lenny/pkg/gateway/replica/pdbwatcher"
	"github.com/lennylabs/lenny/pkg/gateway/replica/tlsprobe"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/tokenanomaly"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/uploadhandler"
	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
	"github.com/lennylabs/lenny/pkg/policy"
	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/storage/failopen"
	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
	"github.com/lennylabs/lenny/pkg/storage/retentiongc"
)

// gatewayFlags holds every command-line flag and environment-derived
// startup input the gateway binary parses. parseFlags populates it from
// flag.CommandLine; the per-subsystem build steps (buildStores,
// buildLLMProxy, startBackgroundWorkers, runServers) read it off the
// gatewayWiring accumulator to wire each subsystem. Carrying the parsed
// inputs on one value lets runGateway stay a flag-parse-then-build call
// sequence while each build step re-aliases the flags it needs.
//
// spec: §4.1 — the gateway is one component internally partitioned into
// subsystem boundaries (Go interfaces within a single binary); the flag
// surface is the composition-root input that wiring threads to each
// subsystem builder.
type gatewayFlags struct {
	addr                                          *string
	multiTenant                                   *bool
	tenancyMode                                   *string
	tenantIDClaim                                 *string
	oidcIssuerURL                                 *string
	oidcClientID                                  *string
	oidcJWKSRefreshInterval                       *time.Duration
	oidcJWKSMinRefetchInterval                    *time.Duration
	oidcFetchTimeout                              *time.Duration
	devMode                                       *bool
	tlsTerminatedUpstream                         *bool
	sloValidated                                  *bool
	bearerTrustHMACKeyFile                        *string
	bearerExpectedIssuer                          *string
	bearerExpectedAudiences                       *string
	runtimeBin                                    *string
	agentRuntime                                  *string
	postgresDSN                                   *string
	sqlitePath                                    *string
	billingAuditDSN                               *string
	billingAuditDDLDSN                            *string
	primaryDDLDSN                                 *string
	readDSN                                       *string
	scatterMaxConcurrency                         *int
	scatterPerShardTimeoutSeconds                 *int
	scatterAggregateTimeoutSeconds                *int
	redisURL                                      *string
	redisSentinelAddrs                            *string
	redisSentinelMaster                           *string
	redisPassword                                 *string
	redisSentinelPassword                         *string
	redisTLS                                      *bool
	redisAllowInsecure                            *bool
	redisClusterAddrs                             *string
	capacityTier                                  *string
	singleTenantRedisTopology                     *string
	redisCoordinationURL                          *string
	redisQuotaURL                                 *string
	redisCachePubSubURL                           *string
	redisSessionDataURL                           *string
	redisDelegationURL                            *string
	shutdownTimeout                               *time.Duration
	evalAggregationRefreshSeconds                 *int
	quotaUserFailOpenFraction                     *float64
	quotaPerReplicaHardCap                        *int64
	quotaFailOpenCumulativeMaxSeconds             *int
	failOpenStatePath                             *string
	auditLockAcquireTimeoutMs                     *int
	tokenRevocationTimeoutMs                      *int
	auditLockMaxRetries                           *int
	auditLockRetryBaseMs                          *int
	globalTokenQuota                              *int64
	userTokenQuota                                *int64
	quotaRollingWindowSeconds                     *int
	quotaSyncIntervalSeconds                      *int
	billingTokenCheckpointIntervalSeconds         *int
	quotaEnforcementMode                          *string
	delegationNodeMemoryFootprintBytes            *int64
	externalInterceptorTLSCert                    *string
	externalInterceptorTLSKey                     *string
	externalInterceptorCA                         *string
	guardrailsClassifier                          *string
	interceptorFailOpenMax                        *int
	delegationMaxInputSize                        *int
	delegationDefaultMaxDepth                     *int
	delegationMaxActiveChildrenPerUser            *int
	gatewayAllowSelfRecursion                     *bool
	interceptorWeakeningCooldownSeconds           *int
	healthTrackerUseCompiledRules                 *bool
	opsNonceCheckpointPath                        *string
	alertingBundleFormats                         *string
	alertingOverrideCount                         *int
	gatewayQueueDepthThreshold                    *float64
	gatewayLatencyThresholdSeconds                *float64
	credentialPoolLowThreshold                    *float64
	sloBurnRateFastMultiplier                     *float64
	sloBurnRateSlowMultiplier                     *float64
	billingFlushIntervalMs                        *int
	billingFlushBatchSize                         *int
	billingFlushMaxPending                        *int
	billingRedisStreamMaxLen                      *int64
	postgresWriteCeilingIops                      *float64
	postgresWriteIopsSampleSeconds                *int
	auditStartupChainCheckEntries                 *int
	auditGrantCheckIntervalSeconds                *int
	auditHardFailOnDrift                          *bool
	auditScatterCacheEnabled                      *bool
	auditSIEMEndpoint                             *string
	auditSIEMSecret                               *string
	auditPgauditEnabled                           *bool
	auditPgauditSinkEndpoint                      *string
	auditSIEMFailureThresholdPercent              *float64
	auditSIEMMaxDeliveryLagSeconds                *int
	auditSIEMPollIntervalSeconds                  *int
	auditOCSFRetryIntervalSeconds                 *int
	auditOCSFMaxAttempts                          *int
	auditOCSFBatchSize                            *int
	auditSyncWritePoolSize                        *int
	auditBatchingEnabled                          *bool
	auditFlushIntervalMs                          *int
	auditFlushBatchSize                           *int
	retryMaxRetries                               *int
	callbackURLAllowedDomains                     *string
	maxResumePendingSeconds                       *int
	agentNamespace                                *string
	clusterQPS                                    *float64
	clusterBurst                                  *int
	standardRuntimeClass                          *string
	sandboxedRuntimeClass                         *string
	microvmRuntimeClass                           *string
	messagingDefaultScope                         *string
	messagingMaxScope                             *string
	messagingMaxPerMinute                         *int
	messagingMaxPerSession                        *int
	messagingMaxInboundPerMinute                  *int
	toolApprovalTimeout                           *time.Duration
	streamFailureReportTimeout                    *time.Duration
	sessionPrimaryReadTimeout                     *time.Duration
	treeArchiveCacheEntries                       *int
	adapterTLSCert                                *string
	adapterTLSKey                                 *string
	adapterCA                                     *string
	holderForwardAdvertiseAddr                    *string
	holderForwardListenAddr                       *string
	tokenServiceAddr                              *string
	credentialsNamespace                          *string
	tokenServiceHTTPURL                           *string
	tokenServiceCert                              *string
	tokenServiceKey                               *string
	tokenServiceCA                                *string
	tokenServiceTenant                            *string
	elicitationFloor                              *string
	elicitationFloorConfigMap                     *string
	elicitationFloorReconcileSeconds              *int
	grpcAddr                                      *string
	leaseAutoMaxPerMin                            *int
	leaseDefaultBudget                            *int64
	leaseMaxBudget                                *int64
	leaseDefaultApproval                          *string
	leaseCoolOffSec                               *int
	leaseRejectionCoolOffSec                      *int
	tokenAnomalyZeroWindow                        *int
	tokenAnomalyImplausibleRatio                  *float64
	directUsagePollIntervalSeconds                *int
	spiffeTrustDomain                             *string
	interceptorNamespaces                         *string
	saTokenAudience                               *string
	adminSATokenAudience                          *string
	llmProxyPublicURL                             *string
	llmSemanticCache                              *bool
	objectStorageProvider                         *string
	objectStorageBucket                           *string
	objectStorageRegion                           *string
	objectStorageAccountURL                       *string
	objectStorageFilesystemRoot                   *string
	minioEndpoint                                 *string
	minioAccessKey                                *string
	minioSecretKey                                *string
	minioBucket                                   *string
	minioUseSSL                                   *bool
	artifactReplicationConfig                     *string
	artifactReplicationRoleARN                    *string
	gcCycleIntervalSeconds                        *int
	gcTombstoneRetentionSeconds                   *int
	maxDeadlockWaitSeconds                        *int
	maxFinalizingTimeoutSeconds                   *int
	delegationBudgetKeyTTLSeconds                 *int
	adapterKeepaliveTimeMs                        *int
	adapterKeepaliveTimeoutMs                     *int
	delegationUsageQuiescenceTimeoutSeconds       *int
	delegationMaxLevelRecoverySeconds             *int
	delegationMaxTreeRecoverySeconds              *int
	delegationCascadeTimeoutSeconds               *int
	delegationMaxOrphanTasksPerTenant             *int
	credentialsExpiryWarningLeadSeconds           *int
	workspaceSealMaxDurationSeconds               *int
	idempotencyGCIntervalSeconds                  *int
	issuedTokenGCIntervalSeconds                  *int
	credentialLeaseGCIntervalSeconds              *int
	objectStorageGCSBucketDefaultCMEK             *string
	objectStorageAzureDefaultEncryptionScope      *string
	objectStorageAzureDenyEncryptionScopeOverride *bool
	noEnvPolicy                                   *string
	connectorOAuthClientSecretKey                 *string
	billingCorrectionRateThreshold                *float64
	billingSinkWebhookURL                         *string
	billingRetentionDays                          *int
	gdprRetentionDays                             *int
	auditRetentionPreset                          *string
	auditRetentionDays                            *int
	auditRetentionPruneIntervalSeconds            *int
	eventBusRetryIntervalSeconds                  *int
	eventBusMaxRetryAttempts                      *int
	eventBusDuplicateInjectionFactor              *int
	eventBusDropAlertThreshold                    *int
	capabilityTokenTTL                            *int
	maxSessionsPerReplica                         *int
	minReplicas                                   *int
	streamCeiling                                 *int
	kmsSignQuotaPerSecond                         *int
	gatewayNamespace                              *string
	gatewayServiceName                            *string
	memoryMaxPerUser                              *int
	memoryEnabled                                 *bool
	memoryRecordCountInterval                     *time.Duration
	poolerMode                                    *string
	tenantDeletionInterval                        *time.Duration
	lifecycleTickBudget                           *time.Duration
	erasureMaxConcurrentJobs                      *int

	// externalInterceptors collects each repeatable --external-interceptor
	// value (§4.8 external RequestInterceptor registration). It is appended
	// to by the flag.Func callback rather than produced by a single flag.
	externalInterceptors []string

	// billingSinkWebhookSecret and billingApproverWebhookSecret are the
	// §11.2.1 HMAC signing secrets, read from the environment only (a Helm
	// secretKeyRef) so they never appear in the process argv.
	billingSinkWebhookSecret     []byte
	billingApproverWebhookSecret []byte

	// kmsOpts and kmsFinalize are the §4 / §17.5 KMS provider selector
	// bound by providerflags.Bind. kmsFinalize is invoked after flag.Parse
	// to resolve the selected provider options.
	kmsOpts     *providerflags.Options
	kmsFinalize func() error
}

// partFlags holds the flags the gateway's parts register in their own
// packages: the flags only that part reads (decision 7 of change 0117). It
// stays apart from gatewayFlags, the composition root's own flag set, which
// many build steps and the closures they hand to parts read, so that none
// of them holds a part's flag values.
type partFlags struct {
	sessionEvents *sessionevents.Flags
	playground    *playground.Flags
	tlsProbe      *tlsprobe.Flags
	pdbWatcher    *pdbwatcher.Flags
	leader        *gatewayleader.Flags
	llmProxy      *llmproxy.Flags
	metering      *metering.Flags
	uploads       *uploadhandler.Flags
	// The API front's flags, read only by the build steps that wire the
	// admin router, the admin token, and the request pipeline.
	admin           *admin.Flags
	adminToken      *admintoken.Flags
	rateLimit       *ratelimitmw.Flags
	deprecation     *deprecationmw.Flags
	serviceIdentity *serviceidentity.Flags
	idempotency     *idempotencymw.Flags
	// The preStop hook's flags, read only by the build step that wires the
	// hook.
	prestop *prestop.Flags
	// The session core's flags, which several build steps wire.
	core *sessionCoreFlags
}

// sessionCoreFlags holds the flags the session core's packages register:
// the flags only the session core reads. The build steps that wire the core
// take the whole set, because most of them wire more than one of its
// packages.
type sessionCoreFlags struct {
	coordination  *coordination.Flags
	dualStore     *dualstore.Flags
	recycle       *recycle.Flags
	slotCounter   *slotcounter.Flags
	slotReaper    *slotreaper.Flags
	budgetState   *budgetstate.Flags
	checkpointer  *checkpointer.Flags
	resumeChunks  *resumechunks.Flags
	watchdog      *watchdog.Flags
	sessionServer *sessionserver.Flags
	sessionInbox  *sessioninbox.Flags
	leaderJobs    *leaderjobs.Flags
}

// registerSessionCoreFlags registers the session core's flags on fs.
func registerSessionCoreFlags(fs *flag.FlagSet) *sessionCoreFlags {
	return &sessionCoreFlags{
		coordination:  coordination.RegisterFlags(fs),
		dualStore:     dualstore.RegisterFlags(fs),
		recycle:       recycle.RegisterFlags(fs),
		slotCounter:   slotcounter.RegisterFlags(fs),
		slotReaper:    slotreaper.RegisterFlags(fs),
		budgetState:   budgetstate.RegisterFlags(fs),
		checkpointer:  checkpointer.RegisterFlags(fs),
		resumeChunks:  resumechunks.RegisterFlags(fs),
		watchdog:      watchdog.RegisterFlags(fs),
		sessionServer: sessionserver.RegisterFlags(fs),
		sessionInbox:  sessioninbox.RegisterFlags(fs),
		leaderJobs:    leaderjobs.RegisterFlags(fs),
	}
}

// registerPartFlags registers the parts' flags on the process-wide flag set.
// It runs before parseFlags parses the set.
func registerPartFlags() *partFlags {
	return &partFlags{
		sessionEvents: sessionevents.RegisterFlags(flag.CommandLine),
		playground:    playground.RegisterFlags(flag.CommandLine),
		tlsProbe:      tlsprobe.RegisterFlags(flag.CommandLine),
		pdbWatcher:    pdbwatcher.RegisterFlags(flag.CommandLine),
		leader:        gatewayleader.RegisterFlags(flag.CommandLine),
		llmProxy:      llmproxy.RegisterFlags(flag.CommandLine),
		metering:      metering.RegisterFlags(flag.CommandLine),
		uploads:       uploadhandler.RegisterFlags(flag.CommandLine),

		admin:           admin.RegisterFlags(flag.CommandLine),
		adminToken:      admintoken.RegisterFlags(flag.CommandLine),
		rateLimit:       ratelimitmw.RegisterFlags(flag.CommandLine),
		deprecation:     deprecationmw.RegisterFlags(flag.CommandLine),
		serviceIdentity: serviceidentity.RegisterFlags(flag.CommandLine),
		idempotency:     idempotencymw.RegisterFlags(flag.CommandLine),

		prestop: prestop.RegisterFlags(flag.CommandLine),
		core:    registerSessionCoreFlags(flag.CommandLine),
	}
}

// parseFlags defines the composition root's own flags on flag.CommandLine,
// parses the command line, and returns the populated gatewayFlags. The
// parts' flags, which registerPartFlags registers first, are parsed in the
// same pass. It performs no wiring; the caller invokes kmsFinalize and then
// the build helpers. The flag definitions are grouped into per-domain
// register helpers so each stays a navigable block rather than one flat
// list.
//
// spec: §4.1 gateway subsystem seams — the composition root parses its
// inputs once and threads them to each subsystem builder.
func parseFlags() *gatewayFlags {
	f := &gatewayFlags{}
	f.registerCoreFlags()
	f.registerStorageFlags()
	f.registerPolicyFlags()
	f.registerSessionFlags()
	f.registerArtifactFlags()
	f.registerOpsFlags()
	flag.Parse()
	return f
}

// resolvedCredentialsNamespace returns the §4.9 credentials namespace the
// --credentials-namespace flag selects. envOr defaults only an empty
// environment variable, so an explicit empty flag value still reaches
// here; credential.ResolveNamespace maps it to credential.DefaultNamespace.
// The composition root passes this one value to both the admin Router and
// the Token Service Secret prober.
//
// spec: §4.9 (Credential Storage Topology), §17.6 (Packaging and Installation).
func (f *gatewayFlags) resolvedCredentialsNamespace() string {
	return credential.ResolveNamespace(*f.credentialsNamespace)
}

// registerCoreFlags registers the core listener, auth, OIDC, dev-mode, Postgres, Redis, and scatter-gather flags.
//
// spec: §4.1 gateway subsystem seams.
func (f *gatewayFlags) registerCoreFlags() {
	f.addr = flag.String("addr", ":8080", "address to bind (host:port)")
	// spec: §10.2 — the auth.multiTenant Helm value. The gateway refuses
	// to start when it disagrees with --tenancy-mode (see
	// validateTenancyConfig).
	f.multiTenant = flag.Bool("multi-tenant", envFlag("LENNY_MULTI_TENANT"),
		"§10.2 auth.multiTenant: when true the gateway reads the tenant from the Bearer tenant claim and refuses an unregistered tenant with 403 TENANT_NOT_FOUND; when false it ignores the claim and serves every request as the built-in `default` tenant. Must be true exactly when --tenancy-mode=multi. Mirrors the auth.multiTenant Helm value. Override via LENNY_MULTI_TENANT.")
	// spec: §4.9 — the platform tenancy.mode signal the gateway enforces
	// the two cross-tenant credential-delivery rejections against. It
	// mirrors the lenny-webhook binary's --tenancy-mode flag (wired from
	// the same .Values.tenancy.mode) rather than overloading the
	// semantically inverted §10.2 --multi-tenant auth flag. This one flag
	// is the source both the admin Router and the sessionserver read the
	// tenancy mode from. Defaults to "single".
	f.tenancyMode = flag.String("tenancy-mode", envOr("LENNY_TENANCY_MODE", "single"),
		"§4.9 platform tenancy.mode (\"multi\" or \"single\"). The gateway enforces the §4.9 direct/standard and proxy/spiffe cross-tenant credential-delivery rejections only in multi-tenant mode. Mirrors the tenancy.mode Helm value. Override via LENNY_TENANCY_MODE.")
	f.tenantIDClaim = flag.String("tenant-id-claim", envOr("LENNY_TENANT_ID_CLAIM", "tenant_id"),
		"§10.2 OIDC claim name the gateway reads the tenant identifier from. Defaults to `tenant_id` (matches the canonical Lenny claim shape); set to e.g. `https://acme.example/tenant` when the upstream IdP stamps tenant identity under a different claim. Mirrors the `auth.tenantIdClaim` Helm value. F-10.2.9.")
	f.oidcIssuerURL = flag.String("oidc-issuer-url", os.Getenv("LENNY_OIDC_ISSUER_URL"),
		"§10.3 auth.oidc.issuerUrl: the OIDC issuer the gateway's token validation trusts. A §10.3 required platform key — outside --dev-mode an empty or non-absolute-URL value is a fatal startup misconfiguration (LENNY_CONFIG_MISSING config_key=auth.oidc.issuerUrl). Override via LENNY_OIDC_ISSUER_URL. F-10.3.14.")
	f.oidcClientID = flag.String("oidc-client-id", os.Getenv("LENNY_OIDC_CLIENT_ID"),
		"§10.3 auth.oidc.clientId: the OIDC client registration whose audience the gateway checks. A §10.3 required platform key — outside --dev-mode an empty value is a fatal startup misconfiguration (LENNY_CONFIG_MISSING config_key=auth.oidc.clientId). Override via LENNY_OIDC_CLIENT_ID. F-10.3.14.")
	f.devMode = flag.Bool("dev-mode", envFlag("LENNY_DEV_MODE"),
		"enable dev-mode auth shortcuts (X-Lenny-Roles dev-header). Override via LENNY_DEV_MODE.")
	f.tlsTerminatedUpstream = flag.Bool("tls-terminated-upstream", envFlag("LENNY_TLS_TERMINATED_UPSTREAM"),
		"§17.4 acknowledgment that an ingress or proxy terminates TLS in front of the gateway's plain-HTTP listener (the §17 production Deployment+Service+Ingress topology). Outside --dev-mode the gateway refuses to start without it. Override via LENNY_TLS_TERMINATED_UPSTREAM.")
	f.sloValidated = flag.Bool("slo-validated", envFlag("LENNY_SLO_VALIDATED"),
		"§16.5 — set true once the Phase 14.5 benchmark gate has validated the §16.5 SLO targets. When false (the default), the gateway logs the provisional-SLO startup warning so an operator running unvalidated defaults cannot silently treat them as SLA commitments. Mirrors the slo.validated Helm value. Override via LENNY_SLO_VALIDATED.")
	f.oidcJWKSRefreshInterval = flag.Duration("oidc-jwks-refresh-interval", envDuration("LENNY_OIDC_JWKS_REFRESH_INTERVAL", jwt.DefaultOIDCKeyRefreshInterval),
		"§10.2 how long the IdP signing keys fetched from the auth.oidc.issuerUrl discovery document's jwks_uri serve before the gateway fetches them again. Operator-tunable. Override via LENNY_OIDC_JWKS_REFRESH_INTERVAL.")
	f.oidcJWKSMinRefetchInterval = flag.Duration("oidc-jwks-min-refetch-interval", envDuration("LENNY_OIDC_JWKS_MIN_REFETCH_INTERVAL", jwt.DefaultOIDCMinRefetchInterval),
		"§10.2 minimum spacing between two fetches of the IdP signing keys. A bearer naming a kid the cached keys lack triggers at most one fetch per interval. Operator-tunable. Override via LENNY_OIDC_JWKS_MIN_REFETCH_INTERVAL.")
	f.oidcFetchTimeout = flag.Duration("oidc-fetch-timeout", envDuration("LENNY_OIDC_FETCH_TIMEOUT", jwt.DefaultOIDCFetchTimeout),
		"§10.2 timeout of one IdP discovery or signing-key request. Operator-tunable. Override via LENNY_OIDC_FETCH_TIMEOUT.")
	f.bearerTrustHMACKeyFile = flag.String("bearer-trust-hmac-key-file", os.Getenv("LENNY_BEARER_TRUST_HMAC_KEY_FILE"),
		"path to an additional HMAC-SHA256 signing key the §10.2 Bearer path trusts, on top of the §10.2 JWT key set. Unset in a production install; §17.4 Embedded Mode sets it so the gateway accepts the embedded OIDC provider's tokens. Override via LENNY_BEARER_TRUST_HMAC_KEY_FILE.")
	f.bearerExpectedIssuer = flag.String("bearer-expected-issuer", os.Getenv("LENNY_BEARER_EXPECTED_ISSUER"),
		"§10.2 expected iss claim on every Bearer JWT. When set, a token whose iss differs is rejected with TOKEN_INVALID (reason=issuer_mismatch). Empty (default) skips the check, matching the existing wiring. Override via LENNY_BEARER_EXPECTED_ISSUER.")
	f.bearerExpectedAudiences = flag.String("bearer-expected-audiences", os.Getenv("LENNY_BEARER_EXPECTED_AUDIENCES"),
		"§10.2 comma-separated set of acceptable aud claims on Bearer JWTs. A token whose aud intersects this set is admitted; a token whose aud is disjoint is rejected with TOKEN_INVALID (reason=audience_mismatch). Empty (default) skips the check. Override via LENNY_BEARER_EXPECTED_AUDIENCES.")
	f.runtimeBin = flag.String("runtime-bin", os.Getenv("LENNY_AGENT_BINARY"),
		"path to a Basic-level runtime binary. When set, the gateway dispatches messages to a child process speaking the §28.5.3 adapter protocol instead of the in-process echo executor. §17.4 Source-Mode override; defaults to LENNY_AGENT_BINARY.")
	f.agentRuntime = flag.String("agent-runtime", os.Getenv("LENNY_AGENT_RUNTIME"),
		"§17.4 zero-credential runtime selector. \"echo\" forces the built-in in-process echo runtime (overriding --runtime-bin); empty defaults to echo when no runtime binary is set. The only built-in name is \"echo\"; any other value is a fatal startup error. Override via LENNY_AGENT_RUNTIME.")
	f.postgresDSN = flag.String("postgres-dsn", os.Getenv("LENNY_POSTGRES_DSN"),
		"Postgres connection string. When set, sessions, transcripts, tenants, and runtimes are persisted to Postgres (the migrations/ schema must already be applied). When empty, in-memory stores are used.")
	// spec: §17.4 — Source Mode replaces Postgres with embedded
	// SQLite for session and metadata storage. When --postgres-dsn is
	// empty and this is set, the in-memory session/metadata stores are
	// snapshotted to the named SQLite file so their contents survive a
	// process restart. `make run` points it at ./lenny-data/lenny.db.
	// Ignored when --postgres-dsn is set (Postgres is authoritative).
	// F-17.4.2.
	f.sqlitePath = flag.String("sqlite-path", os.Getenv("LENNY_SQLITE_PATH"),
		"§17.4 Source-Mode embedded-SQLite path. When set (and --postgres-dsn is empty), session and metadata stores are backed by this SQLite file so they survive a restart. Empty keeps the stores purely in-memory. Override via LENNY_SQLITE_PATH. F-17.4.2.")
	// spec: §12.3 — optional dedicated Postgres instance for the
	// billing-event and audit-log write paths (the Tier-3 instance-
	// separation step at §12.3). When set, the §12.3 R-03
	// StoreRouter routes BillingShard/AuditShard/AllAuditShards to this
	// pool while every other write stays on the primary. Requires
	// --postgres-dsn; the schema (billing_events, audit_log) must already
	// be applied on the separate instance. F-12.3.5.
	f.billingAuditDSN = flag.String("postgres-billing-audit-dsn", os.Getenv("LENNY_PG_BILLING_AUDIT_DSN"),
		"§12.3 separate Postgres instance for billing/audit writes. When set (requires --postgres-dsn), billing-event and audit-log inserts route to this instance while all other writes stay on the primary. Empty keeps both paths on the primary. Override via LENNY_PG_BILLING_AUDIT_DSN.")
	// spec: §12.3 / §15.1 — CREATE-privileged DDL login DSN for the
	// billing/audit instance. The per-tenant billing (billing_seq_<40hex>)
	// and audit (audit_seq_<40hex>) sequences are provisioned at tenant
	// creation through this connection, which logs in as a dedicated
	// CREATE-privileged role (migration 0173) distinct from lenny_app, which
	// holds no CREATE ON SCHEMA. Points at the same instance the billing/audit
	// pool addresses (the separate LENNY_PG_BILLING_AUDIT_DSN instance when
	// configured, otherwise the primary). Without it a Helm-deployed gateway
	// cannot issue the CREATE SEQUENCE and every tenant's first billing or
	// audit Append fails on nextval of a nonexistent relation. F-11.2.10.
	f.billingAuditDDLDSN = flag.String("postgres-billing-audit-ddl-dsn", os.Getenv("LENNY_PG_BILLING_AUDIT_DDL_DSN"),
		"§12.3 / §15.1 CREATE-privileged DDL login DSN for the billing/audit instance, used to provision the per-tenant billing_seq_/audit_seq_ sequences at tenant creation. Logs in as the dedicated DDL role (migration 0173), not lenny_app. Override via LENNY_PG_BILLING_AUDIT_DDL_DSN.")
	// spec: §12.3 / §15.1 — CREATE-privileged DDL login DSN for the primary
	// instance. In the separate-instance topology (LENNY_PG_BILLING_AUDIT_DSN
	// set) the §13.3 issued-token write-before-issue path seals its real-tenant
	// audit_seq_<40hex> row on the primary rather than the billing/audit
	// instance, so the per-tenant audit sequence must be created there too
	// through a CREATE-privileged connection to the primary. When
	// LENNY_PG_BILLING_AUDIT_DSN is unset the primary and the billing/audit
	// instance are one and this falls back to LENNY_PG_BILLING_AUDIT_DDL_DSN.
	// F-11.2.10.
	f.primaryDDLDSN = flag.String("postgres-primary-ddl-dsn", os.Getenv("LENNY_PG_PRIMARY_DDL_DSN"),
		"§12.3 / §15.1 CREATE-privileged DDL login DSN for the primary instance, used to provision the per-tenant audit_seq_ sequence the §13.3 issued-token write-before-issue path seals on the primary. Falls back to LENNY_PG_BILLING_AUDIT_DDL_DSN when LENNY_PG_BILLING_AUDIT_DSN is unset (single-instance topology). Override via LENNY_PG_PRIMARY_DDL_DSN.")
	// spec: §12.3 — read-replica reader endpoint. When set, the
	// read-heavy query classes the spec names (session status, task tree,
	// audit reads, usage reports) route to this replica while every write
	// stays on the primary. Requires --postgres-dsn; the replica serves the
	// same migrations/ schema. Empty keeps reads on the primary. F-12.3.16 / F-17.9.13.
	f.readDSN = flag.String("postgres-read-dsn", os.Getenv("LENNY_PG_READ_DSN"),
		"§12.3 read-replica reader endpoint. When set (requires --postgres-dsn), read-heavy queries (session status, task tree, audit reads, usage reports) route to this replica while all writes stay on the primary. Empty keeps reads on the primary. Override via LENNY_PG_READ_DSN.")
	f.scatterMaxConcurrency = flag.Int("scatter-max-concurrency", envInt("LENNY_SCATTER_MAX_CONCURRENCY", 16),
		"§12.6 storeRouter.maxScatterGatherConcurrency: at most this many shards are queried in parallel by a scatter-gather fan-out (list-sessions, GDPR erasure, tenant deletion). v1 is single-shard so the bound is inert; it becomes load-bearing under a multi-shard router. Override via LENNY_SCATTER_MAX_CONCURRENCY. F-12.6.18.")
	f.scatterPerShardTimeoutSeconds = flag.Int("scatter-per-shard-timeout-seconds", envInt("LENNY_SCATTER_PER_SHARD_TIMEOUT_SECONDS", 10),
		"§12.6 storeRouter.scatterGatherPerShardTimeoutSeconds: per-shard query deadline. A timed-out shard is dropped (reads, partial result) or retried twice (writes). Override via LENNY_SCATTER_PER_SHARD_TIMEOUT_SECONDS. F-12.6.18.")
	f.scatterAggregateTimeoutSeconds = flag.Int("scatter-aggregate-timeout-seconds", envInt("LENNY_SCATTER_AGGREGATE_TIMEOUT_SECONDS", 120),
		"§12.6 storeRouter.scatterGatherAggregateTimeoutSeconds: total scatter-gather deadline, capping worst-case latency when many shards are slow. Override via LENNY_SCATTER_AGGREGATE_TIMEOUT_SECONDS. F-12.6.18.")
	f.redisURL = flag.String("redis-url", os.Getenv("LENNY_REDIS_URL"),
		"Redis URL (redis://host:port/db). When set, circuit-breaker state is held in Redis so operator safety blocks survive a restart and stay consistent across replicas. When empty, an in-memory breaker store is used. Mutually exclusive with --redis-sentinel-addrs.")
	f.redisSentinelAddrs = flag.String("redis-sentinel-addrs", os.Getenv("LENNY_REDIS_SENTINEL_ADDRS"),
		"Comma-separated list of §12.8 Redis Sentinel host:port pairs. When set with --redis-sentinel-master, the gateway discovers the master via Sentinel and follows automatic failover. Mutually exclusive with --redis-url.")
	f.redisSentinelMaster = flag.String("redis-sentinel-master", os.Getenv("LENNY_REDIS_SENTINEL_MASTER"),
		"§12.8 Redis Sentinel monitored master name (e.g., lenny-master). Required when --redis-sentinel-addrs is set.")
	f.redisPassword = flag.String("redis-password", os.Getenv("LENNY_REDIS_PASSWORD"),
		"Redis AUTH password applied to the direct, Sentinel, and Cluster modes. §12.4 requires AUTH; an empty password fails startup unless --redis-allow-insecure is set. Override via LENNY_REDIS_PASSWORD.")
	f.redisSentinelPassword = flag.String("redis-sentinel-password", os.Getenv("LENNY_REDIS_SENTINEL_PASSWORD"),
		"AUTH password for the sentinels themselves. Optional; sentinels typically run unauthenticated.")
	f.redisTLS = flag.Bool("redis-tls", envFlag("LENNY_REDIS_TLS"),
		"§12.4 request TLS on the Sentinel and Cluster paths. The direct-URL path derives TLS from the rediss:// scheme instead. TLS is mandatory unless --redis-allow-insecure is set, in which case this flag opts a dev Sentinel or Cluster topology back into TLS. Override via LENNY_REDIS_TLS.")
	f.redisAllowInsecure = flag.Bool("redis-allow-insecure", envFlag("LENNY_REDIS_ALLOW_INSECURE"),
		"§12.4 opt out of the mandatory Redis AUTH-and-TLS startup invariant. The spec requires both on every Redis instance, so this defaults off and a missing password or plaintext connection fails startup. Set only for a dev or local Redis. Override via LENNY_REDIS_ALLOW_INSECURE.")
	f.redisClusterAddrs = flag.String("redis-cluster-addrs", os.Getenv("LENNY_REDIS_CLUSTER_ADDRS"),
		"§12.4 comma-separated Redis Cluster seed nodes (host:port). When set the base Redis client is a CLUSTER KEYSLOT-aware go-redis ClusterClient: the single Redis Cluster that serves every concern at Tier 3 under the §12.4 shared Redis topology, which a deployment reaches through the whole-deployment cutover of the §12.4 Redis Cluster migration pre-plan. Takes precedence over --redis-url and --redis-sentinel-addrs. Override via LENNY_REDIS_CLUSTER_ADDRS.")
	f.capacityTier = flag.String("capacity-tier", envOr("LENNY_CAPACITY_TIER", "tier1"),
		"§17.8.2 capacityPlanning.tier (tier1, tier2, tier3) the deployment is sized for. Drives the billingRedisStreamMaxLen default and the §17.8.2 RedisClusterRecommended startup warning. Override via LENNY_CAPACITY_TIER. F-17.8.5, F-17.8.7.")
	f.singleTenantRedisTopology = flag.String("single-tenant-redis-topology", os.Getenv("LENNY_SINGLE_TENANT_REDIS_TOPOLOGY"),
		"§17.8.2 capacityPlanning.singleTenantRedisTopology. Set to \"sentinel\" to document that a Tier 3 deployment intentionally runs a single-tenant Redis Sentinel topology; this suppresses the RedisClusterRecommended startup warning. Override via LENNY_SINGLE_TENANT_REDIS_TOPOLOGY. F-17.8.7.")
	// spec: §12.4 — "Logical separation of Redis concerns".
	// Each per-concern URL routes one §12.4 store role to a dedicated
	// Redis instance (separate connection string per role); an empty
	// value keeps that concern on the base --redis-url / Sentinel /
	// Cluster client, so the single Tier 1/2 topology needs none of these.
	f.redisCoordinationURL = flag.String("redis-coordination-url", os.Getenv("LENNY_REDIS_COORDINATION_URL"),
		"§12.4 dedicated Redis URL for the Coordination concern (session leases, derive locks, pod slot counters). Empty uses the base Redis client. Override via LENNY_REDIS_COORDINATION_URL.")
	f.redisQuotaURL = flag.String("redis-quota-url", os.Getenv("LENNY_REDIS_QUOTA_URL"),
		"§12.4 dedicated Redis URL for the Quota/Rate Limiting concern (token/rate counters, sliding windows, storage quota, billing stream). Empty uses the base Redis client. Override via LENNY_REDIS_QUOTA_URL.")
	f.redisCachePubSubURL = flag.String("redis-cache-pubsub-url", os.Getenv("LENNY_REDIS_CACHE_PUBSUB_URL"),
		"§12.4 dedicated Redis URL for the Cache/Pub-Sub concern (circuit breakers, event relay, semantic cache, connector state, security bus, playground). Empty uses the base Redis client. Override via LENNY_REDIS_CACHE_PUBSUB_URL.")
	f.redisSessionDataURL = flag.String("redis-session-data-url", os.Getenv("LENNY_REDIS_SESSION_DATA_URL"),
		"§12.4 dedicated Redis URL for the Session-data concern (durable inbox, DLQ). Empty uses the base Redis client. Override via LENNY_REDIS_SESSION_DATA_URL.")
	f.redisDelegationURL = flag.String("redis-delegation-url", os.Getenv("LENNY_REDIS_DELEGATION_URL"),
		"§12.4 dedicated Redis URL for the Delegation concern (tree budget keys {root_session_id}:dlg:*). Empty uses the base Redis client. Override via LENNY_REDIS_DELEGATION_URL.")
}

// registerStorageFlags registers the storage, quota, fail-open, audit, billing, and rate-limit flags.
//
// spec: §4.1 gateway subsystem seams.
func (f *gatewayFlags) registerStorageFlags() {
	f.shutdownTimeout = flag.Duration("shutdown-timeout", 5*time.Second, "graceful shutdown timeout")
	f.evalAggregationRefreshSeconds = flag.Int("eval-aggregation-refresh-seconds",
		envInt("LENNY_EVAL_AGGREGATION_REFRESH_SECONDS", 0),
		"§10.7 evalAggregationRefreshSeconds: when 0 (default), the lenny_eval_aggregates materialized view exists but is unused and the §10.7 results API aggregates on read from eval_results. When positive, the gateway routes unfiltered results queries to the matview and schedules REFRESH MATERIALIZED VIEW CONCURRENTLY at this interval. Requires Postgres. Override via LENNY_EVAL_AGGREGATION_REFRESH_SECONDS. F-10.7.12.")
	// spec: §12.4 — the per-replica fail-open emergency
	// controls. quotaUserFailOpenFraction bounds a single user during the
	// outage window; quotaPerReplicaHardCap caps the per-replica tenant
	// ceiling; quotaFailOpenCumulativeMaxSeconds is the financial-security
	// control that transitions the replica to fail-closed for quota once
	// cumulative fail-open time exceeds it within the rolling hour.
	// F-12.4.9 / F-11.2.6.
	f.quotaUserFailOpenFraction = flag.Float64("quota-user-failopen-fraction",
		envFloat("LENNY_QUOTA_USER_FAILOPEN_FRACTION", failopen.DefaultUserFailOpenFraction),
		"§12.4 quotaUserFailOpenFraction: a single user's fail-open ceiling as a fraction of the per-replica tenant ceiling. Must satisfy 0 < value <= 1.0; >= 0.5 weakens the monopolization control. Default 0.25. Override via LENNY_QUOTA_USER_FAILOPEN_FRACTION.")
	f.quotaPerReplicaHardCap = flag.Int64("quota-per-replica-hard-cap",
		envInt64("LENNY_QUOTA_PER_REPLICA_HARD_CAP", 0),
		"§12.4 quotaPerReplicaHardCap: hard per-replica ceiling on a tenant's fail-open allocation regardless of replica count. Zero defaults to tenant_limit/2 per tenant. Override via LENNY_QUOTA_PER_REPLICA_HARD_CAP.")
	f.quotaFailOpenCumulativeMaxSeconds = flag.Int("quota-failopen-cumulative-max-seconds",
		envInt("LENNY_QUOTA_FAILOPEN_CUMULATIVE_MAX_SECONDS", int(failopen.DefaultCumulativeMaxSeconds/time.Second)),
		"§12.4 quotaFailOpenCumulativeMaxSeconds: cumulative fail-open seconds within the rolling 1h window past which the replica fails closed for quota. Financial-security control. Default 300s. Override via LENNY_QUOTA_FAILOPEN_CUMULATIVE_MAX_SECONDS.")
	f.failOpenStatePath = flag.String("failopen-cumulative-state-path",
		envOr("LENNY_FAILOPEN_CUMULATIVE_STATE_PATH", failopen.DefaultCumulativeStatePath),
		"§12.4 local file the cumulative fail-open timer persists on every transition so a restart resumes rather than resetting the timer. Override via LENNY_FAILOPEN_CUMULATIVE_STATE_PATH.")
	f.auditLockAcquireTimeoutMs = flag.Int("audit-lock-acquire-timeout-ms",
		envInt("LENNY_AUDIT_LOCK_ACQUIRE_TIMEOUT_MS", auditstore.DefaultLockConfig().AcquireTimeoutMs),
		"§11.7 item 3 audit.lock.acquireTimeoutMs: statement_timeout on the per-tenant audit advisory-lock acquisition. Default 5000ms. Override via LENNY_AUDIT_LOCK_ACQUIRE_TIMEOUT_MS.")
	f.tokenRevocationTimeoutMs = flag.Int("token-revocation-timeout-ms",
		envInt("LENNY_TOKEN_REVOCATION_TIMEOUT_MS", int(issuedtokenstore.DefaultRevocationTimeout/time.Millisecond)),
		"§11.4 step 5 and §13.3 auth.tokenRevocationTimeoutMs: the statement timeout of a full revoke's token walk, which holds the tenant's audit advisory lock; a walk that reaches it revokes no token and is reported as partial-failure detail. Default 5000. Override via LENNY_TOKEN_REVOCATION_TIMEOUT_MS.")
	f.auditLockMaxRetries = flag.Int("audit-lock-max-retries",
		envInt("LENNY_AUDIT_LOCK_MAX_RETRIES", auditstore.DefaultLockConfig().MaxRetries),
		"§11.7 item 3 audit.lock.maxRetries: same-replica retries after an audit lock timeout before returning 503 audit_unavailable. Default 3. Override via LENNY_AUDIT_LOCK_MAX_RETRIES.")
	f.auditLockRetryBaseMs = flag.Int("audit-lock-retry-base-ms",
		envInt("LENNY_AUDIT_LOCK_RETRY_BASE_MS", auditstore.DefaultLockConfig().RetryBaseMs),
		"§11.7 item 3 audit.lock.retryBaseMs: exponential-backoff base for audit lock retries, doubling per attempt and jittered ±20%. Default 20ms. Override via LENNY_AUDIT_LOCK_RETRY_BASE_MS.")
	f.globalTokenQuota = flag.Int64("global-token-quota-per-window", 0,
		"§11.2 platform-wide LLM-token budget per reset-period window, enforced by the §4.8 QuotaEvaluator at the global scope. Zero disables the global token cap. Only active when --redis-url is set.")
	f.userTokenQuota = flag.Int64("user-token-quota-per-window", 0,
		"§11.2 per-user LLM-token budget per reset-period window, enforced by the §4.8 QuotaEvaluator at the user scope. Zero disables the per-user token cap. Only active when --redis-url is set.")
	f.quotaRollingWindowSeconds = flag.Int("quota-rolling-window-seconds",
		envInt("LENNY_QUOTA_ROLLING_WINDOW_SECONDS", int(policy.DefaultRollingWindow.Seconds())),
		"§11.2 rolling-window length (seconds) applied when a tenant configures the `rolling` reset period. Default 3600 (1h). Override via LENNY_QUOTA_ROLLING_WINDOW_SECONDS.")
	f.quotaSyncIntervalSeconds = flag.Int("quota-sync-interval-seconds",
		envInt("LENNY_QUOTA_SYNC_INTERVAL_SECONDS", quota.DefaultSyncIntervalSeconds),
		"§11.2 quotaSyncIntervalSeconds: cadence (seconds) at which the gateway checkpoints Redis quota and delegation-budget counters to Postgres. Lower it (toward the 10s minimum) for high-throughput tenants to reduce crash-recovery overshoot; a value below the minimum is clamped up. Default 30s. Override via LENNY_QUOTA_SYNC_INTERVAL_SECONDS.")
	f.billingTokenCheckpointIntervalSeconds = flag.Int("billing-token-checkpoint-interval-seconds",
		envInt("LENNY_BILLING_TOKEN_CHECKPOINT_INTERVAL_SECONDS", 300),
		"§11.2.1 token_usage.checkpoint cadence (seconds): the interval at which the gateway snapshots each active session's proxy-recorded token delta into the per-tenant billing stream, so in-flight cost attribution is visible before session end. A value <= 0 disables the periodic checkpoint. Default 300s. Override via LENNY_BILLING_TOKEN_CHECKPOINT_INTERVAL_SECONDS.")
	f.quotaEnforcementMode = flag.String("quota-enforcement-mode",
		envOr("LENNY_QUOTA_ENFORCEMENT_MODE", string(quota.DefaultEnforcementMode)),
		"§12.4 quotaEnforcementMode: `redis` (default) reads the Redis token counters on the admission path; `in_memory_reconciled` draws a per-replica budget slice from Postgres, decrements it locally, and reconciles every quotaSyncIntervalSeconds (or at 80% slice consumption), tolerating full Redis unavailability for quota enforcement with bounded overshoot. The in-memory mode requires Postgres. Override via LENNY_QUOTA_ENFORCEMENT_MODE.")
	f.delegationNodeMemoryFootprintBytes = flag.Int64("delegation-node-memory-footprint-bytes",
		int64(envInt("LENNY_DELEGATION_NODE_MEMORY_FOOTPRINT_BYTES", int(delegationbudget.DefaultNodeMemoryFootprintBytes))),
		"§11.2 delegationNodeMemoryFootprintBytes: per-node in-memory footprint estimate the delegation-budget crash-recovery reconstruction multiplies by the live descendant count to derive liveMemoryBytes. Default 12288 (12 KB). Override via LENNY_DELEGATION_NODE_MEMORY_FOOTPRINT_BYTES.")
	// §4.8: deployer-supplied external interceptors. Each
	// --external-interceptor value registers one §4 RequestInterceptor
	// service on the policy chain. Repeatable. Form:
	// name=<n>,endpoint=<host:port>,phase=<phase>[,priority=<n>]
	// [,failPolicy=fail-open|fail-closed][,timeout=<dur>].
	flag.Func("external-interceptor",
		"§4.8 external RequestInterceptor registration (repeatable): name=<n>,endpoint=<host:port>,phase=<phase>[,priority=<n>][,failPolicy=fail-open|fail-closed][,timeout=<dur>]",
		func(v string) error { f.externalInterceptors = append(f.externalInterceptors, v); return nil })
	f.externalInterceptorTLSCert = flag.String("external-interceptor-tls-cert", os.Getenv("LENNY_EXTERNAL_INTERCEPTOR_TLS_CERT"),
		"client certificate for mTLS to external interceptor services. When empty (with key/ca also empty) the gateway dials plaintext.")
	f.externalInterceptorTLSKey = flag.String("external-interceptor-tls-key", os.Getenv("LENNY_EXTERNAL_INTERCEPTOR_TLS_KEY"),
		"client private key for mTLS to external interceptor services.")
	f.externalInterceptorCA = flag.String("external-interceptor-ca", os.Getenv("LENNY_EXTERNAL_INTERCEPTOR_CA"),
		"CA bundle verifying external interceptor server certificates.")
	f.guardrailsClassifier = flag.String("guardrails-classifier", os.Getenv("LENNY_GUARDRAILS_CLASSIFIER"),
		"§4.8 GuardrailsInterceptor classifier registration (external RequestInterceptor spec: name=<n>,endpoint=<host:port>[,failPolicy=fail-open|fail-closed][,timeout=<dur>]). When empty the GuardrailsInterceptor is disabled. The priority is fixed at 400 and the phases at PreDelegation, PreLLMRequest, PostLLMResponse, and PostAgentOutput; any phase=/priority= in the spec is ignored.")
	f.interceptorFailOpenMax = flag.Int("interceptor-fail-open-max-consecutive", envInt("LENNY_INTERCEPTOR_FAIL_OPEN_MAX_CONSECUTIVE", 10),
		"§4.8 cumulative fail-open escalation ceiling: when a fail-open interceptor errors more than this many times in a rolling 5-minute window, the gateway auto-escalates it to fail-closed and emits interceptor.fail_open_escalated.")
	f.delegationMaxInputSize = flag.Int("delegation-max-input-size", envInt("LENNY_DELEGATION_MAX_INPUT_SIZE", delegationpolicystore.DefaultMaxInputSize),
		"§8.3 default contentPolicy.maxInputSize: the hard byte cap on TaskSpec.input the §4.8 DelegationPolicyEvaluator (PreDelegation, priority 250) enforces. A delegation exceeding it is rejected with INPUT_TOO_LARGE before pod allocation. Defaults to the §8.3 128 KiB. Override via LENNY_DELEGATION_MAX_INPUT_SIZE.")
	f.delegationDefaultMaxDepth = flag.Int("delegation-default-max-depth", envInt("LENNY_DELEGATION_DEFAULT_MAX_DEPTH", delegation.DefaultMaxDepth),
		"§8.2.bis Helm fallback for delegationLease.maxDepth (gateway.delegation.defaultMaxDepth). Every effective delegation lease MUST carry a positive integer maxDepth; this value is consulted last in the precedence chain (client → preset → runtime default → policy ceiling → Helm fallback), so a delegation request that omits maxDepth still receives a bounded chain. Default 10. Override via LENNY_DELEGATION_DEFAULT_MAX_DEPTH.")
	f.delegationMaxActiveChildrenPerUser = flag.Int("delegation-max-active-children-per-user",
		envInt("LENNY_DELEGATION_MAX_ACTIVE_CHILDREN_PER_USER", 0),
		"§11.1 per-user active-delegated-children admission cap: the maximum count of live (non-terminal) delegated children a single user may hold across all their sessions and trees (the per-session breadth is bounded by the §8.2 lease/treebudget axes). Zero disables the per-user scope. Override via LENNY_DELEGATION_MAX_ACTIVE_CHILDREN_PER_USER. F-11.1.4.")
}

// registerPolicyFlags registers the §4.8 interceptor, delegation, alerting, SLO, and billing-failover flags.
//
// spec: §4.1 gateway subsystem seams.
func (f *gatewayFlags) registerPolicyFlags() {
	f.gatewayAllowSelfRecursion = flag.Bool("gateway-allow-self-recursion", envFlag("LENNY_GATEWAY_ALLOW_SELF_RECURSION"),
		"§8.2 LayerPlatform input to the cycle-detection three-layer AND gate (Helm value gateway.allowSelfRecursion). A self-recursive delegation hop (same runtime+pool tuple appears earlier in the lineage) is admitted under mode=enforce iff this flag, the resolved Runtime.allowSelfRecursion, and the resolved DelegationPolicy.allowSelfRecursion are all true. Default false. Override via LENNY_GATEWAY_ALLOW_SELF_RECURSION.")
	f.interceptorWeakeningCooldownSeconds = flag.Int("interceptor-weakening-cooldown-seconds",
		envInt("LENNY_INTERCEPTOR_WEAKENING_COOLDOWN_SECONDS", int(delegation.DefaultInterceptorWeakeningCooldown/time.Second)),
		"§8.3 Helm value gateway.interceptorWeakeningCooldownSeconds: the cluster-scoped window during which delegate_task rejects every call whose effective DelegationPolicy is inside a `scanExportedFiles: true → false` weakening transition with INTERCEPTOR_WEAKENING_COOLDOWN (TRANSIENT, HTTP 503). Default 60s. Override via LENNY_INTERCEPTOR_WEAKENING_COOLDOWN_SECONDS. F-8.7.12 / F-13.5.7.")
	f.healthTrackerUseCompiledRules = flag.Bool("health-tracker-use-compiled-rules",
		envBool("LENNY_HEALTH_TRACKER_USE_COMPILED_RULES", true),
		"§25.13 Helm value gateway.healthTracker.useCompiledRules: when true (default), the gateway's in-process §16.5 alert evaluator drives the per-replica health view. When false, the gateway suppresses the in-process alert tracker entirely and /v1/admin/health falls back to dependency probes and circuit breaker state only. Operators set this to false for strict consistency with operator-customized Prometheus rules. Override via LENNY_HEALTH_TRACKER_USE_COMPILED_RULES. F-25.13.4.")
	f.opsNonceCheckpointPath = flag.String("ops-nonce-checkpoint-path",
		envOr("LENNY_OPS_NONCE_CHECKPOINT_PATH", ""),
		"§25.3: local-disk path the operational-event nonce counter is periodically checkpointed to so the eventKey stays unique across a restart when the replica id (LENNY_REPLICA_ID) is pinned to a stable value. Empty (the default) keeps the counter in-process; mount a writable volume (e.g. an emptyDir) and set this for a stable-replica-id deployment. Override via LENNY_OPS_NONCE_CHECKPOINT_PATH. F-25.3.8.")
	f.alertingBundleFormats = flag.String("alerting-bundle-formats",
		envOr("LENNY_ALERTING_BUNDLE_FORMATS", "prometheusrule"),
		"§25.13 Helm value monitoring.format (closed-enum subset rendered by the chart): comma-separated list of the formats the chart bundled the §16.5 alert catalogue into. Stamps `lenny_alerting_rules_bundled{format}` so an operator can verify the bundling configuration. Override via LENNY_ALERTING_BUNDLE_FORMATS. F-25.13.3.")
	f.alertingOverrideCount = flag.Int("alerting-override-count",
		envInt("LENNY_ALERTING_OVERRIDE_COUNT", 0),
		"§25.13 Helm value len(monitoring.alertOverrides): count of operator-customized §16.5 rules the chart rendered. Stamps `lenny_alerting_rule_overrides` so the §25.13 metrics surface shows how many rules diverged from the bundled defaults. Override via LENNY_ALERTING_OVERRIDE_COUNT. F-25.13.3.")
	f.gatewayQueueDepthThreshold = flag.Float64("gateway-queue-depth-threshold",
		envFloat("LENNY_GATEWAY_QUEUE_DEPTH_THRESHOLD", 20),
		"§25.13 / §16.5 monitoring.alertThresholds.gatewayQueueDepthHigh.value: the per-subsystem queue-depth ceiling the GatewayQueueDepthHigh alert reads via scalar(lenny_gateway_queue_depth_threshold). Tier presets tighten this (Tier 2: 10, Tier 3: 5). Override via LENNY_GATEWAY_QUEUE_DEPTH_THRESHOLD. F-25.13.2.")
	f.gatewayLatencyThresholdSeconds = flag.Float64("gateway-latency-threshold-seconds",
		envFloat("LENNY_GATEWAY_LATENCY_THRESHOLD_SECONDS", 3.0),
		"§25.13 / §16.5 monitoring.alertThresholds.gatewayLatencyHigh.p99Seconds: the per-subsystem p95 latency ceiling (seconds) the GatewayLatencyHigh alert reads via scalar(lenny_gateway_latency_threshold_seconds). Tier presets tighten this (Tier 2: 2.0, Tier 3: 1.0). Override via LENNY_GATEWAY_LATENCY_THRESHOLD_SECONDS. F-25.13.2.")
	f.credentialPoolLowThreshold = flag.Float64("credential-pool-low-threshold",
		envFloat("LENNY_CREDENTIAL_POOL_LOW_THRESHOLD", 0.80),
		"§25.13 / §16.5 monitoring.alertThresholds.credentialPoolLow.utilizationThreshold: the per-pool utilisation fraction the CredentialPoolLow alert reads via scalar(lenny_credential_pool_low_threshold). Tier presets tighten this (Tier 2: 0.70, Tier 3: 0.60). Override via LENNY_CREDENTIAL_POOL_LOW_THRESHOLD. F-25.13.2.")
	f.sloBurnRateFastMultiplier = flag.Float64("slo-burn-rate-fast-multiplier",
		envFloat("LENNY_SLO_BURN_RATE_FAST_MULTIPLIER", 14),
		"§16.5 slo.burnRate.fastMultiplier: the fast-window (1h) burn-rate multiplier every burn-rate alert pages at, read via scalar(lenny_slo_burn_rate_fast_multiplier). Default 14. Override via LENNY_SLO_BURN_RATE_FAST_MULTIPLIER. F-16.5.3.")
	f.sloBurnRateSlowMultiplier = flag.Float64("slo-burn-rate-slow-multiplier",
		envFloat("LENNY_SLO_BURN_RATE_SLOW_MULTIPLIER", 3),
		"§16.5 slo.burnRate.slowMultiplier: the slow-window (6h) burn-rate multiplier every burn-rate alert warns at, read via scalar(lenny_slo_burn_rate_slow_multiplier). Default 3. Override via LENNY_SLO_BURN_RATE_SLOW_MULTIPLIER. F-16.5.3.")
	f.billingFlushIntervalMs = flag.Int("billing-flush-interval-ms",
		envInt("LENNY_BILLING_FLUSH_INTERVAL_MS", int(failover.DefaultFlushInterval/time.Millisecond)),
		"§12.3 billingFlushIntervalMs: cadence (ms) at which the billing failover flusher drains the Tier 2 write-ahead buffer into Postgres in multi-row batches. Default 500. Override via LENNY_BILLING_FLUSH_INTERVAL_MS. F-12.3.13.")
	f.billingFlushBatchSize = flag.Int("billing-flush-batch-size",
		envInt("LENNY_BILLING_FLUSH_BATCH_SIZE", failover.DefaultFlushBatchSize),
		"§12.3 billingFlushBatchSize: maximum buffered billing events drained into Postgres per flush call (one multi-row INSERT batch). Default 50. Override via LENNY_BILLING_FLUSH_BATCH_SIZE. F-12.3.13.")
	f.billingFlushMaxPending = flag.Int("billing-flush-max-pending",
		envInt("LENNY_BILLING_FLUSH_MAX_PENDING", failover.DefaultFlushMaxPending),
		"§12.3 billingFlushMaxPending: once the Tier 2 write-ahead buffer grows past this many events, the gateway flushes immediately and emits the billing_flush_pressure metric. Default 500. Override via LENNY_BILLING_FLUSH_MAX_PENDING. F-12.3.13.")
	f.billingRedisStreamMaxLen = flag.Int64("billing-redis-stream-max-len",
		envInt64("LENNY_BILLING_REDIS_STREAM_MAX_LEN", 0),
		"§17.8.2 billingRedisStreamMaxLen: per-tenant MAXLEN of the §11.2.1 billing failover Redis stream. 0 (the default) resolves the per-tier default from --capacity-tier (72,000 at Tier 3, else 50,000); a positive value pins it. Override via LENNY_BILLING_REDIS_STREAM_MAX_LEN. F-17.8.5.")
	f.postgresWriteCeilingIops = flag.Float64("postgres-write-ceiling-iops",
		envFloat("LENNY_POSTGRES_WRITE_CEILING_IOPS", 200),
		"§12.3 postgres.writeCeilingIops: the measured sustained write-IOPS ceiling for the primary Postgres instance. Emitted unlabelled on lenny_postgres_write_ceiling_iops so the §16.5 PostgresWriteSaturation alert reads scalar(lenny_postgres_write_ceiling_iops). Tier presets set 200/600/1600. Override via LENNY_POSTGRES_WRITE_CEILING_IOPS. F-12.3.8.")
	f.postgresWriteIopsSampleSeconds = flag.Int("postgres-write-iops-sample-interval-seconds",
		envInt("LENNY_POSTGRES_WRITE_IOPS_SAMPLE_INTERVAL_SECONDS", 15),
		"§12.3 cadence (seconds) at which the gateway samples pg_stat_database row-write deltas to publish the lenny_postgres_write_iops gauge feeding the §16.5 PostgresWriteSaturation alert. Default 15. Override via LENNY_POSTGRES_WRITE_IOPS_SAMPLE_INTERVAL_SECONDS. F-12.3.7.")
	f.auditStartupChainCheckEntries = flag.Int("audit-startup-chain-check-entries",
		envInt("LENNY_AUDIT_STARTUP_CHAIN_CHECK_ENTRIES", 1000),
		"§12.3 audit.startupChainCheckEntries: the most-recent N audit rows per tenant the startup chain-continuity check re-verifies. A non-positive value walks each chain in full. Default 1000. Override via LENNY_AUDIT_STARTUP_CHAIN_CHECK_ENTRIES. F-12.3.9.")
	f.auditGrantCheckIntervalSeconds = flag.Int("audit-grant-check-interval-seconds",
		envInt("LENNY_AUDIT_GRANT_CHECK_INTERVAL_SECONDS", 0),
		"§11.7 item 2 audit.grantCheckInterval: cadence of the periodic background integrity check that re-verifies append-only ledger grants/triggers/erasure guard and samples recent chain segments. 0 selects the profile default (regulated 60s, unregulated 300s). A value above the profile maximum (regulated 120s, unregulated 900s) is a fatal startup error. Override via LENNY_AUDIT_GRANT_CHECK_INTERVAL_SECONDS. F-11.7.3.")
	f.auditHardFailOnDrift = flag.Bool("audit-hard-fail-on-drift",
		envBool("LENNY_AUDIT_HARD_FAIL_ON_DRIFT", false),
		"§11.7 item 2 audit.hardFailOnDrift: when true, a drift detected by the periodic background integrity check initiates a graceful shutdown (in addition to the critical alert and lenny_audit_grant_drift_total increment). Default false. Override via LENNY_AUDIT_HARD_FAIL_ON_DRIFT. F-11.7.3.")
	f.auditScatterCacheEnabled = flag.Bool("audit-scatter-gather-cache-enabled",
		envBool("LENNY_AUDIT_SCATTER_GATHER_CACHE_ENABLED", true),
		"§25.9 ops.audit.scatterGatherCacheEnabled: cache platform-admin cross-tenant audit scatter-gather results for 5 minutes (keyed by query parameters, bypassed per query with ?fresh=true). Backed by Redis (shared across replicas) when a Redis backend is configured, otherwise in-process (per-replica). Set false to disable the cache so every cross-tenant query reads the shards fresh. Default true. Override via LENNY_AUDIT_SCATTER_GATHER_CACHE_ENABLED. F-25.9.11.")
	f.auditSIEMEndpoint = flag.String("audit-siem-endpoint",
		os.Getenv("LENNY_AUDIT_SIEM_ENDPOINT"),
		"§11.7 audit.siem.endpoint: the external SIEM ingest endpoint. When empty, the §11.7 compliance gate rejects creating or updating a tenant to a regulated complianceProfile (soc2, fedramp, hipaa), and creating an environment under one, with COMPLIANCE_SIEM_REQUIRED; in production a regulated tenant with no endpoint is a fatal startup error. When set, the gateway validates SIEM connectivity at startup (a test event must be acknowledged or the gateway refuses to start) and runs the §11.7 OCSF translator → SIEM forwarder pipeline. Override via LENNY_AUDIT_SIEM_ENDPOINT. F-11.7.1 / F-11.7.2.")
	f.auditSIEMSecret = flag.String("audit-siem-secret",
		os.Getenv("LENNY_AUDIT_SIEM_SECRET"),
		"§11.7 SIEM HMAC shared secret. When set, the SIEM HTTP sink signs each OCSF batch with an HMAC-SHA256 X-Lenny-SIEM-Signature header so the receiver can authenticate the gateway. Override via LENNY_AUDIT_SIEM_SECRET. F-11.7.1.")
	f.auditPgauditEnabled = flag.Bool("audit-pgaudit-enabled",
		os.Getenv("LENNY_AUDIT_PGAUDIT_ENABLED") == "true",
		"§11.7 item 5 audit.pgaudit.enabled: when true, the gateway startup preflight verifies the pgaudit extension is installed and pgaudit.log includes the DDL and ROLE classes (fatal in production with a regulated complianceProfile if the check fails). A regulated tenant additionally requires this true with audit.pgaudit.sinkEndpoint set, enforced at startup (fatal in production) and at regulated tenant/environment create/update (HTTP 422 COMPLIANCE_PGAUDIT_REQUIRED). Override via LENNY_AUDIT_PGAUDIT_ENABLED. F-11.7.10.")
	f.auditPgauditSinkEndpoint = flag.String("audit-pgaudit-sink-endpoint",
		os.Getenv("LENNY_AUDIT_PGAUDIT_SINK_ENDPOINT"),
		"§11.7 item 5 audit.pgaudit.sinkEndpoint: the external append-only sink the pgaudit DDL/ROLE records stream to. Required (alongside audit.pgaudit.enabled) for any tenant with a regulated complianceProfile. Override via LENNY_AUDIT_PGAUDIT_SINK_ENDPOINT. F-11.7.10.")
	f.auditSIEMFailureThresholdPercent = flag.Float64("audit-siem-failure-threshold-percent",
		envFloat("LENNY_AUDIT_SIEM_FAILURE_THRESHOLD_PERCENT", 5),
		"§11.7 item 4 audit.siem.failureThresholdPercent: when the SIEM delivery failure rate exceeds this percentage, the §25.3 health API reports the siem component degraded (default 5%). Override via LENNY_AUDIT_SIEM_FAILURE_THRESHOLD_PERCENT. F-11.7.16.")
	f.auditSIEMMaxDeliveryLagSeconds = flag.Int("audit-siem-max-delivery-lag-seconds",
		envInt("LENNY_AUDIT_SIEM_MAX_DELIVERY_LAG_SECONDS", 30),
		"§12.3 audit.siem.maxDeliveryLagSeconds: the threshold above which the §16.5 AuditSIEMDeliveryLag alert fires. Emitted on lenny_audit_siem_max_delivery_lag_seconds so the alert compares against an operator-tunable scalar. Default 30s. Override via LENNY_AUDIT_SIEM_MAX_DELIVERY_LAG_SECONDS. F-12.3.17.")
	f.auditSIEMPollIntervalSeconds = flag.Int("audit-siem-poll-interval-seconds",
		envInt("LENNY_AUDIT_SIEM_POLL_INTERVAL_SECONDS", 10),
		"§12.3 SIEM outbox forwarder poll interval: how often the forwarder tails the committed audit_log rows for newly committed events. Must stay well under audit.siem.maxDeliveryLagSeconds. Default 10s. Override via LENNY_AUDIT_SIEM_POLL_INTERVAL_SECONDS. F-12.3.6.")
	f.auditOCSFRetryIntervalSeconds = flag.Int("audit-ocsf-retry-interval-seconds",
		envInt("LENNY_AUDIT_OCSF_RETRY_INTERVAL_SECONDS", 30),
		"§11.7 audit.ocsf.retryInterval: cadence at which the OCSF translator re-drives pending / retry_pending audit rows toward succeeded | dead_lettered. Default 30s. Override via LENNY_AUDIT_OCSF_RETRY_INTERVAL_SECONDS. F-11.7.11.")
	f.auditOCSFMaxAttempts = flag.Int("audit-ocsf-max-attempts",
		envInt("LENNY_AUDIT_OCSF_MAX_ATTEMPTS", 10),
		"§11.7 audit.ocsf.maxAttempts: the per-row OCSF translation attempt budget; on the final failed attempt the row transitions to dead_lettered and a translation-failure receipt advances the SIEM delivery pointer. Default 10. Override via LENNY_AUDIT_OCSF_MAX_ATTEMPTS. F-11.7.11.")
	f.auditOCSFBatchSize = flag.Int("audit-ocsf-batch-size",
		envInt("LENNY_AUDIT_OCSF_BATCH_SIZE", 256),
		"§11.7 OCSF translator per-cycle batch size: the maximum number of pending audit rows one translation cycle drains. Default 256. Override via LENNY_AUDIT_OCSF_BATCH_SIZE. F-11.7.1.")
	f.auditSyncWritePoolSize = flag.Int("audit-sync-write-pool-size",
		envInt("LENNY_AUDIT_SYNC_WRITE_POOL_SIZE", 4),
		"§12.3 audit.syncWritePoolSize: the size of the dedicated audit sync write pool. Synchronous audit writes use this pool so they do not consume request-serving connections from the shared primary pool. Default 4. Override via LENNY_AUDIT_SYNC_WRITE_POOL_SIZE. F-12.3.14.")
	f.auditBatchingEnabled = flag.Bool("audit-batching-enabled",
		envBool("LENNY_AUDIT_BATCHING_ENABLED", false),
		"§12.3 audit.batchingEnabled: opt-in T2 (non-PII) audit-event batching. Disabled by default. When true, non-PII T2 operational audit events are buffered in memory and flushed in batches (accepting the documented data-loss risk on a crash); T3/T4 PII events always stay synchronous. Override via LENNY_AUDIT_BATCHING_ENABLED. F-12.3.14.")
	f.auditFlushIntervalMs = flag.Int("audit-flush-interval-ms",
		envInt("LENNY_AUDIT_FLUSH_INTERVAL_MS", 250),
		"§12.3 audit.flushIntervalMs: the maximum age of a buffered T2 audit event before it is flushed when batching is enabled. Default 250ms. Override via LENNY_AUDIT_FLUSH_INTERVAL_MS. F-12.3.14.")
	f.auditFlushBatchSize = flag.Int("audit-flush-batch-size",
		envInt("LENNY_AUDIT_FLUSH_BATCH_SIZE", 100),
		"§12.3 audit.flushBatchSize: the buffered T2 audit-event count that triggers an immediate flush when batching is enabled. Default 100. Override via LENNY_AUDIT_FLUSH_BATCH_SIZE. F-12.3.14.")
	f.retryMaxRetries = flag.Int("retry-max-retries", envInt("LENNY_RETRY_MAX_RETRIES", policy.DefaultMaxRetries),
		"§7.3 default retryPolicy.maxRetries: the automatic-retry budget the §4.8 RetryPolicyEvaluator (PostRoute, priority 600) enforces. A session whose retryCount has reached this cap is rejected at routing (it is in awaiting_client_action and requires an explicit client resume). Defaults to the §7.3 example value of 2. Override via LENNY_RETRY_MAX_RETRIES.")
	f.callbackURLAllowedDomains = flag.String("callback-url-allowed-domains", os.Getenv("LENNY_CALLBACK_URL_ALLOWED_DOMAINS"),
		"§14 callbackUrlAllowedDomains: a comma-separated allowlist of hostnames (exact or `*.suffix` wildcard) a session callbackUrl may target. When set, only matching hosts are accepted; when empty, the §14 public-DNS / private-range SSRF validation applies. Override via LENNY_CALLBACK_URL_ALLOWED_DOMAINS.")
	f.maxResumePendingSeconds = flag.Int("max-resume-pending-seconds",
		envInt("LENNY_MAX_RESUME_PENDING_SECONDS", watchdog.DefaultMaxResumePendingSeconds),
		"§6.2 wall-clock cap on resume_pending: a session that has waited this long for a pod to become available transitions to awaiting_client_action. Mirrors the per-session retryPolicy.maxResumeWindowSeconds default; the per-session value tightens the platform cap. Default 900s. Override via LENNY_MAX_RESUME_PENDING_SECONDS.")
	f.agentNamespace = flag.String("agent-namespace", os.Getenv("LENNY_AGENT_NAMESPACE"),
		"Kubernetes namespace the §5 warm pools and AgentPods live in. When set, the gateway places each started session on a warm pod via the §4.7 adapter instead of the in-process executor.")
	f.clusterQPS = flag.Float64("cluster-qps", envFloat("LENNY_CLUSTER_QPS", 100),
		"client-go QPS for the cluster client the gateway uses to list/get/patch PodSet / AgentPod / PodClaim. The session-start path issues 5+ API calls per request, so client-go's default of 5 saturates at trivial load. The spec mandates explicit QPS values for the controller (§4.6.1) but leaves the gateway's client throttle to operator tuning; the kube-apiserver's own priority+fairness is the production-bounded gate. Override via LENNY_CLUSTER_QPS.")
	f.clusterBurst = flag.Int("cluster-burst", envInt("LENNY_CLUSTER_BURST", 200),
		"client-go burst (token-bucket size) for the cluster client. Pairs with --cluster-qps. Override via LENNY_CLUSTER_BURST.")
	// spec: §17.5, §16.1 — the per-profile RuntimeClass name overrides. The
	// chart renders these from the same `runtimeClasses.profiles.<profile>.name` values,
	// with the same flag names, that it renders for the controller, so the
	// gateway's runtime_class labels name the RuntimeClass the controller
	// stamps on each pod (F-16.1.10).
	f.standardRuntimeClass = flag.String("standard-runtime-class", os.Getenv("LENNY_STANDARD_RUNTIME_CLASS"),
		"§17.5 RuntimeClass name override for the §5.3 standard profile, matching the controller's flag of the same name. The gateway labels runtime_class on its warm-pool, startup, time-to-first-token, and recycle series through it. Empty uses the default 'runc'. Override via LENNY_STANDARD_RUNTIME_CLASS.")
	f.sandboxedRuntimeClass = flag.String("sandboxed-runtime-class", os.Getenv("LENNY_SANDBOXED_RUNTIME_CLASS"),
		"§17.5 RuntimeClass name override for the §5.3 sandboxed profile, matching the controller's flag of the same name. Empty uses the default 'gvisor'. Override via LENNY_SANDBOXED_RUNTIME_CLASS.")
	f.microvmRuntimeClass = flag.String("microvm-runtime-class", os.Getenv("LENNY_MICROVM_RUNTIME_CLASS"),
		"§17.5 RuntimeClass name override for the §5.3 microvm profile, matching the controller's flag of the same name. Empty uses the default 'kata'. Override via LENNY_MICROVM_RUNTIME_CLASS.")
}

// registerSessionFlags registers the session-lifecycle, messaging, adapter, token-service, lease, and LLM-proxy flags.
//
// spec: §4.1 gateway subsystem seams.
func (f *gatewayFlags) registerSessionFlags() {
	f.messagingDefaultScope = flag.String("messaging-default-scope", os.Getenv("LENNY_MESSAGING_DEFAULT_SCOPE"),
		"§7.2 deployment default messagingScope for lenny/send_message (`direct` | `siblings`). Empty resolves to the §7.2 default `direct` (siblings is opt-in). Override via LENNY_MESSAGING_DEFAULT_SCOPE.")
	f.messagingMaxScope = flag.String("messaging-max-scope", os.Getenv("LENNY_MESSAGING_MAX_SCOPE"),
		"§7.2 deployment messagingScope ceiling (`direct` | `siblings`); no tenant or runtime can widen beyond it. Empty imposes no ceiling beyond the enum; `direct` forbids sibling messaging tree-wide. Override via LENNY_MESSAGING_MAX_SCOPE.")
	f.messagingMaxPerMinute = flag.Int("messaging-max-per-minute", envInt("LENNY_MESSAGING_MAX_PER_MINUTE", 30),
		"§8.3 lenny/send_message per-sender outbound burst limit per minute. Override via LENNY_MESSAGING_MAX_PER_MINUTE.")
	f.messagingMaxPerSession = flag.Int("messaging-max-per-session", envInt("LENNY_MESSAGING_MAX_PER_SESSION", 200),
		"§8.3 lenny/send_message per-sender lifetime outbound cap. Override via LENNY_MESSAGING_MAX_PER_SESSION.")
	f.messagingMaxInboundPerMinute = flag.Int("messaging-max-inbound-per-minute", envInt("LENNY_MESSAGING_MAX_INBOUND_PER_MINUTE", 60),
		"§8.3 lenny/send_message per-target inbound aggregate limit per minute (the O(N²) sibling-storm brake). Override via LENNY_MESSAGING_MAX_INBOUND_PER_MINUTE.")
	f.toolApprovalTimeout = flag.Duration("tool-approval-timeout", envDuration("LENNY_TOOL_APPROVAL_TIMEOUT", 0),
		"§7.2 tool-use approval wait: how long a blocked tool_call(approvalRequired) waits for a POST /tool-use/{id}/approve|deny before the gateway treats it as a denial. Zero (default) blocks until the user resolves it or the request context is cancelled. Override via LENNY_TOOL_APPROVAL_TIMEOUT.")
	f.streamFailureReportTimeout = flag.Duration("stream-failure-report-timeout", envDuration("LENNY_STREAM_FAILURE_REPORT_TIMEOUT", sessionserver.DefaultStreamFailureReportTimeout),
		"§28.5.1 CH-ATTACH stream-failure report bound: operator-tunable time a runtime_crash report on a failed Attach stream has for the binding release, parent notification, and cascade beyond the workspace seal window (the report's deadline is this value plus the seal window). Default 30s. Override via LENNY_STREAM_FAILURE_REPORT_TIMEOUT.")
	f.sessionPrimaryReadTimeout = flag.Duration("session-primary-read-timeout", envDuration("LENNY_SESSION_PRIMARY_READ_TIMEOUT", sessionstore.DefaultPrimaryReadTimeout),
		"§12.3 bound on each session-row read from the Postgres primary that a one-shot decision makes (a route, a skip, or a compare-and-swap's expected value), so a primary partition bounds the read rather than stalling it until the request ends. Operator-tunable; must be positive. Default 5s. Override via LENNY_SESSION_PRIMARY_READ_TIMEOUT.")
	f.treeArchiveCacheEntries = flag.Int("tree-archive-cache-entries", envInt("LENNY_TREE_ARCHIVE_CACHE_ENTRIES", 128),
		"§8.10 per-replica LRU cache size fronting the Postgres session_tree_archive (default 128 entries). Override via LENNY_TREE_ARCHIVE_CACHE_ENTRIES.")
	f.adapterTLSCert = flag.String("adapter-tls-cert", os.Getenv("LENNY_ADAPTER_TLS_CERT"),
		"path to the gateway's client certificate for the §4.7 mTLS link to pod adapters. Empty dials adapters in plaintext (local development only).")
	f.adapterTLSKey = flag.String("adapter-tls-key", os.Getenv("LENNY_ADAPTER_TLS_KEY"),
		"path to the private key for --adapter-tls-cert.")
	f.adapterCA = flag.String("adapter-ca", os.Getenv("LENNY_ADAPTER_CA"),
		"path to the CA bundle that verifies a pod adapter's server certificate on the §4.7 mTLS link.")
	f.holderForwardAdvertiseAddr = flag.String("holder-forward-advertise-addr", os.Getenv("LENNY_HOLDER_FORWARD_ADVERTISE_ADDR"),
		"§10.1.1 Replica address: the host:port this replica advertises in its REG-COORDLEASE holder value, at which peer replicas reach its CH-HOLDERFORWARD listener (the chart renders <pod IP>:gateway.holderForwardPort; an IPv6 host is bracketed). Required whenever a coordination lease store is configured (Redis); the gateway refuses to start without a valid value.")
	f.holderForwardListenAddr = flag.String("holder-forward-listen-addr", envOr("LENNY_HOLDER_FORWARD_LISTEN_ADDR", ":50054"),
		"§10.3 NET-072 bind address of the CH-HOLDERFORWARD listener, which serves requests forwarded by peer replicas over mTLS with the gateway replica certificate (--adapter-tls-cert, --adapter-tls-key, --adapter-ca). Default :50054.")
	f.tokenServiceAddr = flag.String("token-service-grpc-addr", os.Getenv("LENNY_TOKEN_SERVICE_GRPC_ADDR"),
		"§4.3 lenny-token-service gRPC address (host:port). When set, the gateway materializes every §4.9 credential lease over mTLS against the Token Service instead of running pkg/credential.MintLease in-process, enforcing the §4.3 'gateway has no KMS decrypt rights' boundary. Empty falls back to the in-process credassign.Service for dev mode and self-contained tests.")
	f.credentialsNamespace = flag.String("credentials-namespace", envOr("LENNY_CREDENTIALS_NAMESPACE", credential.DefaultNamespace),
		"§4.9 credentials namespace: the namespace that holds credential Secrets. The admin credential-pool handlers and the bootstrap seed accept a secretRef only as <name> or <credentials-namespace>/<name> and store it in the canonical <credentials-namespace>/<name> form, and the Token Service probe is sent the parsed Secret name. The chart sets it from credentials.namespace. Default lenny-credentials.")
	f.tokenServiceHTTPURL = flag.String("token-service-http-url", os.Getenv("LENNY_TOKEN_SERVICE_HTTP_URL"),
		"§4.3 lenny-token-service HTTP token-exchange URL (scheme://host:port). When set, the gateway reverse-proxies /v1/oauth/* to the Token Service so the §13.3 canonical endpoint is served by the actual minter. Empty disables the /v1/oauth/ surface on the gateway entirely; deployments that wire a Token Service binary MUST set this to keep /v1/oauth/token reachable.")
	f.tokenServiceCert = flag.String("token-service-tls-cert", os.Getenv("LENNY_TOKEN_SERVICE_TLS_CERT"),
		"path to the gateway's client certificate for the §4.3 mTLS link to lenny-token-service. Empty dials the Token Service in plaintext (dev mode only).")
	f.tokenServiceKey = flag.String("token-service-tls-key", os.Getenv("LENNY_TOKEN_SERVICE_TLS_KEY"),
		"path to the private key for --token-service-tls-cert.")
	f.tokenServiceCA = flag.String("token-service-ca", os.Getenv("LENNY_TOKEN_SERVICE_CA"),
		"path to the CA bundle that verifies the Token Service's server certificate on the §4.3 mTLS link.")
	f.tokenServiceTenant = flag.String("token-service-tenant", os.Getenv("LENNY_TOKEN_SERVICE_TENANT"),
		"fallback tenant id the gateway carries on a §4.3 Token Service request whose lease or session names no tenant of its own. The Token Service applies §4.2 RLS against this id. Empty resolves to the built-in `default` tenant under --tenancy-mode=single and stays empty under --tenancy-mode=multi, where every lease carries its own tenant.")
	f.elicitationFloor = flag.String("elicitation-content-integrity-floor", os.Getenv("LENNY_ELICITATION_CONTENT_INTEGRITY_FLOOR"),
		"§9.2 platform-wide elicitation content-integrity floor (off | detect-only | enforce). The §15.1 admin GET endpoint reports the resolved effective mode as max(floor, tenantStored). A PUT below the floor is rejected with ELICITATION_INTEGRITY_BELOW_PLATFORM_FLOOR. Empty defaults to `off` (no floor). This value seeds the floor at startup; the §17.2 phase-stamp ConfigMap reconcile then keeps it live.")
	f.elicitationFloorConfigMap = flag.String("elicitation-floor-configmap-name", envOr("LENNY_ELICITATION_FLOOR_CONFIGMAP", elicitationfloor.DefaultConfigMapName),
		"§17.2 ConfigMap (in --gateway-namespace) whose security.elicitationContentIntegrity.floor key the gateway re-reads to keep the platform floor live without a restart. Requires a cluster client; ignored on a Postgres-only deployment.")
	f.elicitationFloorReconcileSeconds = flag.Int("elicitation-floor-reconcile-interval-seconds", int(elicitationfloor.DefaultReconcileInterval/time.Second),
		"§17.2 cadence the gateway re-reads the phase-stamp ConfigMap floor key. The change is observed by polling because the gateway client is non-cached; the value bounds floor-change staleness.")
	f.grpcAddr = flag.String("grpc-addr", os.Getenv("LENNY_GRPC_ADDR"),
		"§8.6 GatewayControl gRPC listen address (host:port, e.g. :50061). When set, the gateway serves the adapter→gateway control surface — the §9.1 platform-tool, §9.3 connector-tool, and §4.7 scrub-report RPCs — on this address. Empty disables the GatewayControl listener.")
	f.leaseAutoMaxPerMin = flag.Int("lease-extension-auto-max-per-min", 0,
		"§8.6 deployment-default autoModeRateLimit.maxAutoExtensionsPerMinute: the per-task-tree cap on auto-approved lease extensions per minute before the gateway pauses auto-approval and falls back to elicitation. Zero is the spec default (no limit). A tenant or runtime override (when registered) takes precedence. F-8.6.7.")
	f.leaseDefaultBudget = flag.Int64("lease-extension-default-budget", 0,
		"§8.6 deployment-default maxExtendableBudget (Helm leaseExtension.defaults.maxExtendableBudget): the token ceiling a delegation tree may extend to absent a tenant or runtime override. Zero registers root trees with no token-extension headroom (every ExtendLease returns CEILING_REACHED). F-15.3.5.")
	f.leaseMaxBudget = flag.Int64("lease-extension-max-budget", 0,
		"§8.6 absolute maxExtendableBudget ceiling (Helm leaseExtension.max.maxExtendableBudget) no tenant or runtime override may exceed. Zero leaves the deployment default uncapped. F-15.3.5.")
	f.leaseDefaultApproval = flag.String("lease-extension-default-approval", "",
		"§8.6 deployment-default extensionApproval mode (auto | elicitation). Empty resolves to the spec default (elicitation). F-15.3.5.")
	f.leaseCoolOffSec = flag.Int("lease-extension-cooloff-seconds", 0,
		"§8.6 deployment-default coolOffSeconds: the post-approval window during which further extensions auto-grant without re-elicitation. Zero applies the spec default (5s). F-15.3.5.")
	f.leaseRejectionCoolOffSec = flag.Int("lease-extension-rejection-cooloff-seconds", 0,
		"§8.6 deployment-default rejectionCoolOffSeconds: after a denial, the requesting subtree's extensions auto-reject for this long. Zero applies the spec default (300s). F-15.3.5.")
	f.tokenAnomalyZeroWindow = flag.Int("token-anomaly-zero-window", envInt("LENNY_TOKEN_ANOMALY_ZERO_WINDOW", tokenanomaly.DefaultZeroTokenWindow),
		"§11.2 direct-mode token-usage integrity: the count of consecutive zero-token direct-mode ReportUsage pulls a session must exceed before the gateway raises lenny_gateway_token_usage_anomaly_total{reason=\"zero_delta\"}. A non-zero pull resets the run. §11.2 fixes the default at greater than 3; a non-positive value falls back to the default so the primary under-reporting signal is never disabled. Override via LENNY_TOKEN_ANOMALY_ZERO_WINDOW.")
	f.tokenAnomalyImplausibleRatio = flag.Float64("token-anomaly-implausible-ratio", envFloat("LENNY_TOKEN_ANOMALY_IMPLAUSIBLE_RATIO", tokenanomaly.DefaultImplausiblySmallRatio),
		"§11.2 direct-mode token-usage integrity: the cumulative tokens-per-LLM-call ratio below which the gateway raises lenny_gateway_token_usage_anomaly_total{reason=\"implausibly_small\"} for a direct-mode session. §11.2 leaves the numeric value operator-tunable; the default flags a session averaging fewer than one token per call. A non-positive value disables the ratio branch (the zero-token window still fires). Override via LENNY_TOKEN_ANOMALY_IMPLAUSIBLE_RATIO.")
	f.directUsagePollIntervalSeconds = flag.Int("direct-usage-poll-interval-seconds", envInt("LENNY_DIRECT_USAGE_POLL_INTERVAL_SECONDS", defaultDirectUsagePollIntervalSeconds),
		"§11.2 direct-mode usage: cadence (seconds) at which the gateway pulls each direct-delivery session's incremental token delta over the §4.7 ReportUsage RPC and fans it into the usage/quota accounting sinks and the §11.2 under-reporting anomaly detector. §8.3 bounds direct-mode over-run against this interval, and §6.2 idle detection resets a session's idle clock only on a non-zero pulled delta, so a hung pod still idle-terminates. A value below the 10s minimum is clamped up; a non-positive value selects the 30s default. Override via LENNY_DIRECT_USAGE_POLL_INTERVAL_SECONDS.")
	f.spiffeTrustDomain = flag.String("spiffe-trust-domain", os.Getenv("LENNY_SPIFFE_TRUST_DOMAIN"),
		"§10.3 NET-060 SPIFFE trust domain (global.spiffeTrustDomain). When set together with --adapter-ca, the §8.6 GatewayControl listener validates each inbound pod certificate's spiffe://<trust-domain>/agent/{pool}/{pod} URI SAN at TLS handshake and rejects a foreign trust domain, a non-agent identity, or a revoked certificate with no gRPC response (logged pod_identity_mismatch). Empty disables SPIFFE peer validation (local development only).")
	f.interceptorNamespaces = flag.String("interceptor-namespaces", os.Getenv("LENNY_INTERCEPTOR_NAMESPACES"),
		"§10.3 NET-063 comma-separated gateway.interceptorNamespaces allowlist. When --spiffe-trust-domain is set and a §4.8 external interceptor endpoint resolves to an in-cluster Service (a .svc host), the gateway validates the interceptor certificate's spiffe://<trust-domain>/interceptor/{namespace}/{pod} URI SAN on every outbound handshake and rejects the connection unless {namespace} is in this list, the trust domain matches, and the certificate is not on the §10.3 deny list. The endpoint host is pinned as tls.Config.ServerName so a co-located non-interceptor pod's certificate fails DNS-SAN verification. Empty accepts any namespace in the trust domain.")
	f.adminSATokenAudience = flag.String("admin-sa-token-audience", envOr("LENNY_ADMIN_SA_TOKEN_AUDIENCE", "lenny-gateway"),
		"§25.4 audience of the projected ServiceAccount token a platform service account presents to the admin API. §25.4 has lenny-ops call the admin API as a regular authenticated client using a dedicated service account with the platform-admin role; the credential it presents is the projected token the chart mounts, which the gateway validates with a Kubernetes TokenReview bound to this audience. Empty disables admin-API service-account authentication, leaving the admin API reachable by Lenny-minted bearers only.")
	f.saTokenAudience = flag.String("sa-token-audience", os.Getenv("LENNY_SA_TOKEN_AUDIENCE"),
		"§10.3 deployment-specific projected-SA-token audience (global.saTokenAudience, formatted lenny-gateway-<cluster-name>). When set, the §8.6 GatewayControl listener validates the audience claim of the projected SA token presented as the authorization bearer header on every pod→gateway request and rejects a token whose aud claim does not include this value (cross-deployment replay protection, the SA-token layer of the §10.3 defense-in-depth chain). Empty disables the SA-token audience check (local development only).")
	f.llmProxyPublicURL = flag.String("llm-proxy-public-url", os.Getenv("LENNY_LLM_PROXY_PUBLIC_URL"),
		"§4.9 public HTTPS URL agent pods dial to reach the LLM reverse proxy (e.g. https://lenny-proxy.svc:8443). Required for the §4.9 Pre-Authorized Credential Flow's user-source delivery: a user-registered credential is fronted through the proxy in proxy mode, so the gateway writes this URL into the pod's proxy-mode lease. Empty disables user-source credential resolution (sessions fall through to pool per the credentialPolicy fallback).")
	f.llmSemanticCache = flag.Bool("llm-semantic-cache", os.Getenv("LENNY_LLM_SEMANTIC_CACHE") == "1",
		"§4.9 enable the in-process semantic cache on the LLM proxy path. Caching stays disabled by default and is opt-in per pool via the pool's cachePolicy; this flag provisions the in-memory backend the per-pool policy draws on. The Redis-backed backend is wired separately.")
	// spec: §17.9.3 — the canonical objectStorage.provider selector
	// picks the §12.5 ArtifactStore backend (minio | s3 | gcs | azure,
	// plus the dev-only in-memory store). Empty defaults to minio: the
	// MinIO backend when --minio-endpoint is set, otherwise the
	// in-memory store. The cloud backends consume the shared
	// objectStorage.{bucket,region,accountUrl} values. F-17.5.1 /
	// F-12.7.3.
	f.objectStorageProvider = flag.String("object-storage-provider", os.Getenv("LENNY_OBJECT_STORAGE_PROVIDER"),
		"§17.9.3 object-storage backend: minio | s3 | gcs | azure. Empty defaults to minio (MinIO when --minio-endpoint is set, else in-memory). Override via LENNY_OBJECT_STORAGE_PROVIDER. F-17.5.1.")
	f.objectStorageBucket = flag.String("object-storage-bucket", os.Getenv("LENNY_OBJECT_STORAGE_BUCKET"),
		"§17.9.3 objectStorage.bucket: the S3 bucket, GCS bucket, or Azure container for --object-storage-provider in {s3,gcs,azure}. Override via LENNY_OBJECT_STORAGE_BUCKET. F-17.5.1.")
	f.objectStorageRegion = flag.String("object-storage-region", os.Getenv("LENNY_OBJECT_STORAGE_REGION"),
		"§17.9.3 objectStorage.region: the AWS region for --object-storage-provider=s3. Empty falls back to the AWS SDK default chain (AWS_REGION). Override via LENNY_OBJECT_STORAGE_REGION. F-17.5.1.")
	f.objectStorageAccountURL = flag.String("object-storage-account-url", os.Getenv("LENNY_OBJECT_STORAGE_ACCOUNT_URL"),
		"§17.9.3 Azure Blob storage account URL (https://<account>.blob.core.windows.net) for --object-storage-provider=azure. Override via LENNY_OBJECT_STORAGE_ACCOUNT_URL. F-17.5.1.")
	f.objectStorageFilesystemRoot = flag.String("object-storage-filesystem-root", os.Getenv("LENNY_OBJECT_STORAGE_FILESYSTEM_ROOT"),
		"§17.4 local-filesystem object-storage directory for --object-storage-provider=filesystem (e.g. ~/.lenny/artifacts/). Persists artifacts across a restart. Override via LENNY_OBJECT_STORAGE_FILESYSTEM_ROOT. F-17.4.8.")
	f.minioEndpoint = flag.String("minio-endpoint", os.Getenv("LENNY_MINIO_ENDPOINT"),
		"MinIO endpoint (host:port). When set, the §4.5 artifact store is the MinIO-backed blob store; the drain-readiness endpoint runs a real §12.5 bucket probe. When empty, an in-memory blob store is used.")
	f.minioAccessKey = flag.String("minio-access-key", os.Getenv("LENNY_MINIO_ACCESS_KEY"),
		"MinIO access key. Required when --minio-endpoint is set.")
	f.minioSecretKey = flag.String("minio-secret-key", os.Getenv("LENNY_MINIO_SECRET_KEY"),
		"MinIO secret key. Required when --minio-endpoint is set.")
	f.minioBucket = flag.String("minio-bucket", os.Getenv("LENNY_MINIO_BUCKET"),
		"MinIO bucket for §4.5 artifacts. Required when --minio-endpoint is set.")
	// spec: §12.5 — "MinIO connections MUST use TLS". TLS is on
	// by default; only §17.4 Embedded Mode (the chart's backends:embedded
	// posture) renders LENNY_MINIO_USE_SSL=false. The Helm chart fails the
	// render when tls.enabled is false on any non-embedded backend.
	f.minioUseSSL = flag.Bool("minio-use-ssl", envFlagDefault("LENNY_MINIO_USE_SSL", true),
		"connect to MinIO over HTTPS. Defaults to true per §12.5; only §17.4 Embedded Mode disables it. Override via LENNY_MINIO_USE_SSL.")
	f.artifactReplicationConfig = flag.String("artifact-replication-config", os.Getenv("LENNY_ARTIFACT_REPLICATION_CONFIG"),
		"§25.11 minio.artifactBackup configuration as JSON (enabled, regions[].{region,sourceBucket,dataResidencyRegion,target{endpoint,bucket,accessCredentialSecret}}). When set and enabled, the gateway runs the §12.5 ArtifactStore cross-region replication residency preflight. Empty (the default) disables it. Override via LENNY_ARTIFACT_REPLICATION_CONFIG.")
	f.artifactReplicationRoleARN = flag.String("artifact-replication-role-arn", os.Getenv("LENNY_ARTIFACT_REPLICATION_ROLE_ARN"),
		"§25.11 replication Role ARN the source MinIO cluster requires on a replication rule (arn:minio:replication or arn:aws:iam form). Optional. Override via LENNY_ARTIFACT_REPLICATION_ROLE_ARN.")
}

// registerArtifactFlags registers the object-storage, artifact, GC, leader-election, KMS-probe, and watchdog-deadline flags.
//
// spec: §4.1 gateway subsystem seams.
func (f *gatewayFlags) registerArtifactFlags() {
	// spec: §12.5 — gc.cycleIntervalSeconds (default 900, min 60).
	// Drives both the §7.1 retention sweep and the §12.5 hard-prune
	// sweep cadence. A value below the floor is clamped up to 60s.
	f.gcCycleIntervalSeconds = flag.Int("gc-cycle-interval-seconds",
		envInt("LENNY_GC_CYCLE_INTERVAL_SECONDS", int(retentiongc.DefaultSweepInterval/time.Second)),
		"§12.5 gc.cycleIntervalSeconds: the leader-elected GC sweep cadence in seconds (default 900, minimum 60). Drives the §7.1 retention soft-delete sweep and the §12.5 tombstone hard-prune sweep. Override via LENNY_GC_CYCLE_INTERVAL_SECONDS.")
	// spec: §12.5 — gc.tombstoneRetentionSeconds (default 86400).
	// The soft-deleted artifact_store row's tombstone-retention window before
	// the hard-prune sweep physically removes it.
	f.gcTombstoneRetentionSeconds = flag.Int("gc-tombstone-retention-seconds",
		envInt("LENNY_GC_TOMBSTONE_RETENTION_SECONDS", int(retentiongc.DefaultTombstoneRetention/time.Second)),
		"§12.5 gc.tombstoneRetentionSeconds: how long a soft-deleted artifact_store row is retained before the hard-prune sweep removes it, in seconds (default 86400 / 24h). Operators may raise it without affecting GC correctness. Override via LENNY_GC_TOMBSTONE_RETENTION_SECONDS.")
	f.maxDeadlockWaitSeconds = flag.Int("max-deadlock-wait-seconds",
		envInt("LENNY_MAX_DEADLOCK_WAIT_SECONDS", 120),
		"§8.8 maxDeadlockWaitSeconds: the grace window between a subtree deadlock detection and failing the deepest blocked tasks with DEADLOCK_TIMEOUT. Zero disables the detector. Default 120s. Override via LENNY_MAX_DEADLOCK_WAIT_SECONDS.")
	// spec: §11.3 — gateway.max{Finalizing,Ready,Starting}TimeoutSeconds
	// and the platform-wide cap on §6.2 awaiting_client_action /
	// maxSessionAge. Each is operator-tunable; the watchdog applied the
	// constructed defaults silently before. F-11.3.11.
	f.maxFinalizingTimeoutSeconds = flag.Int("max-finalizing-state-timeout-seconds",
		envInt("LENNY_MAX_FINALIZING_STATE_TIMEOUT_SECONDS", watchdog.DefaultMaxFinalizingStateSeconds),
		"§11.3 maxFinalizingTimeoutSeconds: the deadline on the `finalizing` pre-running state. A session stuck longer than this wall-clock window is transitioned to `failed`. The §6.2 invariant `maxFinalizingTimeoutSeconds ≥ runtime.setupTimeoutSeconds` is enforced at admin registration; raising this flag also raises the gateway-side cap admin uses when validating new runtimes. Default 600s. Override via LENNY_MAX_FINALIZING_STATE_TIMEOUT_SECONDS.")
	f.delegationBudgetKeyTTLSeconds = flag.Int("delegation-budget-key-ttl-seconds",
		envInt("LENNY_DELEGATION_BUDGET_KEY_TTL_SECONDS", int(treebudget.DefaultTTL/time.Second)),
		"§8.3 delegation budget key TTL: the Redis TTL on a delegation tree's budget counters, re-armed at each transition into or out of `suspended`. Startup fails unless it exceeds --max-suspended-session-seconds, so a suspended session's keys outlive its suspended lifetime. Default 691200s (8 days). Override via LENNY_DELEGATION_BUDGET_KEY_TTL_SECONDS.")
	// spec: §11.3 — grpc.keepaliveTime{,out}Ms on the
	// adapter client (gateway → pod), operator-tunable. The library
	// default is no keepalive on the client side, so the §11.3 5s timeout
	// is unenforced without this. F-11.3.12.
	f.adapterKeepaliveTimeMs = flag.Int("adapter-keepalive-time-ms",
		envInt("LENNY_ADAPTER_KEEPALIVE_TIME_MS", 10_000),
		"§11.3 grpc.keepaliveTimeMs: the interval at which the gateway sends a keepalive ping on an idle adapter connection. Default 10000ms (10s). Override via LENNY_ADAPTER_KEEPALIVE_TIME_MS.")
	f.adapterKeepaliveTimeoutMs = flag.Int("adapter-keepalive-timeout-ms",
		envInt("LENNY_ADAPTER_KEEPALIVE_TIMEOUT_MS", 5_000),
		"§11.3 grpc.keepaliveTimeoutMs: how long the gateway waits for an adapter keepalive-ping reply before closing the connection. Default 5000ms (5s). Override via LENNY_ADAPTER_KEEPALIVE_TIMEOUT_MS.")
	// spec: §11.3 — delegation.usageQuiescenceTimeoutSeconds,
	// operator-tunable. The §8.10 tree-recovery path waits this long after
	// the last usage report before declaring the tree quiescent and
	// progressing to drain, and the §8.3 budget return of a delegated child
	// waits this long after the child's terminal transition. The gateway
	// refuses to start when the value in effect is not below the seal
	// window in effect. F-11.3.19.
	f.delegationUsageQuiescenceTimeoutSeconds = flag.Int("delegation-usage-quiescence-timeout-seconds",
		envInt("LENNY_DELEGATION_USAGE_QUIESCENCE_TIMEOUT_SECONDS", 5),
		"§11.3 delegation.usageQuiescenceTimeoutSeconds: the wall-clock window the §8.10 tree-recovery path waits after the last child usage report before declaring the delegation tree quiescent, and the wall-clock window the §8.3 budget return of a delegated child waits after the child's terminal transition before it returns the child's reservation. Must be less than the seal window in effect (--workspace-seal-max-duration-seconds). Default 5s; a non-positive value selects the default. Override via LENNY_DELEGATION_USAGE_QUIESCENCE_TIMEOUT_SECONDS.")
	// spec: §8.10 — operator-tunable per-level and
	// whole-tree recovery deadlines. The default (120s / 600s) matches
	// `maxLevelRecoverySeconds` / `maxTreeRecoverySeconds`. Deployers
	// running deep trees apply the §8.10 formula to raise the
	// tree cap. F-8.10.6.
	f.delegationMaxLevelRecoverySeconds = flag.Int("delegation-max-level-recovery-seconds",
		envInt("LENNY_DELEGATION_MAX_LEVEL_RECOVERY_SECONDS", int(recovery.DefaultLevelTimeout/time.Second)),
		"§8.10 delegation.maxLevelRecoverySeconds: maximum time the gateway waits for all nodes at a single tree depth to complete recovery before marking each unrecovered node at that depth that is still in resume_pending as terminally failed. Default 120s. Override via LENNY_DELEGATION_MAX_LEVEL_RECOVERY_SECONDS.")
	f.delegationMaxTreeRecoverySeconds = flag.Int("delegation-max-tree-recovery-seconds",
		envInt("LENNY_DELEGATION_MAX_TREE_RECOVERY_SECONDS", int(recovery.DefaultTreeTimeout/time.Second)),
		"§8.10 delegation.maxTreeRecoverySeconds: total wall-clock bound for recovering the full delegation tree; overrides per-level budgets. Default 600s. Deployers running deep trees should apply the §8.10 formula. Override via LENNY_DELEGATION_MAX_TREE_RECOVERY_SECONDS.")
	// spec: §8.10 — cascadeTimeoutSeconds is the deployer-tuned
	// wall-clock bound an `await_completion` child may run after parent
	// failure and how long a `detach` orphan persists before cleanup.
	// F-8.10.9.
	f.delegationCascadeTimeoutSeconds = flag.Int("delegation-cascade-timeout-seconds",
		envInt("LENNY_DELEGATION_CASCADE_TIMEOUT_SECONDS", int(orphancleanup.DefaultCascadeTimeout/time.Second)),
		"§8.10 delegation.cascadeTimeoutSeconds: deployer-wide cap on how long an `await_completion` child may run after parent failure and how long a `detach` orphan persists before §8.10 cleanup. Default 3600s (1h). Override via LENNY_DELEGATION_CASCADE_TIMEOUT_SECONDS.")
	// spec: §8.10 — maxOrphanTasksPerTenant caps a tenant's
	// active orphan tasks; when exceeded, the `detach` cascade falls back
	// to `cancel_all`. The §16.5 OrphanTasksPerTenantHigh alert reads
	// `scalar(lenny_max_orphan_tasks_per_tenant)` as the denominator;
	// publishing the configured value at startup makes the alert evaluate
	// against the live cap. F-8.10.10.
	f.delegationMaxOrphanTasksPerTenant = flag.Int("delegation-max-orphan-tasks-per-tenant",
		envInt("LENNY_DELEGATION_MAX_ORPHAN_TASKS_PER_TENANT", sessionserver.DefaultMaxOrphanTasksPerTenant),
		"§8.10 delegation.maxOrphanTasksPerTenant: per-tenant cap on active orphan tasks; when the count would exceed this, the gateway falls back from `detach` to `cancel_all`. Default 100. Override via LENNY_DELEGATION_MAX_ORPHAN_TASKS_PER_TENANT.")
	// spec: §11.3 — credentials.expiryWarningLeadSeconds,
	// operator-tunable. Each tracked credential lease fires a structured
	// expiry-warning log line once when now is within this window of the
	// lease's ExpiresAt, so deployers see impending expiry before the
	// §4.9 fault-rotation path is consumed. F-11.3.20.
	f.credentialsExpiryWarningLeadSeconds = flag.Int("credentials-expiry-warning-lead-seconds",
		envInt("LENNY_CREDENTIALS_EXPIRY_WARNING_LEAD_SECONDS", int(credrenewal.DefaultExpiryWarningLead/time.Second)),
		"§11.3 credentials.expiryWarningLeadSeconds: how long before a credential lease's ExpiresAt the gateway fires a structured warning log. Set to 0 to disable. Default 3600 (1h). Override via LENNY_CREDENTIALS_EXPIRY_WARNING_LEAD_SECONDS.")
	f.workspaceSealMaxDurationSeconds = flag.Int("workspace-seal-max-duration-seconds",
		envInt("LENNY_WORKSPACE_SEAL_MAX_DURATION_SECONDS", int(sessionserver.DefaultWorkspaceSealMaxDuration/time.Second)),
		"§7.1 maxWorkspaceSealDurationSeconds: the total wall-clock window the gateway retries seal-and-export (exponential backoff 5s→60s) before failing the session with workspace_seal_timeout and terminating the pod anyway. Default 300s. Override via LENNY_WORKSPACE_SEAL_MAX_DURATION_SECONDS.")
	f.idempotencyGCIntervalSeconds = flag.Int("idempotency-gc-interval-seconds",
		envInt("LENNY_IDEMPOTENCY_GC_INTERVAL_SECONDS", 3600),
		"§11.5 idempotency_keys TTL garbage-collection cadence. The sweeper iterates tenants and drops rows past the 24-hour retention window every interval. Default 3600s (one hour). Lower values reduce row backlog at the cost of more frequent Postgres scans; higher values keep expired rows up to the configured interval past TTL (read-time gate masks them from clients). Override via LENNY_IDEMPOTENCY_GC_INTERVAL_SECONDS.")
	f.issuedTokenGCIntervalSeconds = flag.Int("issued-token-gc-interval-seconds",
		envInt("LENNY_ISSUED_TOKEN_GC_INTERVAL_SECONDS", 3600),
		"§12.2 issued_tokens expired-row garbage-collection cadence. Every interval the gateway deletes, for every tenant including a deleted one, the issued-token rows whose exp precedes the §13.3 trailing hour; user erasure and tenant deletion keep the rows still inside that hour. Default 3600s (one hour). Override via LENNY_ISSUED_TOKEN_GC_INTERVAL_SECONDS.")
	f.credentialLeaseGCIntervalSeconds = flag.Int("credential-lease-gc-interval-seconds",
		envInt("LENNY_CREDENTIAL_LEASE_GC_INTERVAL_SECONDS", 3600),
		"§4.9 credential_leases expired-lease sweep cadence. Every interval the gateway deletes lease rows past ExpiresAt (bounding the credential_leases table) and then reconciles this replica's deny list, dropping a credential's deny entry only when the store definitively reports no remaining active lease for it (failing closed on a store error). Runs on every replica so each replica's in-memory deny list stays bounded within its lifetime. Default 3600s (one hour). Override via LENNY_CREDENTIAL_LEASE_GC_INTERVAL_SECONDS.")
	f.objectStorageGCSBucketDefaultCMEK = flag.String("object-storage-gcs-bucket-default-cmek", os.Getenv("LENNY_OBJECT_STORAGE_GCS_BUCKET_DEFAULT_CMEK"),
		"§12.5 objectStorage.gcs.bucketDefaultCmek: the per-tenant GCS bucket-default CMEK resource name that a T4 checkpoint PUT inherits (the GCS V4 signed URL cannot carry a per-request CMEK). Required when --object-storage-provider=gcs serves a workspaceTier T4 tenant. Override via LENNY_OBJECT_STORAGE_GCS_BUCKET_DEFAULT_CMEK.")
	f.objectStorageAzureDefaultEncryptionScope = flag.String("object-storage-azure-default-encryption-scope", os.Getenv("LENNY_OBJECT_STORAGE_AZURE_DEFAULT_ENCRYPTION_SCOPE"),
		"§12.5 objectStorage.azure.defaultEncryptionScope: the Azure container-level default encryption scope a T4 chunk PUT lands under (the SAS carries no encryption scope). Required with --object-storage-azure-deny-encryption-scope-override when --object-storage-provider=azure serves a workspaceTier T4 tenant. Override via LENNY_OBJECT_STORAGE_AZURE_DEFAULT_ENCRYPTION_SCOPE.")
	f.objectStorageAzureDenyEncryptionScopeOverride = flag.Bool("object-storage-azure-deny-encryption-scope-override", envFlagDefault("LENNY_OBJECT_STORAGE_AZURE_DENY_ENCRYPTION_SCOPE_OVERRIDE", false),
		"§12.5 objectStorage.azure.denyEncryptionScopeOverride: assert the T4 container sets DenyEncryptionScopeOverride so a chunk PUT cannot land under any other scope. Required with --object-storage-azure-default-encryption-scope when --object-storage-provider=azure serves a workspaceTier T4 tenant. Override via LENNY_OBJECT_STORAGE_AZURE_DENY_ENCRYPTION_SCOPE_OVERRIDE.")
	f.noEnvPolicy = flag.String("no-environment-policy", os.Getenv("LENNY_NO_ENVIRONMENT_POLICY"),
		"§10.6 platform-wide noEnvironmentPolicy (deny-all or allow-all). Required outside --dev-mode.")
	f.connectorOAuthClientSecretKey = flag.String("connector-oauth-client-secret-key", envOr("LENNY_CONNECTOR_OAUTH_CLIENT_SECRET_KEY", connectorsecret.DefaultSecretKey),
		"§9.3 Kubernetes Secret data key the confidential-client resolver reads when a connector's auth.clientSecretRef names only namespace/name. A three-segment namespace/name/key reference overrides it per connector. Override via LENNY_CONNECTOR_OAUTH_CLIENT_SECRET_KEY.")
}

// registerOpsFlags registers the ops, playground, admin-token, memory, and remaining startup flags.
//
// spec: §4.1 gateway subsystem seams.
func (f *gatewayFlags) registerOpsFlags() {
	f.billingCorrectionRateThreshold = flag.Float64("billing-correction-rate-threshold", envFloat("LENNY_BILLING_CORRECTION_RATE_THRESHOLD", 0.05),
		"§11.2.1 billing.correctionRateThreshold: BillingCorrectionRateHigh alert threshold as a fraction (0.05 = 5%). Emitted at startup on the lenny_billing_correction_rate_threshold gauge so the §16.5 alert can evaluate via scalar(lenny_billing_correction_rate_threshold). Override via LENNY_BILLING_CORRECTION_RATE_THRESHOLD.")
	f.billingSinkWebhookURL = flag.String("billing-sink-webhook-url", os.Getenv("LENNY_BILLING_SINK_WEBHOOK_URL"),
		"§11.2.1 billing.sink webhook URL: when set, every billing event committed to Postgres is POSTed as JSON with an HMAC-SHA256 X-Lenny-Signature header, retried with exponential backoff, and dead-lettered after exhaustion. Empty disables the webhook sink. Override via LENNY_BILLING_SINK_WEBHOOK_URL. F-11.2.14.")
	// §11.2.1 HMAC signing secrets are read from the environment only (a
	// Helm secretKeyRef) so they never appear in the process argv.
	f.billingSinkWebhookSecret = []byte(os.Getenv("LENNY_BILLING_SINK_WEBHOOK_SECRET"))
	f.billingApproverWebhookSecret = []byte(os.Getenv("LENNY_BILLING_APPROVER_WEBHOOK_SECRET"))
	f.billingRetentionDays = flag.Int("billing-retention-days", envInt("LENNY_BILLING_RETENTION_DAYS", billingretention.DefaultRetentionDays),
		"§11.2.1 billing.retentionDays: how long billing events are retained before the periodic retention pruner deletes them (default 395). The gateway rejects a value below the compliance floor of any tenant's regulated complianceProfile at startup (hipaa 2190, soc2 365, fedramp 365). Override via LENNY_BILLING_RETENTION_DAYS.")
	f.gdprRetentionDays = flag.Int("audit-gdpr-retention-days", envInt("LENNY_AUDIT_GDPR_RETENTION_DAYS", audit.GDPRRetentionDefaultDays),
		"§12.8 audit.gdprRetentionDays: how long gdpr.* audit rows (erasure receipts, legal-hold ledger events) are retained, on a window separate from audit.retentionDays (default 2555 / 7 years). The gateway rejects a value below 2190 (6 years) when any tenant has a regulated complianceProfile (soc2, fedramp, hipaa) at startup. Override via LENNY_AUDIT_GDPR_RETENTION_DAYS.")
	f.auditRetentionPreset = flag.String("audit-retention-preset", envOr("LENNY_AUDIT_RETENTION_PRESET", string(audit.PresetSOC2)),
		"§16.4 audit.retentionPreset: the compliance-aware retention bundle for non-gdpr audit rows (soc2, fedramp-high, hipaa, nis2-dora, custom). A named preset fixes the retention window (soc2 365, fedramp-high 1095, hipaa 2190, nis2-dora 1825); custom uses --audit-retention-days. The gateway warns at startup when the preset is incompatible with an active tenant's complianceProfile. Override via LENNY_AUDIT_RETENTION_PRESET.")
	f.auditRetentionDays = flag.Int("audit-retention-days", envInt("LENNY_AUDIT_RETENTION_DAYS", audit.PresetSOC2.PresetDays()),
		"§16.4 audit.retentionDays: the general (non-gdpr) Postgres audit-log retention window in days, used when --audit-retention-preset is custom. Emitted at startup on the lenny_audit_retention_days gauge so the §16.5 AuditRetentionLow alert can evaluate. Override via LENNY_AUDIT_RETENTION_DAYS.")
	f.auditRetentionPruneIntervalSeconds = flag.Int("audit-retention-prune-interval-seconds", envInt("LENNY_AUDIT_RETENTION_PRUNE_INTERVAL_SECONDS", 3600),
		"§16.4 audit-retention sweep cadence in seconds: how often the leader-elected pruner deletes audit rows past audit.retentionDays (gdpr.* rows held under audit.gdprRetentionDays, undelivered rows held by the SIEM delivery guard). Clamped up to a 60s floor. Override via LENNY_AUDIT_RETENTION_PRUNE_INTERVAL_SECONDS.")
	// §12.6 EventBus retranscribe worker and publish-drop knobs. These mirror the eventBus.* Helm values; the
	// gateway consumes them when constructing the §12.6 RedisEventBus and
	// the leader-elected retranscribe worker. F-12.6.22 / F-12.6.23.
	f.eventBusRetryIntervalSeconds = flag.Int("eventbus-retry-interval-seconds", envInt("LENNY_EVENTBUS_RETRY_INTERVAL_SECONDS", 60),
		"§12.6 eventBus.retryInterval: the retranscribe-worker sweep cadence in seconds (default 60). The worker re-publishes audit rows whose first EventBus publish failed. Override via LENNY_EVENTBUS_RETRY_INTERVAL_SECONDS.")
	f.eventBusMaxRetryAttempts = flag.Int("eventbus-max-retry-attempts", envInt("LENNY_EVENTBUS_MAX_RETRY_ATTEMPTS", 5),
		"§12.6 eventBus.maxRetryAttempts: the retry_count ceiling above which a failed-publish audit row stops being swept and needs a manual republish (default 5). Override via LENNY_EVENTBUS_MAX_RETRY_ATTEMPTS.")
	f.eventBusDuplicateInjectionFactor = flag.Int("eventbus-duplicate-injection-factor", envInt("LENNY_EVENTBUS_DUPLICATE_INJECTION_FACTOR", 1),
		"§12.6 eventBus.duplicateInjectionFactor: a test-only staging knob; the EventBus re-sends every successful publish this many times so the dedup integration test can assert no doubled side effects. 1 (the default) disables injection. Override via LENNY_EVENTBUS_DUPLICATE_INJECTION_FACTOR.")
	f.eventBusDropAlertThreshold = flag.Int("eventbus-drop-alert-threshold", envInt("LENNY_EVENTBUS_DROP_ALERT_THRESHOLD", 10),
		"§12.6 eventBus.dropAlertThreshold: the per-minute dropped-publish rate above which the §16.5 EventBusPublishDropped alert fires (default 10/min). Emitted at startup on the lenny_event_bus_drop_alert_threshold gauge the alert reads via scalar(...). Override via LENNY_EVENTBUS_DROP_ALERT_THRESHOLD.")
	f.capabilityTokenTTL = flag.Int("capability-token-ttl-seconds", envInt("LENNY_CAPABILITY_TOKEN_TTL_SECONDS", int(jwt.DefaultCapabilityTokenTTL/time.Second)),
		"§10.2 auth.capabilityTokenTtlSeconds: the default lifetime of the MCP bearer tokens minted by POST /v1/playground/token (bounded 120..3600; startup is fatal outside the bound). Override via LENNY_CAPABILITY_TOKEN_TTL_SECONDS.")
	f.maxSessionsPerReplica = flag.Int("max-sessions-per-replica", envInt("LENNY_MAX_SESSIONS_PER_REPLICA", 50),
		"§4.1 gateway.maxSessionsPerReplica: per-replica capacity ceiling used as the denominator of the GatewaySessionBudgetNearExhaustion alert (§16.5) and the §17.8.2 SCL-036 burst-absorption minReplicas formula. Provisional Tier defaults: 50 (Tier 1), 200 (Tier 2), 400 (Tier 3). Emitted at startup on the lenny_gateway_max_sessions_per_replica gauge. Override via LENNY_MAX_SESSIONS_PER_REPLICA.")
	// §4.1 / §16.5: scalar gauges read by the GatewayNoHealthyReplicas
	// and GatewayActiveStreamsHigh alert expressions in
	// pkg/alerting/rules/rules.go. The gateway emits them at startup so
	// the scalar(...) lookups in the alert rules resolve to a real
	// value instead of NaN.
	f.minReplicas = flag.Int("min-replicas", envInt("LENNY_MIN_REPLICAS", 1),
		"§4.1 / §16.5 gateway HPA minReplicas floor (§17.8.2 SCL-036). Emitted at startup on the lenny_gateway_min_replicas gauge so the GatewayNoHealthyReplicas alert (§16.5) can evaluate via scalar(lenny_gateway_min_replicas). Override via LENNY_MIN_REPLICAS.")
	f.streamCeiling = flag.Int("stream-ceiling", envInt("LENNY_STREAM_CEILING", 100),
		"§4.1 / §16.5 per-replica streaming-connection ceiling. Emitted at startup on the lenny_gateway_stream_ceiling gauge so the GatewayActiveStreamsHigh alert (§16.5) can evaluate via scalar(lenny_gateway_stream_ceiling). Override via LENNY_STREAM_CEILING.")
	// spec: §16.5 KMSSignQuotaApproaching; §17.8.2 "KMS Sign rate" —
	// the configured provider asymmetric Sign quota (kms.signQuotaPerSecond).
	f.kmsSignQuotaPerSecond = flag.Int("kms-sign-quota-per-second", envInt("LENNY_KMS_SIGN_QUOTA_PER_SECOND", 0),
		"§16.5 / §17.8.2 provider asymmetric KMS Sign quota per second (kms.signQuotaPerSecond). Emitted at startup on the lenny_kms_sign_quota_per_second gauge so the KMSSignQuotaApproaching alert can evaluate via scalar(...). 0 means unset, which keeps the alert silent. Override via LENNY_KMS_SIGN_QUOTA_PER_SECOND.")
	// spec: §10.4 / §16.5 PDBBlockedEvictions — the §10.4 PDB
	// status poller addresses the gateway's PodDisruptionBudget object
	// by namespace+name. The chart sets --gateway-namespace from the
	// release namespace and --gateway-pdb-name to `lenny-gateway`.
	// F-10.4.4.
	f.gatewayNamespace = flag.String("gateway-namespace", envOr("LENNY_GATEWAY_NAMESPACE", os.Getenv("POD_NAMESPACE")),
		"§10.4 namespace holding the gateway PodDisruptionBudget for the periodic poller (defaults to POD_NAMESPACE). Override via LENNY_GATEWAY_NAMESPACE.")
	f.gatewayServiceName = flag.String("gateway-service-name", envOr("LENNY_GATEWAY_SERVICE_NAME", "lenny-gateway"),
		"§12.4 name of the gateway Service whose Endpoints object the fail-open cached_replica_count poller reads. Override via LENNY_GATEWAY_SERVICE_NAME.")
	// spec: §9.4 — `memory.maxMemoriesPerUser` (default
	// 10,000) bounds the per-user record count; a Write that exceeds
	// it evicts the oldest by created_at. F-9.4.5.
	f.memoryMaxPerUser = flag.Int("memory-max-per-user", envInt("LENNY_MEMORY_MAX_PER_USER", memorystore.DefaultMaxMemoriesPerUser),
		"§9.4 per-user memory cap before oldest-first eviction. Override via LENNY_MEMORY_MAX_PER_USER.")
	// spec: §9.4 / §12.8 — `memory.enabled=false`
	// is the escape hatch that disables the MemoryStore entirely;
	// the lenny/memory_write and lenny/memory_query MCP tools are
	// not registered, the §12.8 erasure preflight is skipped, and
	// no `agent_memory` rows are written. Default true. F-9.4.7.
	f.memoryEnabled = flag.Bool("memory-enabled", envFlagDefault("LENNY_MEMORY_ENABLED", true),
		"§9.4 / §12.8 MemoryStore feature flag. false disables the lenny/memory_* MCP tools and skips the preflight. Override via LENNY_MEMORY_ENABLED.")
	// spec: §9.4 / §16.1 — periodic sampler for the
	// `lenny_memory_store_record_count` gauge. The store-specific
	// implementation walks tenants and emits the per-tenant count.
	// Default 60s aligns with the §16.5 alert windows; zero disables.
	f.memoryRecordCountInterval = flag.Duration("memory-record-count-interval", envDuration("LENNY_MEMORY_RECORD_COUNT_INTERVAL", 60*time.Second),
		"§9.4 / §16.1 periodic sampler interval for the MemoryStore record-count gauge. 0 disables. Override via LENNY_MEMORY_RECORD_COUNT_INTERVAL.")
	// spec: §4.2 — LENNY_POOLER_MODE names the deployment
	// posture for the Postgres pooler. The gateway honours the value
	// at the application layer (logging it at startup so operators can
	// confirm the deployment posture); the load-bearing enforcement
	// is the migration 0057 lenny_tenant_guard trigger that rejects
	// the __all__ sentinel unless pgtenant.InAllTenants opts in via
	// the lenny.allow_all_sentinel session GUC.
	f.poolerMode = flag.String("pooler-mode", envOr("LENNY_POOLER_MODE", "transactional"),
		"§4.2 deployment posture for the Postgres pooler. `transactional` is the chart-managed in-cluster default; `external` names an out-of-process / managed pooler (RDS Proxy, Cloud SQL Auth Proxy with pgBouncer, etc.). The value is logged at startup; the underlying __all__ sentinel guard is enforced by the lenny_tenant_guard trigger via the lenny.allow_all_sentinel GUC.")
	// §4 / §17.5 KMS provider selector. The cloud adapters
	// (pkg/kms/{aws,gcp,azure}) reach the gateway through these
	// flags. spec: F-4.3.11 / F-10.2.11 / F-17.5.2.
	f.kmsOpts, f.kmsFinalize = providerflags.Bind(flag.CommandLine, os.Getenv,
		providerflags.Options{Provider: providerflags.ProviderLocal})
	// The tick of the gateway-hosted §12.8 tenant-deletion runner. Each
	// tick advances every in-flight deletion by one phase. The
	// specification fixes no cadence, so the 30s default is
	// operator-tunable.
	f.tenantDeletionInterval = flag.Duration("tenant-deletion-interval", envDuration("LENNY_TENANT_DELETION_INTERVAL", 30*time.Second),
		"§12.8 tenant-deletion runner tick. Each tick advances every in-flight tenant deletion by one phase. Default 30s. Override via LENNY_TENANT_DELETION_INTERVAL.")
	// The bound on each §12.8 lifecycle drain a background tick runs, such
	// as the tenant-deletion Phase 2 drain. A drain that exceeds it stops
	// between sessions and counts as a drain failure, so the job stays in
	// its phase and the next tick re-runs the drain. The specification
	// fixes no bound, so the 10-minute default is operator-tunable.
	f.lifecycleTickBudget = flag.Duration("lifecycle-tick-budget", envDuration("LENNY_LIFECYCLE_TICK_BUDGET", 10*time.Minute),
		"§12.8 bound on each lifecycle drain a background tick runs (the tenant-deletion Phase 2 drain). A drain that exceeds it is a drain failure retried on the next tick. Must be positive. Default 10m. Override via LENNY_LIFECYCLE_TICK_BUDGET.")
	// The number of erasure jobs the gateway leader's erasure runner runs
	// at once, with at most one per tenant. Each run holds one pooled
	// connection at a time and waits for the salt lock on a connection
	// outside the pool, so the limit carries no rule tied to the pool's
	// size. The specification fixes no limit, so the default of 2 is
	// operator-tunable.
	f.erasureMaxConcurrentJobs = flag.Int("erasure-max-concurrent-jobs", envInt("LENNY_ERASURE_MAX_CONCURRENT_JOBS", 2),
		"§12.8 number of user-erasure jobs the gateway leader runs at once, at most one per tenant. Must be at least 1. Default 2. Override via LENNY_ERASURE_MAX_CONCURRENT_JOBS.")
}
