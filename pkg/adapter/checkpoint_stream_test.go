// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// recordingTransport captures each checkpoint PUT so the tests can assert
// the adapter replayed the signed headers and uploaded the archive bytes.
// rejectStatus/rejectCode model an object-store rejection.
type recordingTransport struct {
	mu           sync.Mutex
	puts         []recordedPut
	rejectStatus int
	rejectCode   string
}

type recordedPut struct {
	url           string
	headers       map[string]string
	contentLength int64
	body          []byte
}

func (r *recordingTransport) PutChunk(_ context.Context, url string, headers map[string]string, contentLength int64, body io.Reader) (int, string, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return 0, "", err
	}
	hcopy := make(map[string]string, len(headers))
	for k, v := range headers {
		hcopy[k] = v
	}
	r.mu.Lock()
	r.puts = append(r.puts, recordedPut{url: url, headers: hcopy, contentLength: contentLength, body: b})
	rejectStatus, rejectCode := r.rejectStatus, r.rejectCode
	r.mu.Unlock()
	if rejectStatus != 0 {
		return rejectStatus, rejectCode, nil
	}
	return 200, "", nil
}

func (r *recordingTransport) GetChunk(context.Context, string, map[string]string) (io.ReadCloser, error) {
	return nil, errors.New("recordingTransport does not serve GET")
}

func (r *recordingTransport) allBodies() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []byte
	for _, p := range r.puts {
		out = append(out, p.body...)
	}
	return out
}

// checkpointServer builds an adapter Server with a seeded workspace and
// the given transport, served over bufconn, and returns a connected
// Checkpoint stream client.
func checkpointServer(t *testing.T, transport adapter.CheckpointTransport, files map[string]string, limit int64) adapterv1.AdapterClient {
	t.Helper()
	s := adapter.New("checkpoint-stream")
	s.WorkspaceBase = t.TempDir()
	s.CheckpointTransport = transport
	s.WorkspaceSizeLimitBytes = limit
	s.Runtime = &fakeRuntime{}
	if _, err := s.StartSession(context.Background(), startReq(streamSession)); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	current := filepath.Join(s.WorkspaceBase, "slots", streamSession, "current")
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(current, name), []byte(content), 0o644); err != nil {
			t.Fatalf("seed workspace file: %v", err)
		}
	}
	client, _ := adapterClient(t, s)
	return client
}

// streamSession is the session the Checkpoint stream cases address. Every
// session is bound to a slot on every pod, so the opening frame names it
// and the adapter resolves that session's own tree from it. spec: §5.2.
const streamSession = "sess-stream"

// driveCheckpoint runs the gateway side of the Checkpoint stream: it sends
// the Start, answers each ChunkReady with a Grant carrying the signed
// header set, and returns the terminal Summary or Failed frame.
func driveCheckpoint(t *testing.T, stream adapterv1.Adapter_CheckpointClient, signedHeaders map[string]string) (*adapterv1.CheckpointSummary, *adapterv1.CheckpointFailed, *adapterv1.CheckpointProbe) {
	t.Helper()
	var probe *adapterv1.CheckpointProbe
	for {
		msg, err := stream.Recv()
		if err != nil {
			t.Fatalf("stream.Recv: %v", err)
		}
		switch m := msg.GetMsg().(type) {
		case *adapterv1.CheckpointResponse_Probe:
			probe = m.Probe
		case *adapterv1.CheckpointResponse_ChunkReady:
			grant := &adapterv1.CheckpointGrant{
				Index:         m.ChunkReady.GetIndex(),
				Url:           "https://objectstore.example/chunk",
				ContentLength: m.ChunkReady.GetLength(),
				Headers:       signedHeaders,
			}
			if err := stream.Send(&adapterv1.CheckpointRequest{
				Msg: &adapterv1.CheckpointRequest_Grant{Grant: grant},
			}); err != nil {
				t.Fatalf("send grant: %v", err)
			}
		case *adapterv1.CheckpointResponse_ChunkCommitted:
			// ack observed; continue
		case *adapterv1.CheckpointResponse_Summary:
			return m.Summary, nil, probe
		case *adapterv1.CheckpointResponse_Failed:
			return nil, m.Failed, probe
		}
	}
}

