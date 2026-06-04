// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

// ServiceError is the typed result a session-service call returns to a
// non-HTTP caller (the §15.2 MCP `lenny/create_session` tool). It carries
// the §16.3 error code, category, retryable flag, the HTTP status the REST
// surface would have written, and the error details, so the MCP adapter
// projects the identical error envelope onto its wire format instead of
// inventing its own code/category pair.
//
// spec: §15.2.1 rule 1 line 1380 ("Both API surfaces share a common
// service layer ... validation ... implemented exactly once"); §15.2.1
// rule 3 line 1384 (shared error envelope). F-15.2.4.
type ServiceError struct {
	HTTPStatus int
	Code       string
	Category   string
	Message    string
	Retryable  bool
	Details    map[string]any
}

func (e *ServiceError) Error() string { return e.Code + ": " + e.Message }

// CreateSessionService runs the full §15.1 session-creation flow for an
// already-decoded request and returns the same CreateSessionResponse the
// REST `POST /v1/sessions` handler returns, or a typed ServiceError. It is
// the shared service layer §15.2.1 rule 1 mandates: the active-user,
// quota, concurrency, admission-rate, policy-chain, and environment gates,
// the runtime / isolation-profile / workspace-plan validation, the row
// persist, and the §7.1 uploadToken mint all run here, exactly once, for
// both the REST handler and the MCP `lenny/create_session` tool.
//
// The flow reuses the canonical handleCreate handler against an in-process
// recorder rather than duplicating the ~270-line create path, so the two
// surfaces cannot drift in validation order, error codes, or response
// shaping. The caller's authenticated principal and explicit-environment
// binding ride on ctx exactly as they do on a real request; tenantID is
// stamped on the synthetic request's X-Lenny-Tenant-ID header so a caller
// with no principal (the dev-headers / minimal transport) still scopes the
// row to the resolved tenant (resolveTenant prefers a principal when one is
// present, so this header is inert under an authenticated principal).
//
// spec: §15.2.1 rule 1 line 1380; §15.1 session-creation flow. F-15.2.4.
func (s *Server) CreateSessionService(ctx context.Context, tenantID string, req CreateSessionRequest) (CreateSessionResponse, *ServiceError) {
	body, err := json.Marshal(req)
	if err != nil {
		return CreateSessionResponse{}, &ServiceError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "VALIDATION_ERROR",
			Category:   "VALIDATION",
			Message:    "create request is not serialisable: " + err.Error(),
		}
	}
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body)).WithContext(ctx)
	httpReq.Header.Set("Content-Type", "application/json")
	if tenantID != "" {
		httpReq.Header.Set("X-Lenny-Tenant-ID", tenantID)
	}
	rec := httptest.NewRecorder()
	s.handleCreate(rec, httpReq)

	if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
		var resp CreateSessionResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			return CreateSessionResponse{}, &ServiceError{
				HTTPStatus: http.StatusInternalServerError,
				Code:       "INTERNAL_ERROR",
				Category:   "INTERNAL",
				Message:    "create response decode: " + err.Error(),
				Retryable:  true,
			}
		}
		return resp, nil
	}

	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code == "" {
		return CreateSessionResponse{}, &ServiceError{
			HTTPStatus: rec.Code,
			Code:       "INTERNAL_ERROR",
			Category:   "INTERNAL",
			Message:    rec.Body.String(),
			Retryable:  rec.Code >= http.StatusInternalServerError,
		}
	}
	return CreateSessionResponse{}, &ServiceError{
		HTTPStatus: rec.Code,
		Code:       env.Error.Code,
		Category:   env.Error.Category,
		Message:    env.Error.Message,
		Retryable:  env.Error.Retryable,
		Details:    env.Error.Details,
	}
}
