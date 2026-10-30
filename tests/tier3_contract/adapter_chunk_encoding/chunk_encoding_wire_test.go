//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_chunk_encoding_test is the Tier 3 contract suite for the
// CheckpointStart.chunk_encoding field on the gateway ↔ pod adapter gRPC
// contract (schemas/lenny-adapter.proto). The gateway declares the encoding
// on the opening frame and records it on the manifest row and in every chunk
// key; the adapter is the party that writes the bytes. This suite drives the
// compiled adapter over an in-memory gRPC transport and pins both halves of
// the agreement: the adapter refuses an opening frame that declares an
// encoding it does not produce, and the encoding the compiled gateway
// declares is the one the compiled adapter writes, so the manifest describes
// the chunks it names.
package adapter_chunk_encoding_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

const (
	contractTenant  = "acme"
	contractSession = "sess-encoding"
	workspaceFile   = "state.txt"
	workspaceBody   = "agent workspace state"
)

// storeTransport is the adapter's CheckpointTransport. A PUT writes the body
// into the in-memory object store at the URI the presigner minted for the
// URL, so the gateway's Stat confirm and the test read the bytes the adapter
// wrote. A test that uses no gateway leaves presigner nil and only counts.
type storeTransport struct {
	mu        sync.Mutex
	puts      int
	store     *blobstore.MemoryStore
	presigner *urlPresigner
}

func (s *storeTransport) PutChunk(_ context.Context, url string, _ map[string]string, _ int64, body io.Reader) (int, string, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return 0, "", err
	}
	s.mu.Lock()
	s.puts++
	s.mu.Unlock()
	if s.presigner == nil {
		return 200, "", nil
	}
	uri, ok := s.presigner.lookup(url)
	if !ok {
		return 403, "AccessDenied", nil
	}
	if _, err := s.store.Put(uri, "application/octet-stream", bytes.NewReader(b)); err != nil {
		return 500, "InternalError", nil
	}
	return 200, "", nil
}

func (s *storeTransport) GetChunk(context.Context, string, map[string]string) (io.ReadCloser, error) {
	return nil, errors.New("GetChunk is not used by the checkpoint path")
}

func (s *storeTransport) putCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.puts
}

// urlPresigner mints a URL per URI and remembers the mapping.
type urlPresigner struct {
	mu   sync.Mutex
	uris map[string]blobstore.URI
}

