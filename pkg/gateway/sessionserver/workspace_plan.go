// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// storedWorkspacePlan re-parses the §14 WorkspacePlan recorded on the
// session row at create. It returns the zero Plan when the session was
// created without a plan. The plan was validated at create, so a parse
// failure here indicates gateway-version skew against the stored plan.
// ParseStored is used because the stored plan carries the
// gateway-written resolvedCommitSha that Parse rejects as client input.
func storedWorkspacePlan(row sessionstore.Session) (workspaceplan.Plan, error) {
	if len(row.WorkspacePlan) == 0 || isJSONNull(row.WorkspacePlan) {
		return workspaceplan.Plan{}, nil
	}
	plan, _, err := workspaceplan.ParseStored(row.WorkspacePlan)
	return plan, err
}

// resolvePlanForCreate parses a client-submitted §14 WorkspacePlan and,
// when a RefResolver is wired and the plan has a gitClone source, pins
// each gitClone ref to an immutable commit SHA per §14. It returns the
// parsed plan, the canonical JSON to persist on the session row (the
// pinned form when pinning occurred, the submitted bytes otherwise),
// and the consumer-advisory warnings. On a validation or
// ref-resolution failure it writes the §15.1 error response and
// returns ok=false; the caller must abort.
func (s *Server) resolvePlanForCreate(w http.ResponseWriter, r *http.Request, rawPlan json.RawMessage) (
	plan workspaceplan.Plan, storedJSON json.RawMessage, warnings []workspaceplan.Warning, ok bool,
) {
	if len(rawPlan) == 0 || isJSONNull(rawPlan) {
		return workspaceplan.Plan{}, nil, nil, true
	}
	parsed, warns, err := workspaceplan.Parse(rawPlan)
	if err != nil {
		s.writeWorkspacePlanError(w, err)
		return workspaceplan.Plan{}, nil, nil, false
	}
	storedJSON = rawPlan
	if !s.checkGitCloneAuthBindings(w, r, parsed) {
		return workspaceplan.Plan{}, nil, nil, false
	}
	if s.refResolver != nil && hasGitClone(parsed) {
		if err := workspaceplan.PinCommitSHAs(r.Context(), &parsed, s.refResolver, s.vcsCredentialFunc(r)); err != nil {
			s.writeRefResolveError(w, err)
			return workspaceplan.Plan{}, nil, nil, false
		}
		pinned, err := workspaceplan.Marshal(parsed)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"could not serialize the resolved workspace plan: "+err.Error(), nil)
			return workspaceplan.Plan{}, nil, nil, false
		}
		storedJSON = pinned
	}
	return parsed, storedJSON, warns, true
}

// hasGitClone reports whether the plan has at least one gitClone
// source — the only source type whose ref the gateway must pin.
func hasGitClone(plan workspaceplan.Plan) bool {
	for _, src := range plan.Sources {
		if src.Type == workspaceplan.TypeGitClone {
			return true
		}
	}
	return false
}

// vcsCredentialFunc returns the §14 credential materializer PinCommitSHAs
// uses to authenticate the ls-remote that pins a private gitClone source.
// It binds to the request's tenant and resolves each source through the
// wired VCS resolver, so a private repo's ref resolution uses the same
// credential the clone will (§14). A source with no auth block
// resolves to a zero credential (public). It returns nil when no resolver
// is wired, leaving public-only resolution unchanged.
func (s *Server) vcsCredentialFunc(r *http.Request) workspaceplan.VCSCredentialFunc {
	if s.vcsCreds == nil {
		return nil
	}
	tenantID := s.resolveTenant(r)
	return func(ctx context.Context, gc workspaceplan.GitClone) (workspaceplan.VCSCredential, error) {
		if gc.Auth == nil {
			return workspaceplan.VCSCredential{}, nil
		}
		c, err := s.vcsCreds.Resolve(ctx, tenantID, gc.URL, gc.Auth.LeaseScope)
		if err != nil {
			return workspaceplan.VCSCredential{}, err
		}
		return workspaceplan.VCSCredential{Username: c.Username, Token: c.Token}, nil
	}
}

