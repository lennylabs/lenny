// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the client-facing absence of a slot address.
// A client addresses a session rather than a slot: every session is bound
// to a slot on every pod and a session-mode slot's identifier is its
// session's identifier, so no client-facing payload carries a separate slot
// key. These tests pin that on the §15.1 message request body, on the §7.2
// tool-approval interaction detail, and on the §7.2 tool_use_requested SSE
// payload.

package rest_sessions_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/admission/ownership"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// spec: §7.2 (message dispatch); §15.4 (MessageEnvelope)
// diagnosis: the gateway either rejected a message body carrying a stale
// slotId key or let that key steer delivery. The key is not part of the
// client contract: it does not deserialize onto the payload, and the
// session named by the route is the session the message reaches.
func TestMessageBodyIgnoresStaleSlotAddress_spec_7_2(t *testing.T) {
	store := memstore.New()
	rec := &recordingExecutor{Executor: executor.NewEchoExecutor()}
	srv := sessionserver.New(store, sessionserver.Options{Executor: rec})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	id := createSession(t, ts)
	for _, step := range []string{"finalize", "start"} {
		if resp, body := do(t, ts, "POST", "/v1/sessions/"+id+"/"+step, nil); resp.StatusCode != 200 {
			t.Fatalf("POST /%s: status = %d, body=%v", step, resp.StatusCode, body)
		}
	}

	resp, body := do(t, ts, "POST", "/v1/sessions/"+id+"/messages", map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": "hello", "slotId": "slot-from-an-older-client"},
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (a stale slotId key is ignored, not rejected); body=%v",
			resp.StatusCode, body)
	}
	if _, ok := body["deliveryReceipt"].(map[string]any); !ok {
		t.Fatalf("response carries no deliveryReceipt: %v", body)
	}
	if len(rec.dispatched) != 1 || rec.dispatched[0] != id {
		t.Errorf("executor dispatched for %v, want one dispatch to %q (the session the route names)",
			rec.dispatched, id)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("re-marshal response: %v", err)
	}
	if containsKey(t, raw, "slotId") {
		t.Errorf("message response echoes a slotId key: %s", raw)
	}
}

// spec: §4.1 (message scope); §7.2 (tool-use approval)
// diagnosis: the tool-approval interaction detail or the
// tool_use_requested SSE payload carries a slot address. Both enclosing
// objects already name the session (the interaction's SessionID, and the
// per-session SSE stream), so a slot key duplicates an identifier the
// client already holds.
func TestToolApprovalPayloadsCarryNoSlotAddress_spec_4_1(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_tool_1", TenantID: "acme", UserID: "alice@acme.com",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	inter := interactionstore.NewMemory()
	bus := sessionevents.NewBus(64)
	waits := toolapproval.NewRegistry()

	sub, err := bus.SubscribeForTenant("acme", "sess_tool_1", 0, 8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	now := func() time.Time { return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC) }
	gate := sessionserver.NewToolApprovalGate(store, inter, bus, waits, now, 250*time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = gate.AwaitApproval(context.Background(), "acme", "sess_tool_1",
			executor.PendingToolCall{
				ID:        "tc-1",
				Name:      "lenny/deploy",
				Arguments: json.RawMessage(`{"target":"prod"}`),
			})
	}()

	select {
	case ev := <-sub.Events():
		if ev.Type != "tool_use_requested" {
			t.Fatalf("event type = %q, want tool_use_requested", ev.Type)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
			t.Fatalf("decode tool_use_requested payload: %v (data=%s)", err, ev.Data)
		}
		// The key set is pinned exhaustively so no address key can be
		// reintroduced alongside the three §7.2 members.
		wantKeys := map[string]bool{"tool_call_id": true, "tool": true, "args": true}
		for k := range payload {
			if !wantKeys[k] {
				t.Errorf("tool_use_requested payload carries unexpected key %q: %s", k, ev.Data)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no tool_use_requested event published")
	}

	pending, err := inter.Get(context.Background(), "acme", "sess_tool_1", "alice@acme.com", "tc-1")
	if err != nil {
		t.Fatalf("read recorded interaction: %v", err)
	}
	if pending.SessionID != "sess_tool_1" {
		t.Errorf("interaction.SessionID = %q, want sess_tool_1 (the enclosing object names the session)",
			pending.SessionID)
	}
	wantDetail := map[string]bool{"tool": true, "args": true}
	for k := range pending.Detail {
		if !wantDetail[k] {
			t.Errorf("interaction detail carries unexpected key %q: %v", k, pending.Detail)
		}
	}
	<-done
}

// containsKey reports whether the JSON document raw carries key anywhere
// in its object tree.
func containsKey(t *testing.T, raw []byte, key string) bool {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode JSON while scanning for %q: %v (doc=%s)", key, err, raw)
	}
	return jsonHasKey(v, key)
}

func jsonHasKey(v any, key string) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			if k == key || jsonHasKey(sub, key) {
				return true
			}
		}
	case []any:
		for _, sub := range t {
			if jsonHasKey(sub, key) {
				return true
			}
		}
	}
	return false
}

