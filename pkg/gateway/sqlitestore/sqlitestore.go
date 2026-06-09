// SPDX-License-Identifier: MIT

// Package sqlitestore is the embedded-SQLite durability layer for
// §17.4 Source Mode (`make run`). The spec requires Source Mode to
// replace Postgres with embedded SQLite for session and metadata
// storage while keeping the gateway free of an external database.
//
// The session and metadata stores the gateway uses without
// --postgres-dsn are the same in-memory implementations the tier-3
// REST-contract suites run against; their query logic already
// satisfies each store contract. This package adds durability across a
// process restart by snapshotting each registered in-memory store into
// a single embedded SQLite database file (`./lenny-data/lenny.db` under
// `make run`): the file is loaded into the stores on startup, flushed
// periodically while the process runs, and flushed once more on
// graceful shutdown. A store is registered by name and need only
// implement Snapshotter.
//
// spec: §17.4 line 199 — "Embedded SQLite replaces Postgres for session
// and metadata storage".
package sqlitestore

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	// modernc.org/sqlite is a pure-Go (cgo-free) SQLite driver, so
	// Source Mode stays a zero-dependency `go build` with no C toolchain.
	// It registers under the driver name "sqlite".
	_ "modernc.org/sqlite"
)

// Snapshotter is implemented by an in-memory store whose full state can
// be serialized to a byte slice and restored from one. ExportState
// returns the store's complete current contents; ImportState replaces
// the store's contents with a previously exported snapshot. The two are
// inverses: ImportState(ExportState()) leaves the store unchanged. A nil
// or empty snapshot resets the store to empty.
type Snapshotter interface {
	ExportState() ([]byte, error)
	ImportState(data []byte) error
}

type registration struct {
	name  string
	store Snapshotter
}

// DB is the embedded-SQLite persistence manager. It owns the SQLite
// connection and the ordered set of registered stores. All methods are
// safe for concurrent use.
type DB struct {
	db  *sql.DB
	now func() time.Time

	mu   sync.Mutex
	regs []registration
	last map[string][]byte // last-persisted bytes per store, to skip no-op writes
}

// Open opens (creating if absent) the SQLite database at path and
// ensures the snapshot table exists. path is a normal filesystem path;
// its parent directory must already exist.
func Open(path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: open %q: %w", path, err)
	}
	// A single embedded process owns the file; one connection keeps the
	// pure-Go driver's writes serialized and avoids "database is locked"
	// when the periodic flush and the shutdown flush overlap.
	sqldb.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := sqldb.Exec(pragma); err != nil {
			_ = sqldb.Close()
			return nil, fmt.Errorf("sqlitestore: %s: %w", pragma, err)
		}
	}
	if _, err := sqldb.Exec(`CREATE TABLE IF NOT EXISTS store_snapshots (
		name       TEXT PRIMARY KEY,
		data       BLOB NOT NULL,
		updated_at INTEGER NOT NULL
	)`); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("sqlitestore: create table: %w", err)
	}
	return &DB{db: sqldb, now: time.Now, last: map[string][]byte{}}, nil
}

// Register adds a store under a stable name. The name keys the row in
// the snapshot table and must be unique and stable across restarts (it
// is how Restore finds the store's prior contents). Register panics on a
// duplicate name, which is a wiring bug.
func (d *DB) Register(name string, store Snapshotter) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.regs {
		if r.name == name {
			panic("sqlitestore: duplicate store name " + name)
		}
	}
	d.regs = append(d.regs, registration{name: name, store: store})
}

// Restore loads each registered store's snapshot from the database into
// the store. A store with no stored snapshot is left at its current
// (empty) state. Restore runs once, before the gateway begins serving.
func (d *DB) Restore(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.regs {
		var data []byte
		err := d.db.QueryRowContext(ctx,
			`SELECT data FROM store_snapshots WHERE name = ?`, r.name).Scan(&data)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return fmt.Errorf("sqlitestore: restore %q: %w", r.name, err)
		}
		if err := r.store.ImportState(data); err != nil {
			return fmt.Errorf("sqlitestore: import %q: %w", r.name, err)
		}
		// Record the loaded bytes so a flush with no intervening write
		// does not rewrite an unchanged row.
		d.last[r.name] = append([]byte(nil), data...)
	}
	return nil
}

// Flush snapshots every registered store whose contents changed since
// the previous flush. Stores whose ExportState output is byte-identical
// to the persisted snapshot are skipped so an idle process does not
// churn the file.
func (d *DB) Flush(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.flushLocked(ctx)
}

func (d *DB) flushLocked(ctx context.Context) error {
	for _, r := range d.regs {
		data, err := r.store.ExportState()
		if err != nil {
			return fmt.Errorf("sqlitestore: export %q: %w", r.name, err)
		}
		if prev, ok := d.last[r.name]; ok && bytes.Equal(prev, data) {
			continue
		}
		if _, err := d.db.ExecContext(ctx,
			`INSERT INTO store_snapshots (name, data, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT(name) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
			r.name, data, d.now().UnixNano()); err != nil {
			return fmt.Errorf("sqlitestore: persist %q: %w", r.name, err)
		}
		d.last[r.name] = append([]byte(nil), data...)
	}
	return nil
}

// StartAutoFlush runs Flush every interval in a background goroutine
// until ctx is cancelled, returning immediately. A flush error is
// reported via onErr (nil drops it). The cancelled context stops the
// loop without a final flush; the shutdown flush is Close's job.
func (d *DB) StartAutoFlush(ctx context.Context, interval time.Duration, onErr func(error)) {
	if interval <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := d.Flush(ctx); err != nil && onErr != nil {
					onErr(err)
				}
			}
		}
	}()
}

// Close flushes all stores a final time and closes the database. The
// context bounds the final flush.
func (d *DB) Close(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	flushErr := d.flushLocked(ctx)
	closeErr := d.db.Close()
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}
