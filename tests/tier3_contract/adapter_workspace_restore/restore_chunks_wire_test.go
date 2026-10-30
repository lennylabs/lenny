//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_workspace_restore_test is the Tier 3 contract suite for
// FinalizeWorkspaceRequest.restore_chunks on the gateway to pod adapter gRPC
// contract (schemas/lenny-adapter.proto). The gateway's adapter client sends
// the grants for a derived or replayed session's starting snapshot, and the
// compiled adapter server fetches them, in index order, with each grant's URL
// and signed headers as the gateway minted them, then promotes the restored
// tree with the plan's sources applied over it.
package adapter_workspace_restore_test

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
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

const derivedSession = "sess-derived"

// fetch is one GetChunk call the adapter made.
type fetch struct {
	url     string
	headers map[string]string
}

// recordingTransport serves chunk bodies by URL and records every fetch with
// the headers the adapter replayed.
type recordingTransport struct {
	mu      sync.Mutex
	bodies  map[string][]byte
	fetches []fetch
}

func (r *recordingTransport) PutChunk(context.Context, string, map[string]string, int64, io.Reader) (int, string, error) {
	return 0, "", errors.New("PutChunk is not used by the restore")
}

func (r *recordingTransport) GetChunk(_ context.Context, url string, headers map[string]string) (io.ReadCloser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := make(map[string]string, len(headers))
	for k, v := range headers {
		copied[k] = v
	}
	r.fetches = append(r.fetches, fetch{url: url, headers: copied})
	body, ok := r.bodies[url]
	if !ok {
		return nil, fmt.Errorf("no chunk at %q", url)
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (r *recordingTransport) recorded() []fetch {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fetch(nil), r.fetches...)
}

// bundle builds a gzip-compressed tar carrying files under the workspace
// prefix and one session-file entry under the sessions prefix.
func bundle(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(name, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		write("workspace/"+name, body)
	}
	write("sessions/transcript.jsonl", "source session file")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serve starts the compiled adapter over bufconn and returns a gateway
// adapter client dialed to it and the adapter's workspace base.
func serve(t *testing.T, transport adapter.CheckpointTransport, opts ...grpc.ServerOption) (*adapterclient.Client, string) {
	t.Helper()
	s := adapter.New("workspace-restore-contract")
	s.WorkspaceBase = t.TempDir()
	s.SessionsRoot = t.TempDir()
	s.CheckpointTransport = transport
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s, opts...)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adapterclient.New(conn), s.WorkspaceBase
}

// TestRestoreChunksRoundTripInIndexOrderWithURLsAndHeaders_spec_7_1 sends
// three grants out of order through the gateway's adapter client and pins
// that the compiled adapter fetches each one in ascending index order with
// its URL and signed headers unchanged, restores only the workspace tree,
// and applies the plan's source over it.
//
// spec: §7.1 (Derive restore semantics), §4.7.1 (role and gateway RPC
// contract), §5.1 (Derived Runtime Instantiation), §14.1 (WorkspacePlan
// schema versioning)
//
// diagnosis: restore_chunks did not survive the wire between the gateway's
// adapter client and the adapter server, the adapter fetched the grants out of
// index order or altered a URL or a signed header (the object store rejects an
// altered signature), restored the source session's session-file tree, or let
// the snapshot overwrite a plan source. Check chunkGrantsToProto in
// pkg/gateway/runtime/adapterclient, and restoreSeed and chunkStream in
// pkg/adapter.
func TestRestoreChunksRoundTripInIndexOrderWithURLsAndHeaders_spec_7_1(t *testing.T) {
	transport := &recordingTransport{bodies: map[string][]byte{}}
	cl, base := serve(t, transport)

	raw := bundle(t, map[string]string{"a.txt": "restored a", "keep.txt": "restored keep"})
	third := (len(raw) + 2) / 3
	var grants []adapterclient.ChunkGrant
	for i := 2; i >= 0; i-- {
		lo, hi := i*third, min((i+1)*third, len(raw))
		url := fmt.Sprintf("https://objectstore.example/acme/%s/chunk-%d?X-Amz-Signature=sig%d", derivedSession, i, i)
		transport.bodies[url] = raw[lo:hi]
		grants = append(grants, adapterclient.ChunkGrant{
			Index:     uint32(i),
			Length:    int64(hi - lo),
			URL:       url,
			Headers:   map[string]string{"x-amz-checksum-mode": fmt.Sprintf("ENABLED-%d", i)},
			ExpiresAt: time.Now().Add(30 * time.Second),
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	plan := &adapterv1.WorkspacePlan{SchemaVersion: 1, Sources: []*adapterv1.WorkspaceSource{
		{Type: "inlineFile", Path: "a.txt", Content: "from source", Mode: "644"},
	}}
	warnings, err := cl.FinalizeWorkspace(ctx, derivedSession, plan, nil, grants, "attempt-a", false)
	if err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}

	got := transport.recorded()
	if len(got) != 3 {
		t.Fatalf("fetches = %d, want 3", len(got))
	}
	for i, f := range got {
		wantURL := fmt.Sprintf("https://objectstore.example/acme/%s/chunk-%d?X-Amz-Signature=sig%d", derivedSession, i, i)
		if f.url != wantURL {
			t.Errorf("fetch %d url = %q, want %q", i, f.url, wantURL)
		}
		if want := fmt.Sprintf("ENABLED-%d", i); len(f.headers) != 1 || f.headers["x-amz-checksum-mode"] != want {
			t.Errorf("fetch %d headers = %v, want x-amz-checksum-mode=%s", i, f.headers, want)
		}
	}

	current := filepath.Join(base, "slots", derivedSession, "current")
	for name, want := range map[string]string{"a.txt": "from source", "keep.txt": "restored keep"} {
		b, rerr := os.ReadFile(filepath.Join(current, name))
		if rerr != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", name, b, rerr, want)
		}
	}
	if _, serr := os.Stat(filepath.Join(current, "sessions")); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("session-file tree reached the workspace: %v", serr)
	}
}

// TestRestoreFailureAnswersUnavailableOverTheWire_spec_7_1 pins the status a
// restore failure carries on the wire: a corrupt chunk answers UNAVAILABLE,
// which the gateway's bind path reads as a transient failed bind.
//
// spec: §7.1 (Derive restore semantics), §4.7.1 (role and gateway RPC
// contract)
//
// diagnosis: a restore failure reached the gateway with a status other than
// UNAVAILABLE, so the slot path classifies it as permanent or as a source
// error. Check the errRestore arm in FinalizeWorkspace in pkg/adapter.
func TestRestoreFailureAnswersUnavailableOverTheWire_spec_7_1(t *testing.T) {
	url := "https://objectstore.example/acme/" + derivedSession + "/chunk-0"
	transport := &recordingTransport{bodies: map[string][]byte{url: bytes.Repeat([]byte("not gzip"), 32)}}
	cl, _ := serve(t, transport)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cl.FinalizeWorkspace(ctx, derivedSession, &adapterv1.WorkspacePlan{SchemaVersion: 1}, nil,
		[]adapterclient.ChunkGrant{{Index: 0, URL: url, Length: 256}}, "attempt-a", false)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v, want Unavailable", err)
	}
}

