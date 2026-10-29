// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// noCopyBlobs hides the memory store's Copy, so the server sees a blob
// store without blobstore.Copier.
type noCopyBlobs struct{ blobstore.Store }

// failingCopyBlobs is a memory store whose Copy fails with a store error
// that is neither ErrNotFound nor ErrConflict.
type failingCopyBlobs struct{ *blobstore.MemoryStore }

var errCopyBroken = errors.New("object store copy failed")

func (failingCopyBlobs) Copy(blobstore.URI, blobstore.URI) error { return errCopyBroken }

// copyEndpoint drives one of the two handlers that start a new session from
// a copy of the source session's workspace snapshot.
type copyEndpoint struct {
	name  string
	newID string
	call  func(t *testing.T, h http.Handler) *httptest.ResponseRecorder
}

var copyEndpoints = []copyEndpoint{
	{"derive", "sess_derived", func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
		return deriveRequest(t, h, sessionserver.DeriveRequest{})
	}},
	{"replay prompt_history", "sess_replay", func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
		return replayAs(t, h, "", sessionserver.ReplayRequest{ReplayMode: sessionserver.ReplayPromptHistory, TargetRuntime: "x"})
	}},
	{"replay workspace_derive", "sess_replay", func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
		return replayAs(t, h, "", sessionserver.ReplayRequest{ReplayMode: sessionserver.ReplayWorkspaceDerive, TargetRuntime: "x"})
	}},
}

// copyServer builds a server over f whose IDFunc hands out ids in order.
func copyServer(f *fenceFixture, blobs blobstore.Store, ids []string, opts ...func(*sessionserver.Options)) http.Handler {
	o := sessionserver.Options{
		DevTenantHeader: true,
		IDFunc: func() string {
			id := ids[0]
			ids = append(ids[1:], "id_exhausted")
			return id
		},
		Blobs: blobs, Users: f.users, Tenants: f.tenants,
	}
	for _, m := range opts {
		m(&o)
	}
	return sessionserver.New(f.store, o).Handler()
}

// readWorkspace downloads id's workspace through GET /v1/sessions/{id}/workspace.
func readWorkspace(t *testing.T, h http.Handler, id string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+id+"/workspace", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	body, _ := io.ReadAll(rr.Body)
	return rr.Code, string(body)
}

// TestSnapshotCopyStoresRefOfCopiedObject_spec_7_1 pins the legacy
// single-object copy on derive and on both replay modes: the new row's
// WorkspaceSnapshot.Ref names an object under the new session's own ID that
// holds the source's bytes, the ref differs from the source's, the source
// object is untouched, and the workspace download of the new session
// serves the copied bytes.
// spec: 7.1 (Normal Flow: Derive copy semantics), 15.1 (REST API: Session Replay Semantics), 4.5 (Artifact Store)
func TestSnapshotCopyStoresRefOfCopiedObject_spec_7_1(t *testing.T) {
	for _, ep := range copyEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			f, _, _ := replayFixture(t)
			h := copyServer(f, f.blobs, []string{ep.newID})
			if rr := ep.call(t, h); rr.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
			}
			row, err := f.mem.Get(context.Background(), "acme", ep.newID)
			if err != nil {
				t.Fatalf("new row: %v", err)
			}
			src, _ := f.mem.Get(context.Background(), "acme", "sess_source")
			if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref == src.WorkspaceSnapshot.Ref {
				t.Fatalf("new row snapshot = %+v, want a ref distinct from the source's", row.WorkspaceSnapshot)
			}
			if row.ParentWorkspaceRef != src.WorkspaceSnapshot.Ref {
				t.Errorf("ParentWorkspaceRef = %q, want the source ref", row.ParentWorkspaceRef)
			}
			uri, err := blobstore.ParseURI(row.WorkspaceSnapshot.Ref)
			if err != nil {
				t.Fatalf("stored ref %q is not an object URI: %v", row.WorkspaceSnapshot.Ref, err)
			}
			if uri.SessionID != ep.newID {
				t.Errorf("copied object session = %q, want %q", uri.SessionID, ep.newID)
			}
			_, body, err := f.blobs.Get(uri)
			if err != nil {
				t.Fatalf("no object behind the stored ref %q: %v", row.WorkspaceSnapshot.Ref, err)
			}
			got, _ := io.ReadAll(body)
			_ = body.Close()
			if string(got) != "workspace-bytes" {
				t.Errorf("copied bytes = %q, want the source's", got)
			}
			if code, dl := readWorkspace(t, h, ep.newID); code != http.StatusOK || dl != "workspace-bytes" {
				t.Errorf("workspace download = %d %q, want 200 with the source's bytes", code, dl)
			}
			if ids := f.reserved(t, "alice@acme.com"); len(ids) != 0 {
				t.Errorf("the insert left the reservation: %v", ids)
			}
		})
	}
}

