// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bytes"
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §10.1 (Chunked-object storage model), §4.4 (MinIO object key
// logging for manual recovery) — the adapter writes gzip-compressed tar
// chunks only, so a CheckpointStart that declares any other chunk_encoding
// is refused with InvalidArgument on the opening frame: no probe is sent and
// no chunk is PUT, so no manifest records chunks under an encoding they do
// not carry. The empty proto3 zero value resolves to tar.gz, as it does in
// the gateway's manifest store, and is accepted (F-4.4.35).
func TestCheckpointRefusesADeclaredEncodingItDoesNotProduce_spec_10_1(t *testing.T) {
	for _, tc := range []struct {
		name     string
		encoding string
		refused  bool
	}{
		{name: "uncompressed tar", encoding: "tar", refused: true},
		{name: "outside the enum", encoding: "zip", refused: true},
		{name: "case variant", encoding: "TAR.GZ", refused: true},
		{name: "padded", encoding: " tar.gz", refused: true},
		{name: "produced encoding", encoding: "tar.gz", refused: false},
		{name: "unset", encoding: "", refused: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &recordingTransport{}
			client := checkpointServer(t, transport, map[string]string{"notes.txt": "agent workspace state"}, 0)
			stream, err := client.Checkpoint(context.Background())
			if err != nil {
				t.Fatalf("open Checkpoint stream: %v", err)
			}
			if err := stream.Send(&adapterv1.CheckpointRequest{
				Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
					CheckpointId:   "gw-ckpt-encoding",
					SessionId:      &adapterv1.SessionId{Value: streamSession},
					Trigger:        adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC,
					ChunkSizeBytes: 1 << 20,
					ChunkEncoding:  tc.encoding,
				}},
			}); err != nil {
				t.Fatalf("send start: %v", err)
			}

			if tc.refused {
				msg, rerr := stream.Recv()
				if rerr == nil {
					t.Fatalf("adapter answered a %q CheckpointStart with %T, want InvalidArgument", tc.encoding, msg.GetMsg())
				}
				if status.Code(rerr) != codes.InvalidArgument {
					t.Fatalf("code = %v (%v), want InvalidArgument for chunk_encoding %q", status.Code(rerr), rerr, tc.encoding)
				}
				if n := len(transport.allBodies()); n != 0 {
					t.Fatalf("adapter PUT %d byte(s) for a refused encoding, want 0", n)
				}
				return
			}

			summary, failed, probe := driveCheckpoint(t, stream, nil)
			if failed != nil {
				t.Fatalf("checkpoint failed for chunk_encoding %q: %+v", tc.encoding, failed)
			}
			if probe == nil || summary.GetChunkCount() == 0 {
				t.Fatalf("checkpoint for chunk_encoding %q did not complete: probe %+v summary %+v", tc.encoding, probe, summary)
			}
			// The bytes carry the declared encoding: a gzip member header.
			body := transport.allBodies()
			if len(body) < 2 || !bytes.Equal(body[:2], []byte{0x1f, 0x8b}) {
				t.Fatalf("uploaded chunk bytes do not start with the gzip magic number; ArchiveTree must produce %s", workspace.ArchiveTreeEncoding)
			}
		})
	}
}

// spec: §10.1 (Chunked-object storage model) — the encoding the Checkpoint
// handler accepts is the §10.1 enum value naming what ArchiveTree writes.
func TestArchiveTreeEncodingIsTheGzipEnumValue_spec_10_1(t *testing.T) {
	if workspace.ArchiveTreeEncoding != "tar.gz" {
		t.Fatalf("ArchiveTreeEncoding = %q, want tar.gz (ArchiveTree writes through a gzip.Writer)", workspace.ArchiveTreeEncoding)
	}
}
