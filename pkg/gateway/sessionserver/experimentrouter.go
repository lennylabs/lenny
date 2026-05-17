// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/ofrep"
	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// variantIsolationError is returned by applyExperimentRouting when the
// §10.7 ExperimentRouter fails closed: the assigned variant pool's
// isolation profile is weaker than the session's. The handler maps it
// to 422 VARIANT_ISOLATION_UNAVAILABLE.
type variantIsolationError struct {
	ExperimentID         string
	VariantID            string
	SessionMinIsolation  string
	VariantPoolIsolation string
}

func (e *variantIsolationError) Error() string {
	return fmt.Sprintf(
		"variant %q pool isolation %s is weaker than the session's %s",
		e.VariantID, e.VariantPoolIsolation, e.SessionMinIsolation)
}

// ExperimentRejectionReporter records a §10.7 ExperimentRouter
// fail-closed rejection. The gateway wires an implementation that
// emits the `experiment.isolation_mismatch` operational event and
// increments the `lenny_experiment_isolation_rejections_total` counter
// (§16.1). Defining the interface here keeps the session server
// decoupled from the audit and metrics subsystems, matching the
// DeriveAuditSink pattern. A nil reporter disables reporting; the 422
// rejection still fires.
type ExperimentRejectionReporter interface {
	ReportExperimentIsolationRejection(ctx context.Context, ev ExperimentIsolationRejection)
}

// ExperimentIsolationRejection is the §16.6 `experiment.isolation_mismatch`
// event payload. Field names carry through to the operational-event
// record and the §16.1 counter labels.
type ExperimentIsolationRejection struct {
	TenantID             string
	UserID               string
	ExperimentID         string
	VariantID            string
	SessionMinIsolation  string
	VariantPoolIsolation string
}

