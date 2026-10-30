// SPDX-License-Identifier: MIT

// Package prefixlisttest is the shared conformance suite for
// blobstore.PrefixLister. Every backend's tests run it against that
// backend (the in-memory and filesystem stores directly, the S3, GCS, and
// Azure stores over their in-package fakes at tier 1, and the S3 and GCS
// stores over a MinIO or fake-gcs-server container at tier 2), so the
// §10.1.7 chunk listing behaves the same on every backend: scoped to one
// tenant and session, ordered by key, excluding soft-deleted objects, and
// rejecting a URI that cannot name one session prefix. Pagination and
// provider-error cases depend on each backend's fake and live in that
// backend's own tests.
//
// spec: §10.1.7 (Reassembly on resume); §12.5 (tenant-prefixed keys).
package prefixlisttest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// Store is the surface the suite drives: it writes objects with Put,
// soft-deletes one with SoftDelete, and lists with ListByPrefix.
type Store interface {
	blobstore.Store
	blobstore.Tombstoner
	blobstore.PrefixLister
}

// ChunkURI returns the URI of chunk n of checkpointID on (tenantID,
// sessionID), in the canonical `{checkpoint_id}/chunk-{n:05d}.tar.gz`
// part-id form the checkpoint driver writes.
func ChunkURI(tenantID, sessionID, checkpointID string, n int) blobstore.URI {
	return blobstore.URI{
		TenantID:   tenantID,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  sessionID,
		PartID:     fmt.Sprintf("%s/chunk-%05d.tar.gz", checkpointID, n),
		TTL:        time.Hour,
	}
}

// PrefixURI returns the listing URI for checkpointID's chunk prefix.
func PrefixURI(tenantID, sessionID, checkpointID string) blobstore.URI {
	return blobstore.URI{
		TenantID:   tenantID,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  sessionID,
		PartID:     checkpointID + "/",
		TTL:        time.Hour,
	}
}

// PutChunk writes chunk n with a body of size bytes.
func PutChunk(t testing.TB, s blobstore.Store, tenantID, sessionID, checkpointID string, n, size int) blobstore.URI {
	t.Helper()
	u := ChunkURI(tenantID, sessionID, checkpointID, n)
	if _, err := s.Put(u, "application/gzip", strings.NewReader(strings.Repeat("x", size))); err != nil {
		t.Fatalf("Put %s: %v", u.PartID, err)
	}
	return u
}

