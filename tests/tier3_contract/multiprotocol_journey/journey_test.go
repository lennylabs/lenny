// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract suite for the §15 "one gateway, four protocols"
// claim (README.md: "REST, MCP (Streamable HTTP), OpenAI Chat
// Completions, and Open Responses all terminate at the same gateway
// and share one auth, policy, routing, and audit path.") and the
// §15.2 MCP session-flow list (create_session, send_message, output
// streaming, interrupt). Every existing REST/MCP/OpenAI contract test
// exercises one surface, or one overlapping operation across two
// surfaces, in isolation. This suite drives the full interactive
// session journey — create, prompt, stream, terminate — over each of
// the four client-facing protocols against one shared EchoExecutor-
// backed gateway, so a regression that only shows up when a session
// is carried end to end (rather than probed one call at a time) has a
// test that would catch it.
package multiprotocol_journey_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/matrix"
)

const journeyTenant = "acme"

// journeyGateway bundles the four §15 external-protocol handlers wired
// against one shared session store, one shared EchoExecutor, and one
// shared §15.1 session-event bus, mirroring the single-gateway,
// multi-adapter deployment the spec describes (ExternalAdapterRegistry,
// §15 line 12): "simultaneously active adapters route by path prefix"
// over one dispatcher rather than one server per protocol.
type journeyGateway struct {
	rest            *httptest.Server
	mcpSrv          *httptest.Server
	openaiChat      *httptest.Server
	openaiResponses *httptest.Server
	store           sessionstore.Store
}

// newJourneyGateway builds the four protocol handlers sharing one
// store/executor/event-bus. idSeed is the deterministic session id the
// REST-backed surfaces (REST and MCP, which dispatches its
// create/start/terminate tools through the same *sessionserver.Server
// per §15.2.1 rule 1) assign; the OpenAI Chat and Open Responses
// translators mint their own ids from idSeed-derived generators since
// each owns a separate session per call.
func newJourneyGateway(t *testing.T, clock func() time.Time, idSeed string) *journeyGateway {
	t.Helper()
	store := memstore.New()
	exec := executor.NewEchoExecutor()
	bus := sessionevents.NewBus(256)
	mem := memorystore.NewInMemory(0, nil)

	restHandler := sessionserver.New(store, sessionserver.Options{
		Executor: exec,
		Events:   bus,
		Memory:   mem,
		Clock:    clock,
		IDFunc:   func() string { return idSeed },
	})
	tsREST := httptest.NewServer(restHandler.Handler())
	t.Cleanup(tsREST.Close)

	mcpSrv := mcp.NewServer()
	// spec: §15.2 line 1331 — the Streamable HTTP SSE channel replays
	// from the same §15.1 event bus REST publishes to, so a client
	// attached over MCP observes the lifecycle transitions the shared
	// §15.2.1 rule-1 service layer produces on the REST-proxied tools
	// (create_and_start_session, terminate_session).
	mcpSrv.SetAttach(mcp.AttachConfig{
		Events: bus,
		TenantFromRequest: func(r *http.Request) string {
			if v := r.Header.Get("X-Lenny-Tenant-ID"); v != "" {
				return v
			}
			return journeyTenant
		},
		Now: clock,
	})
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store:          store,
		SessionCreator: restHandler,
		SessionService: restHandler,
		Executor:       exec,
		Memory:         mem,
		Events:         bus,
		Clock:          clock,
		IDFunc:         func() string { return idSeed },
		TenantID:       journeyTenant,
	})
	tsMCP := httptest.NewServer(mcpSrv.Handler())
	t.Cleanup(tsMCP.Close)

	chatHandler := translator.NewOpenAIChatHandler(store, exec, translator.OpenAIChatOptions{
		Clock:  clock,
		IDFunc: func() string { return idSeed },
	})
	tsChat := httptest.NewServer(chatHandler.Handler())
	t.Cleanup(tsChat.Close)

	respHandler := translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
		Clock:  clock,
		IDFunc: func() string { return idSeed },
	})
	tsResponses := httptest.NewServer(respHandler.Handler())
	t.Cleanup(tsResponses.Close)

	return &journeyGateway{
		rest:            tsREST,
		mcpSrv:          tsMCP,
		openaiChat:      tsChat,
		openaiResponses: tsResponses,
		store:           store,
	}
}

