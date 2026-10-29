// SPDX-License-Identifier: MIT

package blobstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/prefixlisttest"
)

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 12.5 (Artifact Store)
func TestMemoryStoreListByPrefixConformance(t *testing.T) {
	prefixlisttest.Run(t, func(t *testing.T) prefixlisttest.Store {
		return blobstore.NewMemoryStore(nil)
	})
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume), 12.5 (Artifact Store)
func TestFilesystemStoreListByPrefixConformance(t *testing.T) {
	prefixlisttest.Run(t, func(t *testing.T) prefixlisttest.Store {
		s, err := blobstore.NewFilesystemStore(t.TempDir(), nil)
		if err != nil {
			t.Fatalf("NewFilesystemStore: %v", err)
		}
		return s
	})
}

// spec: 12.5 (Artifact Store: tenant-prefixed object keys), 10.1.7 (preStop Hook Drain: Reassembly on resume)
func TestPrefixListKeysScopesToOneSession(t *testing.T) {
	cases := []struct {
		name       string
		u          blobstore.URI
		wantKey    string
		wantSess   string
		wantErrURI bool
	}{
		{
			name:     "checkpoint prefix",
			u:        prefixlisttest.PrefixURI("acme", "s1", "ck"),
			wantKey:  "acme/checkpoints/s1/ck/",
			wantSess: "acme/checkpoints/s1/",
		},
		{
			name:     "empty object type normalises to upload",
			u:        blobstore.URI{TenantID: "acme", SessionID: "s1", PartID: "p"},
			wantKey:  "acme/upload/s1/p",
			wantSess: "acme/upload/s1/",
		},
		{
			name:     "empty part id lists the whole session",
			u:        blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeCheckpoint, SessionID: "s1"},
			wantKey:  "acme/checkpoints/s1/",
			wantSess: "acme/checkpoints/s1/",
		},
		{name: "empty tenant", u: blobstore.URI{SessionID: "s1"}, wantErrURI: true},
		{name: "slash in tenant", u: blobstore.URI{TenantID: "a/b", SessionID: "s1"}, wantErrURI: true},
		{name: "empty session", u: blobstore.URI{TenantID: "acme"}, wantErrURI: true},
		{name: "slash in session", u: blobstore.URI{TenantID: "acme", SessionID: "s/1"}, wantErrURI: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, sess, err := blobstore.PrefixListKeys(tc.u)
			if tc.wantErrURI {
				if !errors.Is(err, blobstore.ErrInvalidURI) {
					t.Fatalf("err = %v, want ErrInvalidURI", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("PrefixListKeys: %v", err)
			}
			if key != tc.wantKey || sess != tc.wantSess {
				t.Fatalf("PrefixListKeys = (%q, %q), want (%q, %q)", key, sess, tc.wantKey, tc.wantSess)
			}
		})
	}
}

// spec: 10.1.7 (preStop Hook Drain: Reassembly on resume)
func TestMemoryStoreListByPrefixExcludesExpiredBlob(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := blobstore.NewMemoryStore(func() time.Time { return now })
	prefixlisttest.PutChunk(t, s, "acme", "s", "ck", 0, 1) // TTL one hour
	now = now.Add(time.Hour + time.Second)
	got, err := s.ListByPrefix(context.Background(), prefixlisttest.PrefixURI("acme", "s", "ck"))
	if err != nil {
		t.Fatalf("ListByPrefix: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListByPrefix = %d entries after the TTL elapsed, want 0 as Stat reports", len(got))
	}
}
