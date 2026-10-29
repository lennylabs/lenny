// SPDX-License-Identifier: MIT

package s3

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/blobstore/prefixlisttest"
)

func newPrefixListStore(t *testing.T) (prefixlisttest.Store, *prefixlisttest.FakeListing) {
	t.Helper()
	f := newFakeS3()
	return newWithClient(f, "lenny-test", nil), &f.listing
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 12.5 (Artifact Store)
func TestS3ListByPrefixConformance(t *testing.T) {
	prefixlisttest.Run(t, func(t *testing.T) prefixlisttest.Store {
		s, _ := newPrefixListStore(t)
		return s
	})
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 4.4 (Event / Checkpoint Store: Checkpoint abort cleanup)
func TestS3ListByPrefixFollowsContinuationTokens(t *testing.T) {
	prefixlisttest.RunPagination(t, newPrefixListStore)
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume)
//
// A truncated ListObjectsV2 page without a NextContinuationToken cannot
// be continued. Returning the first page as the whole listing would hide
// the remaining chunks from the contiguity check, so the call fails.
func TestS3ListByPrefixRejectsTruncatedPageWithoutToken(t *testing.T) {
	s, l := newPrefixListStore(t)
	for n := 0; n < 3; n++ {
		prefixlisttest.PutChunk(t, s, "acme", "sess-tok", "ck", n, 2)
	}
	l.PageSize, l.DropToken = 2, true
	got, err := s.ListByPrefix(context.Background(), prefixlisttest.PrefixURI("acme", "sess-tok", "ck"))
	if err == nil || !strings.Contains(err.Error(), "no continuation token") {
		t.Fatalf("ListByPrefix err = %v, want the missing-token error", err)
	}
	if got != nil {
		t.Fatalf("ListByPrefix returned %d entries with an error, want nil", len(got))
	}
}
