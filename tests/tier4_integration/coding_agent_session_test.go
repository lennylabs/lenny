// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §26.2 coding-agent shared workspace
// pattern end to end across the gateway->adapter->seal->catalog path: a
// session's uploadArchive WorkspacePlan materializes at the coding-agent
// repo root, and the workspace (including anything under the output
// convention) survives session teardown as a catalog artifact discoverable
// through the real GET /v1/sessions/{id}/artifacts handler.
//
// The flow drives real production code at every hop rather than a stand-in:
//
//   - pkg/upload/archive.Extract decodes the uploaded tar.gz exactly as the
//     gateway's podsession.Binder does for a §14 uploadArchive source, before
//     the adapter ever sees the plan (the adapter refuses to decompress
//     archives itself).
//   - A real adapter.Server (pkg/adapter) serves PrepareWorkspace and
//     FinalizeWorkspace over a live gRPC connection, materializing the
//     extracted sources onto its on-disk workspace root exactly as a
//     coding-agent pod would.
//   - A real gateway checkpointer.Checkpointer drives that same adapter's
//     Checkpoint stream to seal the workspace, through the real chunked
//     grant/confirm protocol, and records the §12.5 artifact_store catalog
//     row via the same ChunkRecorder seam production wires.
//   - A real sessionserver.Server, driven purely over HTTP, runs the
//     terminate transition (which invokes the Sealer synchronously) and then
//     serves GET /v1/sessions/{id}/artifacts from the same catalog.
//
// spec: §26.2 (shared coding-agent workspace pattern: repo root at
// /workspace/current/, output surviving under /workspace/output/ recoverable
// via GET /v1/sessions/{id}/artifacts, reference uploadArchive WorkspacePlan).
package tier4_integration_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/cataloging"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/upload"
	uploadarchive "github.com/lennylabs/lenny/pkg/upload/archive"
)

const (
	codingAgentTenant  = "acme"
	codingAgentSession = "sess_coding_agent"
)

