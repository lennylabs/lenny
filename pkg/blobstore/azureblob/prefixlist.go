// SPDX-License-Identifier: MIT

package azureblob

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// listPageSize is the MaxResults ListByPrefix requests per
// ListBlobsFlat page. 5,000 is the provider's maximum; the service may
// return fewer and a NextMarker, which ListByPrefix follows.
const listPageSize int32 = 5000

// listedBlob is one entry of a ListBlobsFlat page: the fields
// ListByPrefix reads from the SDK's BlobItem.
type listedBlob struct {
	name         string
	size         int64
	lastModified time.Time
	metadata     map[string]*string
}

// ListByPrefix implements blobstore.PrefixLister with a paginated
// ListBlobsFlat walk under the session-scoped key prefix. It follows
// NextMarker until the provider returns an empty one, so a prefix that
// spans several pages is returned whole. A listing error on any page
// fails the call. The listing includes each blob's metadata, so a §12.5
// soft-deleted blob (one carrying the tombstone metadata key) is excluded
// without a per-blob GetProperties call.
//
// spec: §10.1.7 (Reassembly on resume: list objects under
// chunk_object_key_prefix); §4.4 (Checkpoint abort cleanup); §12.5.
func (s *Store) ListByPrefix(ctx context.Context, u blobstore.URI) ([]blobstore.BlobInfo, error) {
	keyPrefix, sessPrefix, err := blobstore.PrefixListKeys(u)
	if err != nil {
		return nil, err
	}
	out := []blobstore.BlobInfo{}
	marker := ""
	for {
		items, next, err := s.client.ListPage(ctx, keyPrefix, marker, listPageSize)
		if err != nil {
			return nil, fmt.Errorf("blobstore/azureblob: list %s: %w", keyPrefix, err)
		}
		for _, it := range items {
			if _, tombstoned := it.metadata[tombstoneMeta]; tombstoned {
				continue
			}
			out = append(out, blobstore.PrefixListEntry(u, sessPrefix, it.name, it.size, it.lastModified))
		}
		if next == "" {
			break
		}
		marker = next
	}
	blobstore.SortByPartID(out)
	return out, nil
}

// ListPage returns one ListBlobsFlat page of the blobs whose names start
// with prefix, with their metadata, and the marker of the next page (""
// on the last page).
func (a *clientAdapter) ListPage(ctx context.Context, prefix, marker string, maxResults int32) ([]listedBlob, string, error) {
	opts := &azblob.ListBlobsFlatOptions{
		Prefix:     &prefix,
		MaxResults: &maxResults,
		Include:    container.ListBlobsInclude{Metadata: true},
	}
	if marker != "" {
		opts.Marker = &marker
	}
	page, err := a.cli.NewListBlobsFlatPager(a.container, opts).NextPage(ctx)
	if err != nil {
		return nil, "", err
	}
	var out []listedBlob
	if page.Segment != nil {
		for _, item := range page.Segment.BlobItems {
			if item == nil || item.Name == nil {
				continue
			}
			lb := listedBlob{name: *item.Name, metadata: item.Metadata}
			if item.Properties != nil {
				lb.size = ptrInt64(item.Properties.ContentLength)
				if item.Properties.LastModified != nil {
					lb.lastModified = item.Properties.LastModified.UTC()
				}
			}
			out = append(out, lb)
		}
	}
	return out, ptrStr(page.NextMarker), nil
}
