// SPDX-License-Identifier: MIT

package azureblob

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/blobstore/prefixlisttest"
)

// ListPage pages the fake's blobs through the shared FakeListing,
// returning each blob's metadata as a ListBlobsFlat call with
// Include.Metadata does.
func (f *fakeBlob) ListPage(_ context.Context, prefix, marker string, maxResults int32) ([]listedBlob, string, error) {
	keys := make([]string, 0, len(f.objects))
	for k := range f.objects {
		keys = append(keys, k)
	}
	page, next, err := f.listing.Page(keys, prefix, marker, int(maxResults))
	if err != nil {
		return nil, "", err
	}
	out := make([]listedBlob, 0, len(page))
	for _, k := range page {
		obj := f.objects[k]
		meta := map[string]*string{}
		for mk, mv := range obj.metadata {
			meta[mk] = mv
		}
		out = append(out, listedBlob{
			name:         k,
			size:         int64(len(obj.body)),
			lastModified: obj.lastModified,
			metadata:     meta,
		})
	}
	return out, next, nil
}

func newPrefixListStore(t *testing.T) (prefixlisttest.Store, *prefixlisttest.FakeListing) {
	t.Helper()
	f := newFakeBlob()
	return newWithClient(f, "lenny-test", nil), &f.listing
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 12.5 (Artifact Store)
func TestAzureListByPrefixConformance(t *testing.T) {
	prefixlisttest.Run(t, func(t *testing.T) prefixlisttest.Store {
		s, _ := newPrefixListStore(t)
		return s
	})
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 4.4 (Event / Checkpoint Store: Checkpoint abort cleanup)
func TestAzureListByPrefixFollowsNextMarker(t *testing.T) {
	prefixlisttest.RunPagination(t, newPrefixListStore)
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume)
func TestAzureListByPrefixRequestsProviderMaximumPageSize(t *testing.T) {
	s, l := newPrefixListStore(t)
	for n := 0; n < 3; n++ {
		prefixlisttest.PutChunk(t, s, "acme", "sess-size", "ck", n, 1)
	}
	got, err := s.ListByPrefix(context.Background(), prefixlisttest.PrefixURI("acme", "sess-size", "ck"))
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	if len(got) != 3 || len(l.Requested) != 1 || l.Requested[0] != 5000 {
		t.Fatalf("ListByPrefix = %d entries, page sizes requested %v; want 3 entries in one page requested at 5000", len(got), l.Requested)
	}
}
