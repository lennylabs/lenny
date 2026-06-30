// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"log"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quotacheckpoint"
	quotacheckpointpg "github.com/lennylabs/lenny/pkg/gateway/quotacheckpoint/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
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
		w.quotaCheckpointSvc = &quotacheckpoint.Service{
			Store:    quotacheckpointpg.New(w.pgPool),
			Subjects: quotacheckpoint.SessionSubjectLister{Sessions: w.sessions, Tenants: (tenantsLister{w.tenants}).ListTenants},
			Periods: quotacheckpoint.PeriodResolverFunc(func(ctx context.Context, tenantID string) (quota.ResetPeriod, error) {
				lim, err := limitLookup.LookupLimits(ctx, tenantID)
				if err != nil {
					return "", err
				}
				return lim.Period, nil
			}),
			Reader:   quotaCounter,
			Restorer: quotaCounter,
			// §12.4 source (2): fold the in-memory fail-open accumulator into
			// the MAX rule on the Redis-recovery edge. F-12.4.20.
			FailOpen: quotaFailOpenAccum,
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
