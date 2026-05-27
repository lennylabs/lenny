// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
)

// errUploadAborted is the sentinel returned by an abortableReader after
// the per-session abort signal fires. The §7.4 line 463 finalize path
// raises the signal so any in-flight /upload Read aborts before the
// next chunk lands in the blob store. The upload handler maps the
// sentinel to a 410 GONE / UPLOAD_CHANNEL_CLOSED response and
// soft-deletes the partially-written blob.
//
// spec: §7.4 line 463 — "Upload channel closes after workspace
// finalization." F-7.4.16.
var errUploadAborted = errors.New("sessionserver: upload aborted by finalize")

// uploadAbortRegistry tracks per-session in-flight /upload handlers
// so the §7.4 line 463 finalize transition can sever them. Each
// handler registers a chan struct{} keyed by a process-local id; the
// finalize handler closes every chan still keyed under the session
// after the row transitions out of the upload-admitting state. A
// closed channel is the abort signal an abortableReader watches for
// on every Read.
//
// The registry is goroutine-safe. A handler that registers AFTER its
// session has been finalized still gets an already-closed abort chan
// because the finalize call leaves the session-id key behind with a
// closed channel set for new registrations to observe; see register
// for the post-finalize semantics. F-7.4.16.
type uploadAbortRegistry struct {
	mu     sync.Mutex
	state  map[string]*uploadAbortSet
	nextID atomic.Uint64
}

// uploadAbortSet is the per-session in-flight set. The closed flag
// records whether finalize has already fired so a late register hands
// the caller an already-closed channel; the registrations map tracks
// the live abort channels by their process-local id.
type uploadAbortSet struct {
	closed        bool
	registrations map[uint64]chan struct{}
}

// newUploadAbortRegistry returns a fresh registry. Production wires
// exactly one per Server.
func newUploadAbortRegistry() *uploadAbortRegistry {
	return &uploadAbortRegistry{state: map[string]*uploadAbortSet{}}
}

// register adds a new abort signal for the session. The returned ch
// is closed when finalize fires (or already closed if finalize beat
// the registration). release must run on the upload-handler exit path
// to drop the registration; calling release after the channel has
// been closed by finalize is a safe no-op.
//
// Calling register on a registry that is nil — the minimal-gateway
// posture before the abort registry is wired — returns (nil, no-op)
// so callers can treat the registry as optional.
func (r *uploadAbortRegistry) register(sid string) (ch <-chan struct{}, release func()) {
	if r == nil {
		return nil, func() {}
	}
	r.mu.Lock()
	set, ok := r.state[sid]
	if !ok {
		set = &uploadAbortSet{registrations: map[uint64]chan struct{}{}}
		r.state[sid] = set
	}
	id := r.nextID.Add(1)
	c := make(chan struct{})
	if set.closed {
		// Finalize beat the register: hand back an already-closed
		// signal so the caller aborts on the next Read.
		close(c)
	} else {
		set.registrations[id] = c
	}
	r.mu.Unlock()
	return c, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		set, ok := r.state[sid]
		if !ok {
			return
		}
		delete(set.registrations, id)
		// Keep the entry around once finalize has closed it so a late
		// register still sees the closed flag. Drop empty pre-finalize
		// entries so the registry stays bounded.
		if !set.closed && len(set.registrations) == 0 {
			delete(r.state, sid)
		}
	}
}

// closeSession fires the abort signal for every in-flight upload on
// the session. Subsequent register calls on the same session return
// an already-closed channel. closeSession is idempotent. Returns the
// number of in-flight registrations that were aborted (zero when no
// upload was in-flight or when finalize fires twice).
//
// closeSession is the §7.4 line 463 entry point invoked from the
// finalize handler. Calling closeSession on a nil registry is a safe
// no-op.
func (r *uploadAbortRegistry) closeSession(sid string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	set, ok := r.state[sid]
	if !ok {
		// Stamp the closed flag so a future register sees it.
		r.state[sid] = &uploadAbortSet{closed: true, registrations: map[uint64]chan struct{}{}}
		return 0
	}
	if set.closed {
		return 0
	}
	set.closed = true
	aborted := len(set.registrations)
	for id, c := range set.registrations {
		close(c)
		delete(set.registrations, id)
	}
	return aborted
}

// abortableReader wraps an io.Reader with the per-session abort
// signal. On every Read the channel is polled non-blockingly; a
// closed channel surfaces as errUploadAborted before the underlying
// Read fires. This is the §7.4 line 463 channel-close mechanism the
// finalize handler drives. F-7.4.16.
//
// The check is non-preemptive: a Read already blocked inside the
// underlying body will not return until the next chunk lands. In
// practice the http body returns chunks small enough (32 KiB by
// default) that the abort latency is bounded by one network chunk.
// For a /upload stream stalled mid-chunk the close still propagates
// once the stalled Read returns.
type abortableReader struct {
	r     io.Reader
	abort <-chan struct{}
}

// newAbortableReader wraps r with the abort signal. A nil abort
// channel disables the check and returns r unchanged so the minimal
// gateway path stays free of the abort overhead.
func newAbortableReader(r io.Reader, abort <-chan struct{}) io.Reader {
	if abort == nil {
		return r
	}
	return &abortableReader{r: r, abort: abort}
}

func (a *abortableReader) Read(p []byte) (int, error) {
	select {
	case <-a.abort:
		return 0, errUploadAborted
	default:
	}
	return a.r.Read(p)
}
