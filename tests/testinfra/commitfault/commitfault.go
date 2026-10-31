// SPDX-License-Identifier: MIT

// Package commitfault builds a pgxpool whose connections a test can cut at
// the moment a transaction's COMMIT goes on the wire, so the client sees the
// commit fail while the server either commits or rolls back. It reproduces
// a lost commit acknowledgement (§12.3 Write durability categories during
// failover) against a real Postgres without a seam in the code under test.
//
// The pool holds at most one connection, so after a fault the next
// statement the code under test runs dials a new connection, and the hook
// installed with OnRedial decides what that dial sees: a refused dial, a
// dial that blocks until its context ends, or a dial that first lets a
// competing writer run.
package commitfault

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Mode names what the next COMMIT on the wire does.
type Mode int

const (
	// Pass forwards the COMMIT and its reply unchanged.
	Pass Mode = iota
	// CommitThenCut forwards the COMMIT to the server, half-closes the
	// connection so the server processes it and commits, waits for the
	// server to close its side so the commit has applied, and fails the
	// client's read of the reply.
	CommitThenCut
	// CutBeforeCommit closes the connection without forwarding the COMMIT,
	// so the server rolls the open transaction back.
	CutBeforeCommit
)

// ErrCut is the error a cut connection returns to the client.
var ErrCut = errors.New("commitfault: connection cut at COMMIT")

// commitMsg is the simple-protocol Query message pgx sends for Tx.Commit:
// type 'Q', a big-endian length of 11 (the length field plus "commit\x00"),
// and the NUL-terminated statement text.
var commitMsg = []byte("Q\x00\x00\x00\x0bcommit\x00")

// Injector arms faults on the connections of the pool NewPool returns and
// records every frontend message those connections write.
type Injector struct {
	mu       sync.Mutex
	armed    Mode
	fired    bool
	onRedial func(ctx context.Context) error
	writes   [][]byte
}

// NewPool returns a single-connection pool on dsn whose connections the
// returned Injector controls. The cleanup of t closes the pool.
func NewPool(t testing.TB, dsn string) (*pgxpool.Pool, *Injector) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("commitfault: parse dsn: %v", err)
	}
	inj := &Injector{}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.ConnConfig.DialFunc = inj.dial
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("commitfault: new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, inj
}

// Arm sets what the next COMMIT does and clears the fired state, so the
// OnRedial hook applies only to dials after that COMMIT.
func (i *Injector) Arm(m Mode) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.armed = m
	i.fired = false
}

// OnRedial installs fn to run before every dial made after an armed fault
// fires. An error from fn fails the dial; fn may block on ctx to model an
// endpoint that does not answer. A nil fn dials through.
func (i *Injector) OnRedial(fn func(ctx context.Context) error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.onRedial = fn
}

// Fired reports whether an armed fault has cut a connection since Arm.
func (i *Injector) Fired() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.fired
}

// Saw reports whether any connection wrote a message containing text since
// the pool was built or Reset last ran.
func (i *Injector) Saw(text string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, w := range i.writes {
		if bytes.Contains(w, []byte(text)) {
			return true
		}
	}
	return false
}

// Reset discards the recorded writes.
func (i *Injector) Reset() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.writes = nil
}

// dial is the pool's DialFunc.
func (i *Injector) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	i.mu.Lock()
	hook, fired := i.onRedial, i.fired
	i.mu.Unlock()
	if fired && hook != nil {
		if err := hook(ctx); err != nil {
			return nil, err
		}
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return &conn{Conn: c, inj: i}, nil
}

// record keeps a copy of one written buffer.
func (i *Injector) record(b []byte) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.writes = append(i.writes, append([]byte(nil), b...))
}

// take returns the armed mode for a COMMIT on the wire and disarms it. A
// mode other than Pass marks the fault fired.
func (i *Injector) take() Mode {
	i.mu.Lock()
	defer i.mu.Unlock()
	m := i.armed
	i.armed = Pass
	if m != Pass {
		i.fired = true
	}
	return m
}

// conn is one pool connection under the Injector's control.
type conn struct {
	net.Conn
	inj *Injector

	mu  sync.Mutex
	cut bool
}

// Write forwards b unless it carries an armed COMMIT.
func (c *conn) Write(b []byte) (int, error) {
	if c.isCut() {
		return 0, ErrCut
	}
	c.inj.record(b)
	if !bytes.Contains(b, commitMsg) {
		return c.Conn.Write(b)
	}
	switch c.inj.take() {
	case CommitThenCut:
		// The cut is marked before the COMMIT goes on the wire, so no read
		// can return its reply. pgconn starts a background reader when a
		// write takes longer than 15ms, which on a loaded host this one
		// does; a cut marked after the forward let that reader take the
		// server's COMMIT reply, and the client then saw the commit
		// succeed with the fault reported as fired.
		c.markCut()
		n, err := c.Conn.Write(b)
		if err != nil {
			return n, err
		}
		// A half-close delivers the COMMIT and then EOF, so the server
		// commits before it sees the client go away.
		if tc, ok := c.Conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		c.awaitServerClose()
		return n, nil
	case CutBeforeCommit:
		c.markCut()
		_ = c.Close()
		return 0, ErrCut
	default:
		return c.Conn.Write(b)
	}
}

// commitDrainTimeout bounds how long a CommitThenCut fault waits for the
// server to close its side after the half-close. It is a hang guard: a
// server that processes the COMMIT and the EOF closes within milliseconds.
const commitDrainTimeout = 30 * time.Second

// awaitServerClose reads and discards the server's reply until the server
// closes its side of the connection. A Postgres backend processes the
// messages it received in order, so it commits, writes the reply, reads the
// EOF the half-close sent, and exits. Waiting for that close makes the
// commit visible before the client sees the cut: without it the code under
// test can return, and the test can read the primary, while the COMMIT is
// still queued in the backend's receive buffer, and on a loaded host the
// read observes the row as it was before the write.
func (c *conn) awaitServerClose() {
	_ = c.Conn.SetReadDeadline(time.Now().Add(commitDrainTimeout))
	_, _ = io.Copy(io.Discard, c.Conn)
}

// Read fails once the connection is cut, so the client never sees the
// COMMIT reply. The cut is checked again after the underlying read returns,
// because a read already blocked when the COMMIT went out (pgconn's
// background reader, left running by an earlier slow write) would otherwise
// return the reply. The client has consumed every earlier reply before it
// sends the COMMIT, so the bytes such a read discards belong to the COMMIT.
func (c *conn) Read(b []byte) (int, error) {
	if c.isCut() {
		return 0, ErrCut
	}
	n, err := c.Conn.Read(b)
	if c.isCut() {
		return 0, ErrCut
	}
	return n, err
}

func (c *conn) markCut() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cut = true
}

func (c *conn) isCut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cut
}
