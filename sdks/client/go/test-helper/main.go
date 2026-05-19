// SPDX-License-Identifier: MIT

// Command test-helper is the language-native helper the tier-3 SDK
// contract harness (tests/tier3_contract/sdks/harness) spawns for the
// Go client SDK. It reads one Command JSON object per line on stdin,
// executes each op through the SDK against the gateway, and writes
// one Response JSON object per line on stdout.
//
// The gateway base URL is read from the LENNY_GATEWAY_URL environment
// variable, set by the harness when it spawns the helper. An `init`
// op may also carry the URL in its args for harnesses that pass the
// URL after spawn rather than via the environment.
//
// The protocol matches harness.Command / harness.Response: a request
// is {"op": "...", "args": {...}} and a reply is
// {"ok": true, "result": {...}} or
// {"ok": false, "error": "...", "code": "..."}.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/lennylabs/lenny/sdks/client/go/lenny"
	"github.com/lennylabs/lenny/sdks/client/go/webhook"
)

// command is one harness request. It mirrors harness.Command.
type command struct {
	Op   string         `json:"op"`
	Args map[string]any `json:"args,omitempty"`
}

// response is one helper reply. It mirrors harness.Response.
type response struct {
	OK     bool           `json:"ok"`
	Error  string         `json:"error,omitempty"`
	Code   string         `json:"code,omitempty"`
	Result map[string]any `json:"result,omitempty"`
}

func main() {
	h := &helper{
		gatewayURL: os.Getenv("LENNY_GATEWAY_URL"),
		tenantID:   envOrDefault("LENNY_TENANT_ID", "acme"),
	}
	if err := h.connect(); err != nil {
		// A missing gateway URL is reported on the first op rather
		// than aborting at startup, so the harness can still issue a
		// shutdown cleanly.
		fmt.Fprintf(os.Stderr, "test-helper: deferred client init: %v\n", err)
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 64*1024), 4<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for in.Scan() {
		line := in.Bytes()
		if len(line) == 0 {
			continue
		}
		var cmd command
		if err := json.Unmarshal(line, &cmd); err != nil {
			writeResponse(out, response{OK: false, Error: "malformed command: " + err.Error()})
			continue
		}
		if cmd.Op == "shutdown" {
			writeResponse(out, response{OK: true})
			return
		}
		writeResponse(out, h.dispatch(cmd))
	}
}

// helper holds the SDK client and the harness session state.
type helper struct {
	gatewayURL string
	tenantID   string
	client     *lenny.Client
}

// connect builds the SDK client against the configured gateway URL.
func (h *helper) connect() error {
	if h.gatewayURL == "" {
		return fmt.Errorf("LENNY_GATEWAY_URL is not set")
	}
	c, err := lenny.New(h.gatewayURL, lenny.WithTenant(h.tenantID))
	if err != nil {
		return err
	}
	h.client = c
	return nil
}

// dispatch routes one command to its op handler.
func (h *helper) dispatch(cmd command) response {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	switch cmd.Op {
	case "init":
		return h.opInit(cmd.Args)
	case "verify_webhook":
		return h.opVerifyWebhook(cmd.Args)
	}

	// Every remaining op needs a live client.
	if h.client == nil {
		if err := h.connect(); err != nil {
			return response{OK: false, Error: err.Error()}
		}
	}

	switch cmd.Op {
	case "create_session":
		return h.opCreateSession(ctx, cmd.Args)
	case "get_session":
		return h.opGetSession(ctx, cmd.Args)
	case "list_sessions":
		return h.opListSessions(ctx, cmd.Args)
	case "delete_session":
		return h.opDeleteSession(ctx, cmd.Args)
	case "finalize":
		return h.opTransition(ctx, cmd.Args, (*lenny.Client).Finalize)
	case "start":
		return h.opTransition(ctx, cmd.Args, (*lenny.Client).Start)
	case "interrupt":
		return h.opTransition(ctx, cmd.Args, (*lenny.Client).Interrupt)
	case "terminate":
		return h.opTransition(ctx, cmd.Args, (*lenny.Client).Terminate)
	default:
		return response{OK: false, Error: "unknown op: " + cmd.Op}
	}
}

// opInit (re)points the client at a gateway URL supplied in the
// command args. It is the post-spawn URL-injection path.
func (h *helper) opInit(args map[string]any) response {
	if url := stringArg(args, "gateway_url"); url != "" {
		h.gatewayURL = url
	}
	if tenant := stringArg(args, "tenant_id"); tenant != "" {
		h.tenantID = tenant
	}
	if err := h.connect(); err != nil {
		return response{OK: false, Error: err.Error()}
	}
	return response{OK: true, Result: map[string]any{"gateway_url": h.gatewayURL}}
}

