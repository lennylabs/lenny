// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration tests for multi-turn delivery on one pod-backed
// session through each call site that delivers a message: REST direct
// delivery, REST resume-and-deliver followed by a second message, MCP
// lenny/send_message, and lenny/delegate_task followed by a follow-up
// message to the child.
//
// Each request runs through a real http.Server, so the request's context is
// cancelled when the handler returns, exactly as in production. That is the
// condition the in-process tests that pass context.Background() cannot
// reproduce: a session's Attach stream opened on the first delivering
// request's context ended when that request returned, and every later
// message on the session failed. The gateway now holds the stream for the
// life of the session's binding, so the second and later messages each
// receive their own reply over the stream the first message opened.
//
// spec: §28.5.1 (Gateway-to-pod, CH-ATTACH), §7.2 (Interactive Session
// Model).
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// multiTurnEchoRuntime is an adapter.RuntimeProcess that answers every
// message with a `response` echoing the message's text, prefixed with
// "echo:", so each reply names the message it answers. It ignores the
// session_start and session_end frames the adapter writes, as a runtime
// that keeps no per-session context does (§28.5.3, CH-MSGSOCK).
type multiTurnEchoRuntime struct {
	out chan []byte

	mu       sync.Mutex
	messages []string
}

func newMultiTurnEchoRuntime() *multiTurnEchoRuntime {
	return &multiTurnEchoRuntime{out: make(chan []byte, 16)}
}

func (r *multiTurnEchoRuntime) Start(context.Context, string) error { return nil }

func (r *multiTurnEchoRuntime) WriteEnvelope(sessionID string, envelope []byte) error {
	var in struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		Input     []struct {
			Inline string `json:"inline"`
		} `json:"input"`
	}
	if err := json.Unmarshal(envelope, &in); err != nil || in.Type != "message" {
		return nil //nolint:nilerr // Frames other than a message are not answered.
	}
	text := ""
	if len(in.Input) > 0 {
		text = in.Input[0].Inline
	}
	if in.SessionID == "" {
		in.SessionID = sessionID
	}
	r.mu.Lock()
	r.messages = append(r.messages, text)
	r.mu.Unlock()
	reply, err := json.Marshal(map[string]any{"type": "response", "sessionId": in.SessionID, "text": "echo:" + text})
	if err != nil {
		return err
	}
	r.out <- reply
	return nil
}

func (r *multiTurnEchoRuntime) Output(context.Context, string) (<-chan []byte, error) {
	return r.out, nil
}
func (r *multiTurnEchoRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *multiTurnEchoRuntime) Close(context.Context, string) error           { return nil }

// multiTurnBoundExecutor serves an adapter over rt, starts sessionID on it,
// publishes the binding, and returns a PodExecutor over the binding.
func multiTurnBoundExecutor(t *testing.T, rt adapter.RuntimeProcess, sessionID string) *executor.PodExecutor {
	t.Helper()
	srv := adapter.New("multi-turn-test")
	srv.WorkspaceBase = t.TempDir()
	srv.Runtime = rt
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	if err := cl.StartSession(context.Background(), adapterclient.StartSessionParams{
		SessionID: sessionID, Runtime: "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: sessionID, TenantID: "acme", SandboxName: "sbx-" + sessionID, Adapter: cl})
	e := executor.NewPodExecutor(reg, nil)
	t.Cleanup(func() { e.EvictStream(sessionID) })
	return e
}

// multiTurnSeed commits a session row in state st.
func multiTurnSeed(t *testing.T, store sessionstore.Store, id string, st session.State) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: st, RuntimeRef: "echo",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// multiTurnPost posts body to url through the real HTTP client and returns
// the status and the decoded JSON body.
func multiTurnPost(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response %q: %v", raw, err)
	}
	return resp.StatusCode, out
}

// multiTurnRESTMessage posts one message to a session over REST and
// asserts a delivered receipt whose output echoes content.
func multiTurnRESTMessage(t *testing.T, base, sessionID, content, delivery string) {
	t.Helper()
	msg := map[string]any{"role": "user", "content": content}
	if delivery != "" {
		msg["delivery"] = delivery
	}
	body, _ := json.Marshal(map[string]any{"messages": []any{msg}})
	code, resp := multiTurnPost(t, base+"/v1/sessions/"+sessionID+"/messages", string(body))
	if code != http.StatusOK {
		t.Fatalf("message %q: status %d, body %v", content, code, resp)
	}
	receipt, _ := resp["deliveryReceipt"].(map[string]any)
	if receipt["status"] != string(session.DeliveryStatusDelivered) {
		t.Fatalf("message %q: receipt %v, want delivered", content, receipt)
	}
	output, _ := resp["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("message %q: output %v, want its one reply", content, output)
	}
	part, _ := output[0].(map[string]any)
	if part["text"] != "echo:"+content {
		t.Fatalf("message %q: reply %v, want echo:%s", content, part["text"], content)
	}
}

// multiTurnRESTServer serves a session server whose executor is e.
func multiTurnRESTServer(t *testing.T, e executor.Executor) (*httptest.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    e,
		Transcripts: transcriptstore.NewMemory(),
	})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return hs, store
}

