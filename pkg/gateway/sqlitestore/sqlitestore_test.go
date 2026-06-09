// SPDX-License-Identifier: MIT

package sqlitestore

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeStore is a minimal Snapshotter whose state is a single byte slice.
// It records every ImportState call so a test can assert restore
// behaviour, and can be told to fail export or import to exercise the
// error paths.
type fakeStore struct {
	mu        sync.Mutex
	data      []byte
	exportErr error
	importErr error
	imports   int
}

func (f *fakeStore) set(b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = append([]byte(nil), b...)
}

func (f *fakeStore) get() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.data...)
}

func (f *fakeStore) ExportState() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.exportErr != nil {
		return nil, f.exportErr
	}
	return append([]byte(nil), f.data...), nil
}

func (f *fakeStore) ImportState(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imports++
	if f.importErr != nil {
		return f.importErr
	}
	f.data = append([]byte(nil), data...)
	return nil
}

func tmpDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "lenny.db")
}

// spec: §17.4 line 199 — embedded SQLite for session and metadata
// storage. A store flushed to the file and then loaded into a fresh
// process (a second Open of the same path) recovers its contents.
func TestDB_RoundTripAcrossReopen_spec_17_4_199(t *testing.T) {
	path := tmpDBPath(t)
	ctx := context.Background()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	src := &fakeStore{}
	db.Register("sessions", src)
	src.set([]byte(`{"a":1}`))
	if err := db.Close(ctx); err != nil { // Close does the final flush.
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close(ctx)
	dst := &fakeStore{}
	db2.Register("sessions", dst)
	if err := db2.Restore(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := string(dst.get()); got != `{"a":1}` {
		t.Fatalf("restored data = %q, want %q", got, `{"a":1}`)
	}
}

// spec: §17.4 line 199 — the periodic flush must not rewrite a store
// whose contents did not change, so an idle gateway does not churn the
// file. now() is called only on an actual write, so its call count is a
// proxy for the number of rows written.
func TestDB_FlushSkipsUnchanged_spec_17_4_199(t *testing.T) {
	db, err := Open(tmpDBPath(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close(context.Background())
	var writes int
	db.now = func() time.Time { writes++; return time.Unix(int64(writes), 0) }

	src := &fakeStore{}
	db.Register("sessions", src)
	src.set([]byte("v1"))

	if err := db.Flush(context.Background()); err != nil {
		t.Fatalf("flush 1: %v", err)
	}
	if err := db.Flush(context.Background()); err != nil {
		t.Fatalf("flush 2: %v", err)
	}
	if writes != 1 {
		t.Fatalf("unchanged store written %d times, want 1", writes)
	}

	src.set([]byte("v2"))
	if err := db.Flush(context.Background()); err != nil {
		t.Fatalf("flush 3: %v", err)
	}
	if writes != 2 {
		t.Fatalf("after mutation writes = %d, want 2", writes)
	}
}

// A store with no persisted snapshot is left untouched by Restore.
func TestDB_RestoreMissingRowLeavesStore_spec_17_4_199(t *testing.T) {
	db, err := Open(tmpDBPath(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close(context.Background())
	src := &fakeStore{}
	db.Register("fresh", src)
	if err := db.Restore(context.Background()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if src.imports != 0 {
		t.Fatalf("ImportState called %d times for a missing row, want 0", src.imports)
	}
}

// A store whose ImportState rejects the persisted snapshot surfaces the
// error from Restore rather than silently dropping data.
func TestDB_ImportErrorPropagates_spec_17_4_199(t *testing.T) {
	path := tmpDBPath(t)
	ctx := context.Background()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	good := &fakeStore{}
	db.Register("sessions", good)
	good.set([]byte("payload"))
	if err := db.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close(ctx)
	wantErr := errors.New("boom")
	db2.Register("sessions", &fakeStore{importErr: wantErr})
	if err := db2.Restore(ctx); !errors.Is(err, wantErr) {
		t.Fatalf("restore err = %v, want wrap of %v", err, wantErr)
	}
}

// An ExportState failure surfaces from Flush.
func TestDB_ExportErrorPropagates_spec_17_4_199(t *testing.T) {
	db, err := Open(tmpDBPath(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close(context.Background())
	wantErr := errors.New("export failed")
	db.Register("sessions", &fakeStore{exportErr: wantErr})
	if err := db.Flush(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("flush err = %v, want wrap of %v", err, wantErr)
	}
}

func TestDB_DuplicateNamePanics_spec_17_4_199(t *testing.T) {
	db, err := Open(tmpDBPath(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close(context.Background())
	db.Register("sessions", &fakeStore{})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate Register did not panic")
		}
	}()
	db.Register("sessions", &fakeStore{})
}

// StartAutoFlush persists a store mutation without an explicit Flush.
func TestDB_AutoFlushPersists_spec_17_4_199(t *testing.T) {
	path := tmpDBPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	src := &fakeStore{}
	db.Register("sessions", src)
	src.set([]byte("auto"))
	db.StartAutoFlush(ctx, 5*time.Millisecond, func(error) {})

	// Poll a second Open of the same WAL database until the row appears.
	deadline := time.Now().Add(2 * time.Second)
	for {
		probe, err := Open(path)
		if err != nil {
			t.Fatalf("probe open: %v", err)
		}
		dst := &fakeStore{}
		probe.Register("sessions", dst)
		_ = probe.Restore(ctx)
		got := string(dst.get())
		_ = probe.Close(ctx)
		if got == "auto" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("auto-flush did not persist within deadline; last=%q", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := db.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
}