// spec: §26.2 "Reference WorkspacePlan" — the shared coding-agent
// uploadArchive plan: `{ "type": "uploadArchive", "pathPrefix": ".",
// "uploadRef": "<upload_id>", "format": "tar.gz" }`. §26.2 "Workspace
// layout" — "`/workspace/current/` is the repo root ... `/workspace/output/`
// is provided for agents to write artifacts that should survive session
// teardown and be recoverable via `GET /v1/sessions/{id}/artifacts`."
//
// diagnosis: a failure here means the §26.2 coding-agent shared workspace
// pattern regressed end to end. Either the uploadArchive extraction no
// longer lands its content at the workspace root (materialization broke),
// or the seal-and-export on session termination no longer produces a
// catalog row the REST artifacts endpoint can see (the output-survival
// guarantee broke).
func TestCodingAgentUploadArchiveMaterializesAndSealsToArtifacts(t *testing.T) {
	// ---- extract the uploaded archive exactly as the gateway's binder does
	// for a §14 uploadArchive WorkspacePlan source, before the adapter ever
	// sees the plan. ----
	data := buildCodingAgentArchive(t)
	res, err := uploadarchive.Extract(data, "tar.gz", 0, 0, ".", upload.RuntimeAllow{
		WorkspaceRoot: uploadarchive.DefaultWorkspaceRoot,
	})
	if err != nil {
		t.Fatalf("extract uploadArchive: %v", err)
	}
	sources, uploads := extractedToWorkspaceSources(res)

	// ---- stand up a real adapter.Server: PrepareWorkspace + FinalizeWorkspace
	// materialize the extracted sources onto its on-disk workspace root, the
	// same sequence a coding-agent pod runs before StartSession. ----
	adapterSrv := adapter.New("coding-agent-session-test")
	adapterSrv.WorkspaceRoot = filepath.Join(t.TempDir(), "current")
	adapterSrv.StagingDir = filepath.Join(t.TempDir(), "staging")
	store := newCPStore()
	adapterSrv.CheckpointTransport = recordingCheckpointTransport{store: store}
	client := dialCodingAgentAdapter(t, adapterSrv)
	ctx := context.Background()

	if _, err := client.PrepareWorkspace(ctx, codingAgentSession, uploads); err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	if _, err := client.FinalizeWorkspace(ctx, codingAgentSession, &adapterv1.WorkspacePlan{
		SchemaVersion: 1,
		Sources:       sources,
	}, nil, false); err != nil {
		t.Fatalf("FinalizeWorkspace: %v", err)
	}

	// §26.2 "/workspace/current/ is the repo root": the extracted archive's
	// top-level entries land directly under the adapter's workspace root.
	readmePath := filepath.Join(adapterSrv.WorkspaceRoot, "README.md")
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read materialized README.md: %v", err)
	}
	if string(readme) != "coding agent workspace\n" {
		t.Errorf("README.md content = %q, want the uploaded archive content", readme)
	}
	// §26.2 "/workspace/output/ ... recoverable via GET
	// /v1/sessions/{id}/artifacts": content under output/ materializes at the
	// same root, not a separate tree, so the whole-workspace seal below
	// captures it.
	outputPath := filepath.Join(adapterSrv.WorkspaceRoot, "output", "result.txt")
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read materialized output/result.txt: %v", err)
	}
	if string(output) != "agent output artifact\n" {
		t.Errorf("output/result.txt content = %q, want the uploaded archive content", output)
	}

	// ---- wire a real gateway checkpointer against the same adapter binding,
	// recording confirmed chunks into a real cataloging.Store-backed §12.5
	// artifact_store catalog. ----
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: codingAgentSession, TenantID: codingAgentTenant, Adapter: client})

	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: codingAgentSession, TenantID: codingAgentTenant, State: session.StateRunning,
		RuntimeRef: "claude-code",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	catalog := newMemArtifactCatalog()
	catalogingStore := cataloging.New(blobstore.NewMemoryStore(nil), catalog, cataloging.Options{})

	cp := &checkpointer.Checkpointer{
		Sessions:      sessions,
		Registry:      registry,
		Manifests:     partialmanifeststore.NewMemoryStore(nil),
		Quota:         storagequota.NewMemory(),
		QuotaLimitFor: func(context.Context, string) (int64, error) { return 1 << 40, nil },
		Presigner:     cpPresigner{store: store},
		ObjectStore:   store,
		ChunkObjects:  store,
		Cataloging:    catalogingStore,
		Deadline:      10 * time.Second,
	}

	// ---- wire a real sessionserver.Server: the Sealer runs synchronously
	// inside the HTTP terminate handler, and the artifacts handler reads the
	// same catalog the seal just populated. ----
	srv := sessionserver.New(sessions, sessionserver.Options{
		Sealer:    cp,
		Artifacts: catalog,
		Clock:     func() time.Time { return time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) },
	})
	handler := srv.Handler()

	terminateReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+codingAgentSession+"/terminate", nil)
	terminateReq.Header.Set("X-Lenny-Tenant-ID", codingAgentTenant)
	terminateRR := httptest.NewRecorder()
	handler.ServeHTTP(terminateRR, terminateReq)
	if terminateRR.Code != http.StatusOK {
		t.Fatalf("terminate status = %d, want 200; body=%s", terminateRR.Code, terminateRR.Body.String())
	}

	// §26.2 "recoverable via GET /v1/sessions/{id}/artifacts": the sealed
	// workspace is now visible through the REST artifacts subresource.
	artifactsReq := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+codingAgentSession+"/artifacts", nil)
	artifactsReq.Header.Set("X-Lenny-Tenant-ID", codingAgentTenant)
	artifactsRR := httptest.NewRecorder()
	handler.ServeHTTP(artifactsRR, artifactsReq)
	if artifactsRR.Code != http.StatusOK {
		t.Fatalf("GET artifacts status = %d, want 200; body=%s", artifactsRR.Code, artifactsRR.Body.String())
	}
	var body struct {
		Items []struct {
			Ref       string `json:"ref"`
			Type      string `json:"type"`
			SizeBytes int64  `json:"sizeBytes"`
		} `json:"items"`
	}
	if err := json.Unmarshal(artifactsRR.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode artifacts response: %v; body=%s", err, artifactsRR.Body.String())
	}
	if len(body.Items) == 0 {
		t.Fatalf("GET /v1/sessions/%s/artifacts returned no items; the sealed workspace is not discoverable", codingAgentSession)
	}
	var sealedTotal int64
	for _, item := range body.Items {
		if item.SizeBytes <= 0 {
			t.Errorf("artifact %q reported sizeBytes = %d, want > 0", item.Ref, item.SizeBytes)
		}
		sealedTotal += item.SizeBytes
	}
	if sealedTotal == 0 {
		t.Error("sealed artifacts carried zero total bytes; the workspace tar produced no content")
	}
}

