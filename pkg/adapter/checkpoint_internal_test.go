// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakeCheckpointStream is a minimal Adapter_CheckpointServer that feeds a
// single CheckpointStart and records the responses the handler sends. Context
// returns a context the test controls, so a checkpoint whose quiesce handshake
// fails with a non-connection error (a cancelled context) can be driven
// in-process without a gRPC round trip.
type fakeCheckpointStream struct {
	ctx  context.Context
	sent []*adapterv1.CheckpointResponse
	recv []*adapterv1.CheckpointRequest
	idx  int
}

func (f *fakeCheckpointStream) Recv() (*adapterv1.CheckpointRequest, error) {
	if f.idx >= len(f.recv) {
		return nil, io.EOF
	}
	req := f.recv[f.idx]
	f.idx++
	return req, nil
}

func (f *fakeCheckpointStream) Send(resp *adapterv1.CheckpointResponse) error {
	f.sent = append(f.sent, resp)
	return nil
}

func (f *fakeCheckpointStream) Context() context.Context     { return f.ctx }
func (f *fakeCheckpointStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeCheckpointStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeCheckpointStream) SetTrailer(metadata.MD)       {}
func (f *fakeCheckpointStream) SendMsg(any) error            { return nil }
func (f *fakeCheckpointStream) RecvMsg(any) error            { return io.EOF }

// stubCheckpointTransport is a non-nil CheckpointTransport the fail-closed
// path never calls; it exists so the handler passes its transport guard.
type stubCheckpointTransport struct{}

func (stubCheckpointTransport) PutChunk(context.Context, string, map[string]string, int64, io.Reader) (int, string, error) {
	return 0, "", fmt.Errorf("stub transport must not be called on the fail-closed path")
}

func (stubCheckpointTransport) GetChunk(context.Context, string, map[string]string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("stub transport does not serve GET")
}

// TestEvictionSnapshotFailsClosedOnNonDroppedHandshake_spec_4_4 pins the third
// deny cell: even on a pod that is itself terminating (the evicting flag is
// set), a quiesce handshake that fails for a reason other than a dropped
// connection (here a cancelled context) fails closed with codes.Internal and
// takes no best-effort snapshot, so no checkpoint chunk is streamed. The
// downgrade is scoped to a genuinely dropped runtime connection, classified by
// the lifecycle package's connection-state sentinels, rather than any handshake
// failure.
//
// spec: §4.4 (best-effort eviction snapshot), §4.6.1 (agent-pod disruption
// protection).
func TestEvictionSnapshotFailsClosedOnNonDroppedHandshake_spec_4_4(t *testing.T) {
	s := New("ckpt-eviction-nondropped")
	s.WorkspaceRoot = t.TempDir()
	s.StagingDir = t.TempDir()
	s.CheckpointTransport = stubCheckpointTransport{}
	if err := os.WriteFile(filepath.Join(s.WorkspaceRoot, "notes.txt"), []byte("state"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	// A never-run lifecycle channel: RequestCheckpoint returns the cancelled
	// context error, which is not a connection-state sentinel, so the gate
	// stays fail-closed. Close releases the listener socket.
	lc, err := NewLifecycleChannel(filepath.Join(t.TempDir(), "lc.sock"))
	if err != nil {
		t.Fatalf("NewLifecycleChannel: %v", err)
	}
	t.Cleanup(func() { _ = lc.Close() })
	s.Lifecycle = lc
	s.setEvicting()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := &fakeCheckpointStream{
		ctx: ctx,
		recv: []*adapterv1.CheckpointRequest{{
			Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
				CheckpointId:   "gw-ckpt-nondropped",
				Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_EVICTION,
				ChunkSizeBytes: 1 << 20,
			}},
		}},
	}

	rerr := s.Checkpoint(stream)
	if status.Code(rerr) != codes.Internal {
		t.Fatalf("code = %v, want Internal: a non-dropped handshake failure must fail closed even while evicting", status.Code(rerr))
	}
	for _, resp := range stream.sent {
		if resp.GetChunkReady() != nil {
			t.Fatal("fail-closed path streamed a chunk; no best-effort snapshot must be taken on a non-dropped handshake failure")
		}
	}
}

