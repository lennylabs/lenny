// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests that pin whole lenny-ops response bodies to
// JSON Schemas derived from the §25.4 operability response shapes and
// the §25.2 canonical envelopes. The other ops_endpoints contract tests
// assert individual field presence; these validate the entire response
// body against a schema, so a renamed field, a dropped field, or a
// wrong type on the caller-identity, remediation-lock, operations-
// inventory, or error responses fails a test rather than shipping.
//
// The gateway-served OpenAPI document (§25.4 GET /v1/openapi.json)
// documents the operability paths and their status codes, but the
// lenny-ops route registry (pkg/ops/opsserver.RouteSchemas) emits each
// operability route with a status-code-only response and no response
// body schema, so the served document cannot itself supply the response
// schema these bodies are validated against. The schemas here are
// derived directly from the §25.2/§25.4 spec response definitions,
// mirroring the local-fixture pattern the §25.12 MCP envelope
// conformance tests in this package already use.
package ops_endpoints_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const (
	meResponseSchema     = "tests/tier3_contract/ops_endpoints/testdata/schema/ops_me_response.json"
	lockSchema           = "tests/tier3_contract/ops_endpoints/testdata/schema/ops_lock.json"
	operationsPageSchema = "tests/tier3_contract/ops_endpoints/testdata/schema/ops_operations_page.json"
	errorEnvelopeSchema  = "tests/tier3_contract/ops_endpoints/testdata/schema/ops_error_envelope.json"
)

// stubOperationSource is a §25.4 Operations Inventory source returning a
// single held remediation-lock operation so the list endpoint has a live
// operation record to validate against the schema.
type stubOperationSource struct{ op operations.Operation }

func (s stubOperationSource) Kinds() []operations.Kind {
	return []operations.Kind{operations.KindRemediationLock}
}

func (s stubOperationSource) List(context.Context, operations.Filter) ([]operations.Operation, error) {
	return []operations.Operation{s.op}, nil
}

// TestMeResponseSchemaConformance validates the whole §25.4
// GET /v1/admin/me body against the caller-discovery response schema:
// the identity, authorization, token, platform, capabilities, and links
// blocks and the capability field names an agent reads to learn the
// install state.
//
// spec: 25.4 (GET /v1/admin/me Response — the identity/authorization/
// token/platform/capabilities/links blocks; "capabilities reflects the
// actual install state")
// diagnosis: the /me body drifted from the §25.4 response schema — a
// renamed or dropped block, a renamed capability field, or a wrong type.
// A fresh agent bootstraps its entire operability session from this one
// call, so a malformed body breaks discovery before any other endpoint.
func TestMeResponseSchemaConformance(t *testing.T) {
	srv := opsServer(t)
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/me",
		map[string]string{"X-Lenny-Caller": "prod-watchdog"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	schema := schematest.Compile(t, meResponseSchema)
	if err := schema.Validate(body); err != nil {
		t.Fatalf("/v1/admin/me body violates the §25.4 response schema: %v\nbody=%v", err, body)
	}
}

// TestLockResponseSchemaConformance validates the whole §25.4 Lock body
// returned by acquire (201) and get against the Lock schema: every
// server-authoritative field an agent reads for ownership (acquiredBy)
// and expiry (expiresAt), plus lockStore, epoch, and revision.
//
// spec: 25.4 (Lock struct — id, scope, operation, acquiredBy,
// acquiredAt, expiresAt, lockStore, epoch, revision)
// diagnosis: the Lock body drifted from the §25.4 Lock schema — a
// renamed field, a dropped server-authoritative timestamp, a lockStore
// outside {postgres,redis,memory}, or a wrong type. An agent validates
// ownership and re-validates expiry against these fields, so a malformed
// Lock breaks the long-remediation coordination contract.
func TestLockResponseSchemaConformance(t *testing.T) {
	srv := opsServer(t)
	hdr := map[string]string{"X-Lenny-Caller": "prod-watchdog"}
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:default-gvisor", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusCreated {
		t.Fatalf("acquire status = %d, want 201; body=%v", rec.Code, body)
	}
	schema := schematest.Compile(t, lockSchema)
	if err := schema.Validate(body); err != nil {
		t.Fatalf("acquire Lock body violates the §25.4 Lock schema: %v\nbody=%v", err, body)
	}

	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("acquire Lock body has no id: %v", body)
	}
	getRec, getBody := request(t, srv, http.MethodGet, "/v1/admin/remediation-locks/"+id, hdr, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%v", getRec.Code, getBody)
	}
	if err := schema.Validate(getBody); err != nil {
		t.Fatalf("get Lock body violates the §25.4 Lock schema: %v\nbody=%v", err, getBody)
	}
}

