// SPDX-License-Identifier: MIT

package errorclassify

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// decodedEnvelope is the client API error envelope as a client reads it.
type decodedEnvelope struct {
	Error struct {
		Code      string         `json:"code"`
		Category  string         `json:"category"`
		Message   string         `json:"message"`
		Retryable bool           `json:"retryable"`
		Details   map[string]any `json:"details"`
	} `json:"error"`
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) decodedEnvelope {
	t.Helper()
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var env decodedEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	return env
}

// TestWriteErrorClassifiesTheCodeByItsStatus checks that the envelope
// carries the code, the message, the details, and the category and
// retryable pair ClassifyStatus gives, and leaves details out when there
// are none.
//
// spec: §15.2.1 (REST/MCP Consistency Contract)
func TestWriteErrorClassifiesTheCodeByItsStatus(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteError(rr, http.StatusTooManyRequests, "RATE_LIMITED", "slow down", map[string]any{"scope": "upload_session"})
	env := decode(t, rr)
	cat, retryable := ClassifyStatus("RATE_LIMITED", http.StatusTooManyRequests)
	if rr.Code != http.StatusTooManyRequests || env.Error.Code != "RATE_LIMITED" || env.Error.Message != "slow down" ||
		env.Error.Category != string(cat) || env.Error.Retryable != retryable || env.Error.Details["scope"] != "upload_session" {
		t.Errorf("envelope = %d %+v", rr.Code, env)
	}

	rr = httptest.NewRecorder()
	WriteError(rr, http.StatusConflict, "SOME_UNMAPPED_CODE", "no", nil)
	env = decode(t, rr)
	if env.Error.Category != string(CategoryPermanent) || env.Error.Retryable || env.Error.Details != nil {
		t.Errorf("unmapped non-5xx code = %+v, want permanent, not retryable, no details", env)
	}
}

// TestWritePreconditionErrorCarriesTheStates checks that a precondition
// refusal is written with its status and code and the current and allowed
// states, and that any other error is written as INTERNAL_ERROR.
//
// spec: §15.1 (REST API)
func TestWritePreconditionErrorCarriesTheStates(t *testing.T) {
	rr := httptest.NewRecorder()
	WritePreconditionError(rr, &session.PreconditionError{
		Endpoint: session.EndpointUpload, CurrentState: session.StateRunning,
		AllowedStates: []session.State{session.StateCreated},
	})
	env := decode(t, rr)
	allowed, _ := env.Error.Details["allowedStates"].([]any)
	if rr.Code != http.StatusConflict || env.Error.Code != "INVALID_STATE_TRANSITION" ||
		env.Error.Details["currentState"] != "running" || len(allowed) != 1 || allowed[0] != "created" {
		t.Errorf("precondition envelope = %d %+v", rr.Code, env)
	}

	rr = httptest.NewRecorder()
	WritePreconditionError(rr, errors.New("not a precondition"))
	if env := decode(t, rr); rr.Code != http.StatusInternalServerError || env.Error.Code != "INTERNAL_ERROR" || env.Error.Message != "not a precondition" {
		t.Errorf("other error = %d %+v", rr.Code, env)
	}
}