// chunkCopyStore is a blob store that copies and erases by session: the
// in-memory and filesystem stores both qualify.
type chunkCopyStore interface {
	blobstore.Store
	blobstore.Copier
	DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error)
}

// chunkCopyStores builds each blob store the chunked copy runs on in the
// in-memory and filesystem postures, both on the given clock.
var chunkCopyStores = []struct {
	name string
	new  func(t *testing.T, clock func() time.Time) chunkCopyStore
}{
	{"memory", func(_ *testing.T, clock func() time.Time) chunkCopyStore { return blobstore.NewMemoryStore(clock) }},
	{"filesystem", func(t *testing.T, clock func() time.Time) chunkCopyStore {
		s, err := blobstore.NewFilesystemStore(t.TempDir(), clock)
		if err != nil {
			t.Fatalf("filesystem store: %v", err)
		}
		return s
	}},
}

// TestSnapshotCopyChunkedCheckpoint_spec_10_1 pins the chunked copy on
// derive and on both replay modes, on the in-memory and the filesystem blob
// stores: every chunk of the source's checkpoint is copied under the new
// session's prefix and reads back with the source's bytes, a complete
// derived manifest row is written for the new session, and the stored ref
// is the four-segment checkpoint ref naming that row. The copied chunk
// keeps the source chunk's TTL, measured from the copy, because the chunk
// URI names none; before BUILD-GAPS F-4.5.24 the copy expired as it was
// written and every read of it returned not found.
// spec: 7.1 (Normal Flow: Derive copy semantics), 15.1 (REST API: Session Replay Semantics), 10.1 (Horizontal Scaling), 4.5 (Artifact Store)
func TestSnapshotCopyChunkedCheckpoint_spec_10_1(t *testing.T) {
	enc := partialmanifeststore.ChunkEncodingTar
	const sourceTTL = time.Hour
	for _, st := range chunkCopyStores {
		for _, ep := range copyEndpoints {
			t.Run(st.name+"/"+ep.name, func(t *testing.T) {
				now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
				clock := func() time.Time { return now }
				blobs := st.new(t, clock)
				f, _, _ := replayFixture(t)
				if _, err := f.mem.Update(context.Background(), "acme", "sess_source", func(s *sessionstore.Session) error {
					s.WorkspaceSnapshot.Ref = "/acme/checkpoints/sess_source/ckpt_parent"
					return nil
				}); err != nil {
					t.Fatalf("point the source at its checkpoint: %v", err)
				}
				for n := uint32(0); n < 2; n++ {
					u := resumechunks.ChunkObjectURI("acme", "sess_source", "ckpt_parent", n, enc)
					u.TTL = sourceTTL
					if _, err := blobs.Put(u, "application/x-tar", strings.NewReader("chunk-"+string(rune('a'+n)))); err != nil {
						t.Fatalf("seed chunk %d: %v", n, err)
					}
				}
				// The copy runs 20 minutes into the source chunks' lifetime.
				now = now.Add(20 * time.Minute)
				copiedAt := now
				manifests := partialmanifeststore.NewMemoryStore(nil)
				h := copyServer(f, blobs, []string{ep.newID, "ckpt_new"}, func(o *sessionserver.Options) {
					o.CheckpointManifestReader = parentManifest{partialmanifeststore.Record{
						TenantID: "acme", CheckpointID: "ckpt_parent", SessionID: "sess_source", ChunkCount: 2, ChunkEncoding: enc,
					}}
					o.CheckpointManifestWriter = manifests
				})
				if rr := ep.call(t, h); rr.Code != http.StatusCreated {
					t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
				}
				row, _ := f.mem.Get(context.Background(), "acme", ep.newID)
				if want := "/acme/checkpoints/" + ep.newID + "/ckpt_new"; row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref != want {
					t.Fatalf("stored ref = %+v, want %s", row.WorkspaceSnapshot, want)
				}
				// Each copied chunk reads back with its source's bytes until
				// the source TTL has run from the copy, and not after.
				for _, at := range []struct {
					offset time.Duration
					live   bool
				}{{0, true}, {sourceTTL - time.Minute, true}, {sourceTTL + time.Minute, false}} {
					now = copiedAt.Add(at.offset)
					for n := uint32(0); n < 2; n++ {
						u := resumechunks.ChunkObjectURI("acme", ep.newID, "ckpt_new", n, enc)
						info, body, err := blobs.Get(u)
						if !at.live {
							if !errors.Is(err, blobstore.ErrNotFound) {
								t.Errorf("copied chunk %d at copy+%s: err = %v, want ErrNotFound once the source TTL ran out", n, at.offset, err)
							}
							continue
						}
						if err != nil {
							t.Fatalf("copied chunk %d at copy+%s: %v (F-4.5.24)", n, at.offset, err)
						}
						got, _ := io.ReadAll(body)
						_ = body.Close()
						if want := "chunk-" + string(rune('a'+n)); string(got) != want {
							t.Errorf("copied chunk %d = %q, want %q", n, got, want)
						}
						if want := copiedAt.Add(sourceTTL); !info.ExpiresAt.Equal(want) {
							t.Errorf("copied chunk %d expires at %s, want %s (the source TTL from the copy)", n, info.ExpiresAt, want)
						}
					}
				}
				if n, _ := blobs.DeleteBySession(context.Background(), "acme", ep.newID); n != 2 {
					t.Errorf("%d objects copied under %s, want the 2 chunks", n, ep.newID)
				}
				rec, err := manifests.Get(context.Background(), "acme", "ckpt_new")
				if err != nil {
					t.Fatalf("derived manifest row: %v", err)
				}
				if rec.SessionID != ep.newID || rec.ChunkCount != 2 || rec.Partial {
					t.Errorf("derived manifest = session %s chunks %d partial %v, want %s, 2, complete",
						rec.SessionID, rec.ChunkCount, rec.Partial, ep.newID)
				}
			})
		}
	}
}