// spec: §15 line 6 (README.md:75 "One gateway, four protocols. REST,
// MCP (Streamable HTTP), OpenAI Chat Completions, and Open Responses
// all terminate at the same gateway and share one auth, policy,
// routing, and audit path."); spec/15_external-api-surface.md §15.2
// line 6 ("Session flows — MCP. Creating and interacting with
// sessions ... flows through the MCP API"), lines 1289-1298 (the
// create_session / send_message / attach_session / interrupt tool
// list this test's MCP cell drives).
// diagnosis: each cell drives the full create -> prompt -> stream ->
// terminate session journey over one protocol against a gateway that
// wires all four adapters against a shared store, executor, and event
// bus (mirroring the spec's single-ExternalAdapterRegistry deployment
// rather than one server per protocol). A failure means that protocol
// cannot carry a real interactive session end to end even though its
// single-shot fidelity/consistency tests pass — a regression the
// per-call contract tests cannot see because they never chain create,
// prompt, stream, and terminate on the same session.
func TestMultiProtocolSessionJourney(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	const prompt = "hello from the multiprotocol journey"

	matrix.Run(t, matrix.Dim("protocol", []string{"rest", "mcp", "openai-chat", "openai-responses"}))(
		func(t *testing.T, cell map[string]string) {
			protocol := cell["protocol"]
			sessionID := "sess_journey_" + strings.ReplaceAll(protocol, "-", "_")
			gw := newJourneyGateway(t, clock, sessionID)

			switch protocol {
			case "rest":
				runRESTJourney(t, gw, sessionID, prompt)
			case "mcp":
				runMCPJourney(t, gw, sessionID, prompt)
			case "openai-chat":
				runOpenAIChatJourney(t, gw, prompt)
			case "openai-responses":
				runOpenResponsesJourney(t, gw, prompt)
			}
		},
	)
}

// --- REST journey ---

// runRESTJourney drives create -> prompt -> stream -> terminate over
// the §15.1 REST surface: POST /v1/sessions/start (create+start in
// one call), POST .../messages (prompt), GET .../events with
// Accept: text/event-stream (stream), POST .../terminate.
func runRESTJourney(t *testing.T, gw *journeyGateway, sessionID, prompt string) {
	t.Helper()

	createBody := []byte(`{"runtimeRef":"claude-code","userId":"alice@acme.com"}`)
	resp, raw := restPostJSON(t, gw.rest.URL+"/v1/sessions/start", createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("REST create+start status: %d, body=%s", resp.StatusCode, raw)
	}
	var created struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("REST create+start decode: %v; body=%s", err, raw)
	}
	if created.ID != sessionID {
		t.Errorf("REST session id: got %q, want %q", created.ID, sessionID)
	}
	if created.State != "running" {
		t.Fatalf("REST session state after create+start: got %q, want running", created.State)
	}

	msgBody, _ := json.Marshal(map[string]any{
		"messages": []map[string]any{{"role": "user", "content": prompt}},
	})
	resp, raw = restPostJSON(t, gw.rest.URL+"/v1/sessions/"+sessionID+"/messages", msgBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("REST prompt status: %d, body=%s", resp.StatusCode, raw)
	}
	var msgResp struct {
		DeliveryReceipt struct {
			Status string `json:"status"`
		} `json:"deliveryReceipt"`
		Output []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &msgResp); err != nil {
		t.Fatalf("REST prompt decode: %v; body=%s", err, raw)
	}
	if msgResp.DeliveryReceipt.Status != "delivered" {
		t.Errorf("REST delivery receipt status: got %q, want delivered", msgResp.DeliveryReceipt.Status)
	}
	if !containsEchoOf(msgResp.Output, prompt) {
		t.Errorf("REST inline transcript missing echo of prompt %q: %+v", prompt, msgResp.Output)
	}

	resp, raw = restPostJSON(t, gw.rest.URL+"/v1/sessions/"+sessionID+"/terminate", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("REST terminate status: %d, body=%s", resp.StatusCode, raw)
	}

	// spec: §7.2 line 137 (status_change(state)) / line 141
	// (session_complete(result)) — stream the retained backlog and
	// confirm the "response" event carries the same echoed prompt the
	// synchronous call above returned, and that the session's terminal
	// transition is the last frame the backlog replays.
	req, err := http.NewRequest(http.MethodGet, gw.rest.URL+"/v1/sessions/"+sessionID+"/events", nil)
	if err != nil {
		t.Fatalf("build events request: %v", err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", journeyTenant)
	req.Header.Set("Accept", "text/event-stream")
	frames, cancel := openSSE(t, req)
	defer cancel()

	final, seen, ok := waitForFrame(t, frames, 5*time.Second, func(f sseFrame) bool {
		return (f.typ == "status_change" || f.typ == "session_complete") &&
			strings.Contains(f.data, `"completed"`)
	})
	if !ok {
		t.Fatalf("REST events stream never replayed a terminal completed frame; frames seen: %+v", seen)
	}
	if !strings.Contains(final.data, "completed") {
		t.Errorf("REST final streamed frame does not report completed: %s", final.data)
	}
	sawResponse := false
	for _, f := range seen {
		if f.typ == "response" && strings.Contains(f.data, prompt) {
			sawResponse = true
		}
	}
	if !sawResponse {
		t.Errorf("REST events backlog never replayed the response event carrying the echoed prompt; frames: %+v", seen)
	}
}

func containsEchoOf(parts []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}, prompt string,
) bool {
	for _, p := range parts {
		if strings.Contains(p.Text, prompt) {
			return true
		}
	}
	return false
}

