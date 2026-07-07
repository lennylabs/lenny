// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract suite for §15.2.1 REST ↔ OpenAI Responses
// translation. Same shape as the OpenAI Chat suite against the
// /v1/responses translator, including the id field's extended
// behavior the Translation Fidelity Matrix in §15 marks `[extended]`
// — top-level output item ids survive a round trip even though the
// nested per-part ids do not.
package rest_openai_responses_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// repoRoot walks up from the working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("no go.mod found from %s", wd)
	return ""
}

// readFixture loads a JSON fixture from tests/testdata.
func readFixture(t *testing.T, rel string) []byte {
	t.Helper()
	path := filepath.Join(repoRoot(t), "tests", "testdata", rel)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return body
}

// newResponsesServers wires the Open Responses translator and a REST
// sessionserver against a shared memstore and EchoExecutor.
func newResponsesServers(t *testing.T, clock func() time.Time, id string) (*httptest.Server, *httptest.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	exec := executor.NewEchoExecutor()

	respHandler := translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
		Clock:  clock,
		IDFunc: func() string { return id },
	})
	tsResp := httptest.NewServer(respHandler.Handler())
	t.Cleanup(tsResp.Close)

	restHandler := sessionserver.New(store, sessionserver.Options{})
	tsREST := httptest.NewServer(restHandler.Handler())
	t.Cleanup(tsREST.Close)

	return tsResp, tsREST, store
}

// postJSON sends a JSON body to url with the X-Lenny-Tenant-ID
// header.
func postJSON(t *testing.T, url, tenant string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if tenant != "" {
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	buf, _ := readAll(resp)
	return resp, buf
}

// readAll buffers an http.Response body.
func readAll(resp *http.Response) ([]byte, string) {
	const max = 1 << 22
	buf := make([]byte, 0, 2048)
	tmp := make([]byte, 4096)
	for len(buf) < max {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf, resp.Header.Get("Content-Type")
}

// spec: §15.2.1
// diagnosis: §15 Translation Fidelity Matrix asserts that the Open
// Responses wire form drops `schemaVersion`, `ref`, and `annotations`
// (each marked `[dropped]`) while preserving top-level `id` and
// `inline` (`[extended]` and `[exact]`). A response containing a
// dropped field or missing a preserved field is a wire-contract
// regression — clients depending on this matrix would silently start
// seeing data their adapter contract said was unreachable, or losing
// data the adapter contract said survives.
func TestRESTOpenAIResponsesFidelityMatrix(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	tsResp, _, store := newResponsesServers(t, clock, "sess_resp_fidelity")

	body := readFixture(t, "openai_responses/simple/request.json")
	resp, raw := postJSON(t, tsResp.URL+"/v1/responses", "acme", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("responses status: %d, body=%s", resp.StatusCode, raw)
	}
	wireBody := string(raw)
	for _, dropped := range []string{
		`"schemaVersion"`,
		`"ref"`,
		`"protocolHints"`,
		`"parts"`,
	} {
		if strings.Contains(wireBody, dropped) {
			t.Errorf("Responses wire body carries %s; matrix marks it [dropped]", dropped)
		}
	}

	// Preserved fields: object discriminator, top-level id, model,
	// status, output array shape.
	var got translator.OpenResponsesResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Object != "response" {
		t.Errorf("object: got %q, want response", got.Object)
	}
	if got.ID == "" {
		t.Errorf("top-level id missing; matrix marks Responses id [extended]")
	}
	if got.Model != "gpt-4o" {
		t.Errorf("model: got %q, want gpt-4o", got.Model)
	}
	if got.Status != "completed" {
		t.Errorf("status: got %q, want completed", got.Status)
	}
	if len(got.Output) != 1 {
		t.Fatalf("output items: got %d, want 1", len(got.Output))
	}
	// The matrix marks MessagePart.annotations [dropped]: no Lenny
	// annotation metadata (e.g. `role`, `final`, custom keys) may
	// leak into the wire form. The published Responses schema itself
	// requires an `annotations` key on every output_text content
	// block (used for citations), so the key's presence is not the
	// signal — its content is. It must stay empty because this
	// translator never produces citations.
	for _, item := range got.Output {
		for _, c := range item.Content {
			if len(c.Annotations) != 0 {
				t.Errorf("output content annotations: got %v, want empty; matrix marks MessagePart.annotations [dropped]", c.Annotations)
			}
		}
	}

	// The REST surface mirrors the same internal session with its
	// identity-level fields preserved.
	row, err := store.Get(context.Background(), "acme", "sess_resp_fidelity")
	if err != nil {
		t.Fatalf("session row: %v", err)
	}
	if row.RuntimeRef == "" {
		t.Errorf("REST row missing runtimeRef")
	}
}

// spec: §15.2.1
// diagnosis: §15 Translation Fidelity Matrix marks the OpenAI
// Responses `id` field `[extended]` — top-level output item ids are
// preserved on the wire and recoverable on ingest. The translator
// must echo the response id with a documented prefix and assign each
// output item a deterministic id derived from the response id; an id
// that vanishes on the wire breaks the matrix promise.
func TestRESTOpenAIResponsesIDFieldExtendedBehavior(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	tsResp, _, store := newResponsesServers(t, clock, "sess_resp_idext")

	body := readFixture(t, "openai_responses/simple/request.json")
	resp, raw := postJSON(t, tsResp.URL+"/v1/responses", "acme", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("responses status: %d, body=%s", resp.StatusCode, raw)
	}
	var got translator.OpenResponsesResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Top-level id is the session id; the translator stamps the
	// session row with the same value.
	if got.ID != "sess_resp_idext" {
		t.Errorf("response id: got %q, want sess_resp_idext", got.ID)
	}
	row, err := store.Get(context.Background(), "acme", "sess_resp_idext")
	if err != nil {
		t.Fatalf("session row: %v", err)
	}
	if row.ID != got.ID {
		t.Errorf("internal id %q does not match wire id %q", row.ID, got.ID)
	}

	// Each output item carries an id derived from the response id;
	// the matrix marks this as preserved across a round trip.
	if len(got.Output) != 1 {
		t.Fatalf("output items: got %d, want 1", len(got.Output))
	}
	item := got.Output[0]
	if item.ID == "" {
		t.Errorf("output[0].id empty; matrix marks Responses output id [extended]")
	}
	if !strings.Contains(item.ID, got.ID) {
		t.Errorf("output[0].id %q does not derive from response id %q", item.ID, got.ID)
	}

	// `previous_response_id` carries lineage; the test confirms the
	// translator passes it through when the request supplies one.
	withPrev := []byte(`{"model":"gpt-4o","input":"chain","previous_response_id":"resp_parent_42"}`)
	tsResp2, _, _ := newResponsesServers(t, clock, "sess_resp_lineage")
	resp2, raw2 := postJSON(t, tsResp2.URL+"/v1/responses", "acme", withPrev)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("lineage status: %d, body=%s", resp2.StatusCode, raw2)
	}
	var lin translator.OpenResponsesResponse
	if err := json.Unmarshal(raw2, &lin); err != nil {
		t.Fatalf("decode lineage: %v", err)
	}
	if lin.PreviousResponseID != "resp_parent_42" {
		t.Errorf("previous_response_id: got %q, want resp_parent_42", lin.PreviousResponseID)
	}
}
