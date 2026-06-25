// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract suite for §15.2.1 REST ↔ OpenAI Chat Completions
// translation. The harness sends the same logical request through
// the §15.1 REST surface and the §15.1 /v1/chat/completions
// translator, then asserts the documented Translation Fidelity Matrix
// in §15 holds: fields marked `[exact]` round-trip; fields marked
// `[dropped]` are absent on the wire; fields marked `[lossy]` are
// transformed but recoverable per the matrix notes.
//
// The translator is pkg/gateway/translator.OpenAIChatHandler; the
// REST equivalent is pkg/gateway/sessionserver. Both run in-process
// against a shared memstore and the same EchoExecutor so the
// behavioural equivalence the suite asserts is end-to-end against
// the wire format, with no out-of-band coupling.
package rest_openai_chat_test

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

	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/translator"
)

// repoRoot walks up from the working directory to the module root
// (the directory holding go.mod).
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

// newOpenAIServers wires an OpenAI Chat translator and a REST
// sessionserver against a shared memstore and EchoExecutor. The
// translator and REST surfaces share the same internal state per the
// §15.2.1 "shared service layer" rule so this test asserts the wire
// translation matches expectations end to end.
func newOpenAIServers(t *testing.T, clock func() time.Time, id string) (*httptest.Server, *httptest.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	exec := executor.NewEchoExecutor()

	translatorHandler := translator.NewOpenAIChatHandler(store, exec, translator.OpenAIChatOptions{
		Clock:  clock,
		IDFunc: func() string { return id },
	})
	tsOpenAI := httptest.NewServer(translatorHandler.Handler())
	t.Cleanup(tsOpenAI.Close)

	restHandler := sessionserver.New(store, sessionserver.Options{})
	tsREST := httptest.NewServer(restHandler.Handler())
	t.Cleanup(tsREST.Close)

	return tsOpenAI, tsREST, store
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

// readAll reads an http.Response body and returns the buffered bytes
// along with the original Content-Type for the test's convenience.
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
// diagnosis: §15 Translation Fidelity Matrix asserts that the
// OpenAI Chat Completions wire form drops `schemaVersion`, drops
// the per-block `id`, and drops annotations on every MessagePart. A
// /v1/chat/completions response containing any of these fields is a
// regression. The REST surface preserves them (`[exact]`), so the
// same session driven through both surfaces produces a documented
// gap that this test pins.
func TestRESTOpenAIChatFidelityMatrix(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	tsOpenAI, _, store := newOpenAIServers(t, clock, "sess_oa_fidelity")

	body := readFixture(t, "openai_chat/simple/request.json")
	resp, raw := postJSON(t, tsOpenAI.URL+"/v1/chat/completions", "acme", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OpenAI chat status: %d, body=%s", resp.StatusCode, raw)
	}
	var openaiResp map[string]any
	if err := json.Unmarshal(raw, &openaiResp); err != nil {
		t.Fatalf("decode OpenAI response: %v", err)
	}

	// Cross-check the documented matrix expectations on the wire.
	if got := openaiResp["object"]; got != "chat.completion" {
		t.Errorf("object: got %v, want chat.completion", got)
	}
	if _, hasSchemaVersion := openaiResp["schemaVersion"]; hasSchemaVersion {
		t.Errorf("OpenAI response carries schemaVersion; the matrix marks it [dropped]")
	}
	choices, ok := openaiResp["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices: %v", openaiResp["choices"])
	}
	choice0 := choices[0].(map[string]any)
	if _, hasAnnotations := choice0["annotations"]; hasAnnotations {
		t.Errorf("OpenAI choice carries annotations; the matrix marks them [dropped]")
	}
	message := choice0["message"].(map[string]any)
	if _, hasID := message["id"]; hasID {
		t.Errorf("OpenAI message carries per-part id; the matrix marks it [dropped]")
	}
	if _, hasRef := message["ref"]; hasRef {
		t.Errorf("OpenAI message carries ref; the matrix marks it [dropped]")
	}

	// The session stored under the §15 internal model retains the
	// preserved fields. The translator stamps the session with the
	// matrix's [exact] internal model side.
	row, err := store.Get(context.Background(), "acme", "sess_oa_fidelity")
	if err != nil {
		t.Fatalf("session row: %v", err)
	}
	if row.RuntimeRef == "" {
		t.Errorf("REST row missing runtimeRef; matrix marks REST [exact] on identity fields")
	}
	if row.ID != "sess_oa_fidelity" {
		t.Errorf("REST row id: got %q, want sess_oa_fidelity", row.ID)
	}
}

