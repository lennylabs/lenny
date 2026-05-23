// SPDX-License-Identifier: MIT

package objectstore

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
)

// azureStore writes objects to an Azure Blob Storage container.
// The base URL is azureblob://{account}/{container}/{prefix} so the
// account, container, and per-run prefix are all derivable from a
// single configuration value.
type azureStore struct {
	client    *azblob.Client
	container string
	prefix    string
	account   string
}

func newAzureStore(u *url.URL) (*azureStore, error) {
	if u.Host == "" {
		return nil, fmt.Errorf("objectstore: azureblob:// requires an account (host)")
	}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("objectstore: azureblob:// requires a container after the account")
	}
	container := parts[0]
	prefix := ""
	if len(parts) > 1 {
		prefix = parts[1]
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("objectstore: azure credential: %w", err)
	}
	serviceURL := fmt.Sprintf("https://%s.blob.core.windows.net/", u.Host)
	client, err := azblob.NewClient(serviceURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("objectstore: azure NewClient: %w", err)
	}
	return &azureStore{client: client, container: container, prefix: prefix, account: u.Host}, nil
}

func (s *azureStore) Put(ctx context.Context, key string, body io.Reader, contentType string) (string, error) {
	full := joinKey(s.prefix, key)
	buf, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	opts := &azblob.UploadBufferOptions{}
	if contentType != "" {
		ct := contentType
		opts.HTTPHeaders = &blob.HTTPHeaders{BlobContentType: &ct}
	}
	if _, err := s.client.UploadBuffer(ctx, s.container, full, buf, opts); err != nil {
		return "", fmt.Errorf("objectstore: azure upload: %w", err)
	}
	return fmt.Sprintf("azureblob://%s/%s/%s", s.account, s.container, full), nil
}

func (s *azureStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	resp, err := s.client.DownloadStream(ctx, s.container, joinKey(s.prefix, key), nil)
	if err != nil {
		if bloberror.HasCode(err, bloberror.BlobNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("objectstore: azure download: %w", err)
	}
	return resp.Body, nil
}

func (s *azureStore) Close() error {
	// azblob.Client owns no closable resources.
	return nil
}
