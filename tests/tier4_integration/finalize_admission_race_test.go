// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 finalize admission and exit races on a real Binder.
//
// These cases drive POST /v1/sessions/{id}/finalize through a fully wired
// in-process gateway Server whose pod binder runs against a real
// kube-apiserver (envtest), a real Artifact-Store blob store, and a real
// adapter over an in-memory gRPC connection. A hook store wraps the session
// store and forces the interleaving each case needs. Interceptors on the
// adapter count the pod RPCs the prepare phase issues, and a wrapper on the
// envtest client counts SandboxClaim deletes, so a case observes what the
// refused or overtaken finalize call did to the pod rather than only its HTTP
// response.

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// admissionSessionID is the session every finalize admission case creates.
const admissionSessionID = "sess-fin-admission"

// admissionWait bounds every rendezvous a hook waits on, so a hook that never
// fires fails the case instead of hanging the package.
const admissionWait = 30 * time.Second

// rpcCounter counts the adapter RPCs the gateway issues, by full method name.
type rpcCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newRPCCounter() *rpcCounter { return &rpcCounter{counts: map[string]int{}} }

func (c *rpcCounter) record(fullMethod string) {
	c.mu.Lock()
	c.counts[fullMethod]++
	c.mu.Unlock()
}

// count returns the calls recorded for a full gRPC method name.
func (c *rpcCounter) count(fullMethod string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[fullMethod]
}

// serverOptions returns the chained unary and stream interceptors that record
// every RPC before the adapter handles it. The chained form composes with the
// interceptors adapter.NewGRPCServer installs itself.
func (c *rpcCounter) serverOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(func(
			ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
		) (any, error) {
			c.record(info.FullMethod)
			return handler(ctx, req)
		}),
		grpc.ChainStreamInterceptor(func(
			srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler,
		) error {
			c.record(info.FullMethod)
			return handler(srv, ss)
		}),
	}
}

// claimDeleteClient wraps the envtest client and records the name of every
// SandboxClaim Delete, whether or not the object still exists.
type claimDeleteClient struct {
	client.Client

	mu      sync.Mutex
	deleted []string
}

func (c *claimDeleteClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if _, ok := obj.(*lennyv1.SandboxClaim); ok {
		c.mu.Lock()
		c.deleted = append(c.deleted, obj.GetName())
		c.mu.Unlock()
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func (c *claimDeleteClient) claimDeletes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.deleted...)
}

// admissionHook selects the Update whose mutation, probed on a copy of the
// current row, writes state for the occurrence-th time. before runs ahead of
// the delegated Update and afterCommit runs once it has committed.
type admissionHook struct {
	state       session.State
	occurrence  int
	before      func()
	afterCommit func()
	fired       bool
}

// admissionHookStore wraps a session store. It selects the Update to act on
// by the State the mutation writes plus an occurrence count of that State,
// because the prepare phase adds Updates that write `finalizing` again. A
// probe whose mutation refuses the row counts nothing, so a refused finalize
// never consumes a hook. It can also run a one-shot callback after the next
// Get, which is how a case pins another call's pre-lock read.
type admissionHookStore struct {
	sessionstore.Store

	// mu guards seen, hooks, each hook's fired flag, and afterNextGet.
	mu           sync.Mutex
	seen         map[session.State]int
	hooks        []*admissionHook
	afterNextGet func()
}

func newAdmissionHookStore(inner sessionstore.Store) *admissionHookStore {
	return &admissionHookStore{Store: inner, seen: map[session.State]int{}}
}

// arm adds hooks. Arming after /create keeps the create-path Updates, which
// write only `created`, from matching.
func (h *admissionHookStore) arm(hooks ...*admissionHook) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hooks = append(h.hooks, hooks...)
}

// onNextGet arms fn to run once, after the next Get has read the row.
func (h *admissionHookStore) onNextGet(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.afterNextGet = fn
}

func (h *admissionHookStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := h.Store.Get(ctx, tenantID, id)
	h.mu.Lock()
	fn := h.afterNextGet
	h.afterNextGet = nil
	h.mu.Unlock()
	if fn != nil {
		fn()
	}
	return row, err
}

