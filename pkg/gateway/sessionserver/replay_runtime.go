// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// requireReplayRuntimeCompatible enforces the §15.1 targetRuntime rule of
// POST /v1/sessions/{id}/replay: "Must be a registered runtime with the same
// `executionMode` as the source session. A different `executionMode` returns
// `400 INCOMPATIBLE_RUNTIME`."
//
// The source's execution mode is the §7.1 sessionIsolationLevel.executionMode
// frozen on its row at create. An empty column (a row whose pool never
// resolved) reads as the §5.2 default `session`, as persistedIsolationLevel
// reads it for GET. The target's mode is the resolved runtime definition's
// §5.2 executionMode, which a derived runtime inherits from its base, with the
// same `session` default the registry applies.
//
// The outcomes are:
//
//   - A runtime the registry does not hold is not a registered runtime, so
//     the request is invalid: 400 VALIDATION_ERROR on targetRuntime with
//     reason runtime_not_registered. §15.1 names INCOMPATIBLE_RUNTIME only
//     for a mode mismatch, whose details carry a target mode an unregistered
//     runtime does not have.
//   - A registered runtime of another mode answers 400 INCOMPATIBLE_RUNTIME
//     with details.sourceExecutionMode and details.targetExecutionMode, the
//     §15.1 catalog row's fields.
//   - A registry read that fails for any other reason is not a definite
//     answer, so the replay fails closed with 503 SERVICE_UNAVAILABLE and
//     Retry-After, as a transient pool lookup does on create.
//
// With no registry wired there is nothing to compare against, and the check
// admits the replay, as the gateway's other runtime lookups do. The create
// time claim still resolves a pool backed by targetRuntime.
//
// It returns true when the replay may proceed; when it returns false it has
// already written the §15.1 error envelope.
//
// spec: §15.1 (Session Replay Semantics, the INCOMPATIBLE_RUNTIME catalog
// row), §5.2 (Pool Configuration and Execution Modes), §7.1
// (sessionIsolationLevel).
func (s *Server) requireReplayRuntimeCompatible(w http.ResponseWriter, r *http.Request, source sessionstore.Session, targetRuntime string) bool {
	if s.runtimes == nil {
		return true
	}
	rt, err := runtimestore.Resolve(r.Context(), s.runtimes, targetRuntime)
	if errors.Is(err, runtimestore.ErrNotFound) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"targetRuntime is not a registered runtime",
			map[string]any{"field": "targetRuntime", "targetRuntime": targetRuntime, "reason": "runtime_not_registered"})
		return false
	}
	if err != nil {
		w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
		s.writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE",
			"the target runtime could not be resolved because a dependency was transiently unavailable",
			map[string]any{"field": "targetRuntime", "targetRuntime": targetRuntime, "reason": "runtime_lookup_unavailable"})
		return false
	}
	sourceMode := executionModeOrDefault(runtimestore.ExecutionMode(source.ExecutionMode))
	targetMode := executionModeOrDefault(rt.ExecutionMode)
	if sourceMode != targetMode {
		s.writeError(w, http.StatusBadRequest, "INCOMPATIBLE_RUNTIME",
			"targetRuntime has a different executionMode than the source session; replay requires matching execution mode",
			map[string]any{
				"sourceExecutionMode": string(sourceMode),
				"targetExecutionMode": string(targetMode),
				"targetRuntime":       targetRuntime,
			})
		return false
	}
	return true
}

// executionModeOrDefault reads an empty §5.2 execution mode as `session`, the
// default the runtime registry and the persisted isolation level both apply.
func executionModeOrDefault(m runtimestore.ExecutionMode) runtimestore.ExecutionMode {
	if m == "" {
		return runtimestore.ExecutionModeSession
	}
	return m
}
