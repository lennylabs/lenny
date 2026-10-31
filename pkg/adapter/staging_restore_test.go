// SPDX-License-Identifier: MIT

package adapter

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// restoreTransport serves chunk bodies by URL and records the URLs in fetch
// order. A URL in fail answers that error; a URL in readFail serves its body
// and then fails the read.
type restoreTransport struct {
	mu       sync.Mutex
	bodies   map[string][]byte
	fail     map[string]error
	readFail map[string]error
	fetched  []string
}

func (r *restoreTransport) PutChunk(context.Context, string, map[string]string, int64, io.Reader) (int, string, error) {
	return 0, "", errors.New("restoreTransport: PutChunk is not used by the restore")
}

func (r *restoreTransport) GetChunk(_ context.Context, url string, _ map[string]string) (io.ReadCloser, error) {
	r.mu.Lock()
	r.fetched = append(r.fetched, url)
	r.mu.Unlock()
	if err := r.fail[url]; err != nil {
		return nil, err
	}
	body, ok := r.bodies[url]
	if !ok {
		return nil, fmt.Errorf("restoreTransport: no chunk at %q", url)
	}
	var rd io.Reader = bytes.NewReader(body)
	if err := r.readFail[url]; err != nil {
		rd = io.MultiReader(rd, errReader{err})
	}
	return io.NopCloser(rd), nil
}

func (r *restoreTransport) fetchOrder() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.fetched...)
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// tarEntry is one raw entry of a hand-built bundle.
type tarEntry struct {
	name     string
	body     string
	typeflag byte
	linkname string
}

// rawBundle writes entries as a gzip-compressed tar without the
// containment checks ArchiveTree applies, so a test can build a hostile
// bundle.
func rawBundle(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Typeflag: e.typeflag, Linkname: e.linkname}
		if e.typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if e.typeflag == tar.TypeDir {
			hdr.Mode = 0o755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if e.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("write body %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func reg(name, body string) tarEntry { return tarEntry{name: name, body: body, typeflag: tar.TypeReg} }

// gzipOf compresses raw bytes, giving a valid gzip stream that is not a tar.
func gzipOf(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// splitGrants splits bundle into n chunks served by transport and returns
// the grants in descending index order, so a test also pins the ascending
// fetch order.
func splitGrants(transport *restoreTransport, bundle []byte, n int) []*adapterv1.ChunkGrant {
	if transport.bodies == nil {
		transport.bodies = map[string][]byte{}
	}
	size := (len(bundle) + n - 1) / n
	var grants []*adapterv1.ChunkGrant
	for i := 0; i < n; i++ {
		lo, hi := i*size, min((i+1)*size, len(bundle))
		url := fmt.Sprintf("https://objectstore.example/derived/chunk-%d", i)
		transport.bodies[url] = bundle[lo:hi]
		grants = append(grants, &adapterv1.ChunkGrant{Index: uint32(i), Url: url, Length: int64(hi - lo)})
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].GetIndex() > grants[j].GetIndex() })
	return grants
}

func restoreServer(t *testing.T, transport CheckpointTransport) *Server {
	t.Helper()
	srv := &Server{WorkspaceBase: t.TempDir(), SessionsRoot: t.TempDir()}
	if transport != nil {
		srv.CheckpointTransport = transport
	}
	return srv
}

func restoreFinalizeReq(sessionID string, grants []*adapterv1.ChunkGrant, sources ...*adapterv1.WorkspaceSource) *adapterv1.FinalizeWorkspaceRequest {
	req := finalizeReq(sessionID, sources...)
	req.RestoreChunks = grants
	return req
}

// treeSnapshot reads every regular file and symlink under root into a map
// keyed by relative path, so a test can compare a tree byte for byte.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, lerr := os.Readlink(p)
			out[rel] = "-> " + target
			return lerr
		case d.Type().IsRegular():
			b, rerr := os.ReadFile(p)
			out[rel] = string(b)
			return rerr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// A derived or replayed session's first bind seeds the build tree from the
// snapshot and applies the plan's sources over it: a source wins a path it
// shares with the snapshot, a restored path no source writes survives, and
// overwriting a restored path raises no collision warning. Against an adapter
// that ignores restore_chunks keep.txt is missing.
//
// spec: §7.1 (Derive restore semantics); §5.1; §14.1
func TestFinalizeWorkspaceRestoresSnapshotThenAppliesSources_spec_7_1(t *testing.T) {
	transport := &restoreTransport{}
	srv := restoreServer(t, transport)
	bundle := rawBundle(
		t,
		tarEntry{name: "workspace/", typeflag: tar.TypeDir},
		reg("workspace/a.txt", "restored a"),
		reg("workspace/keep.txt", "restored keep"),
		tarEntry{name: "workspace/sub/", typeflag: tar.TypeDir},
		reg("workspace/sub/deep.txt", strings.Repeat("restored deep ", 512)),
	)
	grants := splitGrants(transport, bundle, 3)

	resp, err := srv.FinalizeWorkspace(context.Background(), restoreFinalizeReq("sess-derived", grants,
		wsSource("inlineFile", "a.txt", "from source", "644")))
	if err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}
	got := treeSnapshot(t, slotCurrent(srv, "sess-derived"))
	want := map[string]string{
		"a.txt":        "from source",
		"keep.txt":     "restored keep",
		"sub/deep.txt": strings.Repeat("restored deep ", 512),
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("current = %v, want %v", got, want)
	}
	if n := len(resp.GetWorkspacePlanWarnings()); n != 0 {
		t.Errorf("warnings = %v, want none for a source overwriting a restored path", resp.GetWorkspacePlanWarnings())
	}
	wantOrder := []string{
		"https://objectstore.example/derived/chunk-0",
		"https://objectstore.example/derived/chunk-1",
		"https://objectstore.example/derived/chunk-2",
	}
	if fmt.Sprint(transport.fetchOrder()) != fmt.Sprint(wantOrder) {
		t.Errorf("fetch order = %v, want %v", transport.fetchOrder(), wantOrder)
	}
}