// lcFrame mirrors the fields of the §4.7 lifecycle JSONL frames the
// external test needs to observe: the capability handshake, the
// checkpoint_request/checkpoint_ready quiesce round-trip, and the terminal
// checkpoint_complete carrying the checkpoint disposition.
type lcFrame struct {
	Type         string   `json:"type"`
	Capabilities []string `json:"capabilities,omitempty"`
	CheckpointID string   `json:"checkpointId,omitempty"`
	Status       string   `json:"status,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

// fakeLifecycleRuntime plays the agent-runtime end of the §4.7 CH-RUNTIMEOPS over a raw Unix socket so the checkpoint stream tests can drive
// the cooperative quiesce handshake and read the checkpoint_complete frame.
type fakeLifecycleRuntime struct {
	t    *testing.T
	conn net.Conn
	dec  *json.Decoder
	enc  *json.Encoder
}

func (fr *fakeLifecycleRuntime) read() lcFrame {
	fr.t.Helper()
	var f lcFrame
	if err := fr.dec.Decode(&f); err != nil {
		fr.t.Fatalf("fakeLifecycleRuntime read: %v", err)
	}
	return f
}

func (fr *fakeLifecycleRuntime) write(f lcFrame) {
	fr.t.Helper()
	if err := fr.enc.Encode(f); err != nil {
		fr.t.Fatalf("fakeLifecycleRuntime write: %v", err)
	}
}

// wireLifecycle attaches a running RuntimeOps to s, dials it as a fake
// Full-level runtime, and completes the capability handshake so the adapter's
// Checkpoint stream runs the cooperative quiesce path.
func wireLifecycle(t *testing.T, s *adapter.Server) *fakeLifecycleRuntime {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-lc-*")
	if err != nil {
		t.Fatalf("temp lifecycle dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "lc.sock")

	lc, err := adapter.NewRuntimeOps(sock)
	if err != nil {
		t.Fatalf("NewRuntimeOps: %v", err)
	}
	s.Lifecycle = lc
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- lc.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = lc.Close()
		<-runErr
	})

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial lifecycle socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	fr := &fakeLifecycleRuntime{t: t, conn: conn, dec: json.NewDecoder(conn), enc: json.NewEncoder(conn)}
	fr.enc.SetEscapeHTML(false)

	caps := fr.read()
	if caps.Type != "lifecycle_capabilities" {
		t.Fatalf("first lifecycle frame = %q, want lifecycle_capabilities", caps.Type)
	}
	fr.write(lcFrame{Type: "lifecycle_support", Capabilities: caps.Capabilities})
	return fr
}

// answerQuiesceAndReadComplete answers the adapter's checkpoint_request with
// checkpoint_ready and returns the terminal checkpoint_complete frame the
// adapter emits after the stream ends. It runs on the runtime side of the
// lifecycle socket while the caller drives the gRPC Checkpoint stream.
func answerQuiesceAndReadComplete(fr *fakeLifecycleRuntime) <-chan lcFrame {
	out := make(chan lcFrame, 1)
	go func() {
		req := fr.read()
		if req.Type == "checkpoint_request" {
			fr.write(lcFrame{Type: "checkpoint_ready", CheckpointID: req.CheckpointID})
		}
		out <- fr.read()
	}()
	return out
}

// slotCheckpointServer extends a concurrentServer with the checkpoint
// transport the Checkpoint handler requires, which concurrentServer omits.
// It returns the server so callers bind sessions via StartSession before
// serving.
func slotCheckpointServer(t *testing.T, transport adapter.CheckpointTransport) *adapter.Server {
	t.Helper()
	s, _ := concurrentServer(t)
	s.CheckpointTransport = transport
	return s
}

// seedFile writes content at path, creating parent directories as needed.
func seedFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed file %q: %v", path, err)
	}
}

// spec: §5.2 (per-slot checkpoint granularity), §6.4 —
// a CheckpointStart carrying a slot_id captures only that slot's live
// workspace and session subtree, not the pod-global base tree. The
// non-happy path this pins is a concurrent-session pod whose checkpoint
// archives the empty pod-global /workspace/current base tree and no slot's
// live workspace, which the pre-fix pod-global checkpointRoots() did.
func TestCheckpointStreamCapturesSlotSubtree_spec_5_2(t *testing.T) {
	transport := &recordingTransport{}
	s := slotCheckpointServer(t, transport)
	ctx := context.Background()
	if _, err := s.StartSession(ctx, slotStartReq("sess-a")); err != nil {
		t.Fatalf("StartSession(slot-a): %v", err)
	}
	seedFile(t, filepath.Join(s.WorkspaceBase, "slots", "sess-a", "current", "notes.txt"), "slot-a workspace state")
	seedFile(t, filepath.Join(s.SessionsRoot, "sess-a", "history.jsonl"), "slot-a conversation")
	// A decoy at the workspace base, outside every session's own tree,
	// must never be captured by a session's checkpoint.
	seedFile(t, filepath.Join(s.WorkspaceBase, "decoy.txt"), "out-of-tree decoy")

	client, _ := adapterClient(t, s)
	stream, err := client.Checkpoint(ctx)
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
			CheckpointId:   "gw-ckpt-sess-a",
			SessionId:      &adapterv1.SessionId{Value: "sess-a"},
			Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
			ChunkSizeBytes: 1 << 20,
		}},
	}); err != nil {
		t.Fatalf("send start: %v", err)
	}
	summary, failed, probe := driveCheckpoint(t, stream, nil)
	if failed != nil {
		t.Fatalf("per-slot checkpoint failed unexpectedly: %+v", failed)
	}
	if summary == nil || probe == nil || probe.GetWorkspaceBytes() <= 0 {
		t.Fatalf("expected a positive probe and Summary, got probe=%+v summary=%+v", probe, summary)
	}

	// Reassemble the bundle and confirm it carries the slot's workspace and
	// session subtree under both prefixes and omits the pod-global decoy.
	wsOut, sessOut := t.TempDir(), t.TempDir()
	if _, err := workspace.ExtractTree(
		[]workspace.NamedRoot{
			{Prefix: workspace.WorkspacePrefix, Root: wsOut},
			{Prefix: workspace.SessionsPrefix, Root: sessOut},
		},
		bytes.NewReader(transport.allBodies()),
	); err != nil {
		t.Fatalf("extract per-slot bundle: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(wsOut, "notes.txt")); err != nil || string(got) != "slot-a workspace state" {
		t.Fatalf("slot workspace file = %q (err %v), want the slot-a workspace content", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(sessOut, "history.jsonl")); err != nil || string(got) != "slot-a conversation" {
		t.Fatalf("slot session file = %q (err %v), want the slot-a session content", got, err)
	}
	if _, err := os.Stat(filepath.Join(wsOut, "decoy.txt")); !os.IsNotExist(err) {
		t.Errorf("pod-global decoy captured by a per-slot checkpoint; the archive is not slot-scoped")
	}
}

// spec: §5.2 (per-slot addressing), §4.4 (durability contract) — a
// CheckpointStart naming a slot with no bound session is rejected with
// FailedPrecondition before any grant is minted, so a checkpoint never
// archives an empty or nonexistent subtree for an unassigned slot. Two arms
// are pinned: a slot that was never assigned (no registry entry) and a slot
// whose registry entry exists with an empty sessionID (materialized by a
// pre-StartSession FinalizeWorkspace but idle). The non-happy path is a
// checkpoint minting grants and uploading for a slot that holds no session.
func TestCheckpointStreamRejectsUnassignedSlot_spec_5_2(t *testing.T) {
	transport := &recordingTransport{}
	s := slotCheckpointServer(t, transport)
	ctx := context.Background()

	// Assigned-but-idle arm: FinalizeWorkspace creates the slot registry
	// entry and its on-disk tree ahead of StartSession, so the slot exists
	// with an empty sessionID.
	if _, err := s.FinalizeWorkspace(ctx, &adapterv1.FinalizeWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-idle"},
		WorkspacePlan: &adapterv1.WorkspacePlan{
			SchemaVersion: 1,
			Sources: []*adapterv1.WorkspaceSource{
				{Type: "inlineFile", Path: "seed.txt", Content: "x", Mode: "0644"},
			},
		},
	}); err != nil {
		t.Fatalf("FinalizeWorkspace(slot-idle): %v", err)
	}

	client, _ := adapterClient(t, s)
	for _, slot := range []string{"sess-unknown", "sess-idle"} {
		stream, err := client.Checkpoint(ctx)
		if err != nil {
			t.Fatalf("open Checkpoint stream for %s: %v", slot, err)
		}
		if err := stream.Send(&adapterv1.CheckpointRequest{
			Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
				CheckpointId:   "gw-ckpt-" + slot,
				SessionId:      &adapterv1.SessionId{Value: slot},
				Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
				ChunkSizeBytes: 1 << 20,
			}},
		}); err != nil {
			t.Fatalf("send start for %s: %v", slot, err)
		}
		if _, rerr := stream.Recv(); status.Code(rerr) != codes.FailedPrecondition {
			t.Fatalf("code for %s = %v, want FailedPrecondition for a slot with no bound session", slot, status.Code(rerr))
		}
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.puts) != 0 {
		t.Fatalf("a grant was used for an unassigned slot: %d PUTs", len(transport.puts))
	}
}

// driveCheckpointConc runs the gateway side of a Checkpoint stream from a
// goroutine, answering each ChunkReady with a grant carrying the given URL
// so a shared transport can group PUTs per slot. It returns errors rather
// than calling t.Fatal, which is unsafe off the test goroutine. An op-lock
// abort (a coalesced slot in the pre-fix lock) surfaces here as a
// codes.Aborted error from Recv.
func driveCheckpointConc(stream adapterv1.Adapter_CheckpointClient, url string) (*adapterv1.CheckpointSummary, *adapterv1.CheckpointFailed, error) {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return nil, nil, err
		}
		switch m := msg.GetMsg().(type) {
		case *adapterv1.CheckpointResponse_ChunkReady:
			grant := &adapterv1.CheckpointGrant{
				Index:         m.ChunkReady.GetIndex(),
				Url:           url,
				ContentLength: m.ChunkReady.GetLength(),
			}
			if err := stream.Send(&adapterv1.CheckpointRequest{
				Msg: &adapterv1.CheckpointRequest_Grant{Grant: grant},
			}); err != nil {
				return nil, nil, err
			}
		case *adapterv1.CheckpointResponse_Summary:
			return m.Summary, nil, nil
		case *adapterv1.CheckpointResponse_Failed:
			return nil, m.Failed, nil
		}
	}
}

// spec: §5.2 (one session's checkpoint upload at a time, in the
// lexicographic tie-break over session identifiers) — three or more
// per-session checkpoints opened concurrently on one pod are each admitted
// and each captures its own session's subtree; none is coalesced away. The
// non-happy path is the pre-fix depth-one op lock coalescing the third and
// later sessions into the queued one, returning codes.Aborted and
// terminating those sessions uncheckpointed.
func TestCheckpointStreamConcurrentPerSlotAllCaptured_spec_5_2(t *testing.T) {
	transport := &recordingTransport{}
	s := slotCheckpointServer(t, transport)
	ctx := context.Background()
	slots := []string{"sess-1", "sess-2", "sess-3"}
	for _, slot := range slots {
		if _, err := s.StartSession(ctx, slotStartReq(slot)); err != nil {
			t.Fatalf("StartSession(%s): %v", slot, err)
		}
		seedFile(t, filepath.Join(s.WorkspaceBase, "slots", slot, "current", slot+".txt"), "content-"+slot)
	}
	client, _ := adapterClient(t, s)

	type result struct {
		slot    string
		summary *adapterv1.CheckpointSummary
		failed  *adapterv1.CheckpointFailed
		err     error
	}
	results := make([]result, len(slots))
	var wg sync.WaitGroup
	for i, slot := range slots {
		wg.Add(1)
		go func(i int, slot string) {
			defer wg.Done()
			stream, err := client.Checkpoint(ctx)
			if err != nil {
				results[i] = result{slot: slot, err: err}
				return
			}
			if err := stream.Send(&adapterv1.CheckpointRequest{
				Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
					CheckpointId:   "gw-ckpt-" + slot,
					SessionId:      &adapterv1.SessionId{Value: slot},
					Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
					ChunkSizeBytes: 1 << 20,
				}},
			}); err != nil {
				results[i] = result{slot: slot, err: err}
				return
			}
			summary, failed, err := driveCheckpointConc(stream, "https://objectstore.example/"+slot)
			results[i] = result{slot: slot, summary: summary, failed: failed, err: err}
		}(i, slot)
	}
	wg.Wait()

	// Every slot's checkpoint completed with a Summary: none coalesced into
	// codes.Aborted and none dropped.
	for _, r := range results {
		if r.err != nil {
			t.Fatalf("%s checkpoint errored (a coalesced op lock returns codes.Aborted=%v): %v", r.slot, status.Code(r.err), r.err)
		}
		if r.failed != nil {
			t.Fatalf("%s checkpoint failed: %+v", r.slot, r.failed)
		}
		if r.summary == nil {
			t.Fatalf("%s checkpoint produced no Summary", r.slot)
		}
	}

	// Each slot's uploaded bytes reassemble into that slot's subtree alone.
	transport.mu.Lock()
	byURL := map[string][]byte{}
	for _, p := range transport.puts {
		byURL[p.url] = append(byURL[p.url], p.body...)
	}
	transport.mu.Unlock()
	for _, slot := range slots {
		body, ok := byURL["https://objectstore.example/"+slot]
		if !ok {
			t.Fatalf("%s uploaded no chunk", slot)
		}
		out := t.TempDir()
		if _, err := workspace.ExtractTree(
			[]workspace.NamedRoot{{Prefix: workspace.WorkspacePrefix, Root: out}},
			bytes.NewReader(body),
		); err != nil {
			t.Fatalf("extract %s bundle: %v", slot, err)
		}
		got, err := os.ReadFile(filepath.Join(out, slot+".txt"))
		if err != nil || string(got) != "content-"+slot {
			t.Fatalf("%s workspace file = %q (err %v), want content-%s", slot, got, err, slot)
		}
		// No sibling slot's file leaked into this slot's checkpoint.
		for _, other := range slots {
			if other == slot {
				continue
			}
			if _, err := os.Stat(filepath.Join(out, other+".txt")); !os.IsNotExist(err) {
				t.Errorf("%s checkpoint carried %s's file; per-slot capture is not isolated", slot, other)
			}
		}
	}
}

// spec: §4.4, §15.4.3 — a Full-level runtime quiesces cooperatively
// across the chunked stream, and the adapter's checkpoint_complete frame
// carries status "failed" with a reason when a chunk PUT is rejected by the
// object store. A pre-fix adapter collapsed the terminal Failed frame to a
// nil return and told the quiesced runtime status "ok", resuming it as if
// the checkpoint had succeeded.
func TestCheckpointStreamFullLevelReportsFailedComplete_spec_4_4_241(t *testing.T) {
	transport := &recordingTransport{rejectStatus: 403, rejectCode: "SignatureDoesNotMatch"}
	s := adapter.New("ckpt-full-fail")
	s.WorkspaceBase = t.TempDir()
	s.CheckpointTransport = transport
	s.Runtime = &fakeRuntime{}
	if _, err := s.StartSession(context.Background(), startReq(streamSession)); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	current := filepath.Join(s.WorkspaceBase, "slots", streamSession, "current")
	if err := os.WriteFile(filepath.Join(current, "notes.txt"), []byte("state"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	fr := wireLifecycle(t, s)
	client, _ := adapterClient(t, s)
	completeCh := answerQuiesceAndReadComplete(fr)

	stream, err := client.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
			CheckpointId:   "gw-ckpt-full-fail",
			SessionId:      &adapterv1.SessionId{Value: streamSession},
			Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
			ChunkSizeBytes: 1 << 20,
		}},
	}); err != nil {
		t.Fatalf("send start: %v", err)
	}
	_, failed, _ := driveCheckpoint(t, stream, nil)
	if failed == nil {
		t.Fatal("expected a CheckpointFailed frame on the object-store rejection")
	}

	done := <-completeCh
	if done.Type != "checkpoint_complete" {
		t.Fatalf("lifecycle frame = %q, want checkpoint_complete", done.Type)
	}
	if done.Status != "failed" {
		t.Fatalf("checkpoint_complete status = %q, want failed (the quiesced runtime must not be told a rejected checkpoint succeeded)", done.Status)
	}
	if done.Reason == "" {
		t.Fatal("checkpoint_complete on a failed checkpoint must carry a reason")
	}
}

// spec: §4.4, §15.4.3 — on the success path the adapter emits
// checkpoint_complete with status "ok" only after the Summary frame, so the
// quiesced Full-level runtime resumes on a checkpoint that was actually
// stored.
func TestCheckpointStreamFullLevelReportsOkComplete_spec_4_4_241(t *testing.T) {
	transport := &recordingTransport{}
	s := adapter.New("ckpt-full-ok")
	s.WorkspaceBase = t.TempDir()
	s.CheckpointTransport = transport
	s.Runtime = &fakeRuntime{}
	if _, err := s.StartSession(context.Background(), startReq(streamSession)); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	current := filepath.Join(s.WorkspaceBase, "slots", streamSession, "current")
	if err := os.WriteFile(filepath.Join(current, "notes.txt"), []byte("state"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	fr := wireLifecycle(t, s)
	client, _ := adapterClient(t, s)
	completeCh := answerQuiesceAndReadComplete(fr)

	stream, err := client.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
			CheckpointId:   "gw-ckpt-full-ok",
			SessionId:      &adapterv1.SessionId{Value: streamSession},
			Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
			ChunkSizeBytes: 1 << 20,
		}},
	}); err != nil {
		t.Fatalf("send start: %v", err)
	}
	summary, failed, _ := driveCheckpoint(t, stream, nil)
	if failed != nil {
		t.Fatalf("checkpoint failed unexpectedly: %+v", failed)
	}
	if summary == nil {
		t.Fatal("expected a Summary on the success path")
	}

	done := <-completeCh
	if done.Type != "checkpoint_complete" {
		t.Fatalf("lifecycle frame = %q, want checkpoint_complete", done.Type)
	}
	if done.Status != "ok" {
		t.Fatalf("checkpoint_complete status = %q, want ok", done.Status)
	}
}

// spec: §4.4 — the workspace-size probe runs before the quiescence
// handshake, so an over-limit workspace aborts with FailedPrecondition
// without ever quiescing the Full-level runtime. A pre-fix adapter issued
// checkpoint_request (pausing the agent) before the size check, then resumed
// it via the aborted checkpoint's complete frame: exactly the needless pause
// the probe-before-quiesce rule prevents.
func TestCheckpointStreamProbeBeforeQuiesce_spec_4_4_255(t *testing.T) {
	transport := &recordingTransport{}
	s := adapter.New("ckpt-probe-order")
	s.WorkspaceRoot = t.TempDir()
	s.StagingDir = t.TempDir()
	s.CheckpointTransport = transport
	s.WorkspaceSizeLimitBytes = 1 // any real workspace file is over-limit
	if err := os.WriteFile(filepath.Join(s.WorkspaceRoot, "big.txt"), []byte("this workspace is over the one-byte limit"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	fr := wireLifecycle(t, s)
	client, _ := adapterClient(t, s)

	// Watch the lifecycle socket: no checkpoint_request must arrive, because
	// the size-limit abort precedes the quiescence handshake.
	sawFrame := make(chan string, 1)
	go func() {
		_ = fr.conn.SetReadDeadline(time.Now().Add(750 * time.Millisecond))
		var f lcFrame
		if err := fr.dec.Decode(&f); err != nil {
			sawFrame <- ""
			return
		}
		sawFrame <- f.Type
	}()

	stream, err := client.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
			CheckpointId:   "gw-ckpt-oversize",
			SessionId:      &adapterv1.SessionId{Value: streamSession},
			Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
			ChunkSizeBytes: 1 << 20,
		}},
	}); err != nil {
		t.Fatalf("send start: %v", err)
	}
	if _, rerr := stream.Recv(); status.Code(rerr) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition for an oversize workspace", status.Code(rerr))
	}
	if typ := <-sawFrame; typ == "checkpoint_request" {
		t.Fatal("adapter quiesced the runtime before the size-limit abort; the probe must run before the quiescence handshake")
	}
}

// spec: §4.4 / §10.1 — the adapter probes the workspace, uploads the
// checkpoint bundle chunk by chunk against the gateway's presigned grants
// replaying every signed header verbatim, and closes with a Summary whose
// byte total matches the uploaded bytes.
func TestCheckpointStreamUploadsChunksAndSummarizes(t *testing.T) {
	transport := &recordingTransport{}
	client := checkpointServer(t, transport, map[string]string{"notes.txt": "agent workspace state"}, 0)

	stream, err := client.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
			CheckpointId:   "gw-ckpt-1",
			SessionId:      &adapterv1.SessionId{Value: streamSession},
			Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
			ChunkSizeBytes: 1 << 20,
			ChunkEncoding:  "tar.gz",
		}},
	}); err != nil {
		t.Fatalf("send start: %v", err)
	}

	signed := map[string]string{"x-amz-server-side-encryption": "aws:kms"}
	summary, failed, probe := driveCheckpoint(t, stream, signed)
	if failed != nil {
		t.Fatalf("checkpoint failed unexpectedly: %+v", failed)
	}
	if probe == nil || probe.GetWorkspaceBytes() <= 0 {
		t.Fatalf("expected a positive workspace probe, got %+v", probe)
	}
	if summary.GetChunkCount() == 0 {
		t.Fatal("summary chunk_count = 0, want at least one chunk")
	}

	// Every signed header was replayed verbatim on the PUT.
	transport.mu.Lock()
	if len(transport.puts) == 0 {
		transport.mu.Unlock()
		t.Fatal("no chunk was PUT to the object store")
	}
	if got := transport.puts[0].headers["x-amz-server-side-encryption"]; got != "aws:kms" {
		transport.mu.Unlock()
		t.Fatalf("signed header not replayed: got %q, want aws:kms", got)
	}
	transport.mu.Unlock()

	// The concatenated PUT bodies are the checkpoint bundle: extract them
	// and confirm the workspace file round-trips.
	out := t.TempDir()
	restored, err := workspace.ExtractTree(
		[]workspace.NamedRoot{{Prefix: workspace.WorkspacePrefix, Root: out}},
		bytes.NewReader(transport.allBodies()),
	)
	if err != nil {
		t.Fatalf("extract uploaded bundle: %v", err)
	}
	if summary.GetTotalBytes() != int64(len(transport.allBodies())) {
		t.Errorf("summary total_bytes = %d, want the uploaded byte count %d",
			summary.GetTotalBytes(), len(transport.allBodies()))
	}
	got, err := os.ReadFile(filepath.Join(out, "notes.txt"))
	if err != nil || string(got) != "agent workspace state" {
		t.Fatalf("uploaded workspace file = %q (err %v), want the checkpoint content; restored %d bytes", got, err, restored)
	}
}

// spec: §4.4 — a workspace over the hard size limit aborts the
// checkpoint with FailedPrecondition before any grant is minted. A pre-fix
// adapter with no probe would upload the oversized workspace anyway.
func TestCheckpointStreamRejectsOversizeWorkspace_spec_4_4_255(t *testing.T) {
	transport := &recordingTransport{}
	client := checkpointServer(t, transport,
		map[string]string{"big.txt": "this workspace is over the one-byte limit"}, 1)

	stream, err := client.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
			CheckpointId:   "gw-ckpt-2",
			SessionId:      &adapterv1.SessionId{Value: streamSession},
			Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
			ChunkSizeBytes: 1 << 20,
		}},
	}); err != nil {
		t.Fatalf("send start: %v", err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition for an oversize workspace", status.Code(err))
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.puts) != 0 {
		t.Fatalf("a grant was used despite the size-limit rejection: %d PUTs", len(transport.puts))
	}
}

// spec: §4.4 — a chunk PUT the object store rejects terminates the stream
// with a CheckpointFailed frame carrying the object store's HTTP status
// and error code, so the gateway can map a kms:-coded rejection onto a
// classification-control violation.
func TestCheckpointStreamReportsObjectStoreRejection_spec_4_4(t *testing.T) {
	transport := &recordingTransport{rejectStatus: 403, rejectCode: "SignatureDoesNotMatch"}
	client := checkpointServer(t, transport, map[string]string{"notes.txt": "state"}, 0)

	stream, err := client.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
			CheckpointId:   "gw-ckpt-3",
			SessionId:      &adapterv1.SessionId{Value: streamSession},
			Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
			ChunkSizeBytes: 1 << 20,
		}},
	}); err != nil {
		t.Fatalf("send start: %v", err)
	}
	summary, failed, _ := driveCheckpoint(t, stream, nil)
	if summary != nil {
		t.Fatalf("expected a CheckpointFailed frame, got summary %+v", summary)
	}
	if failed == nil {
		t.Fatal("expected a CheckpointFailed frame on the object-store rejection")
	}
	if failed.GetHttpStatus() != 403 {
		t.Errorf("failed http_status = %d, want 403", failed.GetHttpStatus())
	}
	if failed.GetErrorCode() != "SignatureDoesNotMatch" {
		t.Errorf("failed error_code = %q, want SignatureDoesNotMatch", failed.GetErrorCode())
	}
}

// spec: §4.1 (Checkpoint is session-scoped), §4.2 (an empty per-session
// address is InvalidArgument), §6.4 (the per-slot tree is the only layout)
//
// The opening frame's session identifier is the only address the
// Checkpoint stream carries, so a stream that opens without one, or with
// one that is not a safe path segment, is a malformed request rather than
// a session the pod does not hold. A handler that skipped the boundary
// check answered an unaddressed stream with FailedPrecondition, which
// tells the gateway the pod lost the session, and it took the pod-level
// op lock under the unvalidated address on the way there.
func TestCheckpointStreamRefusesAnUnaddressedStart_spec_4_2(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session *adapterv1.SessionId
	}{
		{name: "no session id field", session: nil},
		{name: "empty session id", session: &adapterv1.SessionId{Value: ""}},
		{name: "path-traversal session id", session: &adapterv1.SessionId{Value: "../escape"}},
		{name: "separator in session id", session: &adapterv1.SessionId{Value: "sess/../other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := checkpointServer(t, &recordingTransport{}, map[string]string{"notes.txt": "state"}, 0)
			stream, err := client.Checkpoint(context.Background())
			if err != nil {
				t.Fatalf("open Checkpoint stream: %v", err)
			}
			if err := stream.Send(&adapterv1.CheckpointRequest{
				Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
					CheckpointId:   "gw-ckpt-unaddressed",
					SessionId:      tc.session,
					Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
					ChunkSizeBytes: 1 << 20,
				}},
			}); err != nil {
				t.Fatalf("send start: %v", err)
			}
			if _, err := stream.Recv(); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("Checkpoint with %s: code = %v, want InvalidArgument", tc.name, status.Code(err))
			}
		})
	}
}

// spec: §4.7 (one pending checkpoint per distinct session identifier),
// §10.1 (the gateway finalises the manifest row partial on an aborted
// attempt) —
// a Checkpoint whose session identifier already has a checkpoint pending
// on the pod operation lock is refused, and the refusal reaches the
// gateway as codes.Aborted, the same status a busy lock returns. The
// non-happy path is an adapter that reports a coalesced checkpoint as a
// successful no-op, which would tell the gateway an attempt it never
// captured had completed.
func TestCheckpointStreamCoalescedAttemptIsAborted_spec_4_7(t *testing.T) {
	s := slotCheckpointServer(t, &recordingTransport{})
	ctx := context.Background()
	for _, session := range []string{"3b7d19f4-carol", "9c02ae55-alice"} {
		if _, err := s.StartSession(ctx, slotStartReq(session)); err != nil {
			t.Fatalf("StartSession(%s): %v", session, err)
		}
	}
	client, _ := adapterClient(t, s)

	// One checkpoint runs and a second waits, so the pending set holds
	// exactly the session the coalescing attempt addresses.
	release, err := s.BeginCheckpointOpForTest(ctx, "3b7d19f4-carol")
	if err != nil {
		t.Fatalf("hold the running op: %v", err)
	}
	defer release()
	pendingCtx, cancelPending := context.WithCancel(ctx)
	defer cancelPending()
	go func() {
		rel, berr := s.BeginCheckpointOpForTest(pendingCtx, "9c02ae55-alice")
		if berr == nil {
			<-pendingCtx.Done()
			rel()
		}
	}()
	if !s.WaitPendingCheckpointForTest("9c02ae55-alice", 2*time.Second) {
		t.Fatal("the queued checkpoint did not enter the pending set")
	}

	stream, err := client.Checkpoint(ctx)
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
			CheckpointId:   "gw-ckpt-coalesced",
			SessionId:      &adapterv1.SessionId{Value: "9c02ae55-alice"},
			Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
			ChunkSizeBytes: 1 << 20,
		}},
	}); err != nil {
		t.Fatalf("send start: %v", err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.Aborted {
		t.Fatalf("coalesced checkpoint: code = %v (err %v), want Aborted", status.Code(err), err)
	}
	// The refusal is the coalescing branch rather than some other abort.
	if !strings.Contains(err.Error(), "coalesced") {
		t.Fatalf("coalesced checkpoint error = %v, want the op lock's coalescing refusal", err)
	}
}
