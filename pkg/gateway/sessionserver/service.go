// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	// RetryAfterSeconds carries the value of the response Retry-After
	// header the REST surface would have written, so a non-HTTP caller
	// (the §15 single-shot adapter binder) re-emits the identical backoff
	// hint in its native envelope. It is zero when the surface set no
	// header, for example the §4.9 CREDENTIAL_POOL_EXHAUSTED pre-claim
	// rejection, which carries no Retry-After. spec: §7.1 create-and-start
	// atomicity; §15.1 line 1138 (Retry-After).
	RetryAfterSeconds int
}

func (e *ServiceError) Error() string { return e.Code + ": " + e.Message }

// ServiceResult is the raw outcome of a successful in-process service
// dispatch: the HTTP status the REST surface wrote, the response body
// bytes verbatim, and the response Content-Type. The §15.2 MCP tool
// projects Body onto its wire format (a JSON tool result for a JSON body,
// a base64 content block for a binary blob), so the two surfaces return
// byte-identical payloads. spec: §15.2.1 rule 1 line 1380. F-15.2.3.
type ServiceResult struct {
	Status      int
	Body        []byte
	ContentType string
}

// serviceMux returns the cached routing handler the in-process service
// layer dispatches through. It is the same handler the public REST
// surface mounts, so an overlapping operation invoked from the §15.2 MCP
// tool runs the identical route, validation, and response shaping — the
// §15.2.1 rule-1 "implemented exactly once" guarantee, with no parallel
// route table to drift. spec: §15.2.1 rule 1 line 1380. F-15.2.3.
func (s *Server) serviceMux() http.Handler {
	s.serviceHandlerOnce.Do(func() {
		s.serviceHandler = s.Handler()
	})
	return s.serviceHandler
}

// ServiceCall dispatches one overlapping REST operation in-process and
// returns the raw 2xx result, or a typed ServiceError carrying the §16.3
// code/category/HTTP-status/retryable the REST surface would have
// written. The caller's authenticated principal rides on ctx exactly as
// it does on a real request (the §10.2 permission gate runs and admits a
// roleless dev principal); tenantID is stamped on the synthetic request's
// X-Lenny-Tenant-ID header for the principal-less transport.
//
// target is the full request target including any query string (for
// example "/v1/sessions/abc/start" or "/v1/sessions?state=running").
// body and contentType are empty for a GET. headers carries any
// operation-specific request headers (for example the §7.1
// X-Lenny-Upload-Token an upload requires); a nil map sets none. A 2xx
// returns the result and a nil error; any other status is decoded as the
// §15.1 error envelope.
//
// spec: §15.2.1 rule 1 line 1380; rule 3 line 1384 (shared error
// envelope). F-15.2.3.
func (s *Server) ServiceCall(ctx context.Context, tenantID, method, target string, body []byte, contentType string, headers map[string]string) (ServiceResult, *ServiceError) {
	var reader *bytes.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader).WithContext(ctx)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if tenantID != "" {
		req.Header.Set("X-Lenny-Tenant-ID", tenantID)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.serviceMux().ServeHTTP(rec, req)

	if rec.Code >= 200 && rec.Code < 300 {
		return ServiceResult{
			Status:      rec.Code,
			Body:        rec.Body.Bytes(),
			ContentType: rec.Header().Get("Content-Type"),
		}, nil
	}

	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code == "" {
		return ServiceResult{}, &ServiceError{
			HTTPStatus: rec.Code,
			Code:       "INTERNAL_ERROR",
			Category:   "INTERNAL",
			Message:    rec.Body.String(),
			Retryable:  rec.Code >= http.StatusInternalServerError,
		}
	}
	return ServiceResult{}, &ServiceError{
		HTTPStatus: rec.Code,
		Code:       env.Error.Code,
		Category:   env.Error.Category,
		Message:    env.Error.Message,
		Retryable:  env.Error.Retryable,
		Details:    env.Error.Details,
	}
}

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