// recordingExecutor records the session identifier each dispatch is
// addressed to so a test can assert that the route's session, rather than
// any body field, decides where a message lands.
type recordingExecutor struct {
	executor.Executor
	dispatched []string
}

func (r *recordingExecutor) Send(ctx context.Context, sessionID string, messages []executor.Message) (executor.Response, error) {
	r.dispatched = append(r.dispatched, sessionID)
	return r.Executor.Send(ctx, sessionID, messages)
}

// slotFailNamespace is the agent namespace the slot-failure fixture's warm
// pool, template, and Sandbox live in.
const slotFailNamespace = "lenny-agents"

// slotFailCluster boots an envtest control plane holding a
// concurrent-session warm pool, its template, and one idle Sandbox, so the
// §5.2 slot reservation the /start route drives is a real one. The
// controller-runtime fake client cannot serve the SSA Apply the slot
// claimer's binding-state write needs, so the reservation half of this
// route case needs a live API server; the test skips where the envtest
// binaries are absent.
func slotFailCluster(t *testing.T) client.Client {
	t.Helper()
	env := envtest.Start(t)
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add lenny scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 scheme: %v", err)
	}
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: slotFailNamespace},
	}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	objs := []client.Object{
		&lennyv1.SandboxWarmPool{
			ObjectMeta: metav1.ObjectMeta{Name: "conc-pool", Namespace: slotFailNamespace},
			Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "conc-tmpl", MinWarm: 1, MaxWarm: 5},
		},
		&lennyv1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "conc-tmpl", Namespace: slotFailNamespace},
			Spec: lennyv1.SandboxTemplateSpec{
				RuntimeRef:       "echo",
				IsolationProfile: string(isolation.ProfileSandboxed),
			},
		},
	}
	for _, o := range objs {
		if err := c.Create(ctx, o); err != nil {
			t.Fatalf("create %T %s: %v", o, o.GetName(), err)
		}
	}
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sbx-conc-1",
			Namespace: slotFailNamespace,
			Labels:    map[string]string{warmpool.LabelPool: "conc-pool"},
		},
	}
	if err := c.Create(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	// The WarmPoolController owns Sandbox.status, so the idle phase and pod
	// address are seeded under its field manager rather than the gateway's.
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Sandbox")
	u.SetName(sb.Name)
	u.SetNamespace(sb.Namespace)
	if err := unstructured.SetNestedField(u.Object,
		map[string]interface{}{"phase": "idle", "podIP": "10.244.3.9"}, "status"); err != nil {
		t.Fatalf("build status seed: %v", err)
	}
	if err := c.Status().Patch(ctx, u, client.Apply,
		client.FieldOwner(string(ownership.WarmPoolController))); err != nil {
		t.Fatalf("seed sandbox status: %v", err)
	}
	return c
}