// finalizeCapture is a server interceptor that keeps every
// FinalizeWorkspaceRequest the adapter server decoded, so a test reads the
// request as it arrived over the wire.
type finalizeCapture struct {
	mu   sync.Mutex
	reqs []*adapterv1.FinalizeWorkspaceRequest
}

func (c *finalizeCapture) intercept(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if fr, ok := req.(*adapterv1.FinalizeWorkspaceRequest); ok {
		c.mu.Lock()
		c.reqs = append(c.reqs, fr)
		c.mu.Unlock()
	}
	return handler(ctx, req)
}

func (c *finalizeCapture) received() []*adapterv1.FinalizeWorkspaceRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*adapterv1.FinalizeWorkspaceRequest(nil), c.reqs...)
}

// TestEmptyRestoreArrivesAbsentAndMaterializesThePlan_spec_7_1 sends an
// ordinary session's FinalizeWorkspace with no grants and pins that the
// adapter server decodes no restore_chunks, fetches nothing, and promotes
// exactly the plan's sources.
//
// spec: §7.1 (Derive restore semantics), §4.7.1 (role and gateway RPC
// contract), §5.1 (Derived Runtime Instantiation)
//
// diagnosis: the gateway's adapter client put an empty or placeholder grant on
// the wire for a session with no snapshot, or the adapter seeded the build tree
// from an empty restore and changed an ordinary session's workspace. Check
// chunkGrantsToProto in pkg/gateway/runtime/adapterclient and restoreSeed in
// pkg/adapter.
func TestEmptyRestoreArrivesAbsentAndMaterializesThePlan_spec_7_1(t *testing.T) {
	transport := &recordingTransport{bodies: map[string][]byte{}}
	capture := &finalizeCapture{}
	cl, base := serve(t, transport, grpc.ChainUnaryInterceptor(capture.intercept))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	plan := &adapterv1.WorkspacePlan{SchemaVersion: 1, Sources: []*adapterv1.WorkspaceSource{
		{Type: "inlineFile", Path: "a.txt", Content: "from source", Mode: "644"},
	}}
	if _, err := cl.FinalizeWorkspace(ctx, "sess-ordinary", plan, nil, nil, "attempt-a", false); err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}

	reqs := capture.received()
	if len(reqs) != 1 {
		t.Fatalf("adapter decoded %d FinalizeWorkspace requests, want 1", len(reqs))
	}
	if n := len(reqs[0].GetRestoreChunks()); n != 0 {
		t.Errorf("restore_chunks arrived with %d grants, want the field absent", n)
	}
	if got := transport.recorded(); len(got) != 0 {
		t.Errorf("adapter fetched %d chunks for a request with no restore", len(got))
	}
	current := filepath.Join(base, "slots", "sess-ordinary", "current")
	entries, err := os.ReadDir(current)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.txt" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("current holds %v, want only a.txt", names)
	}
	if b, rerr := os.ReadFile(filepath.Join(current, "a.txt")); rerr != nil || string(b) != "from source" {
		t.Errorf("a.txt = %q, %v; want %q", b, rerr, "from source")
	}
}