// TestLifecycleConnectionDropped_spec_4_4 pins the connection-state
// classification the best-effort eviction snapshot gates on: a dropped or
// closed lifecycle connection is recognised by the lifecycle package's own
// sentinels (including through a wrap), while a cancelled or deadline-lapsed
// context and any other handshake error are not treated as a dropped
// connection and keep the checkpoint failing closed. Classifying by string
// match rather than by sentinel would misfile a renamed error, so this pins
// the sentinel path.
//
// spec: §4.4 (best-effort eviction snapshot), §4.6.1 (agent-pod disruption
// protection).
func TestLifecycleConnectionDropped_spec_4_4(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"channel-closed", errLifecycleClosed, true},
		{"not-connected", errLifecycleNotConnected, true},
		{"wrapped-closed", fmt.Errorf("quiesce: %w", errLifecycleClosed), true},
		{"context-canceled", context.Canceled, false},
		{"context-deadline", context.DeadlineExceeded, false},
		{"unrelated", fmt.Errorf("checkpoint quiesce handshake: boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lifecycleConnectionDropped(tc.err); got != tc.want {
				t.Errorf("lifecycleConnectionDropped(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestReadChunkSpillsLargeChunkToFile pins the §4.4 line 255 memory bound:
// a chunk larger than the memory threshold is buffered in a StagingDir
// spill file rather than on the heap, and the spilled chunk is re-readable
// (the retry budget re-reads the body) and cleaned up on close.
func TestReadChunkSpillsLargeChunkToFile(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("payload"), 32)
	// Threshold below the chunk size forces the spill path.
	buf, more, err := readChunk(bytes.NewReader(data), int64(len(data)), 8, dir)
	if err != nil || !more {
		t.Fatalf("readChunk spill: more=%v err=%v", more, err)
	}
	if buf.path == "" {
		t.Fatal("expected the oversized chunk to spill to a file, got a heap buffer")
	}
	if buf.len() != int64(len(data)) {
		t.Fatalf("spilled chunk len = %d, want %d", buf.len(), len(data))
	}
	// The spilled chunk is re-readable across retry attempts.
	for attempt := 0; attempt < 2; attempt++ {
		rc, oerr := buf.reopen()
		if oerr != nil {
			t.Fatalf("reopen attempt %d: %v", attempt, oerr)
		}
		got, _ := io.ReadAll(rc)
		_ = rc.Close()
		if !bytes.Equal(got, data) {
			t.Fatalf("reopen attempt %d read %d bytes, want the spilled chunk", attempt, len(got))
		}
	}
	path := buf.path
	buf.close()
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("spill file %q was not removed on close (err %v)", path, statErr)
	}
}

// TestReadChunkHeapPathAndEOF pins the heap-buffer path and the clean
// end-of-stream signal.
func TestReadChunkHeapPathAndEOF(t *testing.T) {
	data := []byte("small chunk")
	r := bytes.NewReader(data)
	// Threshold above the chunk size keeps it on the heap.
	buf, more, err := readChunk(r, int64(len(data)), 1<<20, t.TempDir())
	if err != nil || !more {
		t.Fatalf("readChunk heap: more=%v err=%v", more, err)
	}
	if buf.path != "" {
		t.Fatalf("expected a heap buffer, got a spill file %q", buf.path)
	}
	rc, _ := buf.reopen()
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, data) {
		t.Fatalf("heap chunk = %q, want %q", got, data)
	}
	buf.close() // no-op for the heap form

	// A fully-consumed reader yields a clean end of stream.
	buf, more, err = readChunk(r, 4, 1<<20, t.TempDir())
	if err != nil {
		t.Fatalf("readChunk at EOF: %v", err)
	}
	if more || buf != nil {
		t.Fatalf("expected clean EOF (more=false, buf=nil), got more=%v buf=%v", more, buf)
	}
}
