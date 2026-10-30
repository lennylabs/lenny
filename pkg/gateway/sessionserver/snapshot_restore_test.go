// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// keyRecordingResolver records the full (tenant, session, checkpoint) key a
// restore closure resolves, and returns a fixed result.
type keyRecordingResolver struct {
	calls                       int
	tenant, session, checkpoint string
	grants                      []adapterclient.ChunkGrant
	err                         error
}

func (r *keyRecordingResolver) Resolve(_ context.Context, tenantID, sessionID, checkpointID string) (resumechunks.ResolveResult, error) {
	r.calls++
	r.tenant, r.session, r.checkpoint = tenantID, sessionID, checkpointID
	return resumechunks.ResolveResult{Grants: r.grants}, r.err
}

// prefixLister is an in-memory chunk lister that returns only the objects
// whose tenant, session, and part id fall under the listed prefix, as a
// prefix-scoped object-store LIST does.
type prefixLister struct{ objects []blobstore.BlobInfo }

func (l prefixLister) ListByPrefix(_ context.Context, u blobstore.URI) ([]blobstore.BlobInfo, error) {
	var out []blobstore.BlobInfo
	for _, o := range l.objects {
		if o.URI.TenantID == u.TenantID && o.URI.SessionID == u.SessionID && strings.HasPrefix(o.URI.PartID, u.PartID) {
			out = append(out, o)
		}
	}
	return out, nil
}

// countingPresigner mints a deterministic capability per key and counts
// the capabilities it minted.
type countingPresigner struct{ minted int }

func (p *countingPresigner) PresignGet(u blobstore.URI, ttl time.Duration) (blobstore.Grant, error) {
	p.minted++
	return blobstore.Grant{
		URL:       "https://objects.example/" + u.TenantID + "/" + u.SessionID + "/" + u.PartID,
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

func derivedRow(tenant, id, ref string) sessionstore.Session {
	return sessionstore.Session{
		ID: id, TenantID: tenant,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: ref},
	}
}

// spec: §7.1 (Derive restore semantics) — only a row carrying a starting
// snapshot ref triggers the bind-time restore. A row with no snapshot, or a
// snapshot with an empty ref, composes no restore closure.
func TestWorkspaceRestoreNilWithoutSnapshotRef_spec_7_1(t *testing.T) {
	s := &Server{resumeChunkResolver: &keyRecordingResolver{}}
	cases := map[string]sessionstore.Session{
		"no snapshot": {ID: "sess_a", TenantID: "acme"},
		"empty ref":   {ID: "sess_a", TenantID: "acme", WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{}},
	}
	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			if got := s.workspaceRestore(row); got != nil {
				t.Fatal("workspaceRestore returned a closure for a row without a starting snapshot ref")
			}
		})
	}
}

// spec: §7.1 (Derive restore semantics); §13.1 — the restore capabilities
// are keyed on the derived row's own tenant, its own session id, and the
// checkpoint id its ref names, so they cannot name a key outside the derived
// session's prefix. Both ref forms resolve to the same checkpoint id.
//
// diagnosis: a wrong tenant or session here means the closure mints
// capabilities against the source session's prefix or another tenant.
func TestWorkspaceRestoreKeysOnDerivedRowPrefix_spec_7_1(t *testing.T) {
	for _, ref := range []string{"/acme/checkpoints/sess_derived/ck_derived", "ck_derived"} {
		t.Run(ref, func(t *testing.T) {
			want := []adapterclient.ChunkGrant{{Index: 0, URL: "u0"}, {Index: 1, URL: "u1"}}
			res := &keyRecordingResolver{grants: want}
			s := &Server{resumeChunkResolver: res}
			restore := s.workspaceRestore(derivedRow("acme", "sess_derived", ref))
			if restore == nil {
				t.Fatal("workspaceRestore returned nil for a row with a snapshot ref")
			}
			if res.calls != 0 {
				t.Fatalf("resolver called %d times at composition, want 0 (minting is lazy)", res.calls)
			}
			got, err := restore(context.Background())
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if res.tenant != "acme" || res.session != "sess_derived" || res.checkpoint != "ck_derived" {
				t.Fatalf("resolved (%q, %q, %q), want (acme, sess_derived, ck_derived)", res.tenant, res.session, res.checkpoint)
			}
			if len(got) != len(want) || got[0].URL != "u0" || got[1].URL != "u1" {
				t.Fatalf("grants = %+v, want %+v in index order", got, want)
			}
		})
	}
}

