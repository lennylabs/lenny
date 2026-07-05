// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"time"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint"
	quotacheckpointpg "github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint/pgstore"
	mtlsdenylist "github.com/lennylabs/lenny/pkg/mtls/denylist"
	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
	"github.com/lennylabs/lenny/pkg/quota"
)

// buildQuotaCheckpoint is the §4.1 composition-root build step (R1) for the
// §11.2 token-usage Postgres checkpoint and the §24.6 reconcile service. The
// Service persists each active (tenant, user) window total and the per-tenant
// rollup to the token_usage_checkpoint table on the quotaSyncIntervalSeconds
// cadence, writes a final checkpoint on session completion, and restores
// counters to MAX(redis_current, postgres_checkpoint) on Redis recovery and on
// the operator-driven §24.6 reconcile. It is active only when the Redis quota
// counter, the Postgres pool, and the SessionStore are all wired; otherwise
// the §24.6 endpoint stays the 503 stub and the final write is a no-op. It
// records the service on the accumulator. F-11.2.4 / F-24.6.1 / F-24.6.2 /
// F-24.6.3.
//
// spec: §4.1 gateway subsystem seams; §11.2 token-usage checkpoint; §24.6
// reconcile.
func (w *gatewayWiring) buildQuotaCheckpoint() {
	quotaCounter := w.quotaCounter
	tenantLimits := w.tenantLimits
	quotaFailOpenAccum := w.quotaFailOpenAccum
	gwMetrics := w.gwMetrics

	if quotaCounter != nil && w.pgPool != nil && w.sessions != nil {
		limitLookup := tenantLimits
		periods := quotacheckpoint.PeriodResolverFunc(func(ctx context.Context, tenantID string) (quota.ResetPeriod, error) {
			lim, err := limitLookup.LookupLimits(ctx, tenantID)
			if err != nil {
				return "", err
			}
			return lim.Period, nil
		})
		w.quotaCheckpointSvc = &quotacheckpoint.Service{
			Store:    quotacheckpointpg.New(w.pgPool),
			Subjects: quotacheckpoint.SessionSubjectLister{Sessions: w.sessions, Tenants: (tenantsLister{w.tenants}).ListTenants},
			Periods:  periods,
			Reader:   quotaCounter,
			Restorer: quotaCounter,
			// §12.4 source (2): fold the in-memory fail-open accumulator into
			// the MAX rule on the Redis-recovery edge. F-12.4.20.
			FailOpen: quotaFailOpenAccum,
			// §11.2 line 46 source: fold each bound direct-mode session's
			// pod-reported cumulative total into the MAX rule on the
			// Redis-recovery edge so a reconnected replica reconstructs a
			// direct-mode counter as MAX(redis, postgres, failopen,
			// pod_reported_cumulative) rather than under-counting a session whose
			// Redis usage was lost. Active only when the pod registry, the lease
			// store, and the SessionStore are all present; otherwise a nil reader
			// leaves the MAX at MAX(redis, postgres, failopen). F-15.3.7.
			PodUsage: w.buildDirectUsageRecoveryReader(periods),
			Tenants: quotacheckpoint.TenantExistsFunc(func(ctx context.Context, tenantID string) (bool, error) {
				if _, err := w.tenants.Get(ctx, tenantID); err != nil {
					if errors.Is(err, tenantstore.ErrNotFound) {
						return false, nil
					}
					return false, err
				}
				return true, nil
			}),
			Metrics: gwMetrics,
			Now:     clockinject.Now,
			Logf:    log.Printf,
		}
	}
}

// buildDirectUsageRecoveryReader constructs the §11.2 line 46 crash-recovery
// pod-usage source for the quota checkpoint Service. It is active only when
// the pod registry, the §4.9 credential-lease store, and the SessionStore are
// all wired; otherwise it returns a genuinely nil quotacheckpoint.PodUsageReader
// so a minimal gateway degrades to the MAX(redis, postgres, failopen) rule.
// Returning the concrete typed nil directly would wrap into a non-nil
// interface, so the guard returns the untyped nil explicitly. spec: §11.2 line
// 46; §4.7 (ReportUsage cumulative read).
func (w *gatewayWiring) buildDirectUsageRecoveryReader(periods quotacheckpoint.PeriodResolver) quotacheckpoint.PodUsageReader {
	if w.podRegistry == nil || w.llmLeases == nil || w.sessions == nil {
		return nil
	}
	reader := newDirectUsageRecoveryReader(
		w.podRegistry,
		w.llmLeases,
		sessionStoreSubjectResolver{sessions: w.sessions},
		periods,
		time.Duration(clampDirectUsagePollIntervalSeconds(*w.f.directUsagePollIntervalSeconds))*time.Second/2,
		slog.Default(),
	)
	if reader == nil {
		return nil
	}
	return reader
}

// buildInterceptorRegistration is the §4.1 composition-root build step (R1)
// that registers the deployer-supplied §4.8 external interceptors and the
// §4.8 guardrails classifier onto the policy chain the previous step built. It
// constructs the §10.3 NET-063 interceptor peer-validation context (the
// per-replica deny list and the §16.1 handshake-histogram observer), records
// the deny list on the accumulator for the control server, dials and registers
// each external interceptor at its named phase (applying the reserved-priority
// ceiling and the PreAuth restriction so a misconfigured priority or phase
// fails fast at startup), and dials and registers the guardrails classifier at
// the fixed §4.8 priority when --guardrails-classifier is set.
//
// spec: §4.1 gateway subsystem seams; §4.8 external interceptors / guardrails;
// §10.3 interceptor mTLS peer validation.
func (w *gatewayWiring) buildInterceptorRegistration() {
	f := w.f
	spiffeTrustDomain := f.spiffeTrustDomain
	interceptorNamespaces := f.interceptorNamespaces
	externalInterceptors := f.externalInterceptors
	externalInterceptorTLSCert := f.externalInterceptorTLSCert
	externalInterceptorTLSKey := f.externalInterceptorTLSKey
	externalInterceptorCA := f.externalInterceptorCA
	guardrailsClassifier := f.guardrailsClassifier

	policyChain := w.policyChain
	gwMetrics := w.gwMetrics

	// §10.3 NET-063: the shared interceptor peer-validation context for
	// every gateway→interceptor dial below. The deny list is the same
	// per-replica set the propagator drives (F-10.3.7); the observer is
	// the §16.1 handshake histogram. F-10.3.3.
	mtlsDeny := mtlsdenylist.New()
	w.mtlsDeny = mtlsDeny
	interceptorID := interceptorIdentity{
		trustDomain: *spiffeTrustDomain,
		namespaces:  splitAndTrim(*interceptorNamespaces),
		denyList:    mtlsDeny,
		observe:     gwMetrics.ObserveInterceptorMTLSHandshake,
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
		conn, err := dialInterceptor(spec.Endpoint, *externalInterceptorTLSCert, *externalInterceptorTLSKey, *externalInterceptorCA, interceptorID)
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
		conn, err := dialInterceptor(spec.Endpoint, *externalInterceptorTLSCert, *externalInterceptorTLSKey, *externalInterceptorCA, interceptorID)
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
}
