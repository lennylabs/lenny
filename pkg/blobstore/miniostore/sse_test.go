// SPDX-License-Identifier: MIT

package miniostore

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// spec: §12.5 (SSEKeyResolver hook records the tenant id the
// resolver should look up the per-tenant KEK for)
//
// The resolver is the §12.5 T4-per-tenant-KMS plumbing: production
// installs hand New a function that maps an arrived tenant id to
// the tenant's scoped KMS key alias. The resolver is exercised
// purely through the Config wiring here; the real PutObject path
// requires a MinIO container with a KMS endpoint configured and is
// covered by the production install's preflight rather than a unit
// test.
func TestNewAcceptsSSEKeyResolver(t *testing.T) {
	calls := map[string]int{}
	s, err := New(Config{
		Endpoint: "localhost:9000",
		Bucket:   "lenny",
		SSEKeyResolver: func(tenantID string) (string, bool) {
			calls[tenantID]++
			return "tenant:" + tenantID, true
		},
	})
	if err != nil {
		t.Fatalf("New with SSEKeyResolver: %v", err)
	}
	if s.sseResolver == nil {
		t.Error("Store.sseResolver was not populated from Config.SSEKeyResolver")
	}
	if got, _ := s.sseResolver("acme"); got != "tenant:acme" {
		t.Errorf("resolver returned %q, want tenant:acme", got)
	}
	if calls["acme"] != 1 {
		t.Errorf("resolver call count for acme = %d, want 1", calls["acme"])
	}
}

// spec: §12.8 (legal-hold guard blocks DeleteBySession)
//
// The hold registry is in-memory and per-process; SetLegalHold
// records a hold against the blob's objectKey and DeleteBySession
// fails closed when any object in the session is held.
func TestLegalHoldGuardRecordsAndClears(t *testing.T) {
	s := &Store{clock: func() time.Time { return time.Now().UTC() }}
	u := blobstore.URI{TenantID: "acme", SessionID: "s_1", PartID: "p_1", TTL: time.Hour, Encoding: blobstore.Encoding}
	if s.hasLegalHold(objectKey(u)) {
		t.Error("fresh store should not have any holds")
	}
	s.SetLegalHold(u)
	if !s.hasLegalHold(objectKey(u)) {
		t.Error("SetLegalHold did not record the hold")
	}
	s.ClearLegalHold(u)
	if s.hasLegalHold(objectKey(u)) {
		t.Error("ClearLegalHold did not remove the hold")
	}
}
