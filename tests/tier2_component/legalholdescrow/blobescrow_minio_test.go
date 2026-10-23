//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §12.8 legal-hold escrow store against a real
// MinIO container: the escrow store is built over its own escrow bucket
// from the artifact store's provider options, a sealed escrow write
// succeeds for a tenant the artifact store's tenant SSE resolver refuses,
// the artifact store's tenant prefix delete leaves the escrow copy
// readable, and the escrow-GC release deletes it once the hold clears.
package legalholdescrow_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/providerflags"
	"github.com/lennylabs/lenny/pkg/legalholdescrow"
	"github.com/lennylabs/lenny/pkg/legalholdescrow/blobescrow"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// escrowBucket is the escrow bucket the test configures beside the
// artifact bucket the container pre-creates.
const escrowBucket = "lenny-legal-hold-escrow"

// recordingLedger persists each escrowed resource as an escrow record, as
// the gateway's ledger does, and accepts the release event.
type recordingLedger struct{ records legalholdescrow.RecordStore }

func (recordingLedger) RegionResolved(context.Context, legalholdescrow.RegionResolved) error {
	return nil
}

func (l recordingLedger) Escrowed(ctx context.Context, ev legalholdescrow.Escrowed) error {
	return l.records.Save(ctx, legalholdescrow.Record{
		TenantID: ev.TenantID, ResourceType: ev.ResourceType, ResourceID: ev.ResourceID,
		EscrowObjectKey: ev.EscrowObjectKey, EscrowRegion: ev.EscrowRegion, EscrowKEKID: ev.EscrowKEKID,
		SessionID: ev.SessionID, ArtifactURI: ev.ArtifactURI, MigratedAt: ev.MigratedAt,
	})
}

func (recordingLedger) EscrowReleased(context.Context, legalholdescrow.Released) error { return nil }

// prefixCipher stands in for the escrow KEK cipher; the sealing itself is
// covered by the gateway's unit tests.
type prefixCipher struct{}

func (prefixCipher) Seal(_ context.Context, pt []byte) ([]byte, error) {
	return append([]byte("sealed|"), pt...), nil
}

// artifactOptions are the artifact store's provider options over the
// container: tenant acme is T4 under the container's SSE-KMS key, and any
// other tenant has no row, so the resolver refuses it as the gateway's
// tenant resolver does.
func artifactOptions(mc *containers.MinIO) providerflags.Options {
	return providerflags.Options{
		Provider:       providerflags.ProviderMinIO,
		MinIOEndpoint:  mc.Endpoint,
		MinIOAccessKey: mc.AccessKey,
		MinIOSecretKey: mc.SecretKey,
		MinIOBucket:    mc.Bucket,
		SSEKeyResolver: func(tenantID string) (string, bool, error) {
			if tenantID == "acme" {
				return mc.KMSKeyName, true, nil
			}
			return "", true, errors.New("lookup tenant " + tenantID + ": tenant not found")
		},
	}
}