// slotFailServer wires a session server over cluster whose adapter dial
// fails with dialCode. InvalidArgument is the non-retryable
// workspace_validation category, so the slot is reserved, fails with no
// retry, and the /start route reaches the §5.2 "Client error on exhaustion"
// mapper. A transient code (Unavailable) instead satisfies neither half of
// §5.2's condition and keeps the retryable §15.1 creation fallback.
func slotFailServer(t *testing.T, cluster client.Client, store sessionstore.Store, dialCode codes.Code) *sessionserver.Server {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })

	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name:          "conc-pool",
		RuntimeRef:    "echo",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions:            4,
			AcknowledgeProcessLevelIsolation: true,
		},
	}); err != nil {
		t.Fatalf("seed pool mirror: %v", err)
	}

	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        slotFailNamespace,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		SlotCounter:      slotcounter.New(rc),
		DialAdapter: func(string) (*adapterclient.Client, error) {
			return nil, status.Error(dialCode, "adapter dial failed")
		},
	}
	return sessionserver.New(store, sessionserver.Options{
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          slotFailNamespace,
		Pools:                   pools,
		DefaultIsolationProfile: isolation.ProfileSandboxed,
	})
}

// spec: §5.2 (client error on exhaustion); §15.1 (error envelope)
// diagnosis: the 422 the /start route returns for a §5.2 slot failure no
// longer names the session whose slot failed, or names it with a slot
// address. A session-mode slot's identifier is its session's identifier,
// so error.details carries sessionId and no slotId, and the client reads
// the session from the body.
func TestStartSlotFailedBodyNamesSession_spec_5_2(t *testing.T) {
	cluster := slotFailCluster(t)
	store := memstore.New()
	const id = "sess_slot_failed_1"
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:               id,
		TenantID:         "acme",
		UserID:           "alice@acme.com",
		RuntimeRef:       "echo",
		IsolationProfile: isolation.ProfileSandboxed,
		State:            session.StateReady,
	}); err != nil {
		t.Fatalf("seed session row: %v", err)
	}
	srv := slotFailServer(t, cluster, store, codes.InvalidArgument)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, body := do(t, ts, "POST", "/v1/sessions/"+id+"/start", nil)
	if resp.StatusCode != 422 {
		t.Fatalf("status = %d, want 422 SLOT_FAILED; body=%v", resp.StatusCode, body)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope == nil {
		t.Fatalf("response carries no error envelope: %v", body)
	}
	if envelope["code"] != "SLOT_FAILED" {
		t.Errorf("error.code = %v, want SLOT_FAILED", envelope["code"])
	}
	details, _ := envelope["details"].(map[string]any)
	if details["sessionId"] != id {
		t.Errorf("error.details.sessionId = %v, want %q", details["sessionId"], id)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("re-marshal response: %v", err)
	}
	if containsKey(t, raw, "slotId") {
		t.Errorf("the 422 body carries a slotId key: %s", raw)
	}
}