// TestSnapshotCopyFailsClosed_spec_7_1 pins the failure paths of the
// snapshot copy on derive and on both replay modes. A source object that is
// absent, a source chunk that is absent, and a source ref that names no
// object answer 503 DERIVE_SNAPSHOT_UNAVAILABLE; a blob store without
// Copier and a store copy error answer 500 INTERNAL_ERROR. In every case
// no row is inserted, nothing is left under the new ID, and the
// reservation is released.
// spec: 7.1 (Normal Flow: Derive copy semantics, Derive session semantics), 15.1 (REST API: Session Replay Semantics), 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestSnapshotCopyFailsClosed_spec_7_1(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, f *fenceFixture) (blobstore.Store, []func(*sessionserver.Options))
		status int
		code   string
	}{
		{
			name: "source object absent",
			setup: func(t *testing.T, f *fenceFixture) (blobstore.Store, []func(*sessionserver.Options)) {
				if _, err := f.blobs.DeleteBySession(context.Background(), "acme", "sess_source"); err != nil {
					t.Fatalf("delete source object: %v", err)
				}
				return f.blobs, nil
			},
			status: http.StatusServiceUnavailable, code: "DERIVE_SNAPSHOT_UNAVAILABLE",
		},
		{
			name: "source chunk absent",
			setup: func(t *testing.T, f *fenceFixture) (blobstore.Store, []func(*sessionserver.Options)) {
				setSourceRef(t, f, "/acme/checkpoints/sess_source/ckpt_parent")
				return f.blobs, []func(*sessionserver.Options){func(o *sessionserver.Options) {
					o.CheckpointManifestReader = parentManifest{partialmanifeststore.Record{
						TenantID: "acme", CheckpointID: "ckpt_parent", SessionID: "sess_source", ChunkCount: 1,
					}}
					o.CheckpointManifestWriter = partialmanifeststore.NewMemoryStore(nil)
				}}
			},
			status: http.StatusServiceUnavailable, code: "DERIVE_SNAPSHOT_UNAVAILABLE",
		},
		{
			name: "source ref names no object",
			setup: func(t *testing.T, f *fenceFixture) (blobstore.Store, []func(*sessionserver.Options)) {
				setSourceRef(t, f, "snap-without-a-path")
				return f.blobs, nil
			},
			status: http.StatusServiceUnavailable, code: "DERIVE_SNAPSHOT_UNAVAILABLE",
		},
		{
			name: "blob store without Copier",
			setup: func(t *testing.T, f *fenceFixture) (blobstore.Store, []func(*sessionserver.Options)) {
				return noCopyBlobs{f.blobs}, nil
			},
			status: http.StatusInternalServerError, code: "INTERNAL_ERROR",
		},
		{
			name: "store copy error",
			setup: func(t *testing.T, f *fenceFixture) (blobstore.Store, []func(*sessionserver.Options)) {
				return failingCopyBlobs{f.blobs}, nil
			},
			status: http.StatusInternalServerError, code: "INTERNAL_ERROR",
		},
	}
	for _, ep := range copyEndpoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				f, _, _ := replayFixture(t)
				blobs, opts := tc.setup(t, f)
				h := copyServer(f, blobs, []string{ep.newID, "ckpt_new"}, opts...)
				rr := ep.call(t, h)
				if rr.Code != tc.status {
					t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.status, rr.Body.String())
				}
				if code, _, _ := decodeError(t, rr); code != tc.code {
					t.Errorf("code = %s, want %s", code, tc.code)
				}
				if _, err := f.mem.Get(context.Background(), "acme", ep.newID); !errors.Is(err, sessionstore.ErrNotFound) {
					t.Errorf("the failed copy inserted a row (err %v)", err)
				}
				if f.store.callsOf("Create")+f.store.callsOf("CreateReserved")+f.store.callsOf("CreateWithTranscript") != 0 {
					t.Errorf("the failed copy reached an insert: %v", f.store.calls)
				}
				if n, _ := f.blobs.DeleteBySession(context.Background(), "acme", ep.newID); n != 0 {
					t.Errorf("the failed copy left %d objects under %s", n, ep.newID)
				}
				if ids := f.reserved(t, "alice@acme.com"); len(ids) != 0 {
					t.Errorf("the failed copy left its reservation: %v", ids)
				}
			})
		}
	}
}