func readAll(t *testing.T, s blobstore.Store, u blobstore.URI) ([]byte, error) {
	t.Helper()
	_, rc, err := s.Get(u)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// TestLegalHoldEscrowStoreOnMinIO_spec_12_8 drives the escrow store end
// to end on MinIO.
//
// spec: 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope)), 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 4)
// diagnosis: a failure means the legal-hold escrow copy does not survive
// tenant deletion on MinIO. Either the escrow store inherits the tenant
// SSE-KMS resolver, so Phase 3.5 fails for a tenant with no row or a T4
// tenant, or the escrow copy lands where the artifact store's tenant prefix
// delete reaches it, so Phase 4 destroys held evidence, or the release
// cannot delete the copy once the hold clears.
func TestLegalHoldEscrowStoreOnMinIO_spec_12_8(t *testing.T) {
	ctx := context.Background()
	mc := containers.StartMinIO(t, containers.MinIOOptions{KMSKeyName: "lenny-t4-acme"})
	if err := mc.Client.MakeBucket(ctx, escrowBucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("make escrow bucket: %v", err)
	}
	artOpts := artifactOptions(mc)
	artifacts, err := providerflags.Resolve(ctx, artOpts)
	if err != nil {
		t.Fatalf("artifact store: %v", err)
	}
	escrow, err := blobescrow.NewStore(ctx, artOpts, escrowBucket, "")
	if err != nil || escrow == nil {
		t.Fatalf("escrow store: %v", err)
	}

	// A tenant with no row: the artifact store's resolver refuses the
	// write, and the escrow store, which carries no resolver, accepts it.
	ghostKey := legalholdescrow.EscrowObjectKey("ghost", "artifact", "r")
	if _, err := artifacts.Put(blobescrow.ObjectURI("ghost", ghostKey), "application/octet-stream", bytes.NewReader([]byte("x"))); err == nil {
		t.Fatal("the artifact store accepted a write for a tenant with no row; the discriminating case is vacuous")
	}
	if err := (blobescrow.Writer{Blobs: escrow, TenantID: "ghost"}).Write(ctx, ghostKey, []byte("sealed")); err != nil {
		t.Fatalf("escrow write for a tenant with no row: %v", err)
	}

	// A T4 tenant: escrow its held original, then run the prefix delete.
	orig := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "sess-1", PartID: "snapshot", TTL: time.Hour, Encoding: blobstore.Encoding}
	if _, err := artifacts.Put(orig, "application/octet-stream", bytes.NewReader([]byte("evidence"))); err != nil {
		t.Fatalf("seed T4 original: %v", err)
	}
	records := legalholdescrow.NewMemRecordStore()
	ledger := recordingLedger{records: records}
	mig := &legalholdescrow.Migrator{
		Config:  legalholdescrow.Config{Default: &legalholdescrow.RegionEscrow{Bucket: escrowBucket}},
		Cipher:  func(string) (legalholdescrow.Cipher, error) { return prefixCipher{}, nil },
		Source:  blobescrow.Source{Blobs: artifacts},
		Escrow:  blobescrow.Writer{Blobs: escrow, TenantID: "acme"},
		Ledger:  ledger,
		Records: records,
	}
	res, err := mig.Migrate(ctx, legalholdescrow.Input{TenantID: "acme", JobID: "j", Holds: []legalholdescrow.HeldArtifact{{
		ResourceType: "artifact", ResourceID: "r1", BlobURI: orig.String(), SessionID: "sess-1", ArtifactURI: orig.String(),
	}}})
	if err != nil {
		t.Fatalf("escrow a T4 tenant's held original: %v", err)
	}
	escrowURI := blobescrow.ObjectURI("acme", res.EscrowObjectKeys[0])

	if _, err := artifacts.(interface {
		DeleteByTenant(context.Context, string) (int, error)
	}).DeleteByTenant(ctx, "acme"); err != nil {
		t.Fatalf("artifact prefix delete: %v", err)
	}
	if _, err := readAll(t, artifacts, orig); !errors.Is(err, blobstore.ErrNotFound) {
		t.Fatalf("original after the prefix delete: err = %v, want ErrNotFound", err)
	}
	if got, err := readAll(t, escrow, escrowURI); err != nil || string(got) != "sealed|evidence" {
		t.Fatalf("escrow copy after the prefix delete = %q, %v", got, err)
	}

	releaser := &legalholdescrow.Releaser{Records: records, Deleter: blobescrow.Deleter{Blobs: escrow}, Ledger: ledger}
	if n, err := releaser.ReleaseForArtifact(ctx, "acme", orig.String(), "alice"); err != nil || n != 1 {
		t.Fatalf("ReleaseForArtifact = %d, %v", n, err)
	}
	if _, err := readAll(t, escrow, escrowURI); !errors.Is(err, blobstore.ErrNotFound) {
		t.Fatalf("escrow copy after the release: err = %v, want ErrNotFound", err)
	}
}
