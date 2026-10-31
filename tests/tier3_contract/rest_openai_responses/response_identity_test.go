// SPDX-License-Identifier: MIT

//go:build contract

package rest_openai_responses_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/responsesstub"

	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// responseIDPattern is the wire form of a response id the default id source
// produces: a session UUID, a ".", and a decimal ordinal of at least 1.
var responseIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.[1-9][0-9]*$`)

// newIdentityServer serves the Open Responses translator with the default
// session id source and a transcript store, as the gateway wires it.
func newIdentityServer(t *testing.T, ts transcriptstore.Store) *httptest.Server {
	t.Helper()
	h := translator.NewOpenResponsesHandler(memstore.New(), executor.NewEchoExecutor(), translator.OpenResponsesOptions{
		HolderForwarder: responsesstub.LocalForwarder{},
		Terminal:        responsesstub.ReleasingTerminal{Exec: nil, Store: nil},
		DevTenantHeader: true,
		Clock:           func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Transcripts:     ts,
	})
	srv := httptest.NewServer(ownerprincipal.Wrap(h.Handler(), ownerprincipal.Alice))
	t.Cleanup(srv.Close)
	return srv
}

func getJSON(t *testing.T, url, tenant string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

// openAIErrorEnvelope is the native OpenAI error body.
type openAIErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func decodeOpenAIError(t *testing.T, resp *http.Response, raw []byte) openAIErrorEnvelope {
	t.Helper()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("error Content-Type = %q, want application/json", ct)
	}
	var env openAIErrorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("error body is not the OpenAI envelope: %v; body=%s", err, raw)
	}
	if env.Error.Message == "" {
		t.Errorf("error envelope carries no message: %s", raw)
	}
	return env
}

// spec: §15 (Open Responses conversation sessions)
// diagnosis: a POST answered with an id that is not <session-uuid>.<n>, or a
// GET of that id that does not return the completed response with the same
// id and the previous_response_id echo, means the response-id codec or the
// GET resolution in pkg/gateway/environment/translator (responseid.go,
// continuity.go resolve, open_responses.go handleGet) drifted from the wire
// contract.
func TestOpenResponsesResponseIDAndGetBody_spec_15(t *testing.T) {
	srv := newIdentityServer(t, transcriptstore.NewMemory())

	resp, raw := postJSON(t, srv.URL+"/v1/responses", "acme", []byte(`{"model":"echo","input":"one"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root POST: %d, body=%s", resp.StatusCode, raw)
	}
	var root translator.OpenResponsesResponse
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if !responseIDPattern.MatchString(root.ID) {
		t.Fatalf("root response id %q is not <uuid>.<n>", root.ID)
	}
	if _, n := splitResponseID(t, root.ID); n != "1" {
		t.Errorf("root response ordinal = %q, want 1", n)
	}

	resp, raw = postJSON(t, srv.URL+"/v1/responses", "acme",
		[]byte(`{"model":"echo","input":"two","previous_response_id":"`+root.ID+`"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("continuation POST: %d, body=%s", resp.StatusCode, raw)
	}
	var cont translator.OpenResponsesResponse
	if err := json.Unmarshal(raw, &cont); err != nil {
		t.Fatal(err)
	}
	if !responseIDPattern.MatchString(cont.ID) || cont.ID == root.ID {
		t.Fatalf("continuation response id %q is not a fresh <uuid>.<n>", cont.ID)
	}

	for _, tc := range []struct {
		id, wantPrev string
	}{{root.ID, ""}, {cont.ID, root.ID}} {
		resp, raw := getJSON(t, srv.URL+"/v1/responses/"+tc.id, "acme")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d, body=%s", tc.id, resp.StatusCode, raw)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("GET Content-Type = %q", ct)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body["id"] != tc.id || body["object"] != "response" || body["status"] != "completed" || body["model"] != "echo" {
			t.Errorf("GET %s body = %s", tc.id, raw)
		}
		if _, ok := body["created_at"].(float64); !ok {
			t.Errorf("GET %s carries no numeric created_at: %s", tc.id, raw)
		}
		prev, _ := body["previous_response_id"].(string)
		if prev != tc.wantPrev {
			t.Errorf("GET %s previous_response_id = %q, want %q", tc.id, prev, tc.wantPrev)
		}
	}
}

// spec: §15 (Open Responses conversation sessions); §4.2 (session-store tenant isolation)
// diagnosis: a GET or a previous_response_id naming no completed response of
// the caller's tenant that answers anything other than the native 404
// not_found_error envelope means response resolution in
// pkg/gateway/environment/translator/continuity.go stopped failing closed, or
// a cross-tenant id became distinguishable from an unknown one.
func TestOpenResponsesUnknownResponseIsNative404_spec_15(t *testing.T) {
	srv := newIdentityServer(t, transcriptstore.NewMemory())
	resp, raw := postJSON(t, srv.URL+"/v1/responses", "globex", []byte(`{"model":"echo","input":"secret"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed POST: %d, body=%s", resp.StatusCode, raw)
	}
	var seeded translator.OpenResponsesResponse
	_ = json.Unmarshal(raw, &seeded)
	sessionID, _ := splitResponseID(t, seeded.ID)

	for _, id := range []string{
		seeded.ID,                                // cross-tenant
		sessionID,                                // bare session id
		sessionID + ".2",                         // past the newest ordinal
		sessionID + ".0",                         // ordinal below 1
		"not-a-session.1",                        // malformed prefix
		"0190a1b2-c3d4-8e5f-9a0b-1c2d3e4f5a6b.1", // unknown
	} {
		resp, raw := getJSON(t, srv.URL+"/v1/responses/"+id, "acme")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404; body=%s", id, resp.StatusCode, raw)
			continue
		}
		if env := decodeOpenAIError(t, resp, raw); env.Error.Type != "not_found_error" {
			t.Errorf("GET %s error.type = %q, want not_found_error", id, env.Error.Type)
		}

		resp, raw = postJSON(t, srv.URL+"/v1/responses", "acme",
			[]byte(`{"model":"echo","input":"x","previous_response_id":"`+id+`"}`))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST previous_response_id %s: %d, want 404; body=%s", id, resp.StatusCode, raw)
			continue
		}
		if env := decodeOpenAIError(t, resp, raw); env.Error.Type != "not_found_error" {
			t.Errorf("POST previous_response_id %s error.type = %q, want not_found_error", id, env.Error.Type)
		}
	}
}

// failingAppend is a transcript store whose Append always fails.
type failingAppend struct {
	transcriptstore.Store
}

func (failingAppend) Append(context.Context, string, string, ...transcriptstore.Entry) (uint64, error) {
	return 0, errors.New("transcript store unavailable")
}

// spec: §15 (Open Responses conversation sessions)
// diagnosis: a turn whose transcript record fails after the runtime answered
// that returns 200, or an error body other than the native 500 server_error
// envelope, means the record stopped being required in
// pkg/gateway/environment/translator/open_responses.go serveCold, and a
// later continuation through that response would rehydrate no context.
func TestOpenResponsesPostDeliveryFailureIsNative500_spec_15(t *testing.T) {
	srv := newIdentityServer(t, failingAppend{Store: transcriptstore.NewMemory()})
	resp, raw := postJSON(t, srv.URL+"/v1/responses", "acme", []byte(`{"model":"echo","input":"one"}`))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("POST with a failing record: %d, want 500; body=%s", resp.StatusCode, raw)
	}
	if env := decodeOpenAIError(t, resp, raw); env.Error.Type != "server_error" {
		t.Errorf("error.type = %q, want server_error", env.Error.Type)
	}
}