// The restore names only the workspace root, so the bundle's session-file
// tree is not restored anywhere on the pod, even with a sessions root
// configured.
//
// spec: §7.1 (Derive restore semantics)
func TestFinalizeWorkspaceRestoresOnlyTheWorkspaceTree_spec_7_1(t *testing.T) {
	transport := &restoreTransport{}
	srv := restoreServer(t, transport)
	bundle := rawBundle(
		t,
		reg("workspace/w.txt", "workspace file"),
		tarEntry{name: "sessions/", typeflag: tar.TypeDir},
		reg("sessions/transcript.jsonl", "source session file"),
	)
	if _, err := srv.FinalizeWorkspace(context.Background(),
		restoreFinalizeReq("sess-derived", splitGrants(transport, bundle, 1))); err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}
	if got := treeSnapshot(t, slotCurrent(srv, "sess-derived")); fmt.Sprint(got) != fmt.Sprint(map[string]string{"w.txt": "workspace file"}) {
		t.Errorf("current = %v, want only w.txt", got)
	}
	for _, base := range []string{srv.WorkspaceBase, srv.SessionsRoot} {
		if files := treeSnapshot(t, base); len(files) > 0 {
			for rel := range files {
				if strings.Contains(rel, "transcript.jsonl") {
					t.Errorf("session file restored at %s", filepath.Join(base, rel))
				}
			}
		}
	}
}

