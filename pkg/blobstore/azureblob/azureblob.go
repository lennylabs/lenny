// SPDX-License-Identifier: MIT

// Package azureblob implements the §4.5 / §12.5 blobstore.Store over
// Azure Blob Storage.
//
// The adapter writes blobs into a configured container under a stable
// key derived from the §4.5 lenny-blob URI:
// "{tenant}/{session}/{part}". Storage account-level CMK encryption
// provides at-rest encryption; per-object Customer-Provided Keys
// (CPK) via the Config.CPKResolver hook overrides the default when a
// tenant carries a per-tenant key. Tombstoning records a
// "lenny-deleted-at" blob-metadata key (RFC 3339 UTC).
package azureblob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// objectClient narrows the Azure Blob client surface this Store uses.
type objectClient interface {
	UploadBuffer(ctx context.Context, key string, body []byte, opts *azblob.UploadBufferOptions) error
	DownloadStream(ctx context.Context, key string, opts *azblob.DownloadStreamOptions) (azblob.DownloadStreamResponse, error)
	GetProperties(ctx context.Context, key string, opts *blob.GetPropertiesOptions) (blob.GetPropertiesResponse, error)
	SetMetadata(ctx context.Context, key string, metadata map[string]*string, opts *blob.SetMetadataOptions) (blob.SetMetadataResponse, error)
	Delete(ctx context.Context, key string, opts *azblob.DeleteBlobOptions) error
	List(ctx context.Context) ([]string, error)
}

// CPKResolver returns the per-tenant customer-provided key info. The
// caller passes nil when the storage-account default applies.
type CPKResolver func(u blobstore.URI) *blob.CPKInfo

// Config configures a Store.
type Config struct {
	// ServiceURL is the Azure Blob storage account URL
	// ("https://<account>.blob.core.windows.net").
	ServiceURL string

	// Credential is the Azure SDK credential.
	Credential azcore.TokenCredential

	// Container is the container name. Required.
	Container string

	// CPKResolver, when set, picks per-object customer-provided keys.
	CPKResolver CPKResolver
}

// Store implements blobstore.Store + blobstore.Tombstoner over Azure
// Blob Storage.
type Store struct {
	client    objectClient
	container string
	resolver  CPKResolver
}

var (
	_ blobstore.Store      = (*Store)(nil)
	_ blobstore.Tombstoner = (*Store)(nil)
)

// New returns a Store. Returns an error when cfg.Container is empty
// or the client cannot be created.
func New(cfg Config) (*Store, error) {
	if cfg.ServiceURL == "" {
		return nil, errors.New("blobstore/azureblob: cfg.ServiceURL is required")
	}
	if cfg.Container == "" {
		return nil, errors.New("blobstore/azureblob: cfg.Container is required")
	}
	if cfg.Credential == nil {
		return nil, errors.New("blobstore/azureblob: cfg.Credential is required")
	}
	cli, err := azblob.NewClient(cfg.ServiceURL, cfg.Credential, nil)
	if err != nil {
		return nil, fmt.Errorf("blobstore/azureblob: azblob.NewClient: %w", err)
	}
	return &Store{
		client:    newClientAdapter(cli, cfg.Container),
		container: cfg.Container,
		resolver:  cfg.CPKResolver,
	}, nil
}

// newWithClient is the test-injection constructor.
func newWithClient(c objectClient, containerName string, resolver CPKResolver) *Store {
	return &Store{client: c, container: containerName, resolver: resolver}
}

// objectKey maps a blobstore.URI to the Azure blob key. The key
// follows the §12.5 ll. 295 canonical layout
// `{tenant_id}/{object_type}/{session_id}/{part_id}` so the §12.5
// GC sweep can prefix-scope by object class.
//
// spec: §12.5 ll. 295, 315.
func objectKey(u blobstore.URI) string {
	objType := u.ObjectType
	if objType == "" {
		objType = blobstore.ObjectTypeUpload
	}
	return fmt.Sprintf("%s/%s/%s/%s", u.TenantID, objType, u.SessionID, u.PartID)
}

const tombstoneMeta = "lenny_deleted_at"

func metaPtr(v string) *string { return &v }

// Put implements blobstore.Store.
func (s *Store) Put(u blobstore.URI, mimeType string, data io.Reader) (string, error) {
	ctx := context.Background()
	key := objectKey(u)
	if props, err := s.client.GetProperties(ctx, key, nil); err == nil {
		if _, tombstoned := props.Metadata[tombstoneMeta]; !tombstoned {
			return "", blobstore.ErrConflict
		}
	} else if !isNotFound(err) {
		return "", fmt.Errorf("blobstore/azureblob: GetProperties: %w", err)
	}
	body, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("blobstore/azureblob: read body: %w", err)
	}
	opts := &azblob.UploadBufferOptions{
		HTTPHeaders: &blob.HTTPHeaders{BlobContentType: &mimeType},
	}
	if s.resolver != nil {
		if cpk := s.resolver(u); cpk != nil {
			opts.CPKInfo = cpk
		}
	}
	if err := s.client.UploadBuffer(ctx, key, body, opts); err != nil {
		return "", fmt.Errorf("blobstore/azureblob: UploadBuffer: %w", err)
	}
	return u.String(), nil
}

