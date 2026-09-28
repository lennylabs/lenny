// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/audit/pgaudit"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingretention"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingsink"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/failover/redisstream"
	billingpg "github.com/lennylabs/lenny/pkg/gateway/billing/billingstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/storage/redistopology"
	"github.com/lennylabs/lenny/pkg/storerouter"
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
		// spec: §17.8.2 — size the per-tenant billing stream MAXLEN.
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
	// spec: §11.2.1 — wrap the durable primary so every
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
		// §12.3 billingFlushIntervalMs / billingFlushBatchSize /
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

	// spec: §11.2.1 / §12.8 — reject a retention window
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

	// spec: §11.7 item 2 — resolve the periodic background
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

	// spec: §11.7 — a regulated-profile tenant with no configured
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

	// spec: §11.7 — a regulated-profile tenant additionally
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

	// spec: §11.7 — when audit.pgaudit.enabled is true, verify
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

// activeComplianceProfiles returns the distinct, non-empty
// complianceProfile values across the registered tenants. It backs the
// §11.2.1 billing.retentionDays compliance-floor preflight: a profile
// active on any tenant raises the deployment's retention floor.
// spec: §11.2.1. F-11.2.15.
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

// buildBillingPipeline constructs the §11.2.1 two-tier billing failover
// pipeline (the durable ledger wrapped in the Redis-stream failover Tier so
// a transient Postgres outage never drops a billing event), the billing
// fan-out emitter, and the resolved §11.7 audit-retention and grant-check
// schedule, recording each on the accumulator. It is an extracted
// per-concern step of buildStores.
//
// spec: §4.1 gateway subsystem seams; §11.2.1 billing failover; §11.7 audit
// retention.