// applyExperimentRouting runs the §10.7 ExperimentRouter over a
// session at creation. The tenant's experiments whose baseRuntime
// matches the session's requested runtime are evaluated in created_at
// order; the first one that buckets the session to a non-control
// variant enrolls it. On enrollment the session's runtime and pool are
// rewritten to the variant's and the §10.7 experimentContext is
// recorded. A session that no experiment enrolls is left unchanged and
// runs the base runtime.
//
// §10.7 ExperimentRouter isolation monotonicity: when the assigned
// variant pool's isolation profile is weaker than the session's, the
// router fails closed — it returns a *variantIsolationError and the
// session is not created, rather than silently routing the session
// into a less-isolated pool.
func (s *Server) applyExperimentRouting(ctx context.Context, row *sessionstore.Session) error {
	if s.experiments == nil {
		return nil
	}
	all, err := s.experiments.List(ctx, row.TenantID)
	if err != nil || len(all) == 0 {
		return nil
	}
	candidates := make([]experimentstore.Experiment, 0, len(all))
	for _, e := range all {
		if e.BaseRuntime == row.RuntimeRef {
			candidates = append(candidates, e)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
	defs := make([]experiment.Definition, len(candidates))
	for i, e := range candidates {
		defs[i] = e.Definition()
	}
	// §10.7: percentage-mode experiments bucket by the built-in hash;
	// mode:external experiments resolve through the tenant's OpenFeature
	// provider. A tenant with no external targeting yields a nil
	// evaluator and RouteMixed skips external-mode experiments.
	evaluator := s.buildExternalEvaluator(ctx, row, candidates)
	assignment := experiment.RouteMixed(defs, row.UserID, row.ID, evaluator)
	if assignment.ExperimentID == "" {
		return nil
	}
	variant, ok := findVariant(candidates, assignment.ExperimentID, assignment.VariantID)
	if !ok {
		return nil
	}
	// §10.7 fail-closed isolation check: the variant pool must be at
	// least as restrictive as the session's isolation profile.
	if err := s.checkVariantIsolation(ctx, row, assignment, variant); err != nil {
		return err
	}
	row.ExperimentContext = &sessionstore.ExperimentContext{
		ExperimentID: assignment.ExperimentID,
		VariantID:    assignment.VariantID,
	}
	if variant.Runtime != "" {
		row.RuntimeRef = variant.Runtime
	}
	if variant.Pool != "" {
		row.PoolRef = variant.Pool
	}
	// §16.6: the first-match rule left every routable experiment created
	// after the enrolled one unevaluated. Emit experiment.multi_eligible_skipped
	// so deployers can audit enrollment overlap.
	s.emitMultiEligibleSkipped(row, assignment.ExperimentID,
		experiment.SkippedAfter(defs, assignment.ExperimentID, evaluator != nil))
	return nil
}

// emitMultiEligibleSkipped records the §16.6
// experiment.multi_eligible_skipped operational event when the
// first-match rule left one or more later-created experiments
// unevaluated for an enrolled session. It is best-effort: a nil
// emitter or an empty skipped set is a no-op, so experiment routing
// never depends on the event buffer being wired.
func (s *Server) emitMultiEligibleSkipped(row *sessionstore.Session, enrolledID string, skipped []string) {
	if s.opsEmitter == nil || len(skipped) == 0 {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"tenant_id":              row.TenantID,
		"user_id":                row.UserID,
		"enrolled_experiment_id": enrolledID,
		"skipped_experiment_ids": skipped,
	})
	s.opsEmitter.Emit(opsevents.OperationalEvent{
		Source:          "/v1/sessions",
		Type:            opsevents.EventExperimentMultiEligibleSkipped.CloudEventsType(),
		Severity:        "info",
		DataContentType: "application/json",
		Data:            data,
	})
}

// buildExternalEvaluator returns the §10.7 OpenFeature external-targeting
// evaluator for a session, or nil when the tenant configures no
// external targeting or a provider this build does not ship. The
// closure calls the OFREP endpoint once per mode:external experiment;
// per §10.7 a provider failure makes no external assignment for any
// experiment, so the closure latches the failure, emits
// experiment.targeting_failed once, and returns no enrollment for
// every subsequent experiment.
func (s *Server) buildExternalEvaluator(ctx context.Context, row *sessionstore.Session, candidates []experimentstore.Experiment) experiment.ExternalEvaluator {
	if s.tenants == nil {
		return nil
	}
	tenant, err := s.tenants.Get(ctx, row.TenantID)
	if err != nil || !tenant.ExperimentTargeting.Configured() {
		return nil
	}
	cfg := tenant.ExperimentTargeting
	// v1 ships the OFREP transport; the built-in SDK providers
	// (launchdarkly, statsig, unleash) need their vendor SDKs.
	if cfg.Provider != experiment.TargetingProviderOFREP || cfg.OFREP == nil {
		return nil
	}
	client, err := ofrep.New(ofrep.Options{
		BaseURL: cfg.OFREP.Endpoint,
		Headers: cfg.OFREP.Headers,
		Timeout: time.Duration(cfg.EffectiveTimeoutMs()) * time.Millisecond,
	})
	if err != nil {
		return nil
	}
	evalCtx := experiment.BuildEvaluationContext(experiment.EvaluationContextInput{
		UserID:    row.UserID,
		TenantID:  row.TenantID,
		SessionID: row.ID,
		Runtime:   row.RuntimeRef,
	})
	failed := false
	return func(experimentID string) (string, bool) {
		if failed {
			return "", false
		}
		result, err := client.Evaluate(ctx, experimentID, evalCtx)
		if err != nil {
			failed = true
			s.emitExperimentTargetingFailed(row, string(cfg.Provider), err)
			return "", false
		}
		variantID, known := experiment.ResolveExternalVariant(
			result.Variant, result.Value, variantIDsOf(candidates, experimentID))
		if !known {
			s.emitExperimentUnknownVariant(row, experimentID, string(cfg.Provider), result.Variant)
			return "", false
		}
		return variantID, true
	}
}

// variantIDsOf returns the registered variant IDs of the named
// experiment among candidates — the set ResolveExternalVariant matches
// a provider response against.
func variantIDsOf(candidates []experimentstore.Experiment, experimentID string) []string {
	for _, e := range candidates {
		if e.ID != experimentID {
			continue
		}
		ids := make([]string, len(e.Variants))
		for i, v := range e.Variants {
			ids[i] = v.ID
		}
		return ids
	}
	return nil
}

// emitExperimentTargetingFailed records the §16.6
// experiment.targeting_failed operational event. Best-effort: a nil
// emitter is a no-op.
func (s *Server) emitExperimentTargetingFailed(row *sessionstore.Session, provider string, evalErr error) {
	if s.opsEmitter == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"tenant_id": row.TenantID,
		"user_id":   row.UserID,
		"provider":  provider,
		"error":     evalErr.Error(),
	})
	s.opsEmitter.Emit(opsevents.OperationalEvent{
		Source:          "/v1/sessions",
		Type:            opsevents.EventExperimentTargetingFailed.CloudEventsType(),
		Severity:        "warning",
		DataContentType: "application/json",
		Data:            data,
	})
}