// Every restore failure answers UNAVAILABLE with the restore message, removes
// the build tree, and leaves the current tree byte-identical to its state
// before the call. A source materialization failure on a request that also
// carries a valid restore keeps INVALID_ARGUMENT.
//
// spec: §7.1 (Derive restore semantics); §4.7.1
func TestFinalizeWorkspaceRestoreFailuresFailTheBindAndLeaveCurrent_spec_7_1(t *testing.T) {
	valid := func(t *testing.T) []byte {
		return rawBundle(t, reg("workspace/a.txt", strings.Repeat("restored ", 4096)))
	}
	cases := []struct {
		name     string
		setup    func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant
		noTr     bool
		sources  []*adapterv1.WorkspaceSource
		wantCode codes.Code
		wantMsg  string
		// wantFetched names a chunk the case requires to have been fetched
		// before the failure.
		wantFetched string
	}{
		{
			name: "chunk fetch error after the first chunk streamed",
			setup: func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant {
				grants := splitGrants(tr, valid(t), 2)
				tr.fail = map[string]error{"https://objectstore.example/derived/chunk-1": errors.New("403 AccessDenied")}
				return grants
			},
			wantCode:    codes.Unavailable,
			wantMsg:     "403 AccessDenied",
			wantFetched: "https://objectstore.example/derived/chunk-0",
		},
		{
			name: "chunk body read error",
			setup: func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant {
				grants := splitGrants(tr, valid(t), 2)
				tr.readFail = map[string]error{"https://objectstore.example/derived/chunk-0": errors.New("connection reset")}
				return grants
			},
			wantCode: codes.Unavailable,
			wantMsg:  "connection reset",
		},
		{
			name: "gzip corruption",
			setup: func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant {
				return splitGrants(tr, bytes.Repeat([]byte("not gzip"), 64), 1)
			},
			wantCode: codes.Unavailable,
		},
		{
			name: "tar corruption",
			setup: func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant {
				return splitGrants(tr, gzipOf(t, bytes.Repeat([]byte("x"), 2048)), 1)
			},
			wantCode: codes.Unavailable,
		},
		{
			name: "escaping entry path",
			setup: func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant {
				return splitGrants(tr, rawBundle(t, reg("workspace/ok.txt", "ok"), reg("workspace/../../escape.txt", "x")), 1)
			},
			wantCode: codes.Unavailable,
			wantMsg:  "escape.txt",
		},
		{
			name: "relative symlink leaving the build tree",
			setup: func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant {
				return splitGrants(tr, rawBundle(t, tarEntry{name: "workspace/link", typeflag: tar.TypeSymlink, linkname: "../../../etc/passwd"}), 1)
			},
			wantCode: codes.Unavailable,
			wantMsg:  "escapes the workspace root",
		},
		{
			name: "absolute symlink into the source session's slot",
			setup: func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant {
				return splitGrants(tr, rawBundle(t, tarEntry{name: "workspace/link", typeflag: tar.TypeSymlink, linkname: "/workspace/slots/sess-source/current/secret"}), 1)
			},
			wantCode: codes.Unavailable,
			wantMsg:  "escapes the workspace root",
		},
		{
			name: "symlink into a forbidden mount",
			setup: func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant {
				return splitGrants(tr, rawBundle(t, tarEntry{name: "workspace/link", typeflag: tar.TypeSymlink, linkname: "/proc/self/environ"}), 1)
			},
			wantCode: codes.Unavailable,
		},
		{
			name: "unsupported entry type",
			setup: func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant {
				return splitGrants(tr, rawBundle(t, tarEntry{name: "workspace/fifo", typeflag: tar.TypeFifo}), 1)
			},
			wantCode: codes.Unavailable,
			wantMsg:  "unsupported type",
		},
		{
			name: "no checkpoint transport configured",
			setup: func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant {
				return splitGrants(tr, valid(t), 1)
			},
			noTr:     true,
			wantCode: codes.Unavailable,
			wantMsg:  "checkpoint transport",
		},
		{
			name: "source materialization failure over a valid restore",
			setup: func(t *testing.T, tr *restoreTransport) []*adapterv1.ChunkGrant {
				return splitGrants(tr, valid(t), 1)
			},
			sources:  []*adapterv1.WorkspaceSource{wsSource("inlineFile", "../escape.txt", "x", "644")},
			wantCode: codes.InvalidArgument,
			wantMsg:  "materialize workspace",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := &restoreTransport{}
			var srvTr CheckpointTransport = tr
			if tc.noTr {
				srvTr = nil
			}
			srv := restoreServer(t, srvTr)
			// A prior finalize gives current a known state to compare.
			if _, err := srv.FinalizeWorkspace(context.Background(), finalizeReq("sess-derived",
				wsSource("inlineFile", "prior.txt", "prior", "644"))); err != nil {
				t.Fatalf("prior FinalizeWorkspace: %v", err)
			}
			current := slotCurrent(srv, "sess-derived")
			before := treeSnapshot(t, current)

			_, err := srv.FinalizeWorkspace(context.Background(),
				restoreFinalizeReq("sess-derived", tc.setup(t, tr), tc.sources...))
			if status.Code(err) != tc.wantCode {
				t.Fatalf("err = %v, want %v", err, tc.wantCode)
			}
			msg := status.Convert(err).Message()
			if tc.wantCode == codes.Unavailable && !strings.HasPrefix(msg, "restore workspace snapshot: ") {
				t.Errorf("message = %q, want the restore prefix", msg)
			}
			if tc.wantMsg != "" && !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("message = %q, want it to name %q", msg, tc.wantMsg)
			}
			if tc.wantFetched != "" && !contains(tr.fetchOrder(), tc.wantFetched) {
				t.Errorf("fetched %v, want %s fetched before the failure", tr.fetchOrder(), tc.wantFetched)
			}
			if after := treeSnapshot(t, current); fmt.Sprint(after) != fmt.Sprint(before) {
				t.Errorf("current changed: before %v, after %v", before, after)
			}
			// The slot's staging directory sits where the default build tree
			// would, so the build tree is the root-adjacent fallback.
			if _, serr := os.Stat(current + ".build"); !errors.Is(serr, os.ErrNotExist) {
				t.Errorf("build tree survived the failure: %v", serr)
			}
		})
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// A mid-session request carrying restore_chunks is refused INVALID_ARGUMENT
// before any slot entry or tree is created. Against an adapter without the
// refusal the request reaches the resolve and is answered
// FAILED_PRECONDITION for the missing entry.
//
// spec: §4.7.1 rule 1; §7.1 (Derive restore semantics)
func TestFinalizeWorkspaceRefusesMidSessionRestore_spec_4_7_1(t *testing.T) {
	transport := &restoreTransport{}
	srv := restoreServer(t, transport)
	req := restoreFinalizeReq("sess-derived", splitGrants(transport, rawBundle(t, reg("workspace/a", "a")), 1))
	req.BindAttempt = ""
	req.MidSession = true

	_, err := srv.FinalizeWorkspace(context.Background(), req)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
	if !strings.Contains(status.Convert(err).Message(), "restore_chunks") {
		t.Errorf("message = %q, want it to name restore_chunks", status.Convert(err).Message())
	}
	if n := len(transport.fetchOrder()); n != 0 {
		t.Errorf("fetched %d chunks, want none", n)
	}
	if _, serr := os.Stat(filepath.Join(srv.WorkspaceBase, "slots", "sess-derived")); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("slot tree created for a refused request: %v", serr)
	}
	srv.mu.Lock()
	_, ok := srv.slotStateLocked("sess-derived")
	srv.mu.Unlock()
	if ok {
		t.Error("slot entry created for a refused request")
	}
}

