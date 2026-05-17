// SPDX-License-Identifier: MIT

package adapter_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §4.4 / §4.7 — the adapter Checkpoint RPC snapshots the session
// workspace and stores it in the artifact store.

// fakeCheckpointSink records what SaveCheckpoint received.
type fakeCheckpointSink struct {
	id         string
	err        error
	gotSession string
	received   []byte
}

func (f *fakeCheckpointSink) SaveCheckpoint(_ context.Context, sessionID string, r io.Reader) (string, error) {
	f.gotSession = sessionID
	b, readErr := io.ReadAll(r) // drain to EOF before reporting any error
	f.received = b
	if readErr != nil {
		return "", readErr
	}
	if f.err != nil {
		return "", f.err
	}
	return f.id, nil
}

func checkpointReq(sessionID string) *adapterv1.CheckpointRequest {
	return &adapterv1.CheckpointRequest{SessionId: &adapterv1.SessionId{Value: sessionID}}
}

// startedServer returns an adapter Server with an assigned session and
// one file written into its workspace.
func startedServer(t *testing.T) (*adapter.Server, string) {
	t.Helper()
	s, _, root := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("agent state"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	return s, root
}

func TestCheckpointArchivesAndStoresTheWorkspace(t *testing.T) {
	s, _ := startedServer(t)
	sink := &fakeCheckpointSink{id: "ckpt-1"}
	s.Checkpoints = sink

	resp, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if resp.GetCheckpointId() != "ckpt-1" {
		t.Errorf("checkpoint id = %q, want ckpt-1", resp.GetCheckpointId())
	}
	if resp.GetSizeBytes() != int64(len(sink.received)) {
		t.Errorf("size = %d, want the archived byte count %d", resp.GetSizeBytes(), len(sink.received))
	}
	if sink.gotSession != "sess-1" {
		t.Errorf("sink saw session %q, want sess-1", sink.gotSession)
	}

	// The stored archive is a valid gzip-tar carrying the workspace.
	if content := tarEntry(t, sink.received, "notes.txt"); content != "agent state" {
		t.Errorf("archived notes.txt = %q, want the workspace content", content)
	}
}

func TestCheckpointRejectsMissingSessionID(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "x"}
	_, err := s.Checkpoint(context.Background(), checkpointReq(""))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestCheckpointRejectsUnassignedSession(t *testing.T) {
	s, _, _ := sessionServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "x"}
	_, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition for a pod with no session", status.Code(err))
	}
}

func TestCheckpointRejectsWrongSession(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{id: "x"}
	_, err := s.Checkpoint(context.Background(), checkpointReq("other-session"))
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound for a session not assigned to the pod", status.Code(err))
	}
}

func TestCheckpointUnimplementedWithoutSink(t *testing.T) {
	s, _ := startedServer(t) // s.Checkpoints left nil
	_, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("code = %v, want Unimplemented when no checkpoint sink is configured", status.Code(err))
	}
}

func TestCheckpointSurfacesSinkError(t *testing.T) {
	s, _ := startedServer(t)
	s.Checkpoints = &fakeCheckpointSink{err: errors.New("minio unreachable")}
	_, err := s.Checkpoint(context.Background(), checkpointReq("sess-1"))
	if status.Code(err) != codes.Internal {
		t.Errorf("code = %v, want Internal when the checkpoint sink fails", status.Code(err))
	}
}

// tarEntry decodes a gzip-tar and returns the named entry's content.
func tarEntry(t *testing.T, data []byte, name string) string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			t.Fatalf("entry %q not found in the archive", name)
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Name == name {
			body, err := io.ReadAll(tr)
			if err != nil {
				t.Fatalf("read %q: %v", name, err)
			}
			return string(body)
		}
	}
}