// opCreateSession executes the create_session op. It honors an
// optional idempotency_key arg.
func (h *helper) opCreateSession(ctx context.Context, args map[string]any) response {
	req := lenny.CreateSessionRequest{
		RuntimeRef: stringArg(args, "runtime"),
		UserID:     stringArg(args, "user_id"),
	}
	var opts []lenny.RequestOption
	if key := stringArg(args, "idempotency_key"); key != "" {
		opts = append(opts, lenny.WithIdempotencyKey(key))
	}
	res, err := h.client.CreateSession(ctx, req, opts...)
	if err != nil {
		return errResponse(err)
	}
	return response{OK: true, Result: sessionResult(res.Session, map[string]any{
		"upload_token": res.UploadToken,
	})}
}

// opGetSession executes the get_session op.
func (h *helper) opGetSession(ctx context.Context, args map[string]any) response {
	s, err := h.client.GetSession(ctx, stringArg(args, "id"))
	if err != nil {
		return errResponse(err)
	}
	return response{OK: true, Result: sessionResult(*s, nil)}
}

// opListSessions executes the list_sessions op. It honors an
// optional state filter.
func (h *helper) opListSessions(ctx context.Context, args map[string]any) response {
	opt := lenny.ListOptions{State: lenny.State(stringArg(args, "state"))}
	page, err := h.client.ListSessions(ctx, opt)
	if err != nil {
		return errResponse(err)
	}
	ids := make([]any, 0, len(page.Sessions))
	for _, s := range page.Sessions {
		ids = append(ids, s.ID)
	}
	return response{OK: true, Result: map[string]any{
		"count":    len(page.Sessions),
		"ids":      ids,
		"has_more": page.HasMore,
	}}
}

// opDeleteSession executes the delete_session op.
func (h *helper) opDeleteSession(ctx context.Context, args map[string]any) response {
	s, err := h.client.DeleteSession(ctx, stringArg(args, "id"))
	if err != nil {
		return errResponse(err)
	}
	return response{OK: true, Result: sessionResult(*s, nil)}
}

// transitionFn is the shared signature of the no-body lifecycle
// methods on lenny.Client.
type transitionFn func(*lenny.Client, context.Context, string, ...lenny.RequestOption) (*lenny.Session, error)

// opTransition executes a no-body lifecycle op (finalize, start,
// interrupt, terminate).
func (h *helper) opTransition(ctx context.Context, args map[string]any, fn transitionFn) response {
	s, err := fn(h.client, ctx, stringArg(args, "id"))
	if err != nil {
		return errResponse(err)
	}
	return response{OK: true, Result: sessionResult(*s, nil)}
}

// opVerifyWebhook executes the verify_webhook op. It does not need a
// gateway: it exercises the SDK's webhook signature verifier
// directly against the supplied signature, body, and secret.
func (h *helper) opVerifyWebhook(args map[string]any) response {
	secret := stringArg(args, "secret")
	signature := stringArg(args, "signature")
	body := stringArg(args, "body")
	v, err := webhook.NewWithSecret([]byte(secret))
	if err != nil {
		return response{OK: false, Error: err.Error()}
	}
	if err := v.Verify([]byte(body), signature); err != nil {
		return response{OK: false, Error: err.Error(), Code: "SIGNATURE_INVALID"}
	}
	return response{OK: true, Result: map[string]any{"verified": true}}
}

// sessionResult flattens a session envelope into the harness result
// map, merging any extra fields.
func sessionResult(s lenny.Session, extra map[string]any) map[string]any {
	out := map[string]any{
		"id":      s.ID,
		"state":   string(s.State),
		"runtime": s.RuntimeRef,
	}
	if s.TenantID != "" {
		out["tenant_id"] = s.TenantID
	}
	if s.UserID != "" {
		out["user_id"] = s.UserID
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// errResponse converts an SDK error into a failure response. A
// typed APIError contributes its §15.1 code; any other error is
// reported with an empty code.
func errResponse(err error) response {
	if apiErr, ok := lenny.AsAPIError(err); ok {
		return response{OK: false, Error: apiErr.Message, Code: apiErr.Code}
	}
	return response{OK: false, Error: err.Error()}
}

// writeResponse marshals resp and writes it as one stdout line.
func writeResponse(out *bufio.Writer, resp response) {
	body, err := json.Marshal(resp)
	if err != nil {
		body, _ = json.Marshal(response{OK: false, Error: "marshal response: " + err.Error()})
	}
	out.Write(body)
	out.WriteByte('\n')
	out.Flush()
}

// stringArg returns args[key] as a string, or "" when absent or not
// a string.
func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// envOrDefault returns the environment variable named by key, or def
// when it is unset.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