// An empty restore_chunks list on a bind-sequence request materializes the
// plan exactly as a request without the field and fetches nothing.
//
// spec: §7.1 (Derive restore semantics); §4.7.1
func TestFinalizeWorkspaceEmptyRestoreMaterializesAsBefore_spec_7_1(t *testing.T) {
	transport := &restoreTransport{}
	srv := restoreServer(t, transport)
	if _, err := srv.FinalizeWorkspace(context.Background(), restoreFinalizeReq("sess-1", []*adapterv1.ChunkGrant{},
		wsSource("inlineFile", "a.txt", "a", "644"))); err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}
	if got := treeSnapshot(t, slotCurrent(srv, "sess-1")); fmt.Sprint(got) != fmt.Sprint(map[string]string{"a.txt": "a"}) {
		t.Errorf("current = %v, want only a.txt", got)
	}
	if n := len(transport.fetchOrder()); n != 0 {
		t.Errorf("fetched %d chunks, want none", n)
	}
}

// blockingTransport serves large chunk bodies and records when each body is
// closed, so a test can observe the producer goroutine ending.
type blockingTransport struct {
	mu      sync.Mutex
	fetched []uint32
	closed  chan uint32
}

type closeRecorder struct {
	io.Reader
	idx    uint32
	closed chan uint32
}