func (p *urlPresigner) PresignPut(u blobstore.URI, _ int64, ttl time.Duration) (blobstore.Grant, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	url := u.SessionID + "/" + u.PartID
	p.uris[url] = u
	return blobstore.Grant{URL: url, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (p *urlPresigner) PresignGet(blobstore.URI, time.Duration) (blobstore.Grant, error) {
	return blobstore.Grant{URL: "get"}, nil
}

func (p *urlPresigner) lookup(url string) (blobstore.URI, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	u, ok := p.uris[url]
	return u, ok
}

// noopRuntime binds the session without spawning an agent process.
type noopRuntime struct{}

func (noopRuntime) Start(context.Context, string) error           { return nil }
func (noopRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (noopRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (noopRuntime) Close(context.Context, string) error           { return nil }

func (noopRuntime) Output(ctx context.Context, _ string) (<-chan []byte, error) {
	ch := make(chan []byte)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

// realAdapter serves a real adapter Server bound to contractSession, with a
// one-file workspace, over bufconn and returns the listener.
func realAdapter(t *testing.T, transport adapter.CheckpointTransport) *bufconn.Listener {
	t.Helper()
	s := adapter.New("chunk-encoding-contract")
	s.WorkspaceBase = t.TempDir()
	s.CheckpointTransport = transport
	s.Runtime = noopRuntime{}
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: contractSession},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	current := filepath.Join(s.WorkspaceBase, "slots", contractSession, "current")
	if err := os.WriteFile(filepath.Join(current, workspaceFile), []byte(workspaceBody), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return lis
}

func dialOptions(lis *bufconn.Listener) []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
}

// TestAdapterRefusesADeclaredChunkEncodingItDoesNotProduce_spec_10_1 sends
// CheckpointStart frames over the wire to the compiled adapter and pins that
// an encoding other than tar.gz ends the stream with InvalidArgument before
// any frame is answered or any chunk is PUT.
//
// spec: 10.1 (Chunked-object storage model), 4.4 (MinIO object key logging
// for manual recovery)
// diagnosis: The adapter accepted a CheckpointStart whose chunk_encoding names
//
//	an encoding it does not write, or refused it with a code other than
//	InvalidArgument. The gateway records the declared value on the manifest
//	row and in each chunk key, so an accepted mismatch leaves a manifest that
//	tells the resume path and an operator to decode gzip bytes as a plain tar.
//	A FailedPrecondition refusal would be misread by the gateway as the
//	workspace-size-probe rejection. Re-check checkChunkEncoding in
//	pkg/adapter/checkpoint.go.
func TestAdapterRefusesADeclaredChunkEncodingItDoesNotProduce_spec_10_1(t *testing.T) {
	for _, encoding := range []string{"tar", "zip", "tar.zst"} {
		t.Run(encoding, func(t *testing.T) {
			transport := &storeTransport{}
			lis := realAdapter(t, transport)
			conn, err := grpc.NewClient("passthrough:///bufnet", dialOptions(lis)...)
			if err != nil {
				t.Fatalf("dial adapter: %v", err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			stream, err := adapterv1.NewAdapterClient(conn).Checkpoint(context.Background())
			if err != nil {
				t.Fatalf("open Checkpoint stream: %v", err)
			}
			if err := stream.Send(&adapterv1.CheckpointRequest{
				Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
					CheckpointId:   "gw-ckpt-encoding",
					SessionId:      &adapterv1.SessionId{Value: contractSession},
					Trigger:        checkpoint.TriggerPeriodic.Proto(),
					ChunkSizeBytes: 1 << 20,
					ChunkEncoding:  encoding,
				}},
			}); err != nil {
				t.Fatalf("send start: %v", err)
			}
			msg, err := stream.Recv()
			if err == nil {
				t.Fatalf("adapter answered chunk_encoding %q with %T, want InvalidArgument", encoding, msg.GetMsg())
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("code = %v (%v), want InvalidArgument for chunk_encoding %q", status.Code(err), err, encoding)
			}
			if n := transport.putCount(); n != 0 {
				t.Fatalf("adapter PUT %d chunk(s) for a refused encoding, want 0", n)
			}
		})
	}
}

// TestGatewayDeclaredChunkEncodingMatchesTheAdapterBytes_spec_10_1 runs the
// compiled gateway Checkpointer against the compiled adapter. It pins that
// the adapter accepts the encoding the gateway declares, that the manifest
// row and every chunk key carry tar.gz, and that the stored chunks decode
// with the tar.gz pipeline (gunzip, then untar) back to the workspace file.
//
// spec: 10.1 (Chunked-object storage model, Reassembly on resume), 4.4 (MinIO
// object key logging for manual recovery)
// diagnosis: The gateway and the adapter disagree on the chunk encoding. The
//
//	adapter refused the gateway's declared encoding (the checkpoint fails),
//	the manifest or a chunk key names an encoding other than tar.gz, or the
//	stored chunks do not decode as gzip-compressed tar. Re-check the
//	gateway's chunkEncoding in pkg/gateway/checkpoint/checkpointer and
//	workspace.ArchiveTreeEncoding in pkg/adapter/workspace.
func TestGatewayDeclaredChunkEncodingMatchesTheAdapterBytes_spec_10_1(t *testing.T) {
	store := blobstore.NewMemoryStore(time.Now)
	presigner := &urlPresigner{uris: map[string]blobstore.URI{}}
	lis := realAdapter(t, &storeTransport{store: store, presigner: presigner})
	cl, err := adapterclient.Dial("passthrough:///bufnet", dialOptions(lis)...)
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: contractSession, TenantID: contractTenant, Adapter: cl})
	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: contractSession, TenantID: contractTenant, State: session.StateRunning, RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	manifests := partialmanifeststore.NewMemoryStore(nil)
	cp := &checkpointer.Checkpointer{
		Sessions:       sessions,
		Registry:       registry,
		Manifests:      manifests,
		Quota:          storagequota.NewMemory(),
		QuotaLimitFor:  func(context.Context, string) (int64, error) { return 1 << 30, nil },
		Presigner:      presigner,
		ObjectStore:    store,
		ChunkSizeBytes: 64, // several chunks, so the decode crosses chunk boundaries
		Deadline:       10 * time.Second,
	}
	if err := cp.CheckpointWithTrigger(context.Background(), contractTenant, contractSession, checkpoint.TriggerPeriodic); err != nil {
		t.Fatalf("Checkpoint against the real adapter: %v", err)
	}

	rec, err := manifests.LatestFull(context.Background(), contractTenant, contractSession)
	if err != nil {
		t.Fatalf("read the completed manifest row: %v", err)
	}
	if rec.ChunkEncoding != partialmanifeststore.ChunkEncodingTarGz {
		t.Fatalf("manifest chunk_encoding = %q, want tar.gz", rec.ChunkEncoding)
	}
	if rec.Partial || rec.ChunkCount < 2 {
		t.Fatalf("manifest = partial %v chunk_count %d, want a complete multi-chunk checkpoint", rec.Partial, rec.ChunkCount)
	}

	// Concatenate the chunks in index order from the keys the manifest names
	// and run the tar.gz pipeline over the stream.
	var stream bytes.Buffer
	for _, u := range store.Keys() {
		if u.SessionID == contractSession && !strings.HasSuffix(u.PartID, ".tar.gz") {
			t.Errorf("chunk key %q does not carry the manifest's .tar.gz suffix", u.PartID)
		}
	}
	for n := 0; n < rec.ChunkCount; n++ {
		uri := blobstore.URI{
			TenantID: contractTenant, ObjectType: blobstore.ObjectTypeCheckpoint, SessionID: contractSession,
			PartID: fmt.Sprintf("%s/chunk-%05d.%s", rec.CheckpointID, n, rec.ChunkEncoding),
		}
		_, body, gerr := store.Get(uri)
		if gerr != nil {
			t.Fatalf("read chunk %d at the manifest-derived key: %v", n, gerr)
		}
		_, _ = io.Copy(&stream, body)
		_ = body.Close()
	}
	gz, err := gzip.NewReader(&stream)
	if err != nil {
		t.Fatalf("chunks do not decode as gzip, the encoding the manifest records: %v", err)
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, terr := tr.Next()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			t.Fatalf("untar the gunzipped chunk stream: %v", terr)
		}
		if strings.HasSuffix(hdr.Name, "/"+workspaceFile) {
			b, _ := io.ReadAll(tr)
			if string(b) != workspaceBody {
				t.Fatalf("restored %s = %q, want %q", workspaceFile, b, workspaceBody)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("the decoded archive carries no %s entry", workspaceFile)
	}
}