// checkGitCloneAuthBindings runs the §14 gitClone auth host-to-pool
// check for every gitClone source carrying an auth block. When a
// CredentialPools store is wired, each such source's URL host must
// bind to exactly one of the tenant's VCS credential pools whose
// provider matches the leaseScope; a binding failure writes the §15.1
// GIT_CLONE_AUTH_UNSUPPORTED_HOST or GIT_CLONE_AUTH_HOST_AMBIGUOUS
// response and returns false. With no store wired the check is
// skipped, so a gateway without one is unchanged.
func (s *Server) checkGitCloneAuthBindings(w http.ResponseWriter, r *http.Request, plan workspaceplan.Plan) bool {
	if s.credPools == nil {
		return true
	}
	var pools []credentialpoolstore.CredentialPool
	loaded := false
	for i, src := range plan.Sources {
		gc, ok := src.Variant.(workspaceplan.GitClone)
		if !ok || gc.Auth == nil {
			continue
		}
		host, hostOK := workspaceplan.GitCloneHost(gc)
		provider, _, scopeOK := workspaceplan.ParseLeaseScope(gc.Auth.LeaseScope)
		if !hostOK || !scopeOK {
			// validateGitClone already guaranteed a parseable HTTPS URL
			// and a well-formed leaseScope; nothing to bind otherwise.
			continue
		}
		if !loaded {
			ps, err := s.credPools.List(r.Context(), s.resolveTenant(r), credentialpoolstore.ListFilter{})
			if err != nil {
				s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"could not load credential pools: "+err.Error(), nil)
				return false
			}
			pools, loaded = ps, true
		}
		if _, err := credentialpoolstore.ResolveVCSPool(pools, provider, host); err != nil {
			s.writeVCSResolveError(w, err, i)
			return false
		}
	}
	return true
}

// writeVCSResolveError maps a §14 VCS-pool binding failure to its
// §15.1 response: GIT_CLONE_AUTH_UNSUPPORTED_HOST or
// GIT_CLONE_AUTH_HOST_AMBIGUOUS, both HTTP 422.
func (s *Server) writeVCSResolveError(w http.ResponseWriter, err error, sourceIndex int) {
	var ve *credentialpoolstore.VCSResolveError
	if !errors.As(err, &ve) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	details := map[string]any{"host": ve.Host, "sourceIndex": sourceIndex}
	if ve.Reason == credentialpoolstore.VCSHostAmbiguous {
		details["matchingPools"] = ve.MatchingPools
		s.writeError(w, http.StatusUnprocessableEntity, "GIT_CLONE_AUTH_HOST_AMBIGUOUS",
			"the gitClone URL host matches multiple VCS credential pools", details)
		return
	}
	s.writeError(w, http.StatusUnprocessableEntity, "GIT_CLONE_AUTH_UNSUPPORTED_HOST",
		"the gitClone URL host matches no VCS credential pool", details)
}

// writeRefResolveError maps a §14 gitClone ref-resolution failure to
// its §15.1 response: a transient failure is GIT_CLONE_REF_RESOLVE_TRANSIENT
// (503, retryable), a permanent one is GIT_CLONE_REF_UNRESOLVABLE (422).
func (s *Server) writeRefResolveError(w http.ResponseWriter, err error) {
	var re *workspaceplan.ResolveError
	if !errors.As(err, &re) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	details := map[string]any{
		"url":         re.URL,
		"ref":         re.Ref,
		"sourceIndex": re.SourceIndex,
		"reason":      string(re.Reason),
	}
	if re.Reason.Transient() {
		s.writeError(w, http.StatusServiceUnavailable, "GIT_CLONE_REF_RESOLVE_TRANSIENT",
			"could not resolve a gitClone ref: "+re.Err.Error(), details)
		return
	}
	s.writeError(w, http.StatusUnprocessableEntity, "GIT_CLONE_REF_UNRESOLVABLE",
		"could not resolve a gitClone ref: "+re.Err.Error(), details)
}

