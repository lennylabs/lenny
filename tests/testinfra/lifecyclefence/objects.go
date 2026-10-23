// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"sync"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
)

// Objects is the World's blob store: the in-memory production blob store,
// which the derive handler copies snapshot chunks into and the delegation
// export writes files into, with injectable per-session deletion faults. Its
// DeleteBySession is what the derive undo, the delegation undo, and the
// erasure's artifacts eraser call.
type Objects struct {
	*blobstore.MemoryStore

	mu sync.Mutex
	// deleteFaults is the number of upcoming DeleteBySession calls that
	// fail with ErrInjected and delete nothing.
	deleteFaults int
}

// NewObjects returns an empty Objects.
func NewObjects() *Objects { return &Objects{MemoryStore: blobstore.NewMemoryStore(nil)} }

// FailDeletes makes the next n DeleteBySession calls fail with ErrInjected
// and delete nothing.
func (o *Objects) FailDeletes(n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.deleteFaults = n
}

// DeleteBySession deletes every object under the session, unless an armed
// fault fails the call first.
func (o *Objects) DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error) {
	o.mu.Lock()
	fail := o.deleteFaults > 0
	if fail {
		o.deleteFaults--
	}
	o.mu.Unlock()
	if fail {
		return 0, ErrInjected
	}
	return o.MemoryStore.DeleteBySession(ctx, tenantID, sessionID)
}

// Count returns the number of objects stored under the session.
func (o *Objects) Count(tenantID, sessionID string) int {
	n := 0
	for _, u := range o.Keys() {
		if u.TenantID == tenantID && u.SessionID == sessionID {
			n++
		}
	}
	return n
}

// Manifests is the World's checkpoint_manifest store: the in-memory
// production store, which the derive handler writes the derived manifest
// row into and whose DeleteByUser the derive undo calls, plus the seeded
// chunked checkpoint of each replay source, which the derive reads.
type Manifests struct {
	*partialmanifeststore.MemoryStore

	mu sync.Mutex
	// parents holds each source's chunked checkpoint by checkpoint ID.
	parents map[string]partialmanifeststore.Record
	// bySession lists the checkpoint IDs written under each (tenant,
	// session) key, so Count can find the rows of one session.
	bySession map[[2]string][]string
}

// NewManifests returns an empty Manifests.
func NewManifests() *Manifests {
	return &Manifests{
		MemoryStore: partialmanifeststore.NewMemoryStore(nil),
		parents:     map[string]partialmanifeststore.Record{},
		bySession:   map[[2]string][]string{},
	}
}

// seedParent records a source session's complete chunked checkpoint.
func (m *Manifests) seedParent(r partialmanifeststore.Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.parents[r.CheckpointID] = r
}

// Get returns a seeded source checkpoint or a written row.
func (m *Manifests) Get(ctx context.Context, tenantID, checkpointID string) (partialmanifeststore.Record, error) {
	m.mu.Lock()
	r, ok := m.parents[checkpointID]
	m.mu.Unlock()
	if ok && r.TenantID == tenantID {
		return r, nil
	}
	return m.MemoryStore.Get(ctx, tenantID, checkpointID)
}

// Put writes a manifest row and records its session.
func (m *Manifests) Put(ctx context.Context, r partialmanifeststore.Record) error {
	if err := m.MemoryStore.Put(ctx, r); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := [2]string{r.TenantID, r.SessionID}
	m.bySession[key] = append(m.bySession[key], r.CheckpointID)
	return nil
}

// Count returns the number of manifest rows still stored under the
// session.
func (m *Manifests) Count(tenantID, sessionID string) int {
	m.mu.Lock()
	ids := append([]string(nil), m.bySession[[2]string{tenantID, sessionID}]...)
	m.mu.Unlock()
	n := 0
	for _, id := range ids {
		if _, err := m.MemoryStore.Get(context.Background(), tenantID, id); err == nil {
			n++
		}
	}
	return n
}

// SessionTotal returns the number of manifest rows and seeded source
// checkpoints still stored under the session.
func (m *Manifests) SessionTotal(tenantID, sessionID string) int {
	m.mu.Lock()
	n := 0
	for _, r := range m.parents {
		if r.TenantID == tenantID && r.SessionID == sessionID {
			n++
		}
	}
	m.mu.Unlock()
	return n + m.Count(tenantID, sessionID)
}

// DeleteBySession deletes the session's manifest rows and its seeded source
// checkpoint, as the user-path checkpoint_manifests entry does through the
// production store's session-ID-list DeleteByUser, and returns the number of
// source checkpoints it removed. The production adapter records no count.
//
// spec: §12.8 (Compliance Interfaces: DeleteByUser dependency-ordered deletion sequence)
func (m *Manifests) DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error) {
	if err := m.DeleteByUser(ctx, tenantID, "", []string{sessionID}); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, r := range m.parents {
		if r.TenantID == tenantID && r.SessionID == sessionID {
			delete(m.parents, id)
			n++
		}
	}
	return n, nil
}

// failingFinalise is a manifest writer whose Finalise fails, so a derive
// copies its chunks and writes the derived manifest row and then fails, as
// a derive whose snapshot copy fails part way does.
type failingFinalise struct{ *Manifests }

// Finalise implements the derive's manifest writer and always fails.
func (failingFinalise) Finalise(context.Context, string, string, bool, string) error {
	return errCopyFailed
}

// EraseTenant deletes every manifest row of the tenant and every seeded
// source checkpoint, as the Phase 4 checkpoint_manifests entry does, and
// returns the number of source checkpoints it removed.
func (m *Manifests) EraseTenant(ctx context.Context, tenantID string) (int, error) {
	if err := m.DeleteByTenant(ctx, tenantID); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, r := range m.parents {
		if r.TenantID == tenantID {
			delete(m.parents, id)
			n++
		}
	}
	return n, nil
}

// TenantCount returns the number of manifest rows and seeded source
// checkpoints the tenant still holds.
func (m *Manifests) TenantCount(tenantID string) int {
	m.mu.Lock()
	n := 0
	for _, r := range m.parents {
		if r.TenantID == tenantID {
			n++
		}
	}
	var sessions [][2]string
	for key := range m.bySession {
		if key[0] == tenantID {
			sessions = append(sessions, key)
		}
	}
	m.mu.Unlock()
	for _, key := range sessions {
		n += m.Count(key[0], key[1])
	}
	return n
}

// TenantCount returns the number of objects stored under the tenant.
func (o *Objects) TenantCount(tenantID string) int {
	n := 0
	for _, u := range o.Keys() {
		if u.TenantID == tenantID {
			n++
		}
	}
	return n
}
