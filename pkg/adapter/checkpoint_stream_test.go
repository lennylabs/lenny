// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

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
		case *adapterv1.CheckpointServerMessage_Probe:
			probe = m.Probe
		case *adapterv1.CheckpointServerMessage_ChunkReady:
			grant := &adapterv1.CheckpointGrant{
				Index:         m.ChunkReady.GetIndex(),
				Url:           "https://objectstore.example/chunk",
				ContentLength: m.ChunkReady.GetLength(),
				Headers:       signedHeaders,
			}
			if err := stream.Send(&adapterv1.CheckpointClientMessage{
				Msg: &adapterv1.CheckpointClientMessage_Grant{Grant: grant},
			}); err != nil {
				t.Fatalf("send grant: %v", err)
			}
		case *adapterv1.CheckpointServerMessage_ChunkCommitted:
			// ack observed; continue
		case *adapterv1.CheckpointServerMessage_Summary:
			return m.Summary, nil, probe
		case *adapterv1.CheckpointServerMessage_Failed:
			return nil, m.Failed, probe
		}
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
	if err := stream.Send(&adapterv1.CheckpointClientMessage{
		Msg: &adapterv1.CheckpointClientMessage_Start{Start: &adapterv1.CheckpointStart{
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
	if err := stream.Send(&adapterv1.CheckpointClientMessage{
		Msg: &adapterv1.CheckpointClientMessage_Start{Start: &adapterv1.CheckpointStart{
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
	if err := stream.Send(&adapterv1.CheckpointClientMessage{
		Msg: &adapterv1.CheckpointClientMessage_Start{Start: &adapterv1.CheckpointStart{
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
