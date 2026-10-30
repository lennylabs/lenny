// SPDX-License-Identifier: MIT

package blobstore_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
)

// copyStore is a blob store with Copy, which both the in-memory and the
// filesystem stores satisfy.
type copyStore interface {
	blobstore.Store
	blobstore.Copier
}

// spec: 7.1 (Normal Flow: Derive copy semantics), 15.1 (REST API: Session
// Replay Semantics), 4.5 (Artifact Store)
//
// A Copy destination that names a TTL is stored with it, and one that names
// none keeps the source blob's TTL, measured from the copy's write, on both
// the in-memory and the filesystem stores. Before BUILD-GAPS F-4.5.24 a
// destination with no TTL, such as a checkpoint chunk URI, expired as it was
// written. The cases cover a copy made at once and late in the source's
// lifetime, and each read is checked just inside and just past the copy's
// expiry.
func TestCopyKeepsTheSourceTTLWhenTheDestinationNamesNone_spec_7_1(t *testing.T) {
	stores := []struct {
		name string
		new  func(t *testing.T, clock func() time.Time) copyStore
	}{
		{"memory", func(_ *testing.T, clock func() time.Time) copyStore { return blobstore.NewMemoryStore(clock) }},
		{"filesystem", func(t *testing.T, clock func() time.Time) copyStore {
			s, err := blobstore.NewFilesystemStore(t.TempDir(), clock)
			if err != nil {
				t.Fatalf("filesystem store: %v", err)
			}
			return s
		}},
	}
	cases := []struct {
		name      string
		objType   blobstore.ObjectType
		srcTTL    time.Duration
		dstTTL    time.Duration
		copyAfter time.Duration
		wantTTL   time.Duration
	}{
		{name: "checkpoint chunk copied at once", objType: blobstore.ObjectTypeCheckpoint, srcTTL: time.Hour, wantTTL: time.Hour},
		{name: "checkpoint chunk copied late in its lifetime", objType: blobstore.ObjectTypeCheckpoint, srcTTL: time.Hour, copyAfter: 55 * time.Minute, wantTTL: time.Hour},
		{name: "workspace snapshot with no destination TTL", objType: blobstore.ObjectTypeWorkspace, srcTTL: 48 * time.Hour, wantTTL: 48 * time.Hour},
		{name: "destination TTL wins over the source's", objType: blobstore.ObjectTypeWorkspace, srcTTL: time.Hour, dstTTL: 7 * 24 * time.Hour, wantTTL: 7 * 24 * time.Hour},
		{name: "shorter destination TTL wins too", objType: blobstore.ObjectTypeCheckpoint, srcTTL: 48 * time.Hour, dstTTL: time.Hour, wantTTL: time.Hour},
	}
	for _, st := range stores {
		for _, c := range cases {
			t.Run(st.name+"/"+c.name, func(t *testing.T) {
				start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
				now := start
				s := st.new(t, func() time.Time { return now })
				src := blobstore.URI{TenantID: "acme", ObjectType: c.objType, SessionID: "parent", PartID: "ckpt_1/chunk-00000.tar", TTL: c.srcTTL}
				if _, err := s.Put(src, "application/x-tar", strings.NewReader("chunk-bytes")); err != nil {
					t.Fatalf("Put src: %v", err)
				}
				now = start.Add(c.copyAfter)
				copiedAt := now
				dst := src
				dst.SessionID, dst.TTL = "derived", c.dstTTL
				if err := s.Copy(src, dst); err != nil {
					t.Fatalf("Copy: %v", err)
				}
				// The read URI is the one the restore path builds: it names no
				// TTL whatever the copy was given.
				read := dst
				read.TTL = 0
				info, err := s.Stat(read)
				if err != nil {
					t.Fatalf("Stat the copy at the copy instant: %v (F-4.5.24)", err)
				}
				if want := copiedAt.Add(c.wantTTL); !info.ExpiresAt.Equal(want) {
					t.Fatalf("copy expires at %s, want %s", info.ExpiresAt, want)
				}
				now = copiedAt.Add(c.wantTTL - time.Second)
				_, body, err := s.Get(read)
				if err != nil {
					t.Fatalf("Get the copy just inside its TTL: %v", err)
				}
				got, _ := io.ReadAll(body)
				_ = body.Close()
				if string(got) != "chunk-bytes" {
					t.Fatalf("copy body = %q, want the source's bytes", got)
				}
				now = copiedAt.Add(c.wantTTL + time.Second)
				if _, _, err := s.Get(read); !errors.Is(err, blobstore.ErrNotFound) {
					t.Fatalf("Get the copy past its TTL: err = %v, want ErrNotFound", err)
				}
			})
		}
	}
}
