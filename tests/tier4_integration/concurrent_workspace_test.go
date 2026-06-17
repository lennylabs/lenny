// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 functional integration flow for the concurrent-workspace
// per-slot execution path. A pool whose sessionPolicy.maxConcurrentSessions
// is above 1 multiplexes simultaneous sessions onto one pod, each in its
// own slot, over a single runtime process. This test exercises that path
// end to end across the gateway->adapter->runtime envelope route:
//
//   - A single real adapter.Server stands in for the pod's sidecar adapter,
//     driven over its real gRPC contract — the same surface the gateway's
//     adapterclient speaks.
//   - A single real SocketRuntimeProcess binds the pod's abstract runtime
//     socket and spawns the real cmd/runtimes/echo-concurrent binary, the
//     one reference runtime that implements the slotId dispatch loop. One
//     runtime process per pod serves every slot, multiplexed on slotId over
//     the single connection.
//   - Two sessions land on the pod in distinct slots, each with an isolated
//     per-slot workspace at /workspace/slots/{slotId}/current/.
//
// The flow asserts the two properties the proposal names. First, workspace
// distinctness: each slot's FinalizeWorkspace materializes its own content
// into its own per-slot tree, and neither slot's file leaks into the other's
// workspace or into the whole-pod /workspace/current. Second, per-slot
// response slotId tagging: a message dispatched on one slot's Attach stream
// comes back tagged with that slot's slotId, and each Attach stream receives
// only its slot's responses, proving the adapter stamps the inbound slotId
// and demultiplexes the runtime's interleaved output by slotId.
//
// The pool configuration the path requires (maxConcurrentSessions > 1 with
// acknowledgeProcessLevelIsolation: true) is the deployer contract the
// gateway enforces before it ever assigns a slotId; on the adapter the
// per-slot path activates on slotId presence alone, so a slotId-bearing
// StartSession is by definition a concurrent-pool claim.
//
// spec: §5.2 (concurrent sessions: slotId multiplexing over stdin, dispatch
// loop keyed on slotId, acknowledgeProcessLevelIsolation), §6.4 (per-slot
// filesystem layout /workspace/slots/{slotId}/, per-slot cwd), §15.4.1
// (single stdin channel carrying slotId when maxConcurrentSessions > 1).
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// concurrentPool names the deployer pool contract this flow stands up: a
// session-mode pool with sessionPolicy.maxConcurrentSessions above 1 and
// acknowledgeProcessLevelIsolation accepted. The values are asserted as a
// gate so a future change that lowered maxConcurrentSessions to 1 or
// dropped the acknowledgment would no longer exercise the per-slot path.
//
// spec: §5.2 line 511 — acknowledgeProcessLevelIsolation is required to
// configure maxConcurrentSessions > 1.
type concurrentPool struct {
	maxConcurrentSessions          int
	acknowledgeProcessLevelIsolate bool
}

// slot is one session's binding to a pod slot. The gateway uses the session
// id as the slot id (1:1 per pod), so the test mirrors that derivation.
type slot struct {
	sessionID string
	slotID    string
}

// spec: §5.2 line 509 (slotId multiplexing, dispatch loop keyed on slotId),
// §5.2 line 511 (acknowledgeProcessLevelIsolation for maxConcurrentSessions
// > 1), §6.4 line 365/384 (per-slot workspace /workspace/slots/{slotId}/,
// per-slot cwd).
// diagnosis: a failure means concurrent-workspace per-slot execution
// regressed end to end across the gateway->adapter->runtime path. Either two
// sessions on a maxConcurrentSessions > 1 pod did not land in distinct slots
// with isolated workspaces (one slot's file leaked into the other's tree or
// into the whole-pod /workspace/current), or the per-slot response was not
// tagged with its originating slotId (the adapter failed to stamp the
// inbound slotId or to demultiplex the single runtime connection's
// interleaved output by slotId).
func TestConcurrentWorkspacePerSlotExecution_spec_5_2(t *testing.T) {
	pool := concurrentPool{maxConcurrentSessions: 2, acknowledgeProcessLevelIsolate: true}
	// The deployer contract that admits the per-slot path: the gateway
	// rejects a maxConcurrentSessions > 1 pool that omits the isolation
	// acknowledgment (spec/05:511), so this flow only stands up against a
	// pool that carries both.
	if pool.maxConcurrentSessions <= 1 || !pool.acknowledgeProcessLevelIsolate {
		t.Fatalf("the concurrent-workspace flow requires maxConcurrentSessions > 1 with "+
			"acknowledgeProcessLevelIsolation: true, got %+v", pool)
	}

	echoConcurrentBin := buildConcurrentRuntime(t)

	base := t.TempDir()
	srv := adapter.New("tier4-concurrent")
	srv.WorkspaceRoot = filepath.Join(base, "workspace", "current")
	srv.WorkspaceBase = filepath.Join(base, "workspace")
	srv.SessionsRoot = filepath.Join(base, "sessions")
	srv.ArtifactsRoot = filepath.Join(base, "artifacts")
	srv.CredentialsDir = filepath.Join(base, "run", "lenny")

	// One real runtime process per pod, the §4.7 sidecar transport: the
	// adapter binds the abstract socket and spawns the real echo-concurrent
	// binary, which dials back and runs its slotId dispatch loop over the one
	// connection. Every slot rides this single connection (spec/05:509).
	rt, err := adapter.NewSocketRuntimeProcess(concurrentSocketAddr(t))
	if err != nil {
		t.Fatalf("bind pod runtime socket: %v", err)
	}
	rt.SpawnPath = echoConcurrentBin
	rt.AcceptTimeout = 15 * time.Second
	srv.Runtime = rt
	t.Cleanup(func() { _ = rt.Close(context.Background(), "pod-teardown") })

	client := concurrentAdapterClient(t, srv)
	ctx := context.Background()

	slots := []slot{
		{sessionID: "sess-alice", slotID: "slot-01"},
		{sessionID: "sess-bob", slotID: "slot-02"},
	}
	if len(slots) > pool.maxConcurrentSessions {
		t.Fatalf("flow drives %d slots, more than the pool bound %d", len(slots), pool.maxConcurrentSessions)
	}

	// Two sessions land on the one pod, each claiming its own slot. The
	// second claim is admitted rather than rejected with "pod is not idle":
	// the single pod-global runtime multiplexes both slots on slotId.
	for _, sl := range slots {
		if _, err := client.StartSession(ctx, &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: sl.sessionID},
			Runtime:   "echo-concurrent",
			SlotId:    &adapterv1.SlotId{Value: sl.slotID},
		}); err != nil {
			t.Fatalf("StartSession(%s on %s): %v", sl.sessionID, sl.slotID, err)
		}
	}

	assertWorkspaceDistinctness(t, ctx, client, srv, slots)
	assertPerSlotResponseTagging(t, client, slots)
}