// Run runs the conformance suite. newStore returns an empty store per
// subtest; ids it uses are unique per subtest, so a store shared across
// subtests (a container-backed one) also works.
func Run(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("empty prefix lists nothing", func(t *testing.T) {
		s := newStore(t)
		got, err := s.ListByPrefix(ctx, PrefixURI("acme", "sess-empty", "ck-none"))
		if err != nil {
			t.Fatalf("ListByPrefix on an empty prefix: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("ListByPrefix on an empty prefix = %d entries, want 0", len(got))
		}
	})

	t.Run("lists only the named checkpoint in ascending key order", func(t *testing.T) {
		s := newStore(t)
		const sess = "sess-scope"
		// Written out of order so the listing order is the store's, not
		// the insertion order.
		for _, n := range []int{2, 0, 1} {
			PutChunk(t, s, "acme", sess, "ck-a", n, 10+n)
		}
		// Keys the listing must exclude: a sibling checkpoint whose id
		// extends ck-a, another session of the same tenant, and the same
		// session and checkpoint ids under two other tenants, one of whose
		// ids extends "acme".
		PutChunk(t, s, "acme", sess, "ck-a0", 0, 1)
		PutChunk(t, s, "acme", sess+"-other", "ck-a", 0, 1)
		PutChunk(t, s, "acme2", sess, "ck-a", 0, 1)
		PutChunk(t, s, "globex", sess, "ck-a", 3, 1)

		got, err := s.ListByPrefix(ctx, PrefixURI("acme", sess, "ck-a"))
		if err != nil {
			t.Fatalf("ListByPrefix: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("ListByPrefix = %d entries %v, want the 3 chunks of acme/%s/ck-a", len(got), partIDs(got), sess)
		}
		for i, info := range got {
			want := ChunkURI("acme", sess, "ck-a", i)
			if info.URI.TenantID != "acme" || info.URI.SessionID != sess ||
				info.URI.ObjectType != blobstore.ObjectTypeCheckpoint || info.URI.PartID != want.PartID {
				t.Errorf("entry %d URI = %+v, want tenant acme, session %s, part %s", i, info.URI, sess, want.PartID)
			}
			if info.Size != int64(10+i) {
				t.Errorf("entry %d Size = %d, want %d", i, info.Size, 10+i)
			}
		}
	})

	t.Run("soft-deleted chunk is excluded", func(t *testing.T) {
		s := newStore(t)
		const sess = "sess-tomb"
		PutChunk(t, s, "acme", sess, "ck-t", 0, 4)
		dead := PutChunk(t, s, "acme", sess, "ck-t", 1, 4)
		PutChunk(t, s, "acme", sess, "ck-t", 2, 4)
		if err := s.SoftDelete(dead); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		got, err := s.ListByPrefix(ctx, PrefixURI("acme", sess, "ck-t"))
		if err != nil {
			t.Fatalf("ListByPrefix: %v", err)
		}
		ids := partIDs(got)
		if len(ids) != 2 || strings.Contains(strings.Join(ids, ","), "chunk-00001") {
			t.Fatalf("ListByPrefix = %v, want chunks 0 and 2 without the soft-deleted chunk 1", ids)
		}
	})

	t.Run("URI that cannot name one session prefix is rejected", func(t *testing.T) {
		s := newStore(t)
		for name, u := range map[string]blobstore.URI{
			"empty tenant":       PrefixURI("", "sess", "ck"),
			"slash in tenant":    PrefixURI("acme/checkpoint/sess", "x", "ck"),
			"empty session":      PrefixURI("acme", "", "ck"),
			"slash in session":   PrefixURI("acme", "sess/ck", "ck"),
			"tenant only prefix": {TenantID: "acme"},
		} {
			if _, err := s.ListByPrefix(ctx, u); !errors.Is(err, blobstore.ErrInvalidURI) {
				t.Errorf("%s: ListByPrefix err = %v, want ErrInvalidURI", name, err)
			}
		}
	})
}

// RunPagination runs the pagination and provider-error cases against a
// backend whose in-package fake pages through listing. newStore returns
// an empty store and the FakeListing its fake consults.
func RunPagination(t *testing.T, newStore func(t *testing.T) (Store, *FakeListing)) {
	t.Helper()
	ctx := context.Background()
	const sess = "sess-page"
	wantPrefix := "acme/" + string(blobstore.ObjectTypeCheckpoint) + "/" + sess + "/ck-p/"

	t.Run("listing past one page returns every chunk in order", func(t *testing.T) {
		s, l := newStore(t)
		const chunks = 7
		for n := chunks - 1; n >= 0; n-- {
			PutChunk(t, s, "acme", sess, "ck-p", n, 3)
		}
		// Another tenant's keys sort inside the same bucket listing range
		// and must never reach a page of this prefix.
		PutChunk(t, s, "acme2", sess, "ck-p", 0, 3)
		PutChunk(t, s, "globex", sess, "ck-p", 0, 3)
		l.PageSize = 2
		got, err := s.ListByPrefix(ctx, PrefixURI("acme", sess, "ck-p"))
		if err != nil {
			t.Fatalf("ListByPrefix: %v", err)
		}
		if len(got) != chunks {
			t.Fatalf("ListByPrefix = %d entries %v, want %d across pages", len(got), partIDs(got), chunks)
		}
		for i, info := range got {
			if want := ChunkURI("acme", sess, "ck-p", i).PartID; info.URI.PartID != want {
				t.Errorf("entry %d PartID = %q, want %q", i, info.URI.PartID, want)
			}
		}
		if l.Calls != 4 {
			t.Errorf("provider pages requested = %d, want 4 (7 chunks at 2 per page)", l.Calls)
		}
		for i, p := range l.Prefixes {
			if p != wantPrefix {
				t.Errorf("page %d listed prefix %q, want the tenant- and session-scoped %q", i+1, p, wantPrefix)
			}
		}
	})

	t.Run("an error on a later page fails the whole listing", func(t *testing.T) {
		s, l := newStore(t)
		for n := 0; n < 5; n++ {
			PutChunk(t, s, "acme", sess, "ck-p", n, 3)
		}
		boom := errors.New("provider unavailable")
		l.PageSize, l.FailOnPage, l.Err = 2, 2, boom
		got, err := s.ListByPrefix(ctx, PrefixURI("acme", sess, "ck-p"))
		if !errors.Is(err, boom) {
			t.Fatalf("ListByPrefix err = %v, want the page-2 provider error", err)
		}
		if got != nil {
			t.Fatalf("ListByPrefix returned %d entries with an error, want nil so no truncated set reaches the contiguity check", len(got))
		}
	})

	t.Run("an error on the first page fails the listing", func(t *testing.T) {
		s, l := newStore(t)
		boom := errors.New("access denied")
		l.FailOnPage, l.Err = 1, boom
		if _, err := s.ListByPrefix(ctx, PrefixURI("acme", sess, "ck-p")); !errors.Is(err, boom) {
			t.Fatalf("ListByPrefix err = %v, want the provider error", err)
		}
	})
}

// FakeListing is the pagination and fault-injection core the backends'
// in-package fakes share. Page returns one page of the keys under a
// prefix in ascending order, starting after the key named by the token.
type FakeListing struct {
	// PageSize caps every page. Zero uses the caller's requested size,
	// or returns every key in one page when that is also zero.
	PageSize int
	// FailOnPage makes the Nth call (1-based) return Err. Zero never
	// fails.
	FailOnPage int
	// Err is the error FailOnPage returns.
	Err error
	// DropToken makes an S3 fake report a truncated page with no
	// continuation token, a malformed provider response.
	DropToken bool
	// Calls counts Page calls; Prefixes and Requested record the prefix
	// and the caller's requested page size of each.
	Calls     int
	Prefixes  []string
	Requested []int
}

// Page returns the page of keys under prefix that follows token (the
// last key of the previous page, or "" for the first page) and the token
// of the next page ("" when this page is the last).
func (l *FakeListing) Page(keys []string, prefix, token string, requested int) ([]string, string, error) {
	l.Calls++
	l.Prefixes = append(l.Prefixes, prefix)
	l.Requested = append(l.Requested, requested)
	if l.FailOnPage > 0 && l.Calls == l.FailOnPage {
		return nil, "", l.Err
	}
	matched := make([]string, 0, len(keys))
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) && k > token {
			matched = append(matched, k)
		}
	}
	sort.Strings(matched)
	size := l.PageSize
	if size == 0 {
		size = requested
	}
	if size <= 0 || len(matched) <= size {
		return matched, "", nil
	}
	page := matched[:size]
	return page, page[len(page)-1], nil
}

func partIDs(infos []blobstore.BlobInfo) []string {
	out := make([]string, 0, len(infos))
	for _, i := range infos {
		out = append(out, i.URI.PartID)
	}
	return out
}