// enforceSetupCommandPolicy runs the §5.1 / §7.5 setupCommandPolicy
// validation against the client-supplied workspace plan before the row is
// persisted. The §7.5 contract names maxCommands as a per-session
// cap the gateway enforces; §7.5 add the allowlist /
// blocklist prefix gate. Without these, the worst-case input is Go's slice
// limit and any setup command at all, which lets a buggy or malicious
// client DoS the setup phase or run an arbitrary command in the pod. The
// cap and prefix lists come from the runtime's effective (base→derived
// merged) setupCommandPolicy. A missing policy block declares no cap and
// no list-based gate, preserving the pre-F-7.5.1 admit-everything path.
//
// On any violation the helper writes the §15.1 WORKSPACE_PLAN_INVALID
// envelope with a structured details payload and returns ok=false; the
// caller MUST abort. The §7.5 contract is also recorded on the request's
// rejected setup-output sink (F-7.5.4 / F-7.5.11) when one is wired —
// callers that already write the error to the response carry the same
// reason string via the WORKSPACE_PLAN_INVALID envelope.
//
// spec: §5.1, §7.5 — F-7.5.1 / F-7.5.5.
func (s *Server) enforceSetupCommandPolicy(w http.ResponseWriter, r *http.Request,
	runtimeRef string, plan workspaceplan.Plan,
) bool {
	if s.runtimes == nil || runtimeRef == "" {
		return true
	}
	rt, err := runtimestore.Resolve(r.Context(), s.runtimes, runtimeRef)
	if err != nil {
		// A missing or unresolvable runtime is surfaced by the §7.1
		// session-creation path's own validation; enforceSetupCommandPolicy
		// stays out of that error envelope and just admits the request.
		return true
	}
	policy := rt.SetupCommandPolicy
	if policy == nil {
		return true
	}
	if policy.MaxCommands > 0 {
		if got := len(plan.SetupCommands); got > policy.MaxCommands {
			s.writeError(w, http.StatusBadRequest, "WORKSPACE_PLAN_INVALID",
				fmt.Sprintf("setupCommands count %d exceeds the runtime setupCommandPolicy.maxCommands cap %d",
					got, policy.MaxCommands),
				map[string]any{
					"field":       "setupCommands",
					"reason":      "setup_commands_max_exceeded",
					"maxCommands": policy.MaxCommands,
					"count":       got,
				})
			return false
		}
	}
	if policy.Mode == "" {
		return true
	}
	for i, c := range plan.SetupCommands {
		if policy.PermitsCommand(c.Cmd) {
			continue
		}
		// spec: §7.5 — the rejection reason carries the offending
		// command, its position, and the active mode so an operator can
		// reconcile against the runtime's setupCommandPolicy without
		// parsing the human message. The reason string is identical to the
		// audit-trail / setup-output payload (F-7.5.4 / F-7.5.11).
		s.writeError(w, http.StatusBadRequest, "WORKSPACE_PLAN_INVALID",
			fmt.Sprintf("setupCommands[%d] %q rejected by runtime setupCommandPolicy (mode=%s)",
				i, c.Cmd, policy.Mode),
			map[string]any{
				"field":   "setupCommands",
				"reason":  "setup_command_policy_violation",
				"mode":    string(policy.Mode),
				"index":   i,
				"command": c.Cmd,
			})
		return false
	}
	return true
}

// writeWorkspacePlanError translates a workspaceplan.ValidationError
// into the §15.1 `400 WORKSPACE_PLAN_INVALID` envelope. The one
// exception is an unsupported schemaVersion: per §14.1 the
// gateway is a live consumer that MUST reject a plan whose schemaVersion
// it does not understand with `422 WORKSPACE_PLAN_SCHEMA_UNSUPPORTED`,
// carrying `details.knownVersion` / `details.encounteredVersion` so a
// client can tell "bad plan" apart from "gateway too old." F-14.1.1.
func (s *Server) writeWorkspacePlanError(w http.ResponseWriter, err error) {
	var ve *workspaceplan.ValidationError
	if !errors.As(err, &ve) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ve.Reason == workspaceplan.ReasonUnsupportedSchemaVersion {
		details := map[string]any{"reason": ve.Reason}
		if ve.Field != "" {
			details["field"] = ve.Field
		}
		// spec: §14.1 — `details.knownVersion` /
		// `details.encounteredVersion` are mandatory on this envelope.
		if ve.KnownVersion != nil {
			details["knownVersion"] = *ve.KnownVersion
		}
		if ve.EncounteredVersion != nil {
			details["encounteredVersion"] = *ve.EncounteredVersion
		}
		s.writeError(w, http.StatusUnprocessableEntity, "WORKSPACE_PLAN_SCHEMA_UNSUPPORTED", ve.Error(), details)
		return
	}
	details := map[string]any{}
	if ve.Field != "" {
		details["field"] = ve.Field
	}
	if ve.Reason != "" {
		details["reason"] = ve.Reason
	}
	if len(ve.SubErrs) > 0 {
		subs := make([]map[string]any, 0, len(ve.SubErrs))
		for _, se := range ve.SubErrs {
			subs = append(subs, map[string]any{
				"sourceIndex": se.SourceIndex,
				"field":       se.Field,
				"reason":      se.Reason,
				"message":     se.Message,
			})
		}
		// spec: §15.1. F-14.1.19. The multi-violation report
		// rides under details.fields (plural) per the WORKSPACE_PLAN_INVALID
		// error-catalog row; details.field (singular) carries the offending
		// plan path of the first violation.
		details["fields"] = subs
	}
	s.writeError(w, http.StatusBadRequest, "WORKSPACE_PLAN_INVALID", ve.Error(), details)
}
