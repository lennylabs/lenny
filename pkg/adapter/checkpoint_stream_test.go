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
	s.WorkspaceRoot = t.TempDir()
	s.StagingDir = t.TempDir()
	s.CheckpointTransport = transport
	s.WorkspaceSizeLimitBytes = limit
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(s.WorkspaceRoot, name), []byte(content), 0o644); err != nil {
			t.Fatalf("seed workspace file: %v", err)
		}
	}
	client, _ := adapterClient(t, s)
	return client
}

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

// fakeLifecycleRuntime plays the agent-runtime end of the §4.7 lifecycle
// channel over a raw Unix socket so the checkpoint stream tests can drive
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

// wireLifecycle attaches a running LifecycleChannel to s, dials it as a fake
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

	lc, err := adapter.NewLifecycleChannel(sock)
	if err != nil {
		t.Fatalf("NewLifecycleChannel: %v", err)
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

// spec: §4.4 line 241, §15.4.3 — a Full-level runtime quiesces cooperatively
// across the chunked stream, and the adapter's checkpoint_complete frame
// carries status "failed" with a reason when a chunk PUT is rejected by the
// object store. A pre-fix adapter collapsed the terminal Failed frame to a
// nil return and told the quiesced runtime status "ok", resuming it as if
// the checkpoint had succeeded.
func TestCheckpointStreamFullLevelReportsFailedComplete_spec_4_4_241(t *testing.T) {
	transport := &recordingTransport{rejectStatus: 403, rejectCode: "SignatureDoesNotMatch"}
	s := adapter.New("ckpt-full-fail")
	s.WorkspaceRoot = t.TempDir()
	s.StagingDir = t.TempDir()
	s.CheckpointTransport = transport
	if err := os.WriteFile(filepath.Join(s.WorkspaceRoot, "notes.txt"), []byte("state"), 0o644); err != nil {
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

// spec: §4.4 line 241, §15.4.3 — on the success path the adapter emits
// checkpoint_complete with status "ok" only after the Summary frame, so the
// quiesced Full-level runtime resumes on a checkpoint that was actually
// stored.
func TestCheckpointStreamFullLevelReportsOkComplete_spec_4_4_241(t *testing.T) {
	transport := &recordingTransport{}
	s := adapter.New("ckpt-full-ok")
	s.WorkspaceRoot = t.TempDir()
	s.StagingDir = t.TempDir()
	s.CheckpointTransport = transport
	if err := os.WriteFile(filepath.Join(s.WorkspaceRoot, "notes.txt"), []byte("state"), 0o644); err != nil {
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

// spec: §4.4 line 255 — the workspace-size probe runs before the quiescence
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

// spec: §4.4 line 255 — a workspace over the hard size limit aborts the
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