// restPostJSON POSTs body (nil for an empty body) to url with the
// journey tenant header and returns the response and buffered body.
func restPostJSON(t *testing.T, url string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", journeyTenant)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp, raw
}

// --- MCP journey ---

// runMCPJourney drives create -> prompt -> stream -> terminate over
// the §15.2 MCP surface: lenny/create_and_start_session, lenny/
// send_message, lenny/attach_session (Accept: text/event-stream),
// lenny/terminate_session.
func runMCPJourney(t *testing.T, gw *journeyGateway, sessionID, prompt string) {
	t.Helper()

	createRes := mcpCall(t, gw.mcpSrv.URL+"/mcp", "lenny/create_and_start_session", map[string]any{
		"runtimeRef": "claude-code",
		"userId":     "alice@acme.com",
	})
	created := mcpToolPayload(t, createRes)
	if got, _ := created["id"].(string); got != sessionID {
		t.Errorf("MCP session id: got %q, want %q", got, sessionID)
	}
	if got, _ := created["state"].(string); got != "running" {
		t.Fatalf("MCP session state after create_and_start_session: got %q, want running", got)
	}

	sendRes := mcpCall(t, gw.mcpSrv.URL+"/mcp", "lenny/send_message", map[string]any{
		"to":      sessionID,
		"message": prompt,
	})
	sendPayload := mcpToolPayload(t, sendRes)
	receipt, _ := sendPayload["deliveryReceipt"].(map[string]any)
	if status, _ := receipt["status"].(string); status != "delivered" {
		t.Errorf("MCP delivery receipt status: got %q, want delivered", status)
	}
	out, _ := sendPayload["output"].([]any)
	sawEcho := false
	for _, o := range out {
		part, ok := o.(map[string]any)
		if !ok {
			continue
		}
		if text, _ := part["text"].(string); strings.Contains(text, prompt) {
			sawEcho = true
		}
	}
	if !sawEcho {
		t.Errorf("MCP send_message response missing echo of prompt %q: %v", prompt, sendPayload)
	}

	termRes := mcpCall(t, gw.mcpSrv.URL+"/mcp", "lenny/terminate_session", map[string]any{
		"sessionId": sessionID,
	})
	termPayload := mcpToolPayload(t, termRes)
	if got, _ := termPayload["state"].(string); got != "completed" {
		t.Errorf("MCP session state after terminate_session: got %q, want completed", got)
	}

	// spec: §15.2 line 1289 (attach_session "returns streaming task"),
	// line 1331 (Streamable HTTP SSE channel replays the retained
	// backlog before live delivery) — attach after the journey
	// completes and confirm the backlog's final frame is the §8.8
	// terminal task-status projection (final:true, status:"completed")
	// the shared §15.2.1 rule-1 REST-proxied terminate_session produced
	// on the same event bus REST publishes to.
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      mcp.AttachToolName,
			"arguments": map[string]any{"sessionId": sessionID},
		},
	})
	req, err := http.NewRequest(http.MethodPost, gw.mcpSrv.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build attach request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Lenny-Tenant-ID", journeyTenant)
	frames, cancel := openSSE(t, req)
	defer cancel()

	final, seen, ok := waitForFrame(t, frames, 5*time.Second, func(f sseFrame) bool {
		return strings.Contains(f.data, `"final":true`)
	})
	if !ok {
		t.Fatalf("MCP attach_session stream never replayed a final task-status frame; frames seen: %+v", seen)
	}
	if !strings.Contains(final.data, `"status":"completed"`) {
		t.Errorf("MCP attach_session final frame does not report status completed: %s", final.data)
	}
}