// spec: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model)
// diagnosis: the second or third REST message on a running pod-backed
// session failed after the first returned. The session's Attach stream
// ended with the request that opened it, so the executor's held stream no
// longer outlives the delivering request.
func TestRESTDirectDeliveryCarriesEveryMessageOnTheHeldStream_spec_28_5_1(t *testing.T) {
	e := multiTurnBoundExecutor(t, newMultiTurnEchoRuntime(), "sess-rest")
	hs, store := multiTurnRESTServer(t, e)
	multiTurnSeed(t, store, "sess-rest", session.StateRunning)

	for i := 1; i <= 3; i++ {
		multiTurnRESTMessage(t, hs.URL, "sess-rest", fmt.Sprintf("turn-%d", i), "")
	}
}

// spec: 7.2 (Interactive Session Model, path 6), 28.5.1 (Gateway-to-pod)
// diagnosis: an immediate message resumed a suspended pod-held session and
// was delivered, but the next message on the now-running session failed:
// the stream the resume-and-deliver request opened ended with that request.
func TestResumeAndDeliverThenASecondMessageOnTheHeldStream_spec_7_2(t *testing.T) {
	e := multiTurnBoundExecutor(t, newMultiTurnEchoRuntime(), "sess-resume")
	hs, store := multiTurnRESTServer(t, e)
	multiTurnSeed(t, store, "sess-resume", session.StateSuspended)

	multiTurnRESTMessage(t, hs.URL, "sess-resume", "wake", "immediate")
	row, err := store.Get(context.Background(), "acme", "sess-resume")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.State != session.StateRunning {
		t.Fatalf("session state after resume-and-deliver = %q, want running", row.State)
	}
	multiTurnRESTMessage(t, hs.URL, "sess-resume", "after-wake", "")
}

// multiTurnToolCall posts one MCP tools/call through the real HTTP server
// and returns the text blocks of a successful result.
func multiTurnToolCall(t *testing.T, base, tool, args string) []string {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	code, resp := multiTurnPost(t, base+"/mcp", body)
	if code != http.StatusOK {
		t.Fatalf("%s: status %d, body %v", tool, code, resp)
	}
	result, _ := resp["result"].(map[string]any)
	if result == nil || result["isError"] == true {
		t.Fatalf("%s returned an error: %v", tool, resp)
	}
	content, _ := result["content"].([]any)
	var texts []string
	for _, c := range content {
		block, _ := c.(map[string]any)
		if s, ok := block["text"].(string); ok {
			texts = append(texts, s)
		}
	}
	return texts
}

// multiTurnSendMessage sends content to target with lenny/send_message and
// asserts the result carries the reply echoing content.
func multiTurnSendMessage(t *testing.T, base, target, content string) {
	t.Helper()
	args := `{"to":"` + target + `","message":"` + content + `"}`
	texts := multiTurnToolCall(t, base, "lenny/send_message", args)
	for _, s := range texts {
		if s == "echo:"+content {
			return
		}
	}
	t.Fatalf("send_message %q to %s returned %v, want the reply echo:%s", content, target, texts, content)
}

// spec: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model)
// diagnosis: the second lenny/send_message to a running pod-backed session
// failed after the first returned: the stream the first tool call opened
// ended with its HTTP request.
func TestMCPSendMessageCarriesEveryMessageOnTheHeldStream_spec_28_5_1(t *testing.T) {
	e := multiTurnBoundExecutor(t, newMultiTurnEchoRuntime(), "sess-mcp")
	store := memstore.New()
	multiTurnSeed(t, store, "sess-mcp", session.StateRunning)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: e,
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "unused" },
		TenantID: "acme",
	})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)

	for i := 1; i <= 3; i++ {
		multiTurnSendMessage(t, hs.URL, "sess-mcp", fmt.Sprintf("mcp-turn-%d", i))
	}
}

// spec: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model), 8.2
// (delegated child interaction)
// diagnosis: lenny/delegate_task delivered the task input to the
// materialized child, but a follow-up message to the child failed: the
// child's Attach stream was opened on the delegate_task request's context
// and ended when that request returned.
func TestDelegateTaskThenAFollowUpOnTheHeldStream_spec_28_5_1(t *testing.T) {
	envtest.SkipUnlessAvailable(t)
	cluster := materializeCluster(t)
	tenants, runtimes, credPools := materializeStores(t)
	rt := newMultiTurnEchoRuntime()
	srv, _, _, _ := materializeServer(t, cluster, tenants, runtimes, credPools, &poolRecordingAssigner{}, rt)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)

	args := `{"parentSessionId":"sess_parent","target":"` + matOKRuntime +
		`","poolRef":"pool-b","credentialPropagation":"inherit",` +
		`"task":{"input":[{"type":"text","inline":"do work"}]}}`
	texts := multiTurnToolCall(t, hs.URL, "lenny/delegate_task", args)
	if len(texts) == 0 {
		t.Fatal("delegate_task returned no handle")
	}
	var handle struct {
		ChildSessionID string `json:"childSessionId"`
	}
	if err := json.Unmarshal([]byte(texts[0]), &handle); err != nil || handle.ChildSessionID == "" {
		t.Fatalf("delegate_task handle %q: %v", texts[0], err)
	}

	multiTurnSendMessage(t, hs.URL, handle.ChildSessionID, "follow-up-1")
	multiTurnSendMessage(t, hs.URL, handle.ChildSessionID, "follow-up-2")

	rt.mu.Lock()
	got := append([]string(nil), rt.messages...)
	rt.mu.Unlock()
	want := []string{"do work", "follow-up-1", "follow-up-2"}
	if !slices.Equal(got, want) {
		t.Errorf("child runtime received %v, want %v in order on one stream", got, want)
	}
}
