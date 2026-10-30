// SPDX-License-Identifier: MIT

package objectstore

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

// fileStore writes objects under a base directory on the local
// filesystem. The "access URL" it returns is the same file:// URL.
// Suitable for single-node dev / scaffold deployments.
type fileStore struct {
	root string
}

func newFileStore(u *url.URL) (*fileStore, error) {
	root := u.Path
	if u.Host != "" {
		root = filepath.Join(u.Host, u.Path)
	}
	if root == "" {
		return nil, fmt.Errorf("objectstore: file:// requires a path")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("objectstore: mkdir %q: %w", root, err)
	}
	return &fileStore{root: root}, nil
}

func (s *fileStore) Put(_ context.Context, key string, body io.Reader, _ string) (string, error) {
	path := filepath.Join(s.root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, body); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return "file://" + path, nil
}

func (s *fileStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	path := filepath.Join(s.root, filepath.FromSlash(key))
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

func (s *fileStore) Close() error { return nil }