// TestOperationsPageSchemaConformance validates the whole §25.4
// GET /v1/admin/operations page against the inventory schema: the
// operations array of canonical operation records and the §25.2
// pagination envelope. The inventory is wired with a single held
// remediation-lock operation so a live operation record is validated,
// not just the empty envelope.
//
// spec: 25.4 (Operations Inventory Response — the operations array of
// operation records and the pagination envelope), 25.2 (pagination
// envelope: hasMore, cursor, cursorKind)
// diagnosis: the inventory page drifted from the §25.4 schema — a
// renamed field on an operation record, a dropped pagination field, or
// a wrong type. An agent's "what is in flight?" view is assembled from
// this schema, so a malformed record breaks the unified inventory.
func TestOperationsPageSchemaConformance(t *testing.T) {
	timeoutAt := time.Date(2026, 4, 16, 10, 20, 0, 0, time.UTC)
	inv := operations.New(stubOperationSource{op: operations.Operation{
		OperationID: "lock-7c9e6679-7425-40de-944b-e07fc1f90ae7",
		Kind:        operations.KindRemediationLock,
		Status:      operations.StatusHeld,
		StartedBy:   "prod-watchdog",
		StartedAt:   time.Date(2026, 4, 16, 10, 15, 0, 0, time.UTC),
		TimeoutAt:   &timeoutAt,
		Resources: map[string]string{
			"get":     "GET /v1/admin/remediation-locks/lock-7c9e6679-7425-40de-944b-e07fc1f90ae7",
			"release": "DELETE /v1/admin/remediation-locks/lock-7c9e6679-7425-40de-944b-e07fc1f90ae7",
		},
		Cancellable: true,
		Metadata:    json.RawMessage(`{"scope":"pool:default-gvisor","operation":"scale"}`),
	}})
	srv := opsserver.New(opsserver.Options{
		Locks:     coordination.NewMemStore(),
		Inventory: inv,
	})
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/operations",
		map[string]string{"X-Lenny-Caller": "prod-watchdog"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	assertJSONContentType(t, rec)
	ops, _ := body["operations"].([]any)
	if len(ops) == 0 {
		t.Fatalf("operations page has no operation records to validate: %v", body)
	}
	schema := schematest.Compile(t, operationsPageSchema)
	if err := schema.Validate(body); err != nil {
		t.Fatalf("operations page violates the §25.4 schema: %v\nbody=%v", err, body)
	}
}

// TestErrorEnvelopeSchemaConformance validates a live §25.2 canonical
// error envelope against the error schema: the inner error object's
// code, category, message, and retryable fields, with category
// constrained to the four §25.2 categories. The 409
// REMEDIATION_LOCK_CONFLICT path drives a real error body.
//
// spec: 25.2 (Error Response Envelope — code, category, message,
// retryable; the category taxonomy TRANSIENT|PERMANENT|POLICY|AUTH)
// diagnosis: an operability error body drifted from the §25.2 envelope
// — a renamed field, a wrong type, or a category outside the taxonomy.
// SDK retry logic keys on category and retryable, so a malformed error
// envelope breaks every agent's failure handling.
func TestErrorEnvelopeSchemaConformance(t *testing.T) {
	srv := opsServer(t)
	hdr := map[string]string{"X-Lenny-Caller": "agent"}
	request(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:conflict", "operation": "scale", "ttlSeconds": 300})
	rec, body := request(t, srv, http.MethodPost, "/v1/admin/remediation-locks", hdr,
		map[string]any{"scope": "pool:conflict", "operation": "scale", "ttlSeconds": 300})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%v", rec.Code, body)
	}
	schema := schematest.Compile(t, errorEnvelopeSchema)
	if err := schema.Validate(body); err != nil {
		t.Fatalf("error body violates the §25.2 error envelope schema: %v\nbody=%v", err, body)
	}
}