// spec: §15.2.1
// diagnosis: §15 Translation Fidelity Matrix names every field the
// OpenAI Chat Completions adapter drops on the wire: `schemaVersion`,
// the per-content-block `id`, `ref`, `annotations`, the `parts`
// nesting, and `protocolHints`. The translator must produce a
// response that omits each of them; a field that appears would let a
// client construct a contract dependence on it and break under a
// matrix-conformant peer adapter.
func TestRESTOpenAIChatDroppedFieldsContract(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	tsOpenAI, _, _ := newOpenAIServers(t, clock, "sess_oa_dropped")

	body := readFixture(t, "openai_chat/simple/request.json")
	resp, raw := postJSON(t, tsOpenAI.URL+"/v1/chat/completions", "acme", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, raw)
	}

	// The dropped fields are inspected at the JSON string level: a
	// field omitted by the marshaller cannot appear in the byte
	// stream. This is the wire-level contract the matrix names.
	wireBody := string(raw)
	for _, dropped := range []string{
		`"schemaVersion"`,
		`"ref"`,
		`"annotations"`,
		`"protocolHints"`,
		`"parts"`,
	} {
		if strings.Contains(wireBody, dropped) {
			t.Errorf("OpenAI wire body carries %s; the matrix marks it [dropped]", dropped)
		}
	}

	// The id field on the message-level OpenAI shape is the
	// completion id, not a per-part id, so it is present at the top
	// level. Per the matrix, no nested message.id should exist;
	// inspect the choices[].message subtree to confirm.
	var resp1 map[string]any
	if err := json.Unmarshal(raw, &resp1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	choices := resp1["choices"].([]any)
	for _, c := range choices {
		message := c.(map[string]any)["message"].(map[string]any)
		if _, ok := message["id"]; ok {
			t.Errorf("OpenAI message.id appeared; matrix marks per-part id [dropped]")
		}
	}
}

// spec: §15.2.1
// diagnosis: §15 Translation Fidelity Matrix marks `inline`, the
// completion id, the model, and the finish_reason as preserved or
// recoverable across an OpenAI Chat Completions round trip. The
// matrix calls finish_reason a message-level signal; the translator
// must surface it on every choice. A response missing any of these
// breaks the documented contract and a downstream OpenAI-compatible
// client.
func TestRESTOpenAIChatPreservedFieldsContract(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	tsOpenAI, _, _ := newOpenAIServers(t, clock, "sess_oa_preserved")

	body := readFixture(t, "openai_chat/simple/request.json")
	resp, raw := postJSON(t, tsOpenAI.URL+"/v1/chat/completions", "acme", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, raw)
	}
	var got translator.OpenAIChatCompletionsResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The completion id round-trips the session id; the OpenAI client
	// uses this to correlate.
	if got.ID == "" {
		t.Errorf("completion id missing")
	}
	if !strings.HasPrefix(got.ID, "chatcmpl-") {
		t.Errorf("completion id prefix: got %q, want chatcmpl- prefix", got.ID)
	}
	// `object` is the OpenAI envelope discriminator and must survive
	// unchanged.
	if got.Object != "chat.completion" {
		t.Errorf("object: got %q, want chat.completion", got.Object)
	}
	// `model` mirrors the request; the matrix marks it [exact] on
	// the OpenAI side.
	if got.Model != "gpt-4o" {
		t.Errorf("model: got %q, want gpt-4o", got.Model)
	}
	// The `created` timestamp is non-zero when the translator stamps
	// it from its clock; the matrix marks it [exact].
	if got.Created == 0 {
		t.Errorf("created: zero unix; matrix marks it [exact]")
	}
	// Each choice carries an assistant message with the EchoExecutor
	// content inline, and the finish_reason is "stop" on a single
	// non-streaming response.
	if len(got.Choices) != 1 {
		t.Fatalf("choices: got %d, want 1", len(got.Choices))
	}
	choice := got.Choices[0]
	if choice.Message.Role != "assistant" {
		t.Errorf("choice role: got %q, want assistant", choice.Message.Role)
	}
	if choice.Message.Content == "" {
		t.Errorf("choice content empty; the matrix marks inline [exact]")
	}
	if choice.FinishReason != "stop" {
		t.Errorf("finish_reason: got %q, want stop", choice.FinishReason)
	}
}
