// SPDX-License-Identifier: MIT

package gcs

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// listPageSize is the page size ListByPrefix requests from the GCS
// Objects listing. 1,000 is the provider's maximum page size, so a
// checkpoint prefix of up to 1,000 chunks is listed in one round trip.
const listPageSize = 1000

// ListByPrefix implements blobstore.PrefixLister with a paginated
// Objects listing under the session-scoped key prefix. It follows the
// page token until the provider returns an empty one, so a prefix that
// spans several pages is returned whole. A listing error on any page
// fails the call. The listing carries each object's custom metadata, so a
// §12.5 soft-deleted object (one carrying the tombstone metadata key) is
// excluded without a per-object Attrs call.
//
// spec: §10.1.7 (Reassembly on resume: list objects under
// chunk_object_key_prefix); §4.4 (Checkpoint abort cleanup); §12.5.
func (s *Store) ListByPrefix(ctx context.Context, u blobstore.URI) ([]blobstore.BlobInfo, error) {
	keyPrefix, sessPrefix, err := blobstore.PrefixListKeys(u)
	if err != nil {
		return nil, err
	}
	out := []blobstore.BlobInfo{}
	token := ""
	for {
		attrs, next, err := s.client.ListPage(ctx, keyPrefix, token, listPageSize)
		if err != nil {
			return nil, fmt.Errorf("blobstore/gcs: list %s: %w", keyPrefix, err)
		}
		for _, a := range attrs {
			if a == nil {
				continue
			}
			if _, tombstoned := a.Metadata[tombstoneMeta]; tombstoned {
				continue
			}
			out = append(out, blobstore.PrefixListEntry(u, sessPrefix, a.Name, a.Size, a.Updated.UTC()))
		}
		if next == "" {
			break
		}
		token = next
	}
	blobstore.SortByPartID(out)
	return out, nil
}

// ListPage returns one page of the objects whose names start with
// prefix, and the token of the next page ("" on the last page).
func (a *clientAdapter) ListPage(ctx context.Context, prefix, pageToken string, pageSize int) ([]*storage.ObjectAttrs, string, error) {
	it := a.cli.Bucket(a.bucket).Objects(ctx, &storage.Query{Prefix: prefix})
	var page []*storage.ObjectAttrs
	next, err := iterator.NewPager(it, pageSize, pageToken).NextPage(&page)
	if err != nil {
		return nil, "", err
	}
	return page, next, nil
}
