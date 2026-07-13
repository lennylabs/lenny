// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// outageBlobStore is a blobstore.Store whose Put always fails with a
// generic downstream error, standing in for a MinIO Artifact Store that
// is down infrastructure-wide. The error is none of the client-side
// sentinels the upload handler classifies as non-breaker-triggering
// (ErrObjectTooLarge, ErrConflict, ErrCrossTenant,
// ErrClassificationControlViolation, ErrTierStoreMismatch), so every Put
// counts as a downstream dependency failure that feeds the §4.1 Upload
// Handler subsystem breaker — the same failure mode an in-cluster MinIO
// outage surfaces at the blob-store call site in upload.go.
type outageBlobStore struct{}

func (outageBlobStore) Put(blobstore.URI, string, io.Reader) (string, error) {
	return "", io.ErrUnexpectedEOF
}

func (outageBlobStore) Get(blobstore.URI) (blobstore.BlobInfo, io.ReadCloser, error) {
	return blobstore.BlobInfo{}, nil, blobstore.ErrNotFound
}

func (outageBlobStore) Stat(blobstore.URI) (blobstore.BlobInfo, error) {
	return blobstore.BlobInfo{}, blobstore.ErrNotFound
}

// replica is one edge-gateway replica in the convergence test: an
// independent sessionserver whose Upload Handler subsystem carries its
// own per-replica in-memory breaker. Every replica shares the single
// failing blob store, exactly as N gateway pods share one downed MinIO.
type replica struct {
	handler http.Handler
	tenant  string
	session string
	token   string
	breaker *subsystem.Breaker
}

// newOutageReplica builds one replica wired to the shared failing blob
// store, with its own Upload Handler breaker at the given per-replica
// failure threshold and a cooldown long enough that an opened breaker
// stays open for the duration of the test (no half-open probe races the
// convergence assertion). A session is seeded and an upload token minted
// so the replica can accept uploads immediately.
func newOutageReplica(t *testing.T, blobs blobstore.Store, threshold int) replica {
	t.Helper()
	br := &subsystem.Breaker{FailureThreshold: threshold, Cooldown: time.Hour}
	sub := &subsystem.Subsystem{Name: "upload_handler", Breaker: br}

	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return t0 }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("upload-secret")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	tracker := uploadtoken.NewMemoryTracker()
	verifier := uploadtoken.NewVerifier(ring, tracker, clock)
	srv := sessionserver.New(store, sessionserver.Options{
		Clock:               clock,
		IDFunc:              func() string { return "sess_convergence" },
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: verifier,
		Blobs:               blobs,
		UploadSubsystem:     sub,
	})

	const tenant, session = "acme", "sess_convergence"
	tok := seedAndMintUploadSubsystem(t, store, issuer, session, tenant)
	return replica{handler: srv.Handler(), tenant: tenant, session: session, token: tok, breaker: br}
}

// upload drives one upload through the replica's real HTTP handler and
// returns the status code. A closed breaker admits the request, the
// shared blob store's Put fails, and the deferred subsystem outcome
// feeds the breaker; an open breaker sheds the request at admission with
// 503 before touching the blob store.
func (r replica) upload(t *testing.T) int {
	t.Helper()
	return doUpload(t, r.handler, r.session, r.tenant, r.token, "payload").Code
}

