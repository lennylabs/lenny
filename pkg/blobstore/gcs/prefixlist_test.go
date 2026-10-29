// SPDX-License-Identifier: MIT

package gcs

import (
	"context"
	"testing"

	"cloud.google.com/go/storage"

	"github.com/lennylabs/lenny/pkg/blobstore/prefixlisttest"
)

// ListPage pages the fake's objects through the shared FakeListing,
// returning attrs with the custom metadata the GCS Objects listing
// carries.
func (f *fakeGCS) ListPage(_ context.Context, prefix, pageToken string, pageSize int) ([]*storage.ObjectAttrs, string, error) {
	keys := make([]string, 0, len(f.objects))
	for k := range f.objects {
		keys = append(keys, k)
	}
	page, next, err := f.listing.Page(keys, prefix, pageToken, pageSize)
	if err != nil {
		return nil, "", err
	}
	out := make([]*storage.ObjectAttrs, 0, len(page))
	for _, k := range page {
		obj := f.objects[k]
		meta := map[string]string{}
		for mk, mv := range obj.metadata {
			meta[mk] = mv
		}
		out = append(out, &storage.ObjectAttrs{
			Name:     k,
			Size:     int64(len(obj.body)),
			Updated:  obj.lastModified,
			Metadata: meta,
		})
	}
	return out, next, nil
}

func newPrefixListStore(t *testing.T) (prefixlisttest.Store, *prefixlisttest.FakeListing) {
	t.Helper()
	f := newFakeGCS()
	return newWithClient(f, "lenny-test", nil), &f.listing
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 12.5 (Artifact Store)
func TestGCSListByPrefixConformance(t *testing.T) {
	prefixlisttest.Run(t, func(t *testing.T) prefixlisttest.Store {
		s, _ := newPrefixListStore(t)
		return s
	})
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 4.4 (Event / Checkpoint Store: Checkpoint abort cleanup)
func TestGCSListByPrefixFollowsPageTokens(t *testing.T) {
	prefixlisttest.RunPagination(t, newPrefixListStore)
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume)
func TestGCSListByPrefixRequestsProviderMaximumPageSize(t *testing.T) {
	s, l := newPrefixListStore(t)
	for n := 0; n < 3; n++ {
		prefixlisttest.PutChunk(t, s, "acme", "sess-size", "ck", n, 1)
	}
	got, err := s.ListByPrefix(context.Background(), prefixlisttest.PrefixURI("acme", "sess-size", "ck"))
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	if len(got) != 3 || len(l.Requested) != 1 || l.Requested[0] != 1000 {
		t.Fatalf("ListByPrefix = %d entries, page sizes requested %v; want 3 entries in one page requested at 1000", len(got), l.Requested)
	}
}