func (c closeRecorder) Close() error {
	c.closed <- c.idx
	return nil
}

func (b *blockingTransport) PutChunk(context.Context, string, map[string]string, int64, io.Reader) (int, string, error) {
	return 0, "", errors.New("unused")
}

func (b *blockingTransport) GetChunk(_ context.Context, url string, _ map[string]string) (io.ReadCloser, error) {
	var idx uint32
	if _, err := fmt.Sscanf(url, "chunk-%d", &idx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.fetched = append(b.fetched, idx)
	b.mu.Unlock()
	return closeRecorder{Reader: bytes.NewReader(make([]byte, 1<<20)), idx: idx, closed: b.closed}, nil
}

// A consumer that stops reading and closes the stream ends the producer
// goroutine: the chunk body in flight is closed and no later chunk is
// fetched.
//
// spec: §10.1.7; §7.1 (Derive restore semantics)
func TestChunkStreamConsumerStoppingEarlyEndsTheProducer_spec_10_1_7(t *testing.T) {
	bt := &blockingTransport{closed: make(chan uint32, 4)}
	srv := &Server{CheckpointTransport: bt}
	grants := []*adapterv1.ChunkGrant{
		{Index: 2, Url: "chunk-2"}, {Index: 0, Url: "chunk-0"}, {Index: 1, Url: "chunk-1"},
	}
	stream := srv.chunkStream(context.Background(), grants)
	if _, err := io.ReadFull(stream, make([]byte, 16)); err != nil {
		t.Fatalf("read: %v", err)
	}
	_ = stream.Close()
	select {
	case idx := <-bt.closed:
		if idx != 0 {
			t.Errorf("closed chunk %d first, want chunk 0", idx)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("producer did not close the in-flight chunk after the consumer closed the stream")
	}
	time.Sleep(50 * time.Millisecond)
	bt.mu.Lock()
	defer bt.mu.Unlock()
	if fmt.Sprint(bt.fetched) != "[0]" {
		t.Errorf("fetched %v after the consumer closed, want only [0]", bt.fetched)
	}
}

// Extraction through the shared stream still sees the chunks in index order
// when the grants arrive out of order.
//
// spec: §10.1.7
func TestChunkStreamConcatenatesInIndexOrder_spec_10_1_7(t *testing.T) {
	tr := &restoreTransport{bodies: map[string][]byte{"u0": []byte("ab"), "u1": []byte("cd"), "u2": []byte("ef")}}
	srv := &Server{CheckpointTransport: tr}
	stream := srv.chunkStream(context.Background(), []*adapterv1.ChunkGrant{
		{Index: 1, Url: "u1"}, {Index: 2, Url: "u2"}, {Index: 0, Url: "u0"},
	})
	defer stream.Close()
	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "abcdef" {
		t.Errorf("stream = %q, want abcdef", got)
	}
}