// buildCodingAgentArchive builds a tar.gz archive matching the §26.2 shared
// coding-agent workspace shape: a repo-root file, a nested source file, and
// a file under the output/ convention.
func buildCodingAgentArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	writeDir := func(name string, mode int64) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: mode}); err != nil {
			t.Fatalf("write tar dir %q: %v", name, err)
		}
	}
	writeFile := func(name string, mode int64, content string) {
		hdr := &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write tar content %q: %v", name, err)
		}
	}
	writeDir("src/", 0o755)
	writeFile("src/main.go", 0o644, "package main\n\nfunc main() {}\n")
	writeFile("README.md", 0o644, "coding agent workspace\n")
	writeDir("output/", 0o755)
	writeFile("output/result.txt", 0o644, "agent output artifact\n")

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

// extractedToWorkspaceSources converts an uploadarchive.Result into the
// adapterv1.WorkspaceSource list the adapter's FinalizeWorkspace expects,
// mirroring the gateway podsession.Binder's own extracted-source rewrite
// (mkdir for directories, uploadFile with a synthetic ref for files staged
// via PrepareWorkspace, symlink verbatim). It returns the sources alongside
// the ref->content map PrepareWorkspace streams.
func extractedToWorkspaceSources(res *uploadarchive.Result) ([]*adapterv1.WorkspaceSource, map[string][]byte) {
	uploads := map[string][]byte{}
	var sources []*adapterv1.WorkspaceSource
	for _, d := range res.Dirs {
		sources = append(sources, &adapterv1.WorkspaceSource{
			Type: "mkdir", Path: d.Path, Mode: modeOctalString(d.Mode),
		})
	}
	for n, f := range res.Files {
		ref := fmt.Sprintf("__coding_agent_archive_%d", n)
		uploads[ref] = f.Content
		sources = append(sources, &adapterv1.WorkspaceSource{
			Type: "uploadFile", Path: f.Path, UploadRef: ref, Mode: modeOctalString(f.Mode),
		})
	}
	for _, sl := range res.Symlinks {
		sources = append(sources, &adapterv1.WorkspaceSource{
			Type: "symlink", Path: sl.Path, LinkTarget: sl.Target,
		})
	}
	return sources, uploads
}

// modeOctalString renders a file mode's permission bits as the octal string
// the adapter's mkdir / uploadFile materializer parses, mirroring
// podsession.modeOctal.
func modeOctalString(mode os.FileMode) string {
	return "0" + strconv.FormatUint(uint64(mode.Perm()), 8)
}

// dialCodingAgentAdapter serves a real adapter.Server over an in-memory
// bufconn connection and returns a connected *adapterclient.Client, the same
// concrete type podsession.BindResult.Adapter carries in production.
func dialCodingAgentAdapter(t *testing.T, srv *adapter.Server) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// recordingCheckpointTransport is the adapter's CheckpointTransport double:
// every PUT succeeds and records its byte count into the shared cpStore so
// the gateway checkpointer's Stat-confirm observes what was "uploaded".
type recordingCheckpointTransport struct {
	store *cpStore
}

func (t recordingCheckpointTransport) PutChunk(_ context.Context, url string, _ map[string]string, _ int64, body io.Reader) (int, string, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return 0, "", err
	}
	t.store.recordPut(url, int64(len(b)))
	return 200, "", nil
}

func (t recordingCheckpointTransport) GetChunk(context.Context, string, map[string]string) (io.ReadCloser, error) {
	return nil, io.EOF
}

// memArtifactCatalog is a minimal in-memory artifactcatalog.Store: a real
// implementation of the §12.5 catalog API sufficient to back both the
// checkpointer's Cataloging seam (Insert via RecordPut) and the
// sessionserver's Artifacts seam (ListBySession), without a live Postgres.
type memArtifactCatalog struct {
	mu   sync.Mutex
	rows map[string]artifactcatalog.Record
}

func newMemArtifactCatalog() *memArtifactCatalog {
	return &memArtifactCatalog{rows: map[string]artifactcatalog.Record{}}
}

func (c *memArtifactCatalog) Insert(_ context.Context, r artifactcatalog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.State == "" {
		r.State = artifactcatalog.StateLive
	}
	c.rows[r.URI] = r
	return nil
}

