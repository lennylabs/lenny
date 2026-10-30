// SPDX-License-Identifier: MIT

// Package objectstore is the tier-12 object-storage seam. Callers
// upload per-run artefacts (k6 summary JSON, HTML reports) addressed
// by URL and read them back through the same URL.
//
// The package exposes URL-scheme-driven backends:
//
//   - file:///abs/path   — local filesystem; the dev / single-node path.
//   - s3://bucket/prefix — AWS S3 via aws-sdk-go-v2.
//   - gs://bucket/prefix — Google Cloud Storage via cloud.google.com/go/storage.
//   - azureblob://account/container/prefix — Azure Blob Storage via azblob.
//
// Backends share a small interface; a caller picks one with `Open`
// from a configured base URL and writes through `Put`. The returned
// access URL is what loadctl persists on `Run.ReportURL`.
package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// ErrNotFound is returned by Get when an object does not exist.
var ErrNotFound = errors.New("objectstore: object not found")

// Store is the small surface every backend exposes.
type Store interface {
	// Put writes the supplied bytes at relative `key` (joined to the
	// store's base URL) and returns the canonical URL the bytes are
	// reachable from. Overwrites silently.
	Put(ctx context.Context, key string, body io.Reader, contentType string) (string, error)
	// Get reads the object at `key`. Returns ErrNotFound when the
	// object does not exist.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Close releases any client resources the backend holds.
	Close() error
}

// Open resolves baseURL to a Store. The URL scheme selects the
// backend; the remainder of the URL is the per-backend address
// (bucket + prefix, filesystem path, etc.).
//
// An empty baseURL is treated as a disabled store — Put returns a
// fabricated URL that mirrors the input key but writes nowhere, so
// the dev / no-storage flow keeps working.
func Open(baseURL string) (Store, error) {
	if baseURL == "" {
		return noopStore{}, nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("objectstore: parse %q: %w", baseURL, err)
	}
	switch u.Scheme {
	case "file":
		return newFileStore(u)
	case "s3":
		return newS3Store(u)
	case "gs":
		return newGCSStore(u)
	case "azureblob":
		return newAzureStore(u)
	case "":
		// Bare path; treat as file://.
		return newFileStore(&url.URL{Scheme: "file", Path: baseURL})
	default:
		return nil, fmt.Errorf("objectstore: unsupported scheme %q", u.Scheme)
	}
}

// joinKey safely joins two URL path segments with a single slash.
func joinKey(prefix, key string) string {
	prefix = strings.TrimRight(prefix, "/")
	key = strings.TrimLeft(key, "/")
	if prefix == "" {
		return key
	}
	return prefix + "/" + key
}

// noopStore discards every Put and reports ErrNotFound on Get. It
// returns the URL the bytes *would* have been at so callers can
// surface a placeholder link in the UI; the URL is not reachable.
type noopStore struct{}

func (noopStore) Put(_ context.Context, key string, body io.Reader, _ string) (string, error) {
	if body != nil {
		_, _ = io.Copy(io.Discard, body)
	}
	return "objectstore://disabled/" + strings.TrimLeft(key, "/"), nil
}
func (noopStore) Get(context.Context, string) (io.ReadCloser, error) { return nil, ErrNotFound }
func (noopStore) Close() error                                       { return nil }