// assertWorkspaceDistinctness materializes a distinct marker file into each
// slot's per-slot workspace and asserts that each slot's tree holds only its
// own content, that no slot's file leaked into a sibling's tree, and that the
// whole-pod /workspace/current was never used. This pins the §6.4 per-slot
// layout: each slot's cwd is /workspace/slots/{slotId}/current/, not a shared
// /workspace/current.
//
// spec: §6.4 line 365/384 — per-slot workspace /workspace/slots/{slotId}/,
// the runtime MUST NOT assume a global /workspace/current.
func assertWorkspaceDistinctness(t *testing.T, ctx context.Context, client adapterv1.AdapterClient, srv *adapter.Server, slots []slot) {
	t.Helper()
	content := map[string]string{
		slots[0].slotID: "workspace-of-" + slots[0].sessionID,
		slots[1].slotID: "workspace-of-" + slots[1].sessionID,
	}
	for _, sl := range slots {
		if _, err := client.FinalizeWorkspace(ctx, &adapterv1.FinalizeWorkspaceRequest{
			SessionId: &adapterv1.SessionId{Value: sl.sessionID},
			SlotId:    &adapterv1.SlotId{Value: sl.slotID},
			WorkspacePlan: &adapterv1.WorkspacePlan{
				SchemaVersion: 1,
				Sources: []*adapterv1.WorkspaceSource{
					{Type: "inlineFile", Path: "marker.txt", Content: content[sl.slotID], Mode: "0644"},
				},
			},
		}); err != nil {
			t.Fatalf("FinalizeWorkspace(%s): %v", sl.slotID, err)
		}
	}

	for _, sl := range slots {
		got, err := os.ReadFile(filepath.Join(srv.WorkspaceBase, "slots", sl.slotID, "current", "marker.txt"))
		if err != nil {
			t.Fatalf("read %s per-slot workspace marker: %v", sl.slotID, err)
		}
		if string(got) != content[sl.slotID] {
			t.Errorf("%s workspace marker = %q, want %q; per-slot workspaces are not isolated",
				sl.slotID, got, content[sl.slotID])
		}
	}
	// The whole-pod /workspace/current must never have been written: a
	// concurrent-workspace pod has no shared current cwd.
	if _, err := os.Stat(filepath.Join(srv.WorkspaceBase, "current", "marker.txt")); !os.IsNotExist(err) {
		t.Errorf("whole-pod /workspace/current was written; concurrent slots are not isolated (err=%v)", err)
	}
}

