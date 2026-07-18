// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §4.4 grant re-mint on expiry, driven
// end to end by the real adapter checkpoint stream against the real gateway
// upload driver.
//
// A chunk PUT that outlives its grant's capability TTL is retried within the
// §4.4 retry budget, and once the grant expires the adapter's own
// grant-expiry check requests a fresh grant for the same index on the open
// stream. The gateway re-signs the identical object key and Content-Length
// without taking a second storage reservation, and the chunk confirms exactly
// once. This exercises the real adapter's grantExpired path against a real
// short capability TTL, so the gateway populating CheckpointGrant.expires_at
// is load-bearing: a grant sent without an expiry leaves the re-mint path
// dead and the retry replays the expired signature instead of recovering.
//
// spec: §4.4 lines 261-264 (retry budget, grant re-mint on expiry),
// §10.1 line 131 (monotonic confirm counter), §13.2 (capability expiry).

package tier4_integration_test

import (
	"context"
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
	"google.golang.org/grpc/credentials/insecure"
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
)

// countingPresigner mints grant URLs against a cpStore and counts how many
// grants the gateway signed per chunk object key (PartID), so a test can
// assert a chunk was re-minted. The signed capability expiry is passed
// through on every grant so the adapter can observe the capability window.
type countingPresigner struct {
	store *cpStore
	mu    sync.Mutex
	perID map[string]int
}

func newCountingPresigner(store *cpStore) *countingPresigner {
	return &countingPresigner{store: store, perID: map[string]int{}}
}

func (p *countingPresigner) PresignPut(u blobstore.URI, contentLength int64, ttl time.Duration) (blobstore.Grant, error) {
	exp := time.Now().Add(ttl)
	url := fmt.Sprintf("https://obj.test/%s/%s?len=%d&exp=%d", u.SessionID, u.PartID, contentLength, exp.UnixNano())
	p.store.registerGrant(url, u)
	p.mu.Lock()
	p.perID[u.PartID]++
	p.mu.Unlock()
	return blobstore.Grant{URL: url, ExpiresAt: exp}, nil
}

func (p *countingPresigner) PresignGet(u blobstore.URI, _ time.Duration) (blobstore.Grant, error) {
	return blobstore.Grant{URL: "https://obj.test/get"}, nil
}

// grantsForChunk returns the number of grants the gateway signed for chunk
// index. The PartID the gateway signs is `{checkpoint_id}/chunk-{n}.{enc}`,
// so a count keyed on the zero-padded index counts every mint for that chunk.
func (p *countingPresigner) grantsForChunk(index int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	total := 0
	marker := fmt.Sprintf("chunk-%05d.", index)
	for id, n := range p.perID {
		if strings.Contains(id, marker) {
			total += n
		}
	}
	return total
}

// flakyPutTransport is the adapter's CheckpointTransport double: it fails the
// first PUT with a transport-level error (an unreachable object store, which
// the adapter retries), then records subsequent PUTs into the shared cpStore
// so the gateway's Stat confirm observes the uploaded bytes. Failing the
// first PUT forces the adapter into its retry loop; with a short capability
// TTL the grant expires before the retry, so the adapter re-mints.
type flakyPutTransport struct {
	store *cpStore
	mu    sync.Mutex
	puts  int
	// okBytes is the Content-Length of the PUT that succeeded.
	okBytes int64
}

func (t *flakyPutTransport) PutChunk(_ context.Context, url string, _ map[string]string, contentLength int64, body io.Reader) (int, string, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return 0, "", err
	}
	t.mu.Lock()
	t.puts++
	n := t.puts
	t.mu.Unlock()
	if n == 1 {
		// Transport-level failure: the object store is unreachable. The adapter
		// retries within the §4.4 budget, and by the retry the grant has
		// expired, so it requests a fresh one.
		return 0, "", fmt.Errorf("object store unreachable")
	}
	t.store.recordPut(url, int64(len(b)))
	t.mu.Lock()
	t.okBytes = contentLength
	t.mu.Unlock()
	return 200, "", nil
}

func (t *flakyPutTransport) GetChunk(context.Context, string, map[string]string) (io.ReadCloser, error) {
	return nil, io.EOF
}

func (t *flakyPutTransport) successBytes() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.okBytes
}

