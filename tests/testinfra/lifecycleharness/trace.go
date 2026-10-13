// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// FaultKind is one kind of fault the harness injects at a trace index.
type FaultKind int

const (
	// FaultNone injects nothing.
	FaultNone FaultKind = iota
	// FaultStoreError makes the call return a store error without applying.
	FaultStoreError
	// FaultContextCancel makes the call return context.Canceled.
	FaultContextCancel
	// FaultCrash lets the call complete and then crashes the replica that
	// issued it, between this call and the next.
	FaultCrash
	// FaultPeerTakeover lets the call complete and then hands the session to
	// a peer replica, advancing the row's coordination generation.
	FaultPeerTakeover
)

func (k FaultKind) String() string {
	switch k {
	case FaultStoreError:
		return "store-error"
	case FaultContextCancel:
		return "context-cancel"
	case FaultCrash:
		return "crash"
	case FaultPeerTakeover:
		return "peer-takeover"
	default:
		return "none"
	}
}

// ErrInjected is the error a FaultStoreError returns.
var ErrInjected = errors.New("lifecycleharness: injected store error")

// TraceEntry is one recorded call on the shared session store.
type TraceEntry struct {
	// Index is the entry's position in the trace.
	Index int
	// Call is the store method, such as Update or Get.
	Call string
	// Site is the gateway function that issued the call.
	Site string
	// SessionID is the session the call named, when it named one.
	SessionID string
}

// Key is the entry's call and call site, the identity two traces compare.
func (e TraceEntry) Key() string { return e.Call + "@" + e.Site }

// Trace is the ordered record of the shared store's calls, with at most one
// armed fault.
type Trace struct {
	mu        sync.Mutex
	entries   []TraceEntry
	faultAt   int
	faultKey  string
	fault     FaultKind
	onFault   func(TraceEntry, FaultKind)
	recording bool
}

// NewTrace returns an empty trace with recording on and no fault armed.
func NewTrace() *Trace { return &Trace{faultAt: -1, recording: true} }

// Arm arms one fault of kind at index. The harness calls onFault for the
// crash and peer-takeover kinds, after the call at index completes.
func (t *Trace) Arm(index int, kind FaultKind, onFault func(TraceEntry, FaultKind)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.faultAt, t.fault, t.onFault = index, kind, onFault
}

// ArmAtKey arms one fault of kind at the next recorded call whose Key is
// key, so a re-run injects at the same call and call site as the clean run
// without depending on absolute indices. Recording restarts.
func (t *Trace) ArmAtKey(key string, kind FaultKind, onFault func(TraceEntry, FaultKind)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.faultAt, t.faultKey, t.fault, t.onFault, t.recording = -1, key, kind, onFault, true
}

// Start restarts recording with no fault armed and an empty trace.
func (t *Trace) Start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries, t.faultAt, t.faultKey, t.fault, t.onFault, t.recording = nil, -1, "", FaultNone, nil, true
}

// Entries returns a copy of the recorded entries.
func (t *Trace) Entries() []TraceEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]TraceEntry(nil), t.entries...)
}

// Stop stops recording; the checker's own reads are not part of a trace.
func (t *Trace) Stop() {
	t.mu.Lock()
	t.recording = false
	t.mu.Unlock()
}

// record appends an entry and reports the fault armed at its index.
func (t *Trace) record(call, sessionID string) (TraceEntry, FaultKind, func(TraceEntry, FaultKind)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.recording {
		return TraceEntry{}, FaultNone, nil
	}
	e := TraceEntry{Index: len(t.entries), Call: call, Site: callSite(), SessionID: sessionID}
	t.entries = append(t.entries, e)
	if e.Index == t.faultAt || (t.faultKey != "" && e.Key() == t.faultKey) {
		f, cb := t.fault, t.onFault
		t.faultAt, t.faultKey, t.fault, t.onFault = -1, "", FaultNone, nil
		return e, f, cb
	}
	return e, FaultNone, nil
}

// callSite names the first gateway function on the stack outside the store
// packages and this harness.
func callSite() string {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		fn := f.Function
		if strings.Contains(fn, "lenny/pkg/") && !strings.Contains(fn, "/sessionstore") {
			return fn[strings.LastIndex(fn, "/")+1:]
		}
		if !more {
			return "unknown"
		}
	}
}

// TracedStore records every call on the wrapped session store in the trace
// and applies the fault armed at the call's index.
type TracedStore struct {
	sessionstore.Store
	trace *Trace
}

// NewTracedStore wraps st.
func NewTracedStore(st sessionstore.Store, trace *Trace) *TracedStore {
	return &TracedStore{Store: st, trace: trace}
}

func (s *TracedStore) before(call, id string) (TraceEntry, FaultKind, func(TraceEntry, FaultKind), error) {
	e, f, cb := s.trace.record(call, id)
	switch f {
	case FaultStoreError:
		return e, f, cb, fmt.Errorf("%s %s: %w", call, id, ErrInjected)
	case FaultContextCancel:
		return e, f, cb, context.Canceled
	}
	return e, f, cb, nil
}

func after(e TraceEntry, f FaultKind, cb func(TraceEntry, FaultKind)) {
	if (f == FaultCrash || f == FaultPeerTakeover) && cb != nil {
		cb(e, f)
	}
}

// Get records and reads a row.
func (s *TracedStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	e, f, cb, err := s.before("Get", id)
	if err != nil {
		return sessionstore.Session{}, err
	}
	defer after(e, f, cb)
	return s.Store.Get(ctx, tenantID, id)
}

// GetPrimary records and reads a row from the primary.
func (s *TracedStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	e, f, cb, err := s.before("GetPrimary", id)
	if err != nil {
		return sessionstore.Session{}, err
	}
	defer after(e, f, cb)
	if pr, ok := s.Store.(sessionstore.PrimaryReader); ok {
		return pr.GetPrimary(ctx, tenantID, id)
	}
	return s.Store.Get(ctx, tenantID, id)
}

// Update records and runs one guarded write.
func (s *TracedStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	e, f, cb, err := s.before("Update", id)
	if err != nil {
		return sessionstore.Session{}, err
	}
	defer after(e, f, cb)
	return s.Store.Update(ctx, tenantID, id, mut)
}

// Create records and creates a row.
func (s *TracedStore) Create(ctx context.Context, row sessionstore.Session) error {
	e, f, cb, err := s.before("Create", row.ID)
	if err != nil {
		return err
	}
	defer after(e, f, cb)
	return s.Store.Create(ctx, row)
}

// List records and lists rows.
func (s *TracedStore) List(ctx context.Context, tenantID string, filter sessionstore.ListFilter) ([]sessionstore.Session, error) {
	e, f, cb, err := s.before("List", "")
	if err != nil {
		return nil, err
	}
	defer after(e, f, cb)
	return s.Store.List(ctx, tenantID, filter)
}