// assertPerSlotResponseTagging opens a per-slot Attach stream for each slot,
// dispatches a message on it, and asserts the response comes back tagged with
// that slot's slotId. Each stream receives only its own slot's response,
// proving the adapter stamps the inbound slotId onto the envelope it writes
// to the single runtime connection and demultiplexes the runtime's
// interleaved output by slotId. The real echo-concurrent runtime stamps the
// originating slotId onto every response, so the tag the stream observes is
// the runtime's, carried back across the adapter unchanged.
//
// spec: §15.4.1 line 1459 — single stdin channel, dispatch loop keyed on
// slotId; §6.4 line 401-405 — adapter sets the slot's cwd when dispatching.
func assertPerSlotResponseTagging(t *testing.T, client adapterv1.AdapterClient, slots []slot) {
	t.Helper()
	for _, sl := range slots {
		sl := sl
		t.Run(sl.slotID, func(t *testing.T) {
			streamCtx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			stream, err := client.Attach(streamCtx)
			if err != nil {
				t.Fatalf("Attach(%s): %v", sl.slotID, err)
			}
			// The first AttachRequest binds the slot and carries a message
			// envelope with no slotId in its body: the adapter stamps the
			// slot's slotId before the envelope reaches the shared runtime.
			msg := map[string]any{
				"type":  "message",
				"id":    "m_" + sl.sessionID,
				"input": []map[string]any{{"type": "text", "inline": "ping-" + sl.sessionID}},
			}
			body, err := json.Marshal(msg)
			if err != nil {
				t.Fatalf("encode message for %s: %v", sl.slotID, err)
			}
			if err := stream.Send(&adapterv1.AttachRequest{
				SessionId:    &adapterv1.SessionId{Value: sl.sessionID},
				SlotId:       &adapterv1.SlotId{Value: sl.slotID},
				EnvelopeJson: body,
			}); err != nil {
				t.Fatalf("Send bind+message(%s): %v", sl.slotID, err)
			}

			resp := recvResponse(t, stream)
			if resp.SlotID != sl.slotID {
				t.Errorf("%s response carried slotId %q, want %q; per-slot response tagging regressed",
					sl.slotID, resp.SlotID, sl.slotID)
			}
			if len(resp.Output) != 1 || resp.Output[0].Inline == "" {
				t.Errorf("%s response output = %+v, want the echoed input", sl.slotID, resp.Output)
			}
			// echocore wraps the echoed input with a per-slot sequence
			// prefix, so the response contains this slot's input rather than
			// equalling it. The assertion catches cross-slot misrouting: each
			// slot's response must echo its own input, never a sibling's.
			if want := "ping-" + sl.sessionID; len(resp.Output) == 1 && !strings.Contains(resp.Output[0].Inline, want) {
				t.Errorf("%s echoed %q, want it to contain %q; a sibling slot's input was misrouted",
					sl.slotID, resp.Output[0].Inline, want)
			}
		})
	}
}

// slotResponse is the subset of a §15.4.1 outbound `response` frame this
// flow asserts on: the discriminator, the slotId the runtime stamps and the
// adapter carries back, and the echoed text parts.
type slotResponse struct {
	Type   string `json:"type"`
	SlotID string `json:"slotId"`
	Output []struct {
		Inline string `json:"inline"`
	} `json:"output"`
}

// recvResponse reads from the Attach stream until it observes a `response`
// frame, skipping any protocol-level frames the runtime may interleave. It
// bounds the wait so a missing response fails the test rather than hanging.
func recvResponse(t *testing.T, stream adapterv1.Adapter_AttachClient) slotResponse {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		got, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		var resp slotResponse
		if err := json.Unmarshal(got.GetEnvelopeJson(), &resp); err != nil {
			t.Fatalf("decode response frame %q: %v", got.GetEnvelopeJson(), err)
		}
		if resp.Type == "response" {
			return resp
		}
	}
	t.Fatal("the echo-concurrent runtime produced no response within the deadline")
	return slotResponse{}
}

// buildConcurrentRuntime compiles the real cmd/runtimes/echo-concurrent
// binary into a temp path. It is the reference runtime that implements the
// slotId dispatch loop, so the flow exercises the production multiplexing
// path rather than a fake.
func buildConcurrentRuntime(t *testing.T) string {
	t.Helper()
	root := schematest.RepoRoot(t)
	bin := filepath.Join(t.TempDir(), "echo-concurrent")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/runtimes/echo-concurrent")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build echo-concurrent: %v", err)
	}
	return bin
}

// concurrentSocketAddr returns the abstract Unix socket the adapter binds
// for the pod's runtime. On Linux it is an abstract address (no filesystem
// cleanup); elsewhere it is a short filesystem path, since the Unix sun_path
// field is limited to ~104 bytes and t.TempDir() alone can exceed that.
func concurrentSocketAddr(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" {
		return "@lenny-tier4-cworkspace-" + sanitizeName(t.Name())
	}
	f, err := os.CreateTemp("", "cws-*.sock")
	if err != nil {
		t.Fatalf("temp socket path: %v", err)
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

// sanitizeName makes a test name usable as an abstract socket suffix by
// replacing the path separators Go inserts for subtests.
func sanitizeName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '/' || r == ' ' {
			out = append(out, '-')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// concurrentAdapterClient boots the adapter's real gRPC server over an
// in-memory bufconn listener and returns a connected Adapter client. This is
// the exact contract the gateway's adapterclient speaks, so driving the flow
// through it exercises the gateway->adapter wire path without a Kubernetes
// cluster.
func concurrentAdapterClient(t *testing.T, s *adapter.Server) adapterv1.AdapterClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial adapter bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adapterv1.NewAdapterClient(conn)
}