func (h *admissionHookStore) Update(
	ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error,
) (sessionstore.Session, error) {
	hook := h.selectHook(ctx, tenantID, id, mutate)
	if hook == nil {
		return h.Store.Update(ctx, tenantID, id, mutate)
	}
	if hook.before != nil {
		hook.before()
	}
	row, err := h.Store.Update(ctx, tenantID, id, mutate)
	if err == nil && hook.afterCommit != nil {
		hook.afterCommit()
	}
	return row, err
}

// selectHook probes the mutation on a copy of the row read straight from the
// inner store, so the probe never triggers the afterNextGet callback. The
// finalize mutations only assign fields, so probing them is side-effect free.
func (h *admissionHookStore) selectHook(
	ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error,
) *admissionHook {
	cur, err := h.Store.Get(ctx, tenantID, id)
	if err != nil {
		return nil
	}
	probe := cur
	if mutate(&probe) != nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen[probe.State]++
	n := h.seen[probe.State]
	for _, hk := range h.hooks {
		if !hk.fired && hk.state == probe.State && hk.occurrence == n {
			hk.fired = true
			return hk
		}
	}
	return nil
}

// admissionFixture is the wired gateway and the observers a case asserts on.
type admissionFixture struct {
	handler  http.Handler
	store    *admissionHookStore
	cluster  *claimDeleteClient
	assigner *recordingAssigner
	rpcs     *rpcCounter
	bus      *sessionevents.Bus
}

// newAdmissionFixture wires a Server over the hook store and a real Binder
// against envtest, with one blob store shared by the upload path and the
// binder, and creates the session, which claims sbx-1.
func newAdmissionFixture(t *testing.T) *admissionFixture {
	t.Helper()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.CredentialsDir = t.TempDir()
	adapterSrv.Runtime = &eagerRuntime{}

	rpcs := newRPCCounter()
	cluster := &claimDeleteClient{Client: eagerCluster(t)}
	blobs := blobstore.NewMemoryStore(nil)
	assigner := &recordingAssigner{}
	binder := &podsession.Binder{
		Client:           cluster,
		Namespace:        eagerNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      eagerAdapterDialer(t, adapterSrv, rpcs.serverOptions()...),
		Blobs:            blobs,
		Credentials:      assigner,
	}
	tenants, runtimes, credPools := eagerCredentialStores(t)
	store := newAdmissionHookStore(memstore.New())
	bus := sessionevents.NewBus(256)
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return admissionSessionID },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          eagerNS,
		Tenants:                 tenants,
		Runtimes:                runtimes,
		CredentialPools:         credPools,
		CredentialRouter:        credrouter.NewDefault(),
		Blobs:                   blobs,
		Events:                  bus,
	})
	f := &admissionFixture{
		handler: srv.Handler(), store: store, cluster: cluster,
		assigner: assigner, rpcs: rpcs, bus: bus,
	}
	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	if rr := f.do(http.MethodPost, "/v1/sessions", createBody, nil); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body=%s", rr.Code, rr.Body.String())
	}
	return f
}

// do sends one request for tenant acme through the Server's handler. It is
// safe to call from several goroutines.
func (f *admissionFixture) do(method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

// finalize sends a keyless finalize carrying body.
func (f *admissionFixture) finalize(body []byte) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, "/v1/sessions/"+admissionSessionID+"/finalize", body,
		map[string]string{"Content-Type": "application/json"})
}

// upload buffers one file in the Artifact Store and returns its uploadRef.
func (f *admissionFixture) upload(t *testing.T) string {
	t.Helper()
	rr := f.do(http.MethodPost, "/v1/sessions/"+admissionSessionID+"/upload",
		[]byte("# uploaded before the finalize race"), map[string]string{"Content-Type": "text/markdown"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload: status %d, body=%s", rr.Code, rr.Body.String())
	}
	var up sessionserver.UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &up); err != nil || up.UploadRef == "" {
		t.Fatalf("decode upload response (err=%v): %s", err, rr.Body.String())
	}
	return up.UploadRef
}

// storedState reads the row's State from the inner store.
func (f *admissionFixture) storedState(t *testing.T) session.State {
	t.Helper()
	row, err := f.store.Store.Get(context.Background(), "acme", admissionSessionID)
	if err != nil {
		t.Fatalf("read stored row: %v", err)
	}
	return row.State
}

// sseStates returns the state of every status_change event on the bus.
func (f *admissionFixture) sseStates() []string {
	var out []string
	for _, ev := range f.bus.History(admissionSessionID, 0) {
		if ev.Type != "status_change" {
			continue
		}
		var body struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal([]byte(ev.Data), &body)
		out = append(out, body.State)
	}
	return out
}

