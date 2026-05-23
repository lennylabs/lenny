// SPDX-License-Identifier: MIT

package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"cloud.google.com/go/storage"
)

// gcsStore writes objects to a Google Cloud Storage bucket + prefix.
type gcsStore struct {
	client *storage.Client
	bucket string
	prefix string
}

func newGCSStore(u *url.URL) (*gcsStore, error) {
	bucket := u.Host
	if bucket == "" {
		return nil, fmt.Errorf("objectstore: gs:// requires a bucket (host)")
	}
	prefix := strings.TrimPrefix(u.Path, "/")
	client, err := storage.NewClient(context.Background())
	if err != nil {
		return nil, fmt.Errorf("objectstore: gcs NewClient: %w", err)
	}
	return &gcsStore{client: client, bucket: bucket, prefix: prefix}, nil
}

func (s *gcsStore) Put(ctx context.Context, key string, body io.Reader, contentType string) (string, error) {
	full := joinKey(s.prefix, key)
	w := s.client.Bucket(s.bucket).Object(full).NewWriter(ctx)
	if contentType != "" {
		w.ContentType = contentType
	}
	if _, err := io.Copy(w, body); err != nil {
		_ = w.Close()
		return "", fmt.Errorf("objectstore: gcs write: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("objectstore: gcs close: %w", err)
	}
	return fmt.Sprintf("gs://%s/%s", s.bucket, full), nil
}

func (s *gcsStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := s.client.Bucket(s.bucket).Object(joinKey(s.prefix, key)).NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("objectstore: gcs read: %w", err)
	}
	return rc, nil
}

func (s *gcsStore) Close() error { return s.client.Close() }
