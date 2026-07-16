// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/audit/pgaudit"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/cataloging"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	blobproviderflags "github.com/lennylabs/lenny/pkg/blobstore/providerflags"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingretention"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingsink"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover/redisstream"
	billingpg "github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention"
	checkpointretentionpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	connectorpg "github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	coordleasepg "github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	credleasepg "github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	transcriptpg "github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	userpg "github.com/lennylabs/lenny/pkg/gateway/environment/userstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	evalpg "github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	experimentpg "github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	treearchivepg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/capacityplanning"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore/cachingstore"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/drainreadiness"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	ratelimitredis "github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	storagequotaredis "github.com/lennylabs/lenny/pkg/gateway/quota/storagequota/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	poolpg "github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride"
	capoverridepg "github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	interactionpg "github.com/lennylabs/lenny/pkg/gateway/session/interactionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	memorypg "github.com/lennylabs/lenny/pkg/gateway/session/memorystore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/recycle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionlogstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	leasepg "github.com/lennylabs/lenny/pkg/gateway/storage/leasestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
	"github.com/lennylabs/lenny/pkg/gateway/storage/redistopology"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/pkg/gateway/storage/sqlitestore"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/storerouter"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

func (w *gatewayWiring) buildBillingPipeline(
	billing billingstore.Store,
	pgPool *pgxpool.Pool,
	redisClient redis.UniversalClient,
	concernRedis *redistopology.Clients,
	tenants tenantstore.Store,
	replica string,
) {
	f := w.f
	auditGrantCheckIntervalSeconds := f.auditGrantCheckIntervalSeconds
	auditPgauditEnabled := f.auditPgauditEnabled
	auditPgauditSinkEndpoint := f.auditPgauditSinkEndpoint
	auditRetentionDays := f.auditRetentionDays
	auditRetentionPreset := f.auditRetentionPreset
	auditSIEMEndpoint := f.auditSIEMEndpoint
	billingFlushBatchSize := f.billingFlushBatchSize
	billingFlushIntervalMs := f.billingFlushIntervalMs
	billingFlushMaxPending := f.billingFlushMaxPending
	billingRedisStreamMaxLen := f.billingRedisStreamMaxLen
	billingRetentionDays := f.billingRetentionDays
	billingSinkWebhookSecret := f.billingSinkWebhookSecret
	billingSinkWebhookURL := f.billingSinkWebhookURL
	capacityTier := f.capacityTier
	gdprRetentionDays := f.gdprRetentionDays
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
		// spec: §17.8.2 line 1203 — size the per-tenant billing stream MAXLEN.
		// An explicit --billing-redis-stream-max-len pins it; 0 resolves the
		// per-tier default so a Tier 3 install gets 72,000 rather than the
		// Tier 1/2 floor of 50,000 (which fills in ~83s at the Tier 3 rate).
		streamMaxLen := *billingRedisStreamMaxLen
		if streamMaxLen <= 0 {
			streamMaxLen = redisstream.StreamMaxLenForTier(*capacityTier)
		}
		tier, err := redisstream.New(redisstream.Options{
			// §12.4 Quota/Rate Limiting concern: billing stream.
			Client:       concernRedis.For(storerouter.RedisConcernQuota),
			ConsumerName: replica,
			Inserter:     pgStore,
			StreamMaxLen: streamMaxLen,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: billing failover stream: %v", err)
		}
		billingStream = tier
		billingTier = tier
		log.Printf("lenny-gateway: §11.2.1 billing failover Tier 1 backed by the Redis stream (consumer %s, maxlen=%d, tier=%s)", replica, streamMaxLen, *capacityTier)
	} else {
		billingStream = failover.NewMemStream()
	}
	// spec: §11.2.1 lines 132-138 — wrap the durable primary so every
	// event a synchronous Postgres write seals is published to the
	// configured delivery sinks (webhook / message queue). The wrap is a
	// no-op when no sink is configured, and is applied to the failover
	// Primary so delivery happens "only after the synchronous Postgres
	// write confirms" (line 137). Buffered-then-flushed events during a
	// Postgres outage are a tracked residual. F-11.2.14.
	billingPublisher, err := buildBillingPublisher(*billingSinkWebhookURL, billingSinkWebhookSecret)
	if err != nil {
		log.Fatalf("lenny-gateway: billing delivery sink: %v", err)
	}
	billingPrimary := billingsink.NewPublishing(billing, billingPublisher, billingsink.PublishingOptions{})
	if !billingPublisher.Empty() {
		log.Printf("lenny-gateway: §11.2.1 billing delivery sinks active: %v", billingPublisher.Sinks())
	}
	billingPipeline := failover.New(failover.Options{
		Primary: billingPrimary,
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

	// spec: §11.2.1 — billingEmitter tees the cost-attribution / compliance
	// event subset (delegation, credential, export-scan) into the per-tenant
	// billing stream from producers whose primary sink is the §11.7 audit
	// chain. Nil-safe: a no-billing minimal gateway drops every tee. F-11.2.1.
	billingEmitter := billingfanout.NewEmitter(billing)

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
		time.Duration(*auditGrantCheckIntervalSeconds)*time.Second, grantCheckRegulated,
	)
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

	// spec: §11.7 line 377 — a regulated-profile tenant additionally
	// requires audit.pgaudit.enabled with audit.pgaudit.sinkEndpoint
	// configured; absence is a fatal startup error in production mode,
	// symmetric with the SIEM gate above. F-11.7.10.
	pgauditConfigured := *auditPgauditEnabled && *auditPgauditSinkEndpoint != ""
	if err := admin.ValidatePgauditForRegulatedTenants(context.Background(), tenants, pgauditConfigured); err != nil {
		if err.Error() == admin.PgauditStartupFatalMessage {
			if os.Getenv("LENNY_ENV") == "production" {
				log.Fatalf("lenny-gateway: %s", err.Error())
			}
			log.Printf("lenny-gateway: WARNING: %s (non-production, continuing)", err.Error())
		} else {
			log.Printf("lenny-gateway: WARNING: §11.7 pgaudit compliance preflight could not list tenants: %v", err)
		}
	}

	// spec: §11.7 line 375 — when audit.pgaudit.enabled is true, verify
	// the pgaudit extension is installed and pgaudit.log includes the DDL
	// and ROLE classes. A failed check is fatal in production when a
	// regulated tenant is present; otherwise it is logged. F-11.7.10.
	if *auditPgauditEnabled && pgPool != nil {
		if err := pgaudit.Preflight(context.Background(), pgPool); err != nil {
			regulatedPresent := admin.ValidatePgauditForRegulatedTenants(
				context.Background(), tenants, false,
			) != nil
			if regulatedPresent && os.Getenv("LENNY_ENV") == "production" {
				log.Fatalf("lenny-gateway: FATAL: §11.7 pgaudit preflight failed with a regulated tenant present: %v", err)
			}
			log.Printf("lenny-gateway: WARNING: §11.7 pgaudit preflight failed: %v", err)
		}
	}

	// spec: §4.1 — record the constructed billing pipeline and the resolved
	// §11.7 audit-retention / grant-check schedule on the accumulator for
	// the later build steps and the background-worker stage.
	w.billing = billing
	w.billingEmitter = billingEmitter
	w.billingLedger = billingLedger
	w.billingPipeline = billingPipeline
	w.billingTier = billingTier
	w.effectiveAuditRetentionDays = effectiveAuditRetentionDays
	w.grantCheckRegulated = grantCheckRegulated
	w.resolvedGrantCheckInterval = resolvedGrantCheckInterval
}

// buildTokenSigningStores constructs the §7.1 uploadToken key ring, issuer,
// verifier, and rotator; the §4 / §17.5 KMS provider and its breaker
// observer; the §10.3 rotating verifier; and the §10.2 bearer verifier and
// JWT signer, recording each on the accumulator. It is an extracted
// per-concern step of buildStores.
//
// spec: §4.1 gateway subsystem seams; §7.1 uploadToken; §10.2 / §10.3
// verifiers; §4 KMS provider.
func (w *gatewayWiring) buildTokenSigningStores(tenants tenantstore.Store) {
	f := w.f
	devMode := f.devMode
	kmsOpts := f.kmsOpts
	bearerExpectedIssuer := f.bearerExpectedIssuer
	bearerExpectedAudiences := f.bearerExpectedAudiences
	bearerTrustHMACKeyFile := f.bearerTrustHMACKeyFile
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

	// spec: §12.8 line 845 — now that the KMS provider is resolved, wire it
	// into the Postgres tenant store so the §12.8 erasure_salt is
	// envelope-encrypted at rest (the store is built before the provider is
	// resolved, so the injection is deferred to here). F-12.8.5.
	if tps, ok := tenants.(*tenantpg.Store); ok {
		tps.SetSaltKMS(kmsProvider)
	}

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
		// spec: §17.4 line 182 — "the embedded OIDC provider refuses any
		// audience claim not matching dev.local; the gateway rejects
		// externally-issued tokens." The embedded provider's own Verify
		// enforces the audience, but the gateway accepts the embedded key
		// directly and would otherwise honor any aud claim signed under it.
		// Wrap the embedded-key verifier in a ClaimChecker pinned to the
		// embedded OIDC audience (dev.local) so a foreign-audience token —
		// even one validly signed by the trusted key — is refused at the
		// gateway. The Token Service path (rotatingVerifier) is unaffected.
		// F-17.4.16.
		bearerVerifier = jwt.NewMultiVerifier(rotatingVerifier, embeddedHMACVerifier(trusted))
		log.Printf("lenny-gateway: trusting an additional HMAC bearer key from %s (kid %s); embedded tokens must carry aud=dev.local",
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

	// spec: §4.1 — record the §7.1 uploadToken, §4 KMS, and §10.2 / §10.3
	// signing and verification surfaces on the accumulator for the
	// credential-assignment step and the run loop.
	w.ring = ring
	w.uploadIssuer = uploadIssuer
	w.uploadVerifier = uploadVerifier
	w.uploadTracker = uploadTracker
	w.uploadRotator = uploadRotator
	w.kmsProvider = kmsProvider
	w.kmsBreakerObs = kmsBreakerObs
	w.kmsBackedSigner = kmsBackedSigner
	w.jwtSigner = jwtSigner
	w.rotatingVerifier = rotatingVerifier
	w.bearerVerifier = bearerVerifier
	w.expectedAuds = expectedAuds
}

// buildStores constructs the §4.2/§4.4/§4.5 persistence surfaces, the §12.3
// store router, the §11.2.1 billing pipeline, the §7.1/§10.2/§10.3 signing
// and verification surfaces, the §17.4 executor, the §4.9 credential
// assignment service, the §15.1 pod-placement lifecycle, and the §7.2/§7.3
// session-messaging and per-session shared stores, recording each on the
// accumulator. It is the persistence-and-credential build step of the §4.1
// composition root, decomposed into per-concern sub-steps that read and
// record their cross-step state on the accumulator (proposal 0020 §4 Part A
// R1; the buildBillingPipeline / buildTokenSigningStores precedent).
//
// spec: §4.1 gateway subsystem seams; §4.2/§4.4/§4.5 stores; §12.3 store
// router; §4.9 credentials; §15.1 pod placement.
func (w *gatewayWiring) buildStores() {
	// checkpointRetention is the only persistence local without an
	// accumulator field: it is constructed by the §4.4/§12.5 retention step
	// and consumed only by the §15.1 pod-lifecycle checkpointer, so it is
	// threaded through an explicit return rather than onto the accumulator.
	checkpointRetention := w.buildPersistenceStores()
	w.buildRedisAndQuota()
	w.buildStoreRouterAndSecurityBus()
	// spec: §4.1 / §11.2.1 — build the billing failover pipeline. Records
	// the ledger, pipeline, fan-out emitter, and resolved audit-retention
	// schedule on the accumulator.
	w.buildBillingPipeline(w.billing, w.pgPool, w.redisClient, w.concernRedis, w.tenants, w.replica)
	// spec: §4.1 / §7.1 — build the uploadToken, KMS, and signing/verification
	// surfaces. Records them on the accumulator.
	w.buildTokenSigningStores(w.tenants)
	w.buildExecutorAndCredentials()
	w.buildPodLifecycle(checkpointRetention)
	w.buildSessionMessaging()
}

// aliasPrimaryDDLToBillingAudit reports whether the primary-instance DDL
// pool should alias the billing/audit DDL pool rather than open its own
// connection. This is true only in the single-instance topology: no separate
// billing/audit instance is configured (billingAuditDSN empty) and no distinct
// primary DDL DSN is supplied (primaryDDLDSN empty), so the primary and the
// billing/audit instance are one and the single billing/audit DDL pool
// addresses both. When a separate billing/audit instance is configured the
// operator must supply LENNY_PG_PRIMARY_DDL_DSN, because the primary's
// lenny_app pool holds no CREATE ON SCHEMA and the §13.3 issued-token
// write-before-issue path seals its per-tenant audit row on the primary.
//
// spec: §12.3, §15.1. F-11.2.10.
func aliasPrimaryDDLToBillingAudit(billingAuditDSN, primaryDDLDSN string) bool {
	return billingAuditDSN == "" && primaryDDLDSN == ""
}

// distinctDDLPools returns the CREATE-privileged DDL pools that must be closed
// at shutdown, deduplicated by pointer identity. In the single-instance
// topology primaryDDLPool aliases billingAuditDDLPool (aliasPrimaryDDLToBillingAudit),
// so closing each distinct pool once avoids a double Close on the shared pool.
// A nil pool contributes nothing. The shutdown path (runServers) iterates the
// result and calls Close on each, so the dedup decision has a single canonical
// implementation the tier-1 test pins.
//
// spec: §12.3, §15.1. F-11.2.10.
func distinctDDLPools(billingAuditDDLPool, primaryDDLPool *pgxpool.Pool) []*pgxpool.Pool {
	pools := make([]*pgxpool.Pool, 0, 2)
	if billingAuditDDLPool != nil {
		pools = append(pools, billingAuditDDLPool)
	}
	if primaryDDLPool != nil && primaryDDLPool != billingAuditDDLPool {
		pools = append(pools, primaryDDLPool)
	}
	return pools
}

// closeDDLPools closes each CREATE-privileged DDL pool exactly once at
// shutdown, deduplicating the pool shared between the billing/audit and
// primary limbs in the single-instance topology through distinctDDLPools. The
// shutdown path (runServers) delegates here so the close loop is a single
// canonical implementation the tier-1 test pins, rather than an inline loop
// that runs only during a real process shutdown.
//
// spec: §12.3, §15.1. F-11.2.10.
func closeDDLPools(billingAuditDDLPool, primaryDDLPool *pgxpool.Pool) {
	for _, pool := range distinctDDLPools(billingAuditDDLPool, primaryDDLPool) {
		pool.Close()
	}
}

// ddlPoolOpener opens a Postgres connection pool from a DSN. It is the injected
// seam over pgxpool.New that lets openVerifiedDDLPool be exercised at tier 1
// without a live Postgres, and lets buildPersistenceStores pass the real
// pgxpool.New at startup.
type ddlPoolOpener func(ctx context.Context, dsn string) (*pgxpool.Pool, error)

// ddlSchemaVerifier verifies that an opened pool addresses an instance carrying
// the migrations/ schema. It is the injected seam over verifyPostgresSchema.
type ddlSchemaVerifier func(ctx context.Context, pool *pgxpool.Pool) error

// openVerifiedDDLPool opens a CREATE-privileged DDL pool from dsn and verifies
// its schema, returning the pool for the caller to retain. Both the
// billing/audit and the primary per-tenant sequence-provisioning pools are
// opened this way, so the open-then-verify-then-fail-closed sequence has a
// single canonical implementation the tier-1 test pins rather than two
// near-identical inline blocks. An empty dsn yields a nil pool and no error
// (the topology does not use this DDL pool). A verify failure closes the pool
// it opened before returning the error, so a failed startup leaks no
// connection pool. The caller escalates a non-nil error to log.Fatalf, keeping
// the DDL-provisioning path fail-closed at startup: a gateway that cannot open
// a CREATE-privileged pool cannot provision per-tenant sequences and must not
// start.
//
// spec: §12.3, §15.1. F-11.2.10.
func openVerifiedDDLPool(
	ctx context.Context,
	dsn string,
	open ddlPoolOpener,
	verify ddlSchemaVerifier,
) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, nil
	}
	pool, err := open(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := verify(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// ddlPoolDSNs are the three DSNs that determine the §12.3 R-03 per-tenant DDL
// pool topology: the billing/audit instance DSN (billingAudit, whose emptiness
// distinguishes the single-instance from the separate-instance topology), and
// the two CREATE-privileged DDL DSNs (billingAuditDDL, primaryDDL).
type ddlPoolDSNs struct {
	billingAudit    string
	billingAuditDDL string
	primaryDDL      string
}

// resolveDDLPools opens the CREATE-privileged per-tenant DDL pools for the
// configured topology, returning the billing/audit DDL pool and the primary DDL
// pool. It centralizes the topology decision the two pools share so the alias
// branch and the separate-instance branch have a single canonical
// implementation the tier-1 test pins, rather than two inline blocks in the
// startup path. The rules:
//
//   - billingAuditDDL opens the billing/audit DDL pool (empty DSN -> nil).
//   - primaryDDL, when set, opens a distinct primary DDL pool.
//   - when primaryDDL is empty and the topology is single-instance
//     (aliasPrimaryDDLToBillingAudit), the primary DDL pool aliases the
//     billing/audit DDL pool, because the primary and the billing/audit
//     instance are one and the single pool addresses both.
//   - when primaryDDL is empty but a separate billing/audit instance is
//     configured, the primary DDL pool is nil; the operator must supply
//     LENNY_PG_PRIMARY_DDL_DSN for the separate-instance topology, and its
//     absence is surfaced as a nil primary pool the caller acts on.
//
// A failed open or schema-verify on either pool closes any pool already opened
// and returns the error, so a failed startup leaks no connection pool and the
// caller escalates to log.Fatalf (fail-closed DDL provisioning).
//
// spec: §12.3, §15.1. F-11.2.10.
func resolveDDLPools(
	ctx context.Context,
	dsns ddlPoolDSNs,
	open ddlPoolOpener,
	verify ddlSchemaVerifier,
) (billingAuditDDLPool, primaryDDLPool *pgxpool.Pool, err error) {
	billingAuditDDLPool, err = openVerifiedDDLPool(ctx, dsns.billingAuditDDL, open, verify)
	if err != nil {
		return nil, nil, fmt.Errorf("billing/audit DDL pool: %w", err)
	}
	if dsns.primaryDDL != "" {
		primaryDDLPool, err = openVerifiedDDLPool(ctx, dsns.primaryDDL, open, verify)
		if err != nil {
			closeDDLPools(billingAuditDDLPool, nil)
			return nil, nil, fmt.Errorf("primary DDL pool: %w", err)
		}
		return billingAuditDDLPool, primaryDDLPool, nil
	}
	// No explicit primary DDL DSN. In the single-instance topology the primary
	// is the billing/audit instance, so the billing/audit DDL pool addresses
	// the primary too; in the separate-instance topology the primary pool stays
	// nil until the operator supplies LENNY_PG_PRIMARY_DDL_DSN.
	if aliasPrimaryDDLToBillingAudit(dsns.billingAudit, dsns.primaryDDL) {
		primaryDDLPool = billingAuditDDLPool
	}
	return billingAuditDDLPool, primaryDDLPool, nil
}

// buildPersistenceStores constructs the §4.2 session and metadata stores
// (Postgres, §17.4 embedded SQLite, or in-memory), the §12.3 read-replica /
// billing-audit / audit-sync pools, the §4.4 partial-manifest, session-log,
// and §12.5 checkpoint-retention stores, the §4.5/§17.9.3 artifact store and
// its §12.5 catalog, and the §5 pool store, recording each on the
// accumulator. It returns the §12.5 checkpoint-retention store, which is
// consumed only by the §15.1 pod-lifecycle checkpointer.
//
// spec: §4.1 gateway subsystem seams; §4.2/§4.4/§4.5 stores; §12.3 pools;
// §12.5 catalog.
func (w *gatewayWiring) buildPersistenceStores() checkpointretention.Store {
	f := w.f
	auditSyncWritePoolSize := f.auditSyncWritePoolSize
	billingAuditDSN := f.billingAuditDSN
	billingAuditDDLDSN := f.billingAuditDDLDSN
	primaryDDLDSN := f.primaryDDLDSN
	minioAccessKey := f.minioAccessKey
	minioBucket := f.minioBucket
	minioEndpoint := f.minioEndpoint
	minioSecretKey := f.minioSecretKey
	minioUseSSL := f.minioUseSSL
	objectStorageAccountURL := f.objectStorageAccountURL
	objectStorageBucket := f.objectStorageBucket
	objectStorageFilesystemRoot := f.objectStorageFilesystemRoot
	objectStorageProvider := f.objectStorageProvider
	objectStorageRegion := f.objectStorageRegion
	poolerMode := f.poolerMode
	postgresDSN := f.postgresDSN
	readDSN := f.readDSN
	sqlitePath := f.sqlitePath

	// ----- Stores -----
	// session, transcript, tenant, and runtime state is persisted to
	// Postgres when --postgres-dsn is set, and held in memory
	// otherwise. The remaining stores are in-memory pending their
	// Redis (circuit breakers, quota) or Postgres backings.
	var (
		sessions         sessionstore.Store
		tenants          tenantstore.Store
		runtimes         runtimestore.Store
		capOverrides     runtimecapoverride.Store
		transcripts      transcriptstore.Store
		users            userstore.Store
		connectors       connectorstore.Store
		billing          billingstore.Store
		pgPool           *pgxpool.Pool
		readPool         *pgxpool.Pool
		billingAuditPool *pgxpool.Pool
		auditSyncPool    *pgxpool.Pool
		// sqliteDB is the §17.4 line 199 Source-Mode embedded-SQLite
		// durability layer. Non-nil only when --sqlite-path is set and
		// --postgres-dsn is empty; the shutdown path stops the flush loop
		// (sqliteFlushCancel) and flushes+closes it. F-17.4.2.
		sqliteDB          *sqlitestore.DB
		sqliteFlushCancel context.CancelFunc
	)
	// spec: §12.3, §15.1 — the CREATE-privileged per-tenant DDL pools resolved
	// by resolveDDLPools below. Declared separately from the store block so the
	// resolveDDLPools call assigns both in one statement. F-11.2.10.
	var billingAuditDDLPool, primaryDDLPool *pgxpool.Pool
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
		// spec: §12.3 line 146 — the optional read-replica reader endpoint.
		// When --postgres-read-dsn is set, the read-heavy query classes the
		// spec names route to this replica (the StoreRouter audit-read path
		// and the session-status / task-tree / usage-report read paths in
		// their stores); every write stays on the primary. The replica
		// serves the same migrations/ schema, so the same startup schema
		// verification applies. Empty keeps reads on the primary (readPool
		// stays nil and every read shares pgPool). F-12.3.16 / F-17.9.13.
		if *readDSN != "" {
			rpool, err := pgxpool.New(context.Background(), *readDSN)
			if err != nil {
				log.Fatalf("lenny-gateway: read-replica postgres: %v", err)
			}
			if err := verifyPostgresSchema(context.Background(), rpool); err != nil {
				log.Fatalf("lenny-gateway: read-replica postgres: %v", err)
			}
			readPool = rpool
			log.Printf("lenny-gateway: §12.3 routing read-heavy queries (session status, task tree, audit reads, usage reports) to the LENNY_PG_READ_DSN read replica")
		}
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
		// spec: §12.3, §15.1 — CREATE-privileged DDL pools for per-tenant
		// sequence provisioning. The per-tenant billing (billing_seq_<40hex>)
		// and audit (audit_seq_<40hex>) sequences are created at tenant-create
		// time through a role that holds CREATE ON SCHEMA public, distinct from
		// the lenny_app billing/audit pool the StoreRouter resolves for Append
		// (lenny_app holds no CREATE grant). The billing/audit DDL pool targets
		// the instance where billing_events and audit_log physically live (the
		// separate LENNY_PG_BILLING_AUDIT_DSN instance when configured, otherwise
		// the primary). The primary DDL pool targets the primary instance the
		// §13.3 issued-token write-before-issue path seals its per-tenant audit
		// row on; when the separate billing/audit instance is not configured the
		// primary and the billing/audit instance are one, so the primary DDL pool
		// falls back to the single billing/audit DDL pool. The admin Router
		// (adminrouter.go) threads both pools into the provisioning helper (S4).
		// F-11.2.10.
		var ddlErr error
		billingAuditDDLPool, primaryDDLPool, ddlErr = resolveDDLPools(
			context.Background(),
			ddlPoolDSNs{
				billingAudit:    *billingAuditDSN,
				billingAuditDDL: *billingAuditDDLDSN,
				primaryDDL:      *primaryDDLDSN,
			},
			pgxpool.New,
			verifyPostgresSchema,
		)
		if ddlErr != nil {
			log.Fatalf("lenny-gateway: §12.3 per-tenant DDL postgres: %v", ddlErr)
		}
		if billingAuditDDLPool != nil {
			log.Printf("lenny-gateway: §15.1 per-tenant billing/audit sequence provisioning uses the CREATE-privileged LENNY_PG_BILLING_AUDIT_DDL_DSN connection")
		}
		if primaryDDLPool != nil && primaryDDLPool != billingAuditDDLPool {
			log.Printf("lenny-gateway: §15.1 per-tenant primary audit sequence provisioning uses the CREATE-privileged LENNY_PG_PRIMARY_DDL_DSN connection")
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
		sessions = sessionpg.New(pool, sessionpg.WithReadPool(readPool))
		tenants = tenantpg.New(pool)
		runtimes = runtimepg.New(pool)
		capOverrides = capoverridepg.New(pool)
		transcripts = transcriptpg.New(pool)
		users = userpg.New(pool)
		connectors = connectorpg.New(pool)
		// billing is constructed below via the §12.3 R-03 StoreRouter,
		// once the Redis client (if any) is resolved, so the ledger never
		// holds a raw pool. F-12.3.4 / F-12.6.1 / F-12.2.13 / F-12.7.1.
		log.Printf("lenny-gateway: persisting sessions, transcripts, tenants, runtimes, users, connectors, and billing events to Postgres")
	} else {
		sessMem := memstore.New()
		tenantMem := tenantstore.NewMemory()
		runtimeMem := runtimestore.NewMemory()
		capMem := runtimecapoverride.NewMemory()
		transcriptMem := transcriptstore.NewMemory()
		userMem := userstore.NewMemory()
		connectorMem := connectorstore.NewMemory()
		billingMem := billingstore.NewMemory()
		sessions = sessMem
		tenants = tenantMem
		runtimes = runtimeMem
		capOverrides = capMem
		transcripts = transcriptMem
		users = userMem
		connectors = connectorMem
		billing = billingMem
		// spec: §17.4 line 199 — Source Mode replaces Postgres with
		// embedded SQLite for session and metadata storage. With
		// --sqlite-path set, the in-memory stores above are loaded from
		// the SQLite file on startup, snapshotted to it every two seconds
		// while serving, and flushed once more on graceful shutdown, so a
		// `make run` developer keeps their tenants, runtimes, sessions,
		// and transcripts across a restart without a Postgres dependency.
		// The stores' query logic is the same in-memory implementation
		// the tier-3 contract suites exercise; SQLite is purely the
		// durable backing. F-17.4.2.
		if *sqlitePath != "" {
			db, err := sqlitestore.Open(*sqlitePath)
			if err != nil {
				log.Fatalf("lenny-gateway: §17.4 sqlite: %v", err)
			}
			sqliteDB = db
			sqliteDB.Register("sessions", sessMem)
			sqliteDB.Register("tenants", tenantMem)
			sqliteDB.Register("runtimes", runtimeMem)
			sqliteDB.Register("runtime_cap_overrides", capMem)
			sqliteDB.Register("transcripts", transcriptMem)
			sqliteDB.Register("users", userMem)
			sqliteDB.Register("connectors", connectorMem)
			sqliteDB.Register("billing_events", billingMem)
			if err := sqliteDB.Restore(context.Background()); err != nil {
				log.Fatalf("lenny-gateway: §17.4 sqlite restore: %v", err)
			}
			var flushCtx context.Context
			flushCtx, sqliteFlushCancel = context.WithCancel(context.Background())
			// Idempotent backstop release of the auto-flush context. The
			// graceful-shutdown path cancels sqliteFlushCancel explicitly
			// before sqliteDB.Close so the flush loop stops in the right
			// order; this deferred call guarantees the CancelFunc is released
			// on every runGateway exit path and is a no-op once the shutdown
			// path has already called it. F-17.4.2.
			sqliteDB.StartAutoFlush(flushCtx, 2*time.Second, func(err error) {
				log.Printf("lenny-gateway: §17.4 sqlite flush: %v", err)
			})
			log.Printf("lenny-gateway: §17.4 Source Mode embedded SQLite store at %s (sessions, tenants, runtimes, transcripts, users, connectors, billing persist across restart)", *sqlitePath)
		} else {
			log.Printf("lenny-gateway: session and metadata stores are in-memory (no --postgres-dsn or --sqlite-path)")
		}
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
	// §4.5 / §17.9.3 artifact store: the object-storage backend the
	// operator selected via objectStorage.provider (minio | s3 | gcs |
	// azure). Empty defaults to MinIO when --minio-endpoint is set,
	// otherwise the in-memory store for the minimal gateway. The §12.5
	// ll. 297-303 SSEKeyResolver is wired into every backend so a T4
	// tenant's Put is fail-closed under the per-tenant SSE-KMS alias
	// regardless of which provider serves the bucket. F-17.5.1 /
	// F-12.7.3.
	objectStore, err := blobproviderflags.Resolve(context.Background(), blobproviderflags.Options{
		Provider:        *objectStorageProvider,
		Bucket:          *objectStorageBucket,
		Region:          *objectStorageRegion,
		AzureAccountURL: *objectStorageAccountURL,
		FilesystemRoot:  *objectStorageFilesystemRoot,
		MinIOEndpoint:   *minioEndpoint,
		MinIOAccessKey:  *minioAccessKey,
		MinIOSecretKey:  *minioSecretKey,
		MinIOBucket:     *minioBucket,
		MinIOUseSSL:     *minioUseSSL,
		// §12.5 ll. 297-303 SSEKeyResolver: look up the writing
		// tenant's workspaceTier on every Put and hand the backend the
		// per-tenant SSE-KMS alias for T4 tenants.
		SSEKeyResolver: newSSEKeyResolver(tenants),
	})
	if err != nil {
		log.Fatalf("lenny-gateway: §17.9.3 object storage: %v", err)
	}
	// minioStore retains the concrete *miniostore.Store so the
	// MinIO-specific durable-catalog reader and the startup T4 KMS
	// probe wire against it below. It is nil for every non-MinIO
	// backend.
	var minioStore *miniostore.Store
	if ms, ok := objectStore.(*miniostore.Store); ok {
		minioStore = ms
	}
	var blobs blobstore.Store = objectStore
	// blobProbe is the §12.5 drain-readiness liveness probe — a real
	// bucket check when the backend implements drainreadiness.Prober
	// (MinIO), an always-ready stub otherwise (the in-memory store and
	// managed cloud object storage, which cannot degrade the way a
	// self-managed MinIO can).
	var blobProbe drainreadiness.Prober = drainreadiness.ProberFunc(func(context.Context) error { return nil })
	if p, ok := objectStore.(drainreadiness.Prober); ok {
		blobProbe = p
	}
	log.Printf("lenny-gateway: §4.5/§17.9.3 artifact store backend=%q (§12.5 SSEKeyResolver wired; T4 tenant-scoped SSE-KMS)",
		objectStoreBackendName(*objectStorageProvider, *minioEndpoint))

	// §13.2 capability model: a pod-based deployment mints presigned
	// checkpoint capabilities against the resolved backend, so that
	// backend MUST implement blobstore.Presigner. The check is on the
	// resolved store rather than on the configured provider name because
	// provider=minio with an empty --minio-endpoint falls back to the
	// in-memory store, which deliberately omits Presigner. Fail closed at
	// startup rather than at the first checkpoint attempt.
	//
	// spec: §13.2 (capability model).
	if *f.agentNamespace != "" {
		if _, ok := objectStore.(blobstore.Presigner); !ok {
			log.Fatalf("lenny-gateway: §13.2 pod-based sandboxes (--agent-namespace=%q) require a Presigner-capable artifact store, but the resolved backend %T does not implement it (an empty --minio-endpoint falls back to the in-memory store); configure a signing object-store backend",
				*f.agentNamespace, objectStore)
		}
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

	// spec: §4.1 — record the §4.2/§4.4/§4.5 persistence surfaces on the
	// accumulator for the Redis, router, pod-lifecycle, and messaging steps.
	w.sessions = sessions
	w.tenants = tenants
	w.runtimes = runtimes
	w.capOverrides = capOverrides
	w.transcripts = transcripts
	w.users = users
	w.connectors = connectors
	w.billing = billing
	w.pgPool = pgPool
	w.readPool = readPool
	w.billingAuditPool = billingAuditPool
	w.billingAuditDDLPool = billingAuditDDLPool
	w.primaryDDLPool = primaryDDLPool
	w.auditSyncPool = auditSyncPool
	w.sqliteDB = sqliteDB
	w.sqliteFlushCancel = sqliteFlushCancel
	w.partialManifests = partialManifests
	w.sessionLogs = sessionLogs
	w.objectStore = objectStore
	w.minioStore = minioStore
	w.blobs = blobs
	w.blobProbe = blobProbe
	w.artifactCatalog = artifactCatalog
	w.blobsCataloged = blobsCataloged
	w.pools = pools
	return checkpointRetention
}

// buildRedisAndQuota constructs the §12.4 Redis client and per-concern split,
// the §11.6 circuit-breaker registry, the §10.1 coordination-lease sweeper
// and mirror, the §11.2 storage-quota counter and its §12.4 Postgres-fallback
// recovery reconciler, and the §11.1 Redis-backed rate-limit counter,
// recording each on the accumulator. Without Redis it records the in-memory
// breaker registry and leaves the quota and coordination surfaces on their
// in-memory defaults.
//
// spec: §4.1 gateway subsystem seams; §12.4 Redis topology; §11.6 breakers;
// §10.1 coordination; §11.1/§11.2 quota.
func (w *gatewayWiring) buildRedisAndQuota() {
	f := w.f
	capacityTier := f.capacityTier
	coordInterval := f.coordInterval
	redisAllowInsecure := f.redisAllowInsecure
	redisCachePubSubURL := f.redisCachePubSubURL
	redisClusterAddrs := f.redisClusterAddrs
	redisCoordinationURL := f.redisCoordinationURL
	redisDelegationURL := f.redisDelegationURL
	redisPassword := f.redisPassword
	redisQuotaURL := f.redisQuotaURL
	redisSentinelAddrs := f.redisSentinelAddrs
	redisSentinelMaster := f.redisSentinelMaster
	redisSentinelPassword := f.redisSentinelPassword
	redisSessionDataURL := f.redisSessionDataURL
	redisTLS := f.redisTLS
	redisURL := f.redisURL
	singleTenantRedisTopology := f.singleTenantRedisTopology

	// pgPool, the session/tenant stores, and the artifact catalog were
	// constructed by buildPersistenceStores and recorded on the accumulator.
	pgPool := w.pgPool
	sessions := w.sessions
	tenants := w.tenants
	artifactCatalog := w.artifactCatalog

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
		// erasureLeaseStore captures the concrete §12.4 lease store for
		// the §12.8 step-1 LeaseStore erasure wiring (the store is built
		// inside the Redis-available branch below; nil without Redis).
		erasureLeaseStore leasestore.LeaseStore

		// coordMirror is the §10.1 line 165 coordination_lease barrier-target
		// mirror the Sweeper writes and the preStop barrier reads. Postgres-
		// backed when pgPool is wired; nil otherwise, in which case the
		// barrier falls back to the in-memory lease cache.
		coordMirror coordlease.Store

		// coordFencer drives the §10.1 / §4.2 CoordinatorFence after a
		// resume re-bind (announce coordination_generation; §11.3 line 209
		// retry/relinquish). Built inside the Redis-available branch below
		// (it needs the lease store for the relinquish path); nil interface
		// without Redis, which leaves the resume path unfenced.
		coordFencer sessionserver.CoordinationFencer

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
		// §12.8 step 1: expose the lease store to the erasure orchestrator
		// so a user erasure releases the user's active coordination leases.
		erasureLeaseStore = leaseStore
		// §10.1 line 165: mirror held leases into Postgres so the preStop
		// barrier-target query observes coordinator handoffs that occurred
		// in the seconds before drain. Without Postgres the mirror is nil
		// and the barrier falls back to the in-memory lease cache.
		if pgPool != nil {
			coordMirror = coordleasepg.New(pgPool, nil)
		}
		coordinator = coordination.NewSweeper(
			tenantsLister{tenants}, sessions, leaseStore,
			coordination.Options{ReplicaID: replica, Interval: *coordInterval, Mirror: coordMirror},
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
		redisTopology := capacityplanning.RedisTopologyStandalone
		switch {
		case *redisClusterAddrs != "":
			redisTopology = capacityplanning.RedisTopologyCluster
			log.Printf("lenny-gateway: Redis via Cluster nodes=%d split=%t; coordination replica %s",
				len(splitAndTrim(*redisClusterAddrs)), concernRedis.Split(), replica)
		case *redisSentinelAddrs != "":
			redisTopology = capacityplanning.RedisTopologySentinel
			log.Printf("lenny-gateway: Redis via Sentinel master=%q sentinels=%d split=%t; coordination replica %s",
				*redisSentinelMaster, len(splitAndTrim(*redisSentinelAddrs)), concernRedis.Split(), replica)
		default:
			log.Printf("lenny-gateway: circuit-breaker state in Redis split=%t; coordination replica %s", concernRedis.Split(), replica)
		}
		// spec: §17.8.2 line 1164 — a Tier 3 deployment on a single-tenant
		// Redis Sentinel topology gets the RedisClusterRecommended startup
		// warning unless capacityPlanning.singleTenantRedisTopology=sentinel
		// documents the operator's intent.
		if capacityplanning.ShouldWarnRedisClusterRecommended(*capacityTier, redisTopology, *singleTenantRedisTopology) {
			log.Printf("lenny-gateway: %s", capacityplanning.RedisClusterRecommendedWarning)
		}
	} else {
		breakers = breakerstore.NewMemory()
	}

	// spec: §4.1 — record the §12.4 Redis surfaces, the §11.6 breaker
	// registry, the §10.1 coordination sweeper/mirror, and the §11.1/§11.2
	// quota counters on the accumulator for the router, billing,
	// pod-lifecycle, and worker steps.
	w.replica = replica
	w.redisClient = redisClient
	w.concernRedis = concernRedis
	w.breakers = breakers
	w.breakerCache = breakerCache
	w.coordinator = coordinator
	w.coordMirror = coordMirror
	w.coordFencer = coordFencer
	w.erasureLeaseStore = erasureLeaseStore
	w.storageCounter = storageCounter
	w.storageRecoveryReconciler = storageRecoveryReconciler
	w.delegationBudgetReconciler = delegationBudgetReconciler
	w.rateLimiter = rateLimiter
}

// buildStoreRouterAndSecurityBus constructs the §12.3 R-03 single-shard store
// router (wrapping the billing ledger), runs the §11.2 storage-quota
// rehydration from the §12.5 artifact catalog, and constructs the §4.9/§10.3
// security-cache Redis pub/sub bus, recording each on the accumulator. The
// router and the bus are built only on the Postgres / Redis paths
// respectively; an in-memory deployment leaves them nil.
//
// spec: §4.1 gateway subsystem seams; §12.3 store router; §11.2 quota
// rehydration; §4.9/§10.3 security pub/sub.
func (w *gatewayWiring) buildStoreRouterAndSecurityBus() {
	f := w.f
	scatterAggregateTimeoutSeconds := f.scatterAggregateTimeoutSeconds
	scatterMaxConcurrency := f.scatterMaxConcurrency
	scatterPerShardTimeoutSeconds := f.scatterPerShardTimeoutSeconds

	// The persistence, Redis, and quota surfaces were recorded on the
	// accumulator by the earlier build steps.
	pgPool := w.pgPool
	readPool := w.readPool
	billingAuditPool := w.billingAuditPool
	redisClient := w.redisClient
	concernRedis := w.concernRedis
	tenants := w.tenants
	artifactCatalog := w.artifactCatalog
	blobsCataloged := w.blobsCataloged
	storageCounter := w.storageCounter
	billing := w.billing

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
			ReadPostgres:         readPool,
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

	// spec: §4.1 — record the §12.3 router and the §4.9 security bus on the
	// accumulator. billing is now the router-wrapped ledger the billing
	// pipeline step reads.
	w.storeRouter = storeRouter
	w.scatterRouter = scatterRouter
	w.billing = billing
	w.securityBus = securityBus
}

// buildExecutorAndCredentials constructs the §17.4 executor (echo, subprocess,
// or pod), the §4.9 upstream-credential cache and lease store, and the §4.9
// credential-assignment Assigner (the §4.3 Token Service client over mTLS or
// the in-process Service), recording each on the accumulator.
//
// spec: §4.1 gateway subsystem seams; §17.4 executor; §4.9 credentials; §4.3
// Token Service.
func (w *gatewayWiring) buildExecutorAndCredentials() {
	f := w.f
	agentRuntime := f.agentRuntime
	runtimeBin := f.runtimeBin
	tokenServiceAddr := f.tokenServiceAddr
	tokenServiceCA := f.tokenServiceCA
	tokenServiceCert := f.tokenServiceCert
	tokenServiceKey := f.tokenServiceKey
	tokenServiceTenant := f.tokenServiceTenant

	// pgPool and the §4 KMS provider (recorded by buildTokenSigningStores)
	// are read back from the accumulator.
	pgPool := w.pgPool

	// ----- Session API + Executor -----
	// §17.4 local-dev runtime selection: LENNY_AGENT_RUNTIME=echo forces
	// the built-in echo executor (zero-credential mode), --runtime-bin /
	// LENNY_AGENT_BINARY dispatches to a child process speaking the
	// §15.4.1 adapter protocol, and the default is the in-process echo
	// executor. The --agent-namespace branch below replaces this with a
	// PodExecutor when the gateway places sessions on warm pods.
	// F-17.4.15.
	exec, execDesc, err := resolveExecutor(*runtimeBin, *agentRuntime)
	if err != nil {
		log.Fatalf("lenny-gateway: %v", err)
	}
	log.Printf("lenny-gateway: %s", execDesc)

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
		// §12.9 classifies a credential lease as T4 — Restricted, so the
		// Postgres-backed store envelope-encrypts the lease body under
		// the platform "platform:credential-leases" KEK.
		pgLeases, lerr := credleasepg.New(pgPool, w.kmsProvider)
		if lerr != nil {
			log.Fatalf("lenny-gateway: construct credential-lease store: %v", lerr)
		}
		llmLeases = pgLeases
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

	// spec: §4.1 — record the §17.4 executor and the §4.9 credential
	// surfaces on the accumulator for the pod-lifecycle, messaging, and
	// later subsystem steps.
	w.exec = exec
	w.credCache = credCache
	w.llmLeases = llmLeases
	w.credAssign = credAssign
	w.inProcessAssign = inProcessAssign
	w.tokenServiceConn = tokenServiceConn
	w.secretProber = secretProber
	w.tokenServiceSubsystem = tokenServiceSubsystem
}

// buildPodLifecycle constructs the §15.1 pod-placement surfaces when
// --agent-namespace is set: the cluster client, the §25.3 apiserver health
// probe, the §10.2 SA-token verifier, the §5.2 slot counter, the §3.2/§3.4
// reserved-hold and recycle-boundary coordinators, the §4.7 pod binder, the
// §4.6.1 Postgres-backed fallback claim, the §4.4/§12.5 checkpointer, and the
// §7.1 seal-and-export sealer, recording each on the accumulator. Without
// --agent-namespace every field stays nil and the in-process / subprocess
// executor recorded by buildExecutorAndCredentials remains in force.
//
// checkpointRetention is threaded in by buildStores because it has no
// accumulator field and is consumed only by the checkpointer built here.
//
// spec: §4.1 gateway subsystem seams; §15.1 pod placement; §4.7 binder;
// §5.2 slot counter; §3.2/§3.4 recycle.
func (w *gatewayWiring) buildPodLifecycle(checkpointRetention checkpointretention.Store) {
	f := w.f
	adapterCA := f.adapterCA
	adapterKeepaliveTimeMs := f.adapterKeepaliveTimeMs
	adapterKeepaliveTimeoutMs := f.adapterKeepaliveTimeoutMs
	adapterTLSCert := f.adapterTLSCert
	adapterTLSKey := f.adapterTLSKey
	agentNamespace := f.agentNamespace
	checkpointInterval := f.checkpointInterval
	checkpointJitterFraction := f.checkpointJitterFraction
	claimHoldTTLSeconds := f.claimHoldTTLSeconds
	clusterBurst := f.clusterBurst
	clusterQPS := f.clusterQPS
	saTokenAudience := f.saTokenAudience
	slotCounterPostgresFallbackMaxSeconds := f.slotCounterPostgresFallbackMaxSeconds

	// The stores, Redis client, executor, credential assigner, and pool
	// store were recorded on the accumulator by the earlier build steps.
	pgPool := w.pgPool
	redisClient := w.redisClient
	concernRedis := w.concernRedis
	sessions := w.sessions
	pools := w.pools
	blobs := w.blobs
	credAssign := w.credAssign
	exec := w.exec

	// §15.1 pod placement: with --agent-namespace the gateway claims a
	// §5 warm pod for each started session and dispatches its messages
	// to the pod's §4.7 adapter. The in-process and subprocess
	// executors stay available for local development.
	var (
		podBinder     *podsession.Binder
		podRegistry   *podsession.Registry
		checkpointSvc *checkpointer.Checkpointer
		// holdCoordinator runs the §3.2 reserved-hold expiry timers on this
		// replica. It is shared between the Binder (whose acquisition-path
		// rebind cancels the local timer) and the scrub-report service (whose
		// recycle disposition driver arms the timer after a reserved patch). It
		// stays nil when --agent-namespace is unset (single-process dev) so
		// the recycle path and the rebind branch are inert there.
		holdCoordinator *recycle.HoldCoordinator
		// recycleBoundary runs the §3.4 gateway-side recycle-boundary timers on
		// this replica: the missing-report timeout armed at the bound → recycling
		// patch (cancelled by ReportPodScrub, retiring the pod if no report
		// arrives within cleanupTimeoutSeconds plus a grace) and the preConnect
		// re-warm completion poll that drives the claim recycling → reserved once
		// the SDK re-warm makes the pod Ready. It is shared between the Binder /
		// SlotClaimer (which arm the timeout at the recycle patch) and the
		// scrub-report disposition driver (which cancels it and starts the
		// re-warm poll). It stays nil when --agent-namespace is unset so the
		// recycle path is inert there; the §4.6.1 orphan GC remains the
		// coordinator-crash backstop.
		recycleBoundary *recycle.RecycleBoundaryCoordinator
		// clusterClient is the controller-runtime client used by the
		// session-start path and by the §10.4 PDB poller (F-10.4.4). It
		// stays nil when --agent-namespace is unset (single-process dev).
		clusterClient client.Client
		// kubeHealthzProbe is the §25.3 Kubernetes API server dependency
		// probe (GET /healthz). It stays nil when --agent-namespace is
		// unset, so the §25.3 health surface omits the component on a
		// Postgres-only deployment with no cluster client.
		kubeHealthzProbe func(context.Context) error
		// saTokenVerifier validates the projected SA token's signature and
		// audience on every pod→gateway GatewayControl request via a
		// Kubernetes TokenReview (§10.2 line 227). It stays nil when there
		// is no in-cluster client or no configured audience, in which case
		// the SA-token interceptor degrades to the audience-only decode.
		saTokenVerifier leasecontrol.TokenVerifier
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
		// spec: §25.3 line 441 — the §25.3 health service probes the
		// Kubernetes API server with a GET /healthz over the cluster
		// transport. Built here where the rest config is in scope; wired
		// onto the health aggregator below.
		probe, err := podsession.NewHealthzProbe(cfg)
		if err != nil {
			log.Fatalf("lenny-gateway: build apiserver /healthz probe: %v", err)
		}
		kubeHealthzProbe = probe
		// spec: §10.2 line 227 — "Pods cannot forge or extend this token.
		// The gateway validates the signature on every pod→gateway
		// request." When an audience is configured the gateway validates
		// the projected SA token via a Kubernetes TokenReview (the
		// apiserver checks the SA-issuer signature and expiry), binding the
		// deployment-specific audience at the same time. Built from a
		// clientset here where the rest config is in scope; wired onto the
		// §8.6 GatewayControl listener below. F-10.2.10.
		if *saTokenAudience != "" {
			cs, err := kubernetes.NewForConfig(cfg)
			if err != nil {
				log.Fatalf("lenny-gateway: §10.2 build TokenReview client: %v", err)
			}
			saTokenVerifier = leasecontrol.TokenReviewVerifier{
				Reviews: cs.AuthenticationV1().TokenReviews(),
			}
		}
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
		// the same pod cannot transiently exceed maxConcurrent. The
		// counter (with its §12.4 Postgres fallback) is the only
		// intra-pod capacity gate; a nil counter makes SlotClaimer
		// ClaimSlot and ReleaseSlot fail closed rather than overrun or
		// over-release the pod, so a concurrent-mode pool requires Redis.
		var slotCounter *slotcounter.Counter
		if redisClient != nil {
			// §5.2 line 521: the SessionStore is the post-recovery
			// rehydration seed source. After a Redis restart the first
			// slot reservation on each concurrent-mode pod re-seeds the
			// pod's active_slots counter from GetActiveSlotsByPod before
			// any new slot is allowed, closing the over-commit race.
			// §12.4 line 208: the SessionStore is also the Redis-outage
			// capacity-gate fallback. When Redis is unreachable the counter
			// gates intra-pod slot admission on GetActiveSlotsByPod under a
			// per-pod Postgres advisory lock (ReserveSlotUnderLock), failing
			// closed only after the bounded outage window.
			// §12.4 / §6.57: the Postgres-fallback window is operator-tunable
			// (gateway.slotCounterPostgresFallbackMaxSeconds) so the spec-default
			// 60s bounded outage window is not hardcoded; a non-positive value
			// keeps the default.
			slotCounter = slotcounter.New(concernRedis.For(storerouter.RedisConcernCoordination),
				slotcounter.WithSlotSource(sessions),
				slotcounter.WithFallbackSource(sessions),
				slotcounter.WithFallbackMaxWindow(time.Duration(*slotCounterPostgresFallbackMaxSeconds)*time.Second))
		}
		// §3.2 reserved-hold coordinator: arms the per-claim hold-TTL expiry
		// timer after a recycle reserves the claim, and is cancelled on an
		// acquisition-path rebind. Constructed here (with the cluster client and
		// agent namespace available) and shared with the scrub-report service so
		// the disposition driver and the rebind branch use one timer registry.
		holdCoordinator, err = recycle.NewHoldCoordinator(recycle.HoldCoordinatorOptions{
			Client:    k8sClient,
			Namespace: *agentNamespace,
			Now:       clockinject.Now,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: §3.2 reserved-hold coordinator: %v", err)
		}
		// §3.4 recycle-boundary coordinator: arms the missing-report timeout at
		// the bound → recycling patch and drives the preConnect re-warm
		// completion (recycling → reserved) once the SDK re-warm makes the pod
		// Ready. It hands the re-warm-completion reserve token to the
		// holdCoordinator so the hold-TTL expiry timer is armed on the same
		// registry the non-preConnect reserve uses.
		recycleBoundary, err = recycle.NewRecycleBoundaryCoordinator(recycle.RecycleBoundaryCoordinatorOptions{
			Client:    k8sClient,
			Namespace: *agentNamespace,
			Pools:     pools,
			HoldTTL:   time.Duration(*claimHoldTTLSeconds) * time.Second,
			Holds:     holdCoordinator,
			Now:       clockinject.Now,
		})
		if err != nil {
			log.Fatalf("lenny-gateway: §3.4 recycle-boundary coordinator: %v", err)
		}
		podBinder = &podsession.Binder{
			Client:           k8sClient,
			Namespace:        *agentNamespace,
			AdapterPort:      adapterGRPCPort,
			AcceptedVersions: []string{adapter.ProtocolVersionV1},
			HoldCanceller:    holdCoordinator,
			RecycleBoundary:  recycleBoundary,
			Now:              clockinject.Now,
			DialAdapter: func(addr string) (*adapterclient.Client, error) {
				return adapterclient.Dial(addr, dialOpt, keepaliveOpt)
			},
			Blobs: blobs,
			// §4.9: the binder mints a session's credential leases and
			// pushes them to the pod via AssignCredentials before
			// StartSession. A BindRequest that names no credential pools
			// assigns nothing.
			Credentials: credAssign,
			// §5.2 atomic slot counter (Redis-backed) with its §12.4
			// Postgres-outage fallback; nil makes concurrent-mode slot
			// assignment and release fail closed (see SlotClaimer).
			SlotCounter: slotCounter,
			// §5.1 line 43 — log the runtime.integrationLevel.underdeclared
			// warning when the adapter handshake observes a higher level
			// than the runtime declared, so the author can raise the
			// declared level in a future release.
			IntegrationLevelUnderdeclared: func(runtime, declared, observed string) {
				log.Printf("lenny-gateway: runtime.integrationLevel.underdeclared runtime=%s declaredLevel=%s observedLevel=%s",
					runtime, declared, observed)
			},
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

	// spec: §4.1 — record the §15.1 pod-placement surfaces (and the
	// pod-executor that replaced the dev executor) on the accumulator for
	// the messaging step and the later subsystem and worker steps.
	w.exec = exec
	w.podBinder = podBinder
	w.podRegistry = podRegistry
	w.checkpointSvc = checkpointSvc
	w.holdCoordinator = holdCoordinator
	w.recycleBoundary = recycleBoundary
	w.clusterClient = clusterClient
	w.kubeHealthzProbe = kubeHealthzProbe
	w.saTokenVerifier = saTokenVerifier
	w.sessionSealer = sessionSealer
}

// buildSessionMessaging constructs the §7.3 session-event bus (with its
// §12.4 (lenny:events: relay row) cross-replica relay and §7.3 durable
// last_seq hooks), the §7.2 session inbox and DLQ coordinator, the §8.10
// tree archive, the §8.2 tree
// budget reserver, the §9.2 interaction store, the §7.2 tool-approval gate
// registry, the §10.7 eval store, the §10.7 experiment store, and the §9.4
// memory store, recording each on the accumulator.
//
// spec: §4.1 gateway subsystem seams; §7.2/§7.3 messaging; §8.10 tree
// archive; §8.2 tree budget; §9.2 interactions; §9.4 memory; §10.7 evals.
func (w *gatewayWiring) buildSessionMessaging() {
	f := w.f
	evalAggregationRefreshSeconds := f.evalAggregationRefreshSeconds
	memoryEnabled := f.memoryEnabled
	memoryMaxPerUser := f.memoryMaxPerUser
	messagingDurableInbox := f.messagingDurableInbox
	messagingMaxDLQSize := f.messagingMaxDLQSize
	messagingMaxInboxSize := f.messagingMaxInboxSize
	sessionEventReplayBufferDepth := f.sessionEventReplayBufferDepth
	toolApprovalTimeout := f.toolApprovalTimeout
	treeArchiveCacheEntries := f.treeArchiveCacheEntries

	// The stores, Redis client, and executor were recorded on the
	// accumulator by the earlier build steps.
	pgPool := w.pgPool
	readPool := w.readPool
	redisClient := w.redisClient
	concernRedis := w.concernRedis
	sessions := w.sessions
	exec := w.exec

	// spec: §10.4 line 389 — replay buffer depth is operator-tunable
	// via gateway.sessionEventReplayBufferDepth. F-10.4.5.
	eventBus := sessionevents.NewBus(*sessionEventReplayBufferDepth)
	// §4.4 line 225 / §12.4 (lenny:events: relay row): when Redis is
	// wired, attach the cross-replica relay so a client reconnecting via
	// Last-Event-ID to a different replica sees prior events (the §15.1
	// streaming-reconnect contract). Single-replica dev mode keeps the
	// Bus's in-memory-only behaviour.
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
		treeArchive = treearchive.NewCached(treearchivepg.New(pgPool, nil, treearchivepg.WithReadPool(readPool)), *treeArchiveCacheEntries)
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
			sessions, interactions, eventBus, toolApprovalWaits, clockinject.Now, *toolApprovalTimeout,
		))
	}
	var evals evalstore.Store = evalstore.NewMemory(0, nil)
	if pgPool != nil {
		evals = evalpg.New(pgPool)
	}
	// spec: §10.7 line 1088 — the lenny_eval_aggregates materialized-view
	// read + REFRESH path requires a Postgres backend. Enable it only when
	// a positive refresh interval is configured against Postgres; with the
	// default 0 (or no Postgres) the §10.7 results API aggregates on read.
	evalMatviewEnabled := false
	if *evalAggregationRefreshSeconds > 0 {
		if pgPool != nil {
			evalMatviewEnabled = true
		} else {
			log.Printf("warning: --eval-aggregation-refresh-seconds=%d ignored: the §10.7 lenny_eval_aggregates materialized view requires a Postgres backend; aggregating on read", *evalAggregationRefreshSeconds)
		}
	} else if *evalAggregationRefreshSeconds < 0 {
		log.Printf("warning: --eval-aggregation-refresh-seconds=%d is negative; treating as 0 (matview disabled)", *evalAggregationRefreshSeconds)
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

	// spec: §4.1 — record the §7.2/§7.3 messaging surfaces and the §8.10 /
	// §8.2 / §9.2 / §9.4 / §10.7 per-session shared stores on the
	// accumulator for the §16.1 metrics step and the later subsystem,
	// admin, and worker steps. Each field is the local this step produced.
	w.eventBus = eventBus
	w.messagingCoord = messagingCoord
	w.treeArchive = treeArchive
	w.treeBudgetReserver = treeBudgetReserver
	w.hwmReader = hwmReader
	w.treeBudgetConcrete = treeBudgetConcrete
	w.interactions = interactions
	w.toolApprovalWaits = toolApprovalWaits
	w.evals = evals
	w.evalMatviewEnabled = evalMatviewEnabled
	w.experiments = experiments
	w.memories = memories
	w.memoryBackendLabel = memoryBackendLabel
}

// runServers registers the §25.13 in-process alert tracker, installs the
// signal handler, starts the §11.7 / §12.3 audit background loops and the
// HTTP, LLM-proxy, and GatewayControl listeners, then blocks on SIGTERM /
// SIGINT and runs the §17 graceful-shutdown drain. It is the tail of the
// gateway composition root: every component it serves is constructed by an
// earlier build step and threaded through the accumulator.
//
// spec: §17 — the run-and-shutdown loop; §25.13 in-process alert tracker.
