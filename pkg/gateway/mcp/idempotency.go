// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	"github.com/lennylabs/lenny/pkg/idempotency"
)

// IdempotencyConfig wires the §11.5 idempotency primitives onto MCP
// tool calls so a client supplying `idempotencyKey` inside a tool's
// arguments collapses retries to a single execution, mirroring the
// REST `Idempotency-Key` header path. spec: §11.5 line 277; F-11.5.1,
// F-11.5.6.
//
// The MCP transport uses the same Postgres-backed Store as the REST
// middleware so a §11.5 row reclaims the same way regardless of which
// surface the client used. Keys are namespaced by tool name
// ("mcp/<tool-name>/<caller-key>") so the same caller key on REST
// (HTTP header) and MCP (tool arg) does not collide — the cached
// response shapes differ between transports and a cross-transport
// replay would deliver a malformed payload.
type IdempotencyConfig struct {
	// Store is the §11.5 cache. Required for MCP idempotency.
	Store idemmw.Store
	// TenantFromRequest extracts the tenant id from the inbound HTTP
	// request. Set to the same function the §11.5 HTTP middleware uses
	// so MCP and REST stay coherent under one auth chain.
	TenantFromRequest func(*http.Request) string
	// Tools is the set of tool names whose arguments admit an
	// idempotencyKey field. Calls to tools outside this set ignore the
	// field — the contract is opt-in per tool so a stray field on a
	// non-idempotent tool does not silently change behaviour.
	Tools map[string]bool
	// Now overrides time.Now in tests.
	Now func() time.Time
}

func (c IdempotencyConfig) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}

// SetIdempotency installs the §11.5 idempotency hook for tool calls.
// A zero IdempotencyConfig (Store == nil) disables the path; the
// server behaves exactly as it did before. spec: §11.5 line 277.
func (s *Server) SetIdempotency(cfg IdempotencyConfig) {
	s.idem = cfg
}

// extractIdempotencyKey reads the `idempotencyKey` string from an
// arguments JSON blob, returning "" when the field is absent or the
// blob is not a JSON object. Decoding errors are not surfaced — they
// will be reported by the tool handler's normal validation path when
// it attempts its own json.Unmarshal.
func extractIdempotencyKey(arguments json.RawMessage) string {
	if len(arguments) == 0 {
		return ""
	}
	var probe struct {
		Key string `json:"idempotencyKey"`
	}
	if err := json.Unmarshal(arguments, &probe); err != nil {
		return ""
	}
	return strings.TrimSpace(probe.Key)
}

// mcpKey namespaces caller keys per tool so REST and MCP do not share
// rows whose response payload shapes differ. spec: §11.5 line 277;
// F-11.5.1.
func mcpKey(tool, caller string) string {
	return "mcp/" + tool + "/" + caller
}

// dispatchIdempotent runs the tool's normal handler under the §11.5
// claim/replay/finalize flow. The replay path returns the cached
// ToolResult directly to the client; the in-flight path returns a
// transient IDEMPOTENCY_KEY_IN_FLIGHT ToolError. The contract caps the
// stored payload at the configured Tools allow-list and namespaces
// keys per tool. spec: §11.5 line 277; F-11.5.1, F-11.5.2, F-11.5.6.
func (s *Server) dispatchIdempotent(ctx context.Context, tenant, tool, callerKey string, arguments json.RawMessage, run func(context.Context, json.RawMessage) (ToolResult, error)) (ToolResult, bool, error) {
	if s.idem.Store == nil || tool == "" || callerKey == "" {
		return ToolResult{}, false, nil
	}
	key := mcpKey(tool, callerKey)
	if err := (idempotency.Key{TenantID: tenant, Value: key}).Validate(); err != nil {
		return ToolResult{}, true, NewToolError("INVALID_IDEMPOTENCY_KEY", err.Error(), nil)
	}
	bodyHash := idempotency.HashBody(arguments)
	now := s.idem.now()

	existing, claimed, err := s.idem.Store.Claim(ctx, tenant, key, bodyHash, now)
	if err != nil {
		return ToolResult{}, true, NewToolError("INTERNAL_ERROR", "idempotency claim: "+err.Error(), nil)
	}
	if !claimed {
		// Either pending (concurrent retry), replay (same body), or
		// 422 reuse (different body).
		if existing.Response.StatusCode == 0 {
			return ToolResult{}, true, NewToolError("IDEMPOTENCY_KEY_IN_FLIGHT",
				"a request with this idempotency key is currently in flight; retry after the original completes",
				map[string]any{"key": callerKey, "tool": tool})
		}
		action, derr := idempotency.DetectReuse(existing, bodyHash, now)
		if derr != nil {
			return ToolResult{}, true, NewToolError("IDEMPOTENCY_KEY_REUSED",
				derr.Error(),
				map[string]any{"key": callerKey, "tool": tool})
		}
		if action == idempotency.ActionReplay {
			var cached ToolResult
			if uerr := json.Unmarshal(existing.Response.Body, &cached); uerr != nil {
				return ToolResult{}, true, NewToolError("INTERNAL_ERROR",
					"idempotency cache decode: "+uerr.Error(), nil)
			}
			return cached, true, nil
		}
		// action == StoreNew implies expired-between-Claim-and-DetectReuse;
		// re-claim and continue to execution below.
		_, claimed, err = s.idem.Store.Claim(ctx, tenant, key, bodyHash, now)
		if err != nil {
			return ToolResult{}, true, NewToolError("INTERNAL_ERROR", "idempotency re-claim: "+err.Error(), nil)
		}
		if !claimed {
			return ToolResult{}, true, NewToolError("INTERNAL_ERROR", "idempotency re-claim returned not-claimed", nil)
		}
	}

	// We won the slot; execute and persist (or release on tool error).
	res, runErr := run(ctx, arguments)
	if runErr != nil {
		// spec: §11.5 line 277 — a tool error is the MCP analogue of a
		// 5xx: do not cache, release the pending row so a retry can
		// re-execute. The caller sees the ToolError directly.
		if relErr := s.idem.Store.Release(ctx, tenant, key); relErr != nil {
			// Release failure is non-fatal; log via the caller path.
			_ = relErr
		}
		return ToolResult{}, true, runErr
	}
	payload, merr := json.Marshal(res)
	if merr != nil {
		// Cannot serialise the result; treat as a runtime error and
		// release the pending row so a retry re-executes.
		_ = s.idem.Store.Release(ctx, tenant, key)
		return ToolResult{}, true, NewToolError("INTERNAL_ERROR",
			fmt.Sprintf("idempotency: serialize ToolResult: %v", merr), nil)
	}
	fresh := idempotency.Record{
		Key:      idempotency.Key{TenantID: tenant, Value: key},
		BodyHash: bodyHash,
		Response: idempotency.Response{
			StatusCode: http.StatusOK,
			Body:       payload,
		},
		StoredAt: now,
	}
	if perr := s.idem.Store.Put(ctx, fresh); perr != nil {
		// Put failure leaves the pending row in place (within TTL); a
		// retry sees IDEMPOTENCY_KEY_IN_FLIGHT until GC reclaims the row.
		// The caller has already received the result; the operator sees
		// the failure via the wrapped store's own observability path.
		_ = perr
	}
	return res, true, nil
}