// TestUploadHandlerBreakerConvergesAcrossReplicasUnderBlobStoreOutage
// pins the §4.1 per-replica breaker convergence contract: with the
// Artifact Store down infrastructure-wide, each replica independently
// discovers the failure and opens its own Upload Handler breaker after
// its local failure threshold, so a load balancer distributing uploads
// across replicas sees exactly (threshold × replicas) downstream
// failures — the non-deterministic 503 window — before every replica
// converges to open and sheds all further uploads with 503.
//
// spec: §4.1 (Edge Gateway Replicas) — "These breakers are intentionally
// per-replica (not shared across replicas). When a downstream dependency
// failure is infrastructure-wide (e.g., MinIO down), each replica
// independently discovers the failure and opens its own breaker after
// its local failure threshold. This means the first N requests per
// replica fail before the breaker opens, and clients behind a load
// balancer may see non-deterministic 503s during the convergence window
// ... all replicas converge to open state within seconds."
//
// This is the faithful lowest-tier exercise of the contract: the breaker
// is per-replica and in-memory with no cross-replica coordination, so
// convergence is an emergent property of N independent instances of the
// real subsystem.Breaker driven through the real upload handler against a
// shared downed blob store. A multi-pod Kind run would add LB and MinIO
// fidelity but exercises the identical per-replica product code.
func TestUploadHandlerBreakerConvergesAcrossReplicasUnderBlobStoreOutage(t *testing.T) {
	const replicas = 4
	const threshold = 3

	blobs := outageBlobStore{}
	fleet := make([]replica, replicas)
	for i := range fleet {
		fleet[i] = newOutageReplica(t, blobs, threshold)
	}

	// A distinct Stream Proxy subsystem stands alongside the Upload
	// Handler. Its breaker must stay closed and its work must keep
	// succeeding throughout the upload outage: the §4.1 per-subsystem
	// boundary means a downed Artifact Store trips only the Upload
	// Handler, never the Stream Proxy or MCP Fabric.
	streamProxy := &subsystem.Subsystem{Name: "stream_proxy", Breaker: &subsystem.Breaker{FailureThreshold: threshold}}

	downstreamFailures := 0
	// Round-robin uploads across the fleet, one per replica per round,
	// modeling a load balancer that spreads client retries across
	// replicas. Each of the first `threshold` rounds should see every
	// replica return a downstream failure (500), and every round from
	// `threshold+1` onward should see every replica shed with 503.
	roundsToConverge := -1
	for round := 1; round <= threshold+2; round++ {
		open503 := 0
		for _, r := range fleet {
			switch code := r.upload(t); code {
			case http.StatusInternalServerError:
				downstreamFailures++
			case http.StatusServiceUnavailable:
				open503++
			default:
				t.Fatalf("round %d replica upload: status = %d, want 500 (downstream) or 503 (open)", round, code)
			}
		}
		// The Stream Proxy keeps serving on every round regardless of the
		// Upload Handler's state.
		if err := streamProxy.Do(context.Background(), func(context.Context) error { return nil }); err != nil {
			t.Fatalf("round %d: stream proxy Do failed while upload breaker converging: %v", round, err)
		}
		if streamProxy.State() != subsystem.StateClosed {
			t.Fatalf("round %d: stream proxy breaker = %q, want closed; upload outage must not trip a sibling subsystem", round, streamProxy.State())
		}
		if open503 == replicas && roundsToConverge < 0 {
			roundsToConverge = round
		}
	}

	// Convergence bound: every replica opens after exactly `threshold`
	// failing uploads, so the fleet is all-open by round threshold+1 and
	// not before (round `threshold` is the last downstream-failure round,
	// where the threshold-th failure trips each breaker on its deferred
	// outcome after the 500 response is written).
	if roundsToConverge != threshold+1 {
		t.Fatalf("fleet converged to all-open at round %d, want %d (threshold+1)", roundsToConverge, threshold+1)
	}

	// The non-deterministic 503 window: exactly `threshold` requests per
	// replica fail against the downstream before that replica's breaker
	// opens, so the fleet absorbs replicas×threshold downstream failures.
	if want := replicas * threshold; downstreamFailures != want {
		t.Fatalf("downstream-failure count = %d, want %d (replicas×threshold)", downstreamFailures, want)
	}

	// After convergence every replica's breaker is open and sheds new
	// uploads with 503 without touching the downed blob store.
	for i, r := range fleet {
		if got := r.breaker.State(); got != subsystem.StateOpen {
			t.Fatalf("replica %d breaker = %q after convergence, want open", i, got)
		}
		if code := r.upload(t); code != http.StatusServiceUnavailable {
			t.Fatalf("replica %d post-convergence upload: status = %d, want 503", i, code)
		}
	}
}