// mcpCall invokes an MCP tool over the JSON-RPC endpoint and returns
// the parsed result envelope.
func mcpCall(t *testing.T, url, tool string, args map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      tool,
			"arguments": args,
		},
	})
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("MCP call %s: %v", tool, err)
	}
	defer resp.Body.Close()
	var rpc struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatalf("MCP call %s decode: %v", tool, err)
	}
	if rpc.Error != nil {
		t.Fatalf("MCP call %s returned a JSON-RPC error: %v", tool, rpc.Error)
	}
	return rpc.Result
}

// mcpToolPayload decodes the JSON text a platform tool encodes as its
// first content block.
func mcpToolPayload(t *testing.T, res map[string]any) map[string]any {
	t.Helper()
	if _, ok := res["_error"]; ok {
		t.Fatalf("MCP result carried an error envelope: %v", res)
	}
	contents, ok := res["content"].([]any)
	if !ok || len(contents) == 0 {
		t.Fatalf("MCP result has no content: %v", res)
	}
	first, ok := contents[0].(map[string]any)
	if !ok {
		t.Fatalf("MCP first content not an object: %v", contents[0])
	}
	textBody, _ := first["text"].(string)
	if textBody == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(textBody), &out); err != nil {
		t.Fatalf("MCP text payload not JSON: %v\n%s", err, textBody)
	}
	return out
}

// --- OpenAI Chat Completions journey ---