// spec: §5.2 (client error on exhaustion); §7.1 (atomic creation)
// diagnosis: the one-call POST /v1/sessions/start did not answer a slot
// failure with the §5.2 client error, or leaked a slot address into its
// error body. That route's path carries no session identifier, so
// the body is the client's only source of one, and a session-mode slot's
// identifier is its session's identifier: no separate slot key belongs
// there. The route reserves the slot inside the §7.1 atomic creation unit,
// so a slot failure there fails the creation and no session row persists.
func TestCreateAndStartSlotFailureBodyCarriesNoSlotAddress_spec_5_2(t *testing.T) {
	cluster := slotFailCluster(t)
	store := memstore.New()
	srv := slotFailServer(t, cluster, store, codes.InvalidArgument)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, body := do(t, ts, "POST", "/v1/sessions/start", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if resp.StatusCode != 422 {
		t.Fatalf("status = %d, want 422 SLOT_FAILED; body=%v", resp.StatusCode, body)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope == nil {
		t.Fatalf("response carries no error envelope: %v", body)
	}
	if envelope["code"] != "SLOT_FAILED" {
		t.Errorf("error.code = %v, want SLOT_FAILED", envelope["code"])
	}
	details, _ := envelope["details"].(map[string]any)
	sessionID, _ := details["sessionId"].(string)
	if sessionID == "" {
		t.Errorf("error.details.sessionId is empty; the one-call route's body is the client's only source of the session identifier: %v", envelope)
	}
	if details["retryable"] != false {
		t.Errorf("error.details.retryable = %v, want false", details["retryable"])
	}
	// §7.1 atomic creation: the slot failed inside the creation unit, so the
	// session the body names must not survive as a row the client can find.
	if _, err := store.GetByID(context.Background(), sessionID); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("session %q persisted after the failed atomic creation (err = %v)", sessionID, err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("re-marshal response: %v", err)
	}
	if containsKey(t, raw, "slotId") {
		t.Errorf("the one-call start error body carries a slotId key: %s", raw)
	}
}

// spec: §5.2 (client error on exhaustion); §15.1 (retryable creation
// fallback)
// diagnosis: a transient slot failure on the no-retry create path is being
// answered with the §5.2 non-retryable client error. §5.2 conditions that
// error on either no retry being attempted (a non-retryable category) or the
// retry budget being exhausted; a transient dial failure with no budget
// satisfies neither, so it stays the retryable 503 fallback with Retry-After
// and the client backs off rather than reading a recoverable transport
// failure as terminal.
func TestCreateAndStartTransientSlotFailureStaysRetryable_spec_5_2(t *testing.T) {
	cluster := slotFailCluster(t)
	store := memstore.New()
	srv := slotFailServer(t, cluster, store, codes.Unavailable)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, body := do(t, ts, "POST", "/v1/sessions/start", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	envelope, _ := body["error"].(map[string]any)
	if envelope != nil && envelope["code"] == "SLOT_FAILED" {
		t.Fatalf("a transient slot failure was answered with the non-retryable §5.2 client error: status=%d body=%v",
			resp.StatusCode, body)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 retryable creation fallback; body=%v", resp.StatusCode, body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Errorf("the retryable creation fallback carries no Retry-After header")
	}
}

// spec: §7.1 (atomic creation envelope); §5.2 (client error on exhaustion)
// diagnosis: a plain POST /v1/sessions is answering a create-time slot
// failure with the §5.2 non-retryable client error. That route starts
// nothing, so §7.1's atomic-creation contract owns its failures: every
// claim failure, including a non-retryable post-reservation slot failure,
// stays the retryable 503 SESSION_CREATION_FAILED envelope with
// Retry-After. The §5.2 client error belongs to the routes that start a
// session on the slot they bind.
func TestCreateOnlySlotFailureKeepsCreationEnvelope_spec_7_1(t *testing.T) {
	cluster := slotFailCluster(t)
	store := memstore.New()
	srv := slotFailServer(t, cluster, store, codes.InvalidArgument)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, body := do(t, ts, "POST", "/v1/sessions", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	envelope, _ := body["error"].(map[string]any)
	if envelope != nil && envelope["code"] == "SLOT_FAILED" {
		t.Fatalf("plain creation answered a slot failure with the §5.2 client error: status=%d body=%v",
			resp.StatusCode, body)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 SESSION_CREATION_FAILED; body=%v", resp.StatusCode, body)
	}
	if envelope == nil {
		t.Fatalf("response carries no error envelope: %v", body)
	}
	if envelope["code"] != "SESSION_CREATION_FAILED" {
		t.Errorf("error.code = %v, want SESSION_CREATION_FAILED", envelope["code"])
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Errorf("the §7.1 creation fallback carries no Retry-After header")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("re-marshal response: %v", err)
	}
	if containsKey(t, raw, "slotId") {
		t.Errorf("the create-time slot-failure body carries a slotId key: %s", raw)
	}
}