// emitExperimentUnknownVariant records the §16.6
// experiment.unknown_variant_from_provider operational event when an
// OpenFeature provider returns a variant the experiment does not
// register. Best-effort: a nil emitter is a no-op.
func (s *Server) emitExperimentUnknownVariant(row *sessionstore.Session, experimentID, provider, rawVariant string) {
	if s.opsEmitter == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"tenant_id":     row.TenantID,
		"user_id":       row.UserID,
		"experiment_id": experimentID,
		"provider":      provider,
		"raw_variant":   rawVariant,
	})
	s.opsEmitter.Emit(opsevents.OperationalEvent{
		Source:          "/v1/sessions",
		Type:            opsevents.EventExperimentUnknownVariantFromProvider.CloudEventsType(),
		Severity:        "warning",
		DataContentType: "application/json",
		Data:            data,
	})
}

// routeExperiment applies the §10.7 ExperimentRouter to a session at
// creation. When the router fails closed on the isolation-monotonicity
// check it writes the §15.1 `422 VARIANT_ISOLATION_UNAVAILABLE`
// response and returns false; the caller must then abort. It returns
// true when the session may proceed to persistence.
func (s *Server) routeExperiment(w http.ResponseWriter, r *http.Request, row *sessionstore.Session) bool {
	err := s.applyExperimentRouting(r.Context(), row)
	if err == nil {
		return true
	}
	var ve *variantIsolationError
	if errors.As(err, &ve) {
		// §10.7 / §16.6: the fail-closed rejection emits the
		// experiment.isolation_mismatch event and increments the
		// rejection counter alongside the 422.
		if s.experimentReporter != nil {
			s.experimentReporter.ReportExperimentIsolationRejection(r.Context(), ExperimentIsolationRejection{
				TenantID:             row.TenantID,
				UserID:               row.UserID,
				ExperimentID:         ve.ExperimentID,
				VariantID:            ve.VariantID,
				SessionMinIsolation:  ve.SessionMinIsolation,
				VariantPoolIsolation: ve.VariantPoolIsolation,
			})
		}
		s.writeError(w, http.StatusUnprocessableEntity, "VARIANT_ISOLATION_UNAVAILABLE",
			"the assigned experiment variant pool's isolation profile is weaker than the session's minimum",
			map[string]any{
				"experimentId":         ve.ExperimentID,
				"variantId":            ve.VariantID,
				"sessionMinIsolation":  ve.SessionMinIsolation,
				"variantPoolIsolation": ve.VariantPoolIsolation,
			})
		return false
	}
	s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	return false
}

// findVariant locates the assigned variant within the candidate
// experiments.
func findVariant(candidates []experimentstore.Experiment, experimentID, variantID string) (experimentstore.Variant, bool) {
	for _, e := range candidates {
		if e.ID != experimentID {
			continue
		}
		for _, v := range e.Variants {
			if v.ID == variantID {
				return v, true
			}
		}
	}
	return experimentstore.Variant{}, false
}

// checkVariantIsolation enforces the §10.7 ExperimentRouter
// isolation-monotonicity rule. It is a no-op when the pool store is
// not wired or the variant pool is unresolvable.
func (s *Server) checkVariantIsolation(ctx context.Context, row *sessionstore.Session, a experiment.Assignment, v experimentstore.Variant) error {
	if s.pools == nil || v.Pool == "" || row.IsolationProfile == "" {
		return nil
	}
	pool, err := s.pools.Get(ctx, v.Pool)
	if err != nil || !isolation.IsValid(pool.IsolationProfile) {
		return nil
	}
	if isolation.AtLeastAsRestrictive(pool.IsolationProfile, row.IsolationProfile) {
		return nil
	}
	return &variantIsolationError{
		ExperimentID:         a.ExperimentID,
		VariantID:            a.VariantID,
		SessionMinIsolation:  string(row.IsolationProfile),
		VariantPoolIsolation: string(pool.IsolationProfile),
	}
}