// realAdapterCheckpointClient serves a real adapter Server with the given
// checkpoint transport and a seeded one-file workspace over bufconn,
// returning a connected adapter client for the gateway to drive.
func realAdapterCheckpointClient(t *testing.T, transport adapter.CheckpointTransport) *adapterclient.Client {
	t.Helper()
	s := adapter.New("checkpoint-grant-remint")
	s.WorkspaceRoot = t.TempDir()
	s.StagingDir = t.TempDir()
	s.CheckpointTransport = transport
	if err := os.WriteFile(filepath.Join(s.WorkspaceRoot, "state.txt"), []byte("agent workspace state"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
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

// spec: §4.4 — a grant that expires mid-retry is re-minted for the same index
// on the open stream. The real adapter's grant-expiry check fires against a
// short capability TTL, so the gateway must stamp CheckpointGrant.expires_at
// for the re-mint to happen; the gateway re-signs the identical key and
// Content-Length without a second reservation, and the chunk confirms once.
// diagnosis: the real adapter never re-minted an expired grant end to end.
// Either the gateway sent the CheckpointGrant without expires_at (so the
// adapter cannot tell the capability expired and replays a dead signature),
// or the re-mint double-reserved or double-confirmed the chunk. Re-check
// pkg/gateway/checkpoint/checkpointer mintGrant (expires_at) and
// pkg/adapter/checkpoint grantExpired.
func TestCheckpointReMintsExpiredGrantEndToEnd(t *testing.T) {
	store := newCPStore()
	presigner := newCountingPresigner(store)
	transport := &flakyPutTransport{store: store}
	client := realAdapterCheckpointClient(t, transport)

	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: cpSession, TenantID: cpTenant, Adapter: client})
	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: cpSession, TenantID: cpTenant, State: session.StateRunning, RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	manifests := partialmanifeststore.NewMemoryStore(nil)
	quota := storagequota.NewMemory()
	cp := &checkpointer.Checkpointer{
		Sessions:      sessions,
		Registry:      registry,
		Manifests:     manifests,
		Quota:         quota,
		QuotaLimitFor: func(context.Context, string) (int64, error) { return 1 << 40, nil },
		Presigner:     presigner,
		ObjectStore:   store,
		ChunkObjects:  store,
		Deadline:      10 * time.Second,
		// A capability TTL far shorter than the retry budget's initial delay
		// (200 ms for a periodic trigger), so the first grant is expired by the
		// time the adapter retries the failed PUT.
		CapabilityTTL: 20 * time.Millisecond,
	}

	if err := cp.CheckpointWithTrigger(context.Background(), cpTenant, cpSession, checkpoint.TriggerPeriodic); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	// The single chunk's original grant expired before the retry, so the
	// adapter requested a fresh grant: the gateway signed chunk 0 twice.
	if got := presigner.grantsForChunk(0); got != 2 {
		t.Fatalf("grants signed for chunk 0 = %d, want 2 (original + expiry re-mint)", got)
	}

	rec, err := manifests.Get(context.Background(), cpTenant, latestFinalisedID(t, sessions))
	if err != nil {
		t.Fatalf("get finalised manifest: %v", err)
	}
	// The chunk confirmed exactly once despite the re-mint.
	if rec.ChunkCount != 1 {
		t.Errorf("chunk_count = %d, want 1 (index confirmed once)", rec.ChunkCount)
	}
	// The re-mint took no second reservation: the tenant counter reflects the
	// single confirmed chunk's bytes, not a doubled reservation.
	used, uerr := quota.Used(context.Background(), cpTenant)
	if uerr != nil {
		t.Fatalf("quota Used: %v", uerr)
	}
	if want := transport.successBytes(); used != want {
		t.Errorf("storage counter = %d, want %d (single reservation reconciled to the confirmed chunk)", used, want)
	}
}

// latestFinalisedID resolves the finalised checkpoint_id the session's
// workspace-snapshot ref names, so the test reads the durable manifest the
// successful attempt finalised.
func latestFinalisedID(t *testing.T, sessions sessionstore.Store) string {
	t.Helper()
	row, err := sessions.Get(context.Background(), cpTenant, cpSession)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref == "" {
		t.Fatalf("session has no finalised workspace-snapshot ref")
	}
	return row.WorkspaceSnapshot.Ref
}