// spec: §7.1 (Derive restore semantics); §28.5.5 — the restore has no
// fallback. An unwired resolver, any resolve error, and an empty chunk set
// each fail the closure, and no failure returns a nil error.
func TestWorkspaceRestoreFailsClosed_spec_7_1(t *testing.T) {
	presignErr := errors.New("presign refused")
	manifestErr := errors.New("manifest row not found")
	cases := []struct {
		name     string
		resolver ResumeChunkResolver
		want     error
	}{
		{name: "unwired resolver", resolver: nil, want: errRestoreUnwired},
		{name: "contiguity", resolver: &keyRecordingResolver{err: resumechunks.ErrReassemblyContiguity}, want: resumechunks.ErrReassemblyContiguity},
		{name: "missing manifest", resolver: &keyRecordingResolver{err: manifestErr}, want: manifestErr},
		{name: "presign", resolver: &keyRecordingResolver{err: presignErr}, want: presignErr},
		{name: "zero grants", resolver: &keyRecordingResolver{}, want: errRestoreEmpty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{}
			if tc.resolver != nil {
				s.resumeChunkResolver = tc.resolver
			}
			restore := s.workspaceRestore(derivedRow("acme", "sess_derived", "ck_derived"))
			got, err := restore(context.Background())
			if err == nil {
				t.Fatalf("restore returned nil error with %d grants, want %v", len(got), tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("restore error = %v, want wrapping %v", err, tc.want)
			}
			if got != nil {
				t.Fatalf("restore returned %d grants alongside an error", len(got))
			}
		})
	}
}

// spec: §7.1 (Derive restore semantics); §13.1 — with the production
// resolver over in-memory fakes, a ref naming a checkpoint whose chunks sit
// under another session's prefix fails contiguity and mints nothing, while
// the derived session's own copied checkpoint resolves.
//
// diagnosis: a minted capability in the cross-prefix case means the
// restore is not confined to the derived session's own prefix.
func TestWorkspaceRestoreConfinedToDerivedPrefix_spec_13_1(t *testing.T) {
	const tenant = "acme"
	mstore := partialmanifeststore.NewMemoryStore(nil)
	seedManifest(t, mstore, tenant, "sess_source", "ck_source", 1, false)
	seedManifest(t, mstore, tenant, "sess_derived", "ck_derived", 1, false)
	lister := prefixLister{objects: []blobstore.BlobInfo{
		{URI: resumechunks.ChunkObjectURI(tenant, "sess_source", "ck_source", 0, partialmanifeststore.ChunkEncodingTarGz), Size: 4096},
		{URI: resumechunks.ChunkObjectURI(tenant, "sess_derived", "ck_derived", 0, partialmanifeststore.ChunkEncodingTarGz), Size: 4096},
	}}

	t.Run("source checkpoint outside the derived prefix", func(t *testing.T) {
		presigner := &countingPresigner{}
		s := &Server{resumeChunkResolver: &resumechunks.Resolver{Manifests: mstore, Presigner: presigner, Lister: lister, TTL: 30 * time.Second}}
		_, err := s.workspaceRestore(derivedRow(tenant, "sess_derived", "ck_source"))(context.Background())
		if !errors.Is(err, resumechunks.ErrReassemblyContiguity) {
			t.Fatalf("restore error = %v, want ErrReassemblyContiguity", err)
		}
		if presigner.minted != 0 {
			t.Fatalf("minted %d capabilities for a checkpoint outside the derived prefix, want 0", presigner.minted)
		}
	})

	t.Run("derived session's own checkpoint", func(t *testing.T) {
		presigner := &countingPresigner{}
		s := &Server{resumeChunkResolver: &resumechunks.Resolver{Manifests: mstore, Presigner: presigner, Lister: lister, TTL: 30 * time.Second}}
		grants, err := s.workspaceRestore(derivedRow(tenant, "sess_derived", "/acme/checkpoints/sess_derived/ck_derived"))(context.Background())
		if err != nil {
			t.Fatalf("restore: %v", err)
		}
		if len(grants) != 1 || !strings.Contains(grants[0].URL, "/sess_derived/ck_derived/") {
			t.Fatalf("grants = %+v, want one capability under the derived prefix", grants)
		}
	})
}
