// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §12.5 checkpoint chunk-release paths
// against the real compose stack (Postgres + Redis + MinIO). Every arm that
// abandons or drops a checkpoint — a stream-truncation abort, a deadline-fire
// row a resume never consumed, a supersede, the §12.5 partial-manifest
// backstop, and the latest-2 rotation of a completed checkpoint — runs the
// same release: it soft-deletes each chunk's artifact_store row (issuing the
// §12.5 rule 4 Redis decrement exactly once), removes the chunk objects from
// MinIO, and releases the row's outstanding reservation with the §11.2 guarded
// exactly-once relative decrement. This test drives those releases against real
// stores and asserts the tenant's storage_bytes_used counter returns to its
// baseline for every arm, that a concurrent second backstop sweep decrements
// nothing twice, and that the backstop emits its gc_collected cleanup outcome.
//
// spec: §12.5 partial-manifest backstop and GC rules 4, 6; §11.2 reservation
// release is exactly-once and guarded; §12.5 GC rule 5 (latest-2 rotation).

package tier4_integration_test

import (
	"bytes"
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/blobstore/cataloging"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota/redisstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pgtenant"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// releaseInfra bundles the real stores the checkpoint-release paths run against.
type releaseInfra struct {
	pool      *pgxpool.Pool
	manifests *partialmanifestpg.Store
	catalog   *artifactcatalog.PgStore
	minio     *miniostore.Store
	cataloged *cataloging.Store
	counter   *redisstore.Store
}

func newReleaseInfra(t *testing.T) *releaseInfra {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rdb := containers.StartRedis(t, containers.RedisOptions{})
	mio := containers.StartMinIO(t, containers.MinIOOptions{})

	minioStore, err := miniostore.New(miniostore.Config{
		Endpoint:  mio.Endpoint,
		AccessKey: mio.AccessKey,
		SecretKey: mio.SecretKey,
		Bucket:    mio.Bucket,
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("miniostore.New: %v", err)
	}
	catalog := artifactcatalog.New(pg.Pool, nil)
	counter := redisstore.New(rdb.Client)
	cataloged := cataloging.New(minioStore, catalog, cataloging.Options{QuotaReleaser: counter})
	return &releaseInfra{
		pool:      pg.Pool,
		manifests: partialmanifestpg.New(pg.Pool, nil),
		catalog:   catalog,
		minio:     minioStore,
		cataloged: cataloged,
		counter:   counter,
	}
}

// releaseSeed describes one abandoned/dropped checkpoint to reclaim.
type releaseSeed struct {
	tenant         string
	session        string
	checkpointID   string
	reason         string
	partial        bool
	chunkSizes     []int64 // confirmed chunk bytes, in order
	reservedBytes  int64
	baselineBytes  int64 // pre-existing tenant usage from other artifacts
	releaseAtFinal bool  // rotation: release the reservation at finalise time
}

func (s releaseSeed) confirmed() int64 {
	var sum int64
	for _, n := range s.chunkSizes {
		sum += n
	}
	return sum
}

// seed writes the manifest row, the chunk objects, the chunk catalog rows, a
// terminal session row (so the §12.5 backstop selector's terminal-state branch
// fires), and primes the storage counter to the tenant's pre-release usage. It
// returns the finalised manifest record the release runs against.
func (in *releaseInfra) seed(t *testing.T, s releaseSeed) partialmanifeststore.Record {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	prefix := "/" + s.tenant + "/checkpoints/" + s.session + "/" + s.checkpointID + "/"

	// The checkpoint_manifest, sessions, and artifact_store rows all reference
	// tenants(id); register the tenant first.
	if _, err := in.pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00') ON CONFLICT DO NOTHING`,
		s.tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	if err := in.manifests.Put(ctx, partialmanifeststore.Record{
		TenantID:             s.tenant,
		CheckpointID:         s.checkpointID,
		SessionID:            s.session,
		ChunkObjectKeyPrefix: prefix,
		ChunkSizeBytes:       16 << 20,
		ChunkEncoding:        partialmanifeststore.ChunkEncodingTar,
		ReservedBytes:        s.reservedBytes,
		CheckpointStartedAt:  now,
		CheckpointTimeoutAt:  now.Add(90 * time.Second),
	}); err != nil {
		t.Fatalf("Put manifest: %v", err)
	}
	// Write each confirmed chunk as a real MinIO object and a live catalog row,
	// then advance the manifest's confirmed counters.
	for i, size := range s.chunkSizes {
		u := resumechunks.ChunkObjectURI(s.tenant, s.session, s.checkpointID, uint32(i), partialmanifeststore.ChunkEncodingTar)
		u.TTL = time.Hour
		if _, err := in.minio.Put(u, "application/octet-stream", bytes.NewReader(make([]byte, size))); err != nil {
			t.Fatalf("put chunk %d: %v", i, err)
		}
		if err := in.catalog.Insert(ctx, artifactcatalog.Record{
			URI:          resumechunks.ChunkObjectURI(s.tenant, s.session, s.checkpointID, uint32(i), partialmanifeststore.ChunkEncodingTar).String(),
			TenantID:     s.tenant,
			SessionID:    s.session,
			PartID:       u.PartID,
			SizeBytes:    size,
			State:        artifactcatalog.StateLive,
			ArtifactType: artifactcatalog.ArtifactTypeCheckpoint,
		}); err != nil {
			t.Fatalf("insert catalog row %d: %v", i, err)
		}
		if err := in.manifests.ConfirmChunk(ctx, s.tenant, s.checkpointID, i, s.confirmed()); err != nil {
			t.Fatalf("ConfirmChunk %d: %v", i, err)
		}
	}
	if err := in.manifests.Finalise(ctx, s.tenant, s.checkpointID, s.partial, s.reason); err != nil {
		t.Fatalf("Finalise: %v", err)
	}
	// The rotation arm releases the reservation at finalise time, so its
	// outstanding headroom is not carried in the counter the release drops.
	headroom := s.reservedBytes - s.confirmed()
	if s.releaseAtFinal {
		if _, err := in.manifests.ReleaseReservation(ctx, s.tenant, s.checkpointID); err != nil {
			t.Fatalf("ReleaseReservation at finalise: %v", err)
		}
		headroom = 0
	}
	// The §11.2 reservation-aware counter holds the tenant's live artifact bytes
	// plus every outstanding reservation's headroom.
	if err := in.counter.Set(ctx, s.tenant, s.baselineBytes+s.confirmed()+headroom); err != nil {
		t.Fatalf("prime counter: %v", err)
	}
	seedTerminalSession(t, in, s.tenant, s.session)

	rec, err := in.manifests.Get(ctx, s.tenant, s.checkpointID)
	if err != nil {
		t.Fatalf("Get manifest: %v", err)
	}
	return rec
}

// seedTerminalSession inserts a terminal (completed) session row so the §12.5
// backstop selector's terminal-state join reclaims the manifest immediately.
func seedTerminalSession(t *testing.T, in *releaseInfra, tenant, sessionID string) {
	t.Helper()
	if err := pgtenant.InTx(context.Background(), in.pool, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
			 VALUES ($1, $2, 'completed', 'echo', gen_random_uuid())`,
			sessionID, tenant)
		return err
	}); err != nil {
		t.Fatalf("seed terminal session: %v", err)
	}
}

// spec: §12.5 partial-manifest backstop and GC rule 4; §11.2 reservation
// release is exactly-once and guarded; §12.5 GC rule 5 — every release arm
// returns the tenant's storage_bytes_used to its baseline: the confirmed
// chunks' bytes are released by the guarded catalog soft-delete, the reservation
// headroom by the guarded relative decrement, and the chunk objects are removed
// from MinIO.
// diagnosis: a release arm left storage_bytes_used above baseline, so
// confirmed-chunk bytes, reservation headroom, or the MinIO objects leak
// after the checkpoint is released.
func TestCheckpointReleaseArmsReturnStorageToBaseline(t *testing.T) {
	in := newReleaseInfra(t)
	ctx := context.Background()

	const baseline = 1000

	arms := []struct {
		name  string
		seed  releaseSeed
		final bool // rotation: reservation released at finalise, dropped via the latest-2 release
	}{
		{
			name: "abort_stream_truncated",
			seed: releaseSeed{reason: partialmanifeststore.ReasonStreamTruncated, partial: true},
		},
		{
			name: "resume_deadline_fire",
			seed: releaseSeed{reason: partialmanifeststore.ReasonTimeout, partial: true},
		},
		{
			name: "supersede",
			seed: releaseSeed{reason: partialmanifeststore.ReasonSuperseded, partial: true},
		},
		{
			name: "backstop",
			seed: releaseSeed{reason: partialmanifeststore.ReasonQuotaExceeded, partial: true},
		},
		{
			name:  "rotation_complete",
			seed:  releaseSeed{reason: partialmanifeststore.ReasonComplete, partial: false, releaseAtFinal: true},
			final: true,
		},
	}

	for _, arm := range arms {
		arm := arm
		t.Run(arm.name, func(t *testing.T) {
			s := arm.seed
			s.tenant = "acme-" + arm.name
			s.session = uuid.NewString()
			s.checkpointID = uuid.NewString()
			s.chunkSizes = []int64{100, 150}
			s.reservedBytes = 400
			s.baselineBytes = baseline
			rec := in.seed(t, s)
			prefix := resumechunks.ChunkObjectURI(s.tenant, s.session, s.checkpointID, 0, partialmanifeststore.ChunkEncodingTar)
			prefix.PartID = s.checkpointID + "/"

			if arm.final {
				// Rotation drops a completed (partial = false) checkpoint via the
				// latest-2 release; its reservation was released at finalise.
				if err := checkpointer.ReleaseCheckpointChunks(ctx, checkpointer.CheckpointChunkRelease{
					Manifests:          in.manifests,
					Catalog:            in.cataloged,
					Objects:            in.minio,
					TombstoneRetention: time.Hour,
				}, rec); err != nil {
					t.Fatalf("ReleaseCheckpointChunks: %v", err)
				}
			} else {
				// The abandoned partial arms are reclaimed by the §12.5 backstop.
				// Assert the ListReclaimable selector returns this arm's row.
				assertReclaimableIncludes(t, in, s.checkpointID)
				metrics := &releaseMetricsStub{}
				if err := checkpointer.ReclaimAbandonedManifest(ctx, checkpointer.BackstopReclaim{
					Manifests:          in.manifests,
					Catalog:            in.cataloged,
					Objects:            in.minio,
					Quota:              in.counter,
					TombstoneRetention: time.Hour,
					Metrics:            metrics,
				}, rec); err != nil {
					t.Fatalf("ReclaimAbandonedManifest: %v", err)
				}
				if got := metrics.count(string(checkpointer.PartialCleanupGCCollected)); got != 1 {
					t.Errorf("gc_collected metric count = %d, want 1", got)
				}
			}

			// storage_bytes_used returns to baseline.
			used, err := in.counter.Used(ctx, s.tenant)
			if err != nil {
				t.Fatalf("Used: %v", err)
			}
			if used != baseline {
				t.Errorf("storage_bytes_used = %d, want %d (baseline)", used, baseline)
			}
			// The chunk objects are gone from MinIO.
			remaining, err := in.minio.ListByPrefix(ctx, prefix)
			if err != nil {
				t.Fatalf("ListByPrefix: %v", err)
			}
			if len(remaining) != 0 {
				t.Errorf("chunk objects remaining under prefix = %d, want 0", len(remaining))
			}
			// The manifest row is soft-deleted.
			row, err := in.manifests.Get(ctx, s.tenant, s.checkpointID)
			if err != nil {
				t.Fatalf("Get manifest after release: %v", err)
			}
			if row.DeletedAt.IsZero() {
				t.Error("release did not soft-delete the manifest row")
			}
		})
	}
}

// spec: §11.2 reservation release is exactly-once and guarded; §12.5 GC rule 4
// — two concurrent backstop sweeps of the same abandoned row decrement the
// reservation and the chunk bytes exactly once: the guarded ReleaseReservation
// and the guarded catalog soft-delete each admit a single writer, so the
// counter lands on baseline rather than being driven below it.
// diagnosis: two concurrent backstop sweeps of the same abandoned row
// double-decremented the reservation or the chunk bytes, driving the
// counter below baseline.
func TestCheckpointBackstopConcurrentSweepDecrementsOnce(t *testing.T) {
	in := newReleaseInfra(t)
	ctx := context.Background()

	const baseline = 1000
	s := releaseSeed{
		tenant:        "acme-concurrent",
		session:       uuid.NewString(),
		checkpointID:  uuid.NewString(),
		reason:        partialmanifeststore.ReasonTimeout,
		partial:       true,
		chunkSizes:    []int64{100, 150},
		reservedBytes: 400,
		baselineBytes: baseline,
	}
	rec := in.seed(t, s)
	cfg := checkpointer.BackstopReclaim{
		Manifests:          in.manifests,
		Catalog:            in.cataloged,
		Objects:            in.minio,
		Quota:              in.counter,
		TombstoneRetention: time.Hour,
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = checkpointer.ReclaimAbandonedManifest(ctx, cfg, rec)
		}(i)
	}
	wg.Wait()
	// At least one sweep completes; the other may race the object delete and
	// defer, but neither may double-decrement.
	if errs[0] != nil && errs[1] != nil {
		t.Fatalf("both concurrent sweeps failed: %v / %v", errs[0], errs[1])
	}

	used, err := in.counter.Used(ctx, s.tenant)
	if err != nil {
		t.Fatalf("Used: %v", err)
	}
	if used != baseline {
		t.Errorf("storage_bytes_used after concurrent sweeps = %d, want %d (decrement exactly once)", used, baseline)
	}
}

// assertReclaimableIncludes fails the test unless the §12.5 ListReclaimable
// selector returns the row for checkpointID.
func assertReclaimableIncludes(t *testing.T, in *releaseInfra, checkpointID string) {
	t.Helper()
	rows, err := in.manifests.ListReclaimable(context.Background(), 15*time.Minute)
	if err != nil {
		t.Fatalf("ListReclaimable: %v", err)
	}
	for _, r := range rows {
		if r.CheckpointID == checkpointID {
			return
		}
	}
	t.Fatalf("ListReclaimable did not return the abandoned row %s", checkpointID)
}

// releaseMetricsStub captures the partial-manifest cleanup outcomes the backstop
// emits.
type releaseMetricsStub struct {
	mu       sync.Mutex
	outcomes map[string]int
}

func (m *releaseMetricsStub) IncPartialManifestCleanup(outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outcomes == nil {
		m.outcomes = map[string]int{}
	}
	m.outcomes[outcome]++
}

func (m *releaseMetricsStub) count(outcome string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.outcomes[outcome]
}