// Get implements blobstore.Store.
func (s *Store) Get(u blobstore.URI) (blobstore.BlobInfo, io.ReadCloser, error) {
	ctx := context.Background()
	key := objectKey(u)
	if tombstoned, err := s.isTombstoned(ctx, key); err != nil {
		return blobstore.BlobInfo{}, nil, err
	} else if tombstoned {
		return blobstore.BlobInfo{}, nil, blobstore.ErrNotFound
	}
	out, err := s.client.DownloadStream(ctx, key, nil)
	if err != nil {
		if isNotFound(err) {
			return blobstore.BlobInfo{}, nil, blobstore.ErrNotFound
		}
		return blobstore.BlobInfo{}, nil, fmt.Errorf("blobstore/azureblob: DownloadStream: %w", err)
	}
	info := blobstore.BlobInfo{
		URI:      u,
		MimeType: ptrStr(out.ContentType),
		Size:     ptrInt64(out.ContentLength),
	}
	if out.LastModified != nil {
		info.StoredAt = out.LastModified.UTC()
	}
	return info, out.Body, nil
}

// Stat implements blobstore.Store.
func (s *Store) Stat(u blobstore.URI) (blobstore.BlobInfo, error) {
	ctx := context.Background()
	key := objectKey(u)
	if tombstoned, err := s.isTombstoned(ctx, key); err != nil {
		return blobstore.BlobInfo{}, err
	} else if tombstoned {
		return blobstore.BlobInfo{}, blobstore.ErrNotFound
	}
	props, err := s.client.GetProperties(ctx, key, nil)
	if err != nil {
		if isNotFound(err) {
			return blobstore.BlobInfo{}, blobstore.ErrNotFound
		}
		return blobstore.BlobInfo{}, fmt.Errorf("blobstore/azureblob: GetProperties: %w", err)
	}
	info := blobstore.BlobInfo{
		URI:      u,
		MimeType: ptrStr(props.ContentType),
		Size:     ptrInt64(props.ContentLength),
	}
	if props.LastModified != nil {
		info.StoredAt = props.LastModified.UTC()
	}
	return info, nil
}

// SoftDelete implements blobstore.Tombstoner.
func (s *Store) SoftDelete(u blobstore.URI) error {
	ctx := context.Background()
	key := objectKey(u)
	if _, err := s.client.GetProperties(ctx, key, nil); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("blobstore/azureblob: GetProperties: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	meta := map[string]*string{tombstoneMeta: metaPtr(now)}
	if _, err := s.client.SetMetadata(ctx, key, meta, nil); err != nil {
		return fmt.Errorf("blobstore/azureblob: SetMetadata: %w", err)
	}
	return nil
}

// HardPrune implements blobstore.Tombstoner.
func (s *Store) HardPrune(now time.Time, retention time.Duration) int {
	ctx := context.Background()
	cutoff := now.Add(-retention).UTC()
	keys, err := s.client.List(ctx)
	if err != nil {
		return 0
	}
	count := 0
	for _, key := range keys {
		props, err := s.client.GetProperties(ctx, key, nil)
		if err != nil {
			continue
		}
		rawPtr, ok := props.Metadata[tombstoneMeta]
		if !ok || rawPtr == nil {
			continue
		}
		deletedAt, err := time.Parse(time.RFC3339, *rawPtr)
		if err != nil || deletedAt.After(cutoff) {
			continue
		}
		if err := s.client.Delete(ctx, key, nil); err == nil {
			count++
		}
	}
	return count
}

func (s *Store) isTombstoned(ctx context.Context, key string) (bool, error) {
	props, err := s.client.GetProperties(ctx, key, nil)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("blobstore/azureblob: GetProperties: %w", err)
	}
	_, ok := props.Metadata[tombstoneMeta]
	return ok, nil
}

func isNotFound(err error) bool {
	if bloberror.HasCode(err, bloberror.BlobNotFound) || bloberror.HasCode(err, bloberror.ContainerNotFound) {
		return true
	}
	var rerr *azcore.ResponseError
	if errors.As(err, &rerr) && rerr.StatusCode == 404 {
		return true
	}
	return strings.Contains(err.Error(), "BlobNotFound") || strings.Contains(err.Error(), "404")
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptrInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// clientAdapter wraps a real *azblob.Client.
type clientAdapter struct {
	cli       *azblob.Client
	container string
}

func newClientAdapter(cli *azblob.Client, container string) objectClient {
	return &clientAdapter{cli: cli, container: container}
}

func (a *clientAdapter) UploadBuffer(ctx context.Context, key string, body []byte, opts *azblob.UploadBufferOptions) error {
	_, err := a.cli.UploadBuffer(ctx, a.container, key, body, opts)
	return err
}

func (a *clientAdapter) DownloadStream(ctx context.Context, key string, opts *azblob.DownloadStreamOptions) (azblob.DownloadStreamResponse, error) {
	return a.cli.DownloadStream(ctx, a.container, key, opts)
}

func (a *clientAdapter) GetProperties(ctx context.Context, key string, opts *blob.GetPropertiesOptions) (blob.GetPropertiesResponse, error) {
	return a.cli.ServiceClient().NewContainerClient(a.container).NewBlobClient(key).GetProperties(ctx, opts)
}

func (a *clientAdapter) SetMetadata(ctx context.Context, key string, metadata map[string]*string, opts *blob.SetMetadataOptions) (blob.SetMetadataResponse, error) {
	return a.cli.ServiceClient().NewContainerClient(a.container).NewBlobClient(key).SetMetadata(ctx, metadata, opts)
}

func (a *clientAdapter) Delete(ctx context.Context, key string, opts *azblob.DeleteBlobOptions) error {
	_, err := a.cli.DeleteBlob(ctx, a.container, key, opts)
	return err
}

func (a *clientAdapter) List(ctx context.Context) ([]string, error) {
	out := []string{}
	pager := a.cli.NewListBlobsFlatPager(a.container, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Segment.BlobItems {
			if item.Name != nil {
				out = append(out, *item.Name)
			}
		}
	}
	return out, nil
}

// Compile-time imports kept alive so tests can reference them.
var (
	_ = bytes.NewReader
	_ = container.NewClient
	_ = blockblob.NewClient
)
