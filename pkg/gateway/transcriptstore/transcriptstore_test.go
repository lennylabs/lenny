// SPDX-License-Identifier: MIT

package transcriptstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
)

func TestAppendAndGet(t *testing.T) {
	s := transcriptstore.NewMemory()
	err := s.Append(
		context.Background(), "acme", "sess_1",
		transcriptstore.Entry{Role: "user", Content: "hello"},
		transcriptstore.Entry{Role: "assistant", Content: "hi"},
	)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	entries, err := s.Get(context.Background(), "acme", "sess_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries: %d", len(entries))
	}
	if entries[0].Seq != 1 || entries[1].Seq != 2 {
		t.Errorf("seq not monotonic: %+v", entries)
	}
	if entries[0].Timestamp.IsZero() {
		t.Error("timestamp should be auto-set")
	}
}

func TestAppendAccumulatesSeq(t *testing.T) {
	s := transcriptstore.NewMemory()
	_ = s.Append(context.Background(), "acme", "sess_1", transcriptstore.Entry{Content: "a"})
	_ = s.Append(context.Background(), "acme", "sess_1", transcriptstore.Entry{Content: "b"})
	entries, _ := s.Get(context.Background(), "acme", "sess_1")
	if len(entries) != 2 || entries[1].Seq != 2 {
		t.Errorf("seq across appends: %+v", entries)
	}
}

func TestGetMissing(t *testing.T) {
	s := transcriptstore.NewMemory()
	if _, err := s.Get(context.Background(), "acme", "missing"); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Errorf("Get missing: %v", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	s := transcriptstore.NewMemory()
	_ = s.Append(context.Background(), "acme", "sess_1", transcriptstore.Entry{Content: "acme-data"})
	// Different tenant, same session id — must not see acme's data.
	if _, err := s.Get(context.Background(), "globex", "sess_1"); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Errorf("cross-tenant Get should be ErrNotFound: %v", err)
	}
}

func TestPage(t *testing.T) {
	s := transcriptstore.NewMemory()
	for i := 0; i < 5; i++ {
		_ = s.Append(context.Background(), "acme", "sess_1", transcriptstore.Entry{Content: "x"})
	}
	page, err := s.Page(context.Background(), "acme", "sess_1", 2, 2)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if len(page) != 2 || page[0].Seq != 3 || page[1].Seq != 4 {
		t.Errorf("page: %+v", page)
	}
}

func TestPageMissing(t *testing.T) {
	s := transcriptstore.NewMemory()
	if _, err := s.Page(context.Background(), "acme", "missing", 0, 10); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Errorf("Page missing: %v", err)
	}
}

func TestAppendEmptyIsNoOp(t *testing.T) {
	s := transcriptstore.NewMemory()
	if err := s.Append(context.Background(), "acme", "sess_1"); err != nil {
		t.Errorf("empty append: %v", err)
	}
	if _, err := s.Get(context.Background(), "acme", "sess_1"); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Error("empty append should not create a transcript")
	}
}