func (c *memArtifactCatalog) Get(_ context.Context, uri string) (artifactcatalog.Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.rows[uri]
	if !ok {
		return artifactcatalog.Record{}, artifactcatalog.ErrNotFound
	}
	return r, nil
}

func (c *memArtifactCatalog) SoftDelete(_ context.Context, uri string, deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.rows[uri]
	if !ok {
		return artifactcatalog.ErrNotFound
	}
	r.State = artifactcatalog.StateSoftDeleted
	r.TombstoneDeadline = deadline
	c.rows[uri] = r
	return nil
}

func (c *memArtifactCatalog) Tombstone(_ context.Context, uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.rows[uri]
	if !ok {
		return artifactcatalog.ErrNotFound
	}
	r.State = artifactcatalog.StateTombstoned
	c.rows[uri] = r
	return nil
}

func (c *memArtifactCatalog) HardPruneExpired(_ context.Context, now time.Time) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for uri, r := range c.rows {
		if r.State != artifactcatalog.StateLive && !r.TombstoneDeadline.IsZero() && !r.TombstoneDeadline.After(now) {
			delete(c.rows, uri)
			n++
		}
	}
	return n, nil
}

func (c *memArtifactCatalog) ListPrunable(_ context.Context, now time.Time) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for uri, r := range c.rows {
		if r.State != artifactcatalog.StateLive && !r.TombstoneDeadline.IsZero() && !r.TombstoneDeadline.After(now) {
			out = append(out, uri)
		}
	}
	return out, nil
}

func (c *memArtifactCatalog) HardPruneURIs(_ context.Context, uris []string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, uri := range uris {
		if _, ok := c.rows[uri]; ok {
			delete(c.rows, uri)
			n++
		}
	}
	return n, nil
}

func (c *memArtifactCatalog) ListBySession(_ context.Context, tenantID, sessionID string) ([]artifactcatalog.Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []artifactcatalog.Record
	for _, r := range c.rows {
		if r.TenantID == tenantID && r.SessionID == sessionID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (c *memArtifactCatalog) SetLegalHold(_ context.Context, uri string, hold bool, setBy string, setAt time.Time, note string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.rows[uri]
	if !ok {
		return artifactcatalog.ErrNotFound
	}
	r.LegalHold = hold
	if hold {
		r.LegalHoldSetBy, r.LegalHoldSetAt, r.LegalHoldNote = setBy, setAt, note
	} else {
		r.LegalHoldSetBy, r.LegalHoldSetAt, r.LegalHoldNote = "", time.Time{}, ""
	}
	c.rows[uri] = r
	return nil
}

func (c *memArtifactCatalog) ListLegalHeld(_ context.Context, tenantID string) ([]artifactcatalog.Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []artifactcatalog.Record
	for _, r := range c.rows {
		if r.TenantID == tenantID && r.LegalHold {
			out = append(out, r)
		}
	}
	return out, nil
}

func (c *memArtifactCatalog) IsLegalHeldAt(_ context.Context, tenantID, sessionID string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.rows {
		if r.TenantID == tenantID && r.SessionID == sessionID && r.LegalHold {
			return true, nil
		}
	}
	return false, nil
}

func (c *memArtifactCatalog) SessionsWithLegalHoldAndCheckpoints(_ context.Context) ([]artifactcatalog.SessionRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[artifactcatalog.SessionRef]bool{}
	var out []artifactcatalog.SessionRef
	for _, r := range c.rows {
		if !r.LegalHold || r.ArtifactType != artifactcatalog.ArtifactTypeCheckpoint {
			continue
		}
		ref := artifactcatalog.SessionRef{TenantID: r.TenantID, SessionID: r.SessionID}
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out, nil
}

func (c *memArtifactCatalog) SumLiveBytes(_ context.Context, tenantID string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total int64
	for _, r := range c.rows {
		if r.TenantID == tenantID && r.State == artifactcatalog.StateLive {
			total += r.SizeBytes
		}
	}
	return total, nil
}

func (c *memArtifactCatalog) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for uri, r := range c.rows {
		if r.TenantID == tenantID && !r.LegalHold {
			delete(c.rows, uri)
			n++
		}
	}
	return n, nil
}

var _ artifactcatalog.Store = (*memArtifactCatalog)(nil)