func (a *recordingAssigner) releasedSessions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.released...)
}

// preconditionDetails decodes the §15.1 INVALID_STATE_TRANSITION envelope.
type preconditionDetails struct {
	Code          string
	CurrentState  string
	AllowedStates []string
}

func decodePrecondition(t *testing.T, rr *httptest.ResponseRecorder) preconditionDetails {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				CurrentState  string   `json:"currentState"`
				AllowedStates []string `json:"allowedStates"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rr.Body.String())
	}
	return preconditionDetails{
		Code:          env.Error.Code,
		CurrentState:  env.Error.Details.CurrentState,
		AllowedStates: env.Error.Details.AllowedStates,
	}
}

// waitAdmission blocks until ch closes or admissionWait elapses, reporting a
// timeout as a test error. It is safe to call from a handler goroutine.
func waitAdmission(t *testing.T, ch <-chan struct{}, what string) {
	select {
	case <-ch:
	case <-time.After(admissionWait):
		t.Errorf("timed out after %s waiting for %s", admissionWait, what)
	}
}

// uploadFilePlan is a finalize body whose WorkspacePlan stages uploadRef.
func uploadFilePlan(uploadRef string) []byte {
	body, _ := json.Marshal(map[string]any{
		"workspacePlan": map[string]any{
			"schemaVersion": 1,
			"sources": []map[string]any{
				{"type": "uploadFile", "path": "CLAUDE.md", "uploadRef": uploadRef},
			},
		},
	})
	return body
}

// spec: 15.1 (finalize precondition), 7.1 (steps 11-13), 4.9 (Credential Leasing Service), 7.2 (terminal states)
//
// diagnosis: a failure means a refused finalize reached the pod. Two keyless
// finalize calls both read `created` before either entry write; exactly one
// must commit `finalizing` and the other must be refused under the row lock
// with 409 before any pod RPC. A second PrepareWorkspace, a second lease
// assignment, a DemoteSDK, or a SandboxClaim delete means the refused call
// ran the prepare phase against the pod the winner holds, or reclaimed it.
func TestFinalizeEntryRaceRefusedCallIssuesNoPodRPC_spec_15_1(t *testing.T) {
	f := newAdmissionFixture(t)
	body := uploadFilePlan(f.upload(t))

	// Call 1 stops at its entry Update until call 2 has read `created`, and
	// call 2 stops after that read until call 1 has committed `finalizing`,
	// so both pass the pre-lock check and call 2's entry Update runs against
	// the committed row.
	atLock := make(chan struct{})
	secondRead := make(chan struct{})
	committed := make(chan struct{})
	f.store.arm(&admissionHook{
		state: session.StateFinalizing, occurrence: 1,
		before: func() {
			close(atLock)
			waitAdmission(t, secondRead, "the second finalize's pre-lock read")
		},
		afterCommit: func() { close(committed) },
	})

	responses := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		responses[0] = f.finalize(body)
	}()
	waitAdmission(t, atLock, "the first finalize's entry Update")
	f.store.onNextGet(func() {
		close(secondRead)
		waitAdmission(t, committed, "the first finalize's entry commit")
	})
	go func() {
		defer wg.Done()
		responses[1] = f.finalize(body)
	}()
	wg.Wait()

	if responses[0].Code != http.StatusOK {
		t.Fatalf("first finalize: status %d, want 200; body=%s", responses[0].Code, responses[0].Body.String())
	}
	if responses[1].Code != http.StatusConflict {
		t.Fatalf("second finalize: status %d, want 409; body=%s", responses[1].Code, responses[1].Body.String())
	}
	d := decodePrecondition(t, responses[1])
	if d.Code != "INVALID_STATE_TRANSITION" {
		t.Errorf("refused finalize code = %q, want INVALID_STATE_TRANSITION", d.Code)
	}
	if d.CurrentState != string(session.StateFinalizing) && d.CurrentState != string(session.StateReady) {
		t.Errorf("refused finalize currentState = %q, want finalizing or ready", d.CurrentState)
	}
	if !reflect.DeepEqual(d.AllowedStates, []string{string(session.StateCreated)}) {
		t.Errorf("refused finalize allowedStates = %v, want [created]", d.AllowedStates)
	}
	if got := f.storedState(t); got != session.StateReady {
		t.Errorf("stored state = %q, want ready (the admitted call's outcome)", got)
	}
	if n := f.rpcs.count(adapterv1.Adapter_PrepareWorkspace_FullMethodName); n != 1 {
		t.Errorf("PrepareWorkspace RPCs = %d, want exactly 1 (only the admitted call stages)", n)
	}
	if n := f.rpcs.count(adapterv1.Adapter_RunSetup_FullMethodName); n > 1 {
		t.Errorf("RunSetup RPCs = %d, want at most 1", n)
	}
	if n := f.rpcs.count(adapterv1.Adapter_DemoteSDK_FullMethodName); n != 0 {
		t.Errorf("DemoteSDK RPCs = %d, want 0 (the refused call must not demote the runtime)", n)
	}
	if n := f.assigner.assignCount(); n != 1 {
		t.Errorf("AssignProto calls = %d, want exactly 1 (only the admitted call assigns the lease)", n)
	}
	if got := f.cluster.claimDeletes(); len(got) != 0 {
		t.Errorf("SandboxClaim deletes = %v, want none (the refused call must not reclaim the pod)", got)
	}
}

// spec: 15.1 (finalize precondition), 7.1 (steps 11-13), 4.9 (Credential Leasing Service), 7.2 (terminal states)
//
// diagnosis: a failure means an overtaken finalize wrote over a terminal
// state or deleted a claim it no longer owned. A DELETE through the handler
// cancels the session while the finalize prepare phase is complete but
// before its ready write. The finalize must answer 409 with
// currentState=cancelled, keep the row cancelled, emit no ready status
// change, and revoke its lease without reclaiming the pod, so the
// SandboxClaim is deleted once, by the DELETE's terminal reclaim.
func TestFinalizeExitRaceKeepsCancelledAndReclaimsOnce_spec_15_1(t *testing.T) {
	f := newAdmissionFixture(t)

	var deleteRR *httptest.ResponseRecorder
	f.store.arm(&admissionHook{
		state: session.StateReady, occurrence: 1,
		before: func() {
			deleteRR = f.do(http.MethodDelete, "/v1/sessions/"+admissionSessionID, nil, nil)
		},
	})

	rr := f.finalize(nil)
	if deleteRR == nil {
		t.Fatalf("the ready-write hook never fired; finalize status %d, body=%s", rr.Code, rr.Body.String())
	}
	if deleteRR.Code != http.StatusOK {
		t.Fatalf("DELETE: status %d, want 200; body=%s", deleteRR.Code, deleteRR.Body.String())
	}
	if rr.Code != http.StatusConflict {
		t.Fatalf("overtaken finalize: status %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	d := decodePrecondition(t, rr)
	if d.CurrentState != string(session.StateCancelled) {
		t.Errorf("overtaken finalize currentState = %q, want cancelled", d.CurrentState)
	}
	if !reflect.DeepEqual(d.AllowedStates, []string{string(session.StateCreated)}) {
		t.Errorf("overtaken finalize allowedStates = %v, want [created]", d.AllowedStates)
	}
	if got := f.storedState(t); got != session.StateCancelled {
		t.Errorf("stored state = %q, want cancelled (the terminal state the DELETE committed)", got)
	}
	for _, st := range f.sseStates() {
		if st == string(session.StateReady) {
			t.Errorf("status_change events %v include ready; the overtaken finalize must not announce ready", f.sseStates())
			break
		}
	}
	want := []string{admissionSessionID, admissionSessionID}
	if got := f.assigner.releasedSessions(); !reflect.DeepEqual(got, want) {
		t.Errorf("ReleaseSession calls = %v, want %v (the terminal reclaim and the finalize handler's revoke)", got, want)
	}
	if got := f.cluster.claimDeletes(); !reflect.DeepEqual(got, []string{"claim-sbx-1"}) {
		t.Errorf("SandboxClaim deletes = %v, want exactly [claim-sbx-1] (the DELETE's terminal reclaim only)", got)
	}
	var claim lennyv1.SandboxClaim
	err := f.cluster.Get(context.Background(), client.ObjectKey{Namespace: eagerNS, Name: "claim-sbx-1"}, &claim)
	if err == nil {
		t.Errorf("claim-sbx-1 still present after the DELETE's terminal reclaim")
	}
}