// TestReplayReservationBracketsCopy_spec_12_8 pins the replay's reservation
// around the snapshot copy: a refused reservation answers 403 before any
// object is copied under the new ID, and an insert refused because the
// owner's erasure ended the reservation after the copy deletes the copied
// object, releases the reservation, and answers 403, in both replay modes.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API: Session Replay Semantics)
func TestReplayReservationBracketsCopy_spec_12_8(t *testing.T) {
	refusal := sessionstore.ReservationEndedRefusal("alice@acme.com")
	t.Run("reservation refused", func(t *testing.T) {
		f, _, _ := replayFixture(t)
		f.store.reserveErr = refusal
		h := copyServer(f, f.blobs, []string{"sess_replay"})
		rr := replayAs(t, h, "", sessionserver.ReplayRequest{ReplayMode: sessionserver.ReplayWorkspaceDerive, TargetRuntime: "x"})
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
		}
		if n, _ := f.blobs.DeleteBySession(context.Background(), "acme", "sess_replay"); n != 0 {
			t.Errorf("the refused replay copied %d objects", n)
		}
	})
	for _, mode := range []sessionserver.ReplayMode{sessionserver.ReplayPromptHistory, sessionserver.ReplayWorkspaceDerive} {
		t.Run("insert refused after the copy/"+string(mode), func(t *testing.T) {
			f, _, _ := replayFixture(t)
			f.store.createReservedErr = refusal
			f.store.createWithTransErr = refusal
			h := copyServer(f, f.blobs, []string{"sess_replay"})
			rr := replayAs(t, h, "", sessionserver.ReplayRequest{ReplayMode: mode, TargetRuntime: "x"})
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
			}
			if n, _ := f.blobs.DeleteBySession(context.Background(), "acme", "sess_replay"); n != 0 {
				t.Errorf("the refused replay left %d copied objects", n)
			}
			if ids := f.reserved(t, "alice@acme.com"); len(ids) != 0 {
				t.Errorf("the refused replay left its reservation: %v", ids)
			}
		})
	}
}

func setSourceRef(t *testing.T, f *fenceFixture, ref string) {
	t.Helper()
	if _, err := f.mem.Update(context.Background(), "acme", "sess_source", func(s *sessionstore.Session) error {
		s.WorkspaceSnapshot.Ref = ref
		return nil
	}); err != nil {
		t.Fatalf("set source ref: %v", err)
	}
}