// CreateAndStartService runs the full §15.1 create → finalize → start chain
// for an already-decoded request and returns the same CreateSessionResponse
// the REST `POST /v1/sessions/start` handler returns (State = "running",
// with a pod claimed, the runtime launched, and the binding registered into
// podRegistry), or a typed ServiceError. It is the create-and-start peer of
// CreateSessionService: the §15.2.1 rule-1 shared service layer the §15
// single-shot adapter binder reuses so the admission gates, the warm-pod
// claim, the runtime launch, and the binding registration all run exactly
// once for both the REST handler and the adapter.
//
// The flow reuses the canonical handleCreateAndStart handler against an
// in-process recorder rather than duplicating the create-and-start
// composition, so the two surfaces cannot drift in gate order, error codes,
// or response shaping (the same rationale CreateSessionService documents).
// Unlike CreateSessionService, the error path also reads the recorder's
// Retry-After header into ServiceError.RetryAfterSeconds: the pod-claim
// exhaustion 503 (SESSION_CREATION_FAILED) carries that backoff hint on the
// response header rather than in the body, and it is zero when absent (the
// §4.9 CREDENTIAL_POOL_EXHAUSTED pre-claim rejection sets no header). The
// caller's authenticated principal rides on ctx as it does on a real
// request; tenantID is stamped on the synthetic request's X-Lenny-Tenant-ID
// header so a principal-less transport still scopes the row to the resolved
// tenant.
//
// spec: §15.2.1 rule 1; §7.1 create-and-start atomicity.
func (s *Server) CreateAndStartService(ctx context.Context, tenantID string, req CreateSessionRequest) (CreateSessionResponse, *ServiceError) {
	body, err := json.Marshal(req)
	if err != nil {
		return CreateSessionResponse{}, &ServiceError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "VALIDATION_ERROR",
			Category:   "VALIDATION",
			Message:    "create request is not serialisable: " + err.Error(),
		}
	}
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body)).WithContext(ctx)
	httpReq.Header.Set("Content-Type", "application/json")
	if tenantID != "" {
		httpReq.Header.Set("X-Lenny-Tenant-ID", tenantID)
	}
	rec := httptest.NewRecorder()
	s.handleCreateAndStart(rec, httpReq)

	if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
		var resp CreateSessionResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			return CreateSessionResponse{}, &ServiceError{
				HTTPStatus: http.StatusInternalServerError,
				Code:       "INTERNAL_ERROR",
				Category:   "INTERNAL",
				Message:    "create-and-start response decode: " + err.Error(),
				Retryable:  true,
			}
		}
		return resp, nil
	}

	// spec: §15.1 line 1138 — the pod-claim exhaustion 503 carries its
	// backoff hint on the Retry-After response header, so read it off the
	// recorder for every non-2xx result. parseRetryAfterHeader returns zero
	// when the header is absent (the §4.9 CREDENTIAL_POOL_EXHAUSTED rejection
	// sets none), so RetryAfterSeconds stays zero on those envelopes.
	retryAfter := parseRetryAfterHeader(rec.Header().Get("Retry-After"))

	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code == "" {
		return CreateSessionResponse{}, &ServiceError{
			HTTPStatus:        rec.Code,
			Code:              "INTERNAL_ERROR",
			Category:          "INTERNAL",
			Message:           rec.Body.String(),
			Retryable:         rec.Code >= http.StatusInternalServerError,
			RetryAfterSeconds: retryAfter,
		}
	}
	return CreateSessionResponse{}, &ServiceError{
		HTTPStatus:        rec.Code,
		Code:              env.Error.Code,
		Category:          env.Error.Category,
		Message:           env.Error.Message,
		Retryable:         env.Error.Retryable,
		Details:           env.Error.Details,
		RetryAfterSeconds: retryAfter,
	}
}

// parseRetryAfterHeader parses the numeric-seconds form of a Retry-After
// response header into an int, returning zero when the header is empty or
// not a valid non-negative integer. The gateway writes Retry-After only in
// the delta-seconds form (strconv.Itoa on a seconds count), so the HTTP-date
// form is intentionally unsupported. spec: §15.1 line 1138.
func parseRetryAfterHeader(v string) int {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
