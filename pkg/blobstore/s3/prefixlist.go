// SPDX-License-Identifier: MIT

package s3

import (
	"context"
	"fmt"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// ListByPrefix implements blobstore.PrefixLister with a paginated
// ListObjectsV2 walk under the session-scoped key prefix. It follows
// NextContinuationToken until the provider reports the listing is not
// truncated, so a checkpoint whose chunks span several 1,000-key pages is
// returned whole. A listing error on any page fails the call. Each listed
// key's tombstone tag is read so a §12.5 soft-deleted chunk is excluded,
// as miniostore does.
//
// spec: §10.1.7 (Reassembly on resume: list objects under
// chunk_object_key_prefix via ListObjectsV2); §4.4 (Checkpoint abort
// cleanup); §12.5.
func (s *Store) ListByPrefix(ctx context.Context, u blobstore.URI) ([]blobstore.BlobInfo, error) {
	keyPrefix, sessPrefix, err := blobstore.PrefixListKeys(u)
	if err != nil {
		return nil, err
	}
	out := []blobstore.BlobInfo{}
	var token *string
	for {
		page, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            awssdk.String(s.bucket),
			Prefix:            awssdk.String(keyPrefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("blobstore/s3: list %s: %w", keyPrefix, err)
		}
		for _, obj := range page.Contents {
			key := awssdk.ToString(obj.Key)
			tombstoned, terr := s.isTombstoned(ctx, key)
			if terr != nil {
				return nil, terr
			}
			if tombstoned {
				continue
			}
			var storedAt time.Time
			if obj.LastModified != nil {
				storedAt = obj.LastModified.UTC()
			}
			out = append(out, blobstore.PrefixListEntry(u, sessPrefix, key, awssdk.ToInt64(obj.Size), storedAt))
		}
		if !awssdk.ToBool(page.IsTruncated) {
			break
		}
		if page.NextContinuationToken == nil {
			return nil, fmt.Errorf("blobstore/s3: list %s: truncated page carries no continuation token", keyPrefix)
		}
		token = page.NextContinuationToken
	}
	blobstore.SortByPartID(out)
	return out, nil
}
