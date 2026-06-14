// SPDX-License-Identifier: MIT

package fakekube

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Object is one entry in the typed object store. ResourceVersion is a
// monotonically increasing string so SSA conflict checks observe the
// versioning the upstream API server enforces. Annotations carry
// arbitrary scenario state (e.g. the §5.2 Sandbox phase mirror).
type Object struct {
	Kind            string
	Namespace       string
	Name            string
	ResourceVersion string
	Labels          map[string]string
	Annotations     map[string]string
	Data            []byte
}

// ObjectStore is the typed CRUD surface scenarios drive. It is the
// minimal subset of the Kubernetes API server needed by the §5.2
// admission-ordering and §4.6.1 admission-webhook tests:
//
//   - ResourceVersion checks on Update / Patch.
//   - Optimistic-locking conflict on stale ResourceVersion.
//   - Watch-event delivery deferred by the watchlag stream.
type ObjectStore struct {
	mu    sync.RWMutex
	rv    atomic.Uint64
	store map[string]*Object
	hooks []ObjectHook
}

// ObjectHook is a notification function called for every store
// mutation. Scenarios register hooks to drive admission decisions
// against the latest object state.
type ObjectHook func(op string, obj *Object)

// NewObjectStore returns an empty typed store.
func NewObjectStore() *ObjectStore {
	return &ObjectStore{store: make(map[string]*Object)}
}

// ErrConflict is returned by Update / Patch when the supplied
// ResourceVersion does not match the stored version.
var ErrConflict = errors.New("fakekube: resource-version conflict")

// ErrNotFound is returned by Get / Update / Delete on an unknown key.
var ErrNotFound = errors.New("fakekube: not found")

// AddHook registers a notification function for every store mutation.
func (s *ObjectStore) AddHook(h ObjectHook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = append(s.hooks, h)
}

// Create inserts obj. Fails if the key already exists.
func (s *ObjectStore) Create(obj *Object) error {
	if obj == nil {
		return errors.New("fakekube: Create requires non-nil Object")
	}
	s.mu.Lock()
	key := keyOf(obj.Kind, obj.Namespace, obj.Name)
	if _, exists := s.store[key]; exists {
		s.mu.Unlock()
		return fmt.Errorf("fakekube: %s already exists", key)
	}
	rv := s.nextRV()
	stored := cloneObject(obj)
	stored.ResourceVersion = rv
	s.store[key] = stored
	hooks := append([]ObjectHook{}, s.hooks...)
	s.mu.Unlock()
	for _, h := range hooks {
		h("create", cloneObject(stored))
	}
	return nil
}

// Get returns a snapshot of the object under key.
func (s *ObjectStore) Get(kind, namespace, name string) (*Object, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.store[keyOf(kind, namespace, name)]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneObject(o), nil
}

// Update replaces the stored object. The supplied
// ResourceVersion must match the current one; otherwise the call
// returns ErrConflict (Kubernetes 409). On success the
// ResourceVersion is bumped.
func (s *ObjectStore) Update(obj *Object) error {
	if obj == nil {
		return errors.New("fakekube: Update requires non-nil Object")
	}
	s.mu.Lock()
	key := keyOf(obj.Kind, obj.Namespace, obj.Name)
	current, ok := s.store[key]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	if obj.ResourceVersion != "" && obj.ResourceVersion != current.ResourceVersion {
		s.mu.Unlock()
		return ErrConflict
	}
	stored := cloneObject(obj)
	stored.ResourceVersion = s.nextRV()
	s.store[key] = stored
	hooks := append([]ObjectHook{}, s.hooks...)
	s.mu.Unlock()
	for _, h := range hooks {
		h("update", cloneObject(stored))
	}
	return nil
}

// Delete removes the object under key. Idempotent.
func (s *ObjectStore) Delete(kind, namespace, name string) error {
	s.mu.Lock()
	key := keyOf(kind, namespace, name)
	o, ok := s.store[key]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	delete(s.store, key)
	hooks := append([]ObjectHook{}, s.hooks...)
	s.mu.Unlock()
	for _, h := range hooks {
		h("delete", cloneObject(o))
	}
	return nil
}

// List returns every object of the supplied kind in the namespace.
// Empty namespace matches everything.
func (s *ObjectStore) List(kind, namespace string) []*Object {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*Object{}
	for _, o := range s.store {
		if o.Kind != kind {
			continue
		}
		if namespace != "" && o.Namespace != namespace {
			continue
		}
		out = append(out, cloneObject(o))
	}
	return out
}

func (s *ObjectStore) nextRV() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), s.rv.Add(1))
}

func keyOf(kind, ns, name string) string {
	return kind + "/" + ns + "/" + name
}

func cloneObject(o *Object) *Object {
	if o == nil {
		return nil
	}
	clone := *o
	if o.Labels != nil {
		clone.Labels = make(map[string]string, len(o.Labels))
		for k, v := range o.Labels {
			clone.Labels[k] = v
		}
	}
	if o.Annotations != nil {
		clone.Annotations = make(map[string]string, len(o.Annotations))
		for k, v := range o.Annotations {
			clone.Annotations[k] = v
		}
	}
	if o.Data != nil {
		clone.Data = append([]byte(nil), o.Data...)
	}
	return &clone
}