// TestMidSessionFinalizeWithRestoreIsRefusedOverTheWire_spec_4_7_1 sends a
// mid-session FinalizeWorkspace that carries restore_chunks and an empty
// bind_attempt, so only the restore pairing clause can refuse it, and pins
// that the adapter answers INVALID_ARGUMENT before fetching any grant or
// creating any slot entry.
//
// spec: §4.7.1 (role and gateway RPC contract), §7.1 (Derive restore
// semantics)
//
// diagnosis: the adapter accepted a restore on a mid-session upload, which
// would overwrite a running session's workspace with a snapshot, or refused it
// with a status the gateway reads as transient. Check validateRestorePairing in
// pkg/adapter.
func TestMidSessionFinalizeWithRestoreIsRefusedOverTheWire_spec_4_7_1(t *testing.T) {
	url := "https://objectstore.example/acme/" + derivedSession + "/chunk-0"
	transport := &recordingTransport{bodies: map[string][]byte{url: bundle(t, map[string]string{"a.txt": "restored"})}}
	cl, base := serve(t, transport)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cl.FinalizeWorkspace(ctx, derivedSession, &adapterv1.WorkspacePlan{SchemaVersion: 1}, nil,
		[]adapterclient.ChunkGrant{{Index: 0, URL: url}}, "", true)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
	if got := transport.recorded(); len(got) != 0 {
		t.Errorf("adapter fetched %d chunks for a refused request", len(got))
	}
	if _, serr := os.Stat(filepath.Join(base, "slots", derivedSession)); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("a refused request created the slot entry: %v", serr)
	}
}