// runOpenAIChatJourney drives create -> prompt -> stream -> terminate
// as the single POST /v1/chat/completions call the OpenAI Chat
// Completions protocol supports: the translator creates the
// underlying session, delivers the prompt, marks the session
// completed, and (with stream:true) streams the response as
// chat.completion.chunk SSE frames terminated by data: [DONE]. spec:
// §15.1 (OpenAI Chat Completions is a single-shot completion
// protocol: OpenAIChatHandler creates one session per request and
// marks it completed once the executor responds).
func runOpenAIChatJourney(t *testing.T, gw *journeyGateway, prompt string) {
	t.Helper()

	reqBody, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]any{{"role": "user", "content": prompt}},
		"stream":   true,
	})
	req, err := http.NewRequest(http.MethodPost, gw.openaiChat.URL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("build chat request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", journeyTenant)
	frames, cancel := openSSE(t, req)
	defer cancel()

	first, ok := <-frames
	if !ok {
		t.Fatalf("OpenAI chat stream closed before the first frame")
	}
	var roleFrame struct {
		Choices []struct {
			Delta struct {
				Role string `json:"role"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(first.data), &roleFrame); err != nil {
		t.Fatalf("decode first chat chunk: %v; frame=%s", err, first.data)
	}
	if len(roleFrame.Choices) == 0 || roleFrame.Choices[0].Delta.Role != "assistant" {
		t.Errorf("OpenAI chat first frame is not the assistant role delta: %s", first.data)
	}

	var content strings.Builder
	finishReason := ""
	sawDone := false
	for f := range frames {
		if f.data == "[DONE]" {
			sawDone = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(f.data), &chunk); err != nil {
			t.Fatalf("decode chat chunk: %v; frame=%s", err, f.data)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		content.WriteString(chunk.Choices[0].Delta.Content)
		if chunk.Choices[0].FinishReason != nil {
			finishReason = *chunk.Choices[0].FinishReason
		}
	}
	if !sawDone {
		t.Errorf("OpenAI chat stream never emitted the terminal data: [DONE] frame")
	}
	if !strings.Contains(content.String(), prompt) {
		t.Errorf("OpenAI chat streamed content %q does not contain the prompt %q", content.String(), prompt)
	}
	if finishReason != "stop" {
		t.Errorf("OpenAI chat final finish_reason: got %q, want stop", finishReason)
	}
}

// --- Open Responses journey ---

// runOpenResponsesJourney drives create -> prompt -> stream ->
// terminate as the single POST /v1/responses call the Open Responses
// protocol supports: response.created opens the envelope,
// response.output_text.delta streams the tokenized echo, and
// response.completed carries the full response (including the
// terminal "completed" status) as the streamed final chunk. spec:
// §15.1 (Open Responses is a single-shot completion protocol with the
// same create-and-complete-per-call contract as OpenAI Chat
// Completions).
func runOpenResponsesJourney(t *testing.T, gw *journeyGateway, prompt string) {
	t.Helper()

	reqBody, _ := json.Marshal(map[string]any{
		"model":  "gpt-4o",
		"input":  prompt,
		"stream": true,
	})
	req, err := http.NewRequest(http.MethodPost, gw.openaiResponses.URL+"/v1/responses", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("build responses request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", journeyTenant)
	frames, cancel := openSSE(t, req)
	defer cancel()

	final, seen, ok := waitForFrame(t, frames, 5*time.Second, func(f sseFrame) bool {
		return f.typ == "response.completed"
	})
	if !ok {
		t.Fatalf("Open Responses stream never emitted response.completed; frames seen: %+v", seen)
	}

	sawCreated := false
	for _, f := range seen {
		if f.typ == "response.created" {
			sawCreated = true
		}
	}
	if !sawCreated {
		t.Errorf("Open Responses stream never emitted response.created before response.completed")
	}

	var completed struct {
		Response struct {
			Status string `json:"status"`
			Output []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(final.data), &completed); err != nil {
		t.Fatalf("decode response.completed: %v; frame=%s", err, final.data)
	}
	if completed.Response.Status != "completed" {
		t.Errorf("Open Responses final streamed status: got %q, want completed", completed.Response.Status)
	}
	sawEcho := false
	for _, item := range completed.Response.Output {
		for _, c := range item.Content {
			if strings.Contains(c.Text, prompt) {
				sawEcho = true
			}
		}
	}
	if !sawEcho {
		t.Errorf("Open Responses response.completed output does not contain the prompt %q: %+v", prompt, completed.Response.Output)
	}
}

// --- shared SSE plumbing ---

// sseFrame is one generic Server-Sent Events frame: the REST events
// stream and the MCP attach_session stream both carry id:/data: lines
// (REST additionally carries event:); the OpenAI Chat and Open
// Responses streams carry a bare data: line (Open Responses adds
// event:). typ captures whichever "event:" line (if any) is present.
type sseFrame struct {
	id   string
	typ  string
	data string
}

// openSSE issues req (Accept: text/event-stream must already be set)
// and returns a channel of decoded frames plus a cancel func. The
// REST events and MCP attach_session handlers hold the connection
// open for live delivery after replaying the backlog, so a caller
// MUST call cancel once it has read the frames it needs; the OpenAI
// Chat and Open Responses handlers close the response on their own
// once the single request/response cycle completes, so cancel is a
// no-op there.
func openSSE(t *testing.T, req *http.Request) (<-chan sseFrame, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open SSE stream %s: %v", req.URL, err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		t.Fatalf("SSE stream %s status: %d, body=%s", req.URL, resp.StatusCode, b)
	}
	frames := make(chan sseFrame, 32)
	go func() {
		defer close(frames)
		defer resp.Body.Close()
		scanRawSSE(resp.Body, frames)
	}()
	return frames, cancel
}

// scanRawSSE decodes id:/event:/data: frames from r, publishing each
// completed frame (terminated by a blank line) onto out until r is
// exhausted.
func scanRawSSE(r io.Reader, out chan<- sseFrame) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var cur sseFrame
	has := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if has {
				out <- cur
				cur = sseFrame{}
				has = false
			}
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
			has = true
		case strings.HasPrefix(line, "event: "):
			cur.typ = strings.TrimPrefix(line, "event: ")
			has = true
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
			has = true
		}
	}
}

// waitForFrame reads frames from ch until pred matches one or timeout
// elapses, returning the matching frame plus every frame observed up
// to and including it.
func waitForFrame(t *testing.T, ch <-chan sseFrame, timeout time.Duration, pred func(sseFrame) bool) (sseFrame, []sseFrame, bool) {
	t.Helper()
	deadline := time.After(timeout)
	var seen []sseFrame
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return sseFrame{}, seen, false
			}
			seen = append(seen, f)
			if pred(f) {
				return f, seen, true
			}
		case <-deadline:
			return sseFrame{}, seen, false
		}
	}
}
