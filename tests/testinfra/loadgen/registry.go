// SPDX-License-Identifier: MIT

package loadgen

import (
	"fmt"
	"sort"
	"sync"
)

// Registry tracks all registered scenarios.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]func() Scenario
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]func() Scenario)}
}

// Register adds a scenario factory under name. Panics if the name is
// already taken; scenario names are part of the verdict surface and
// collisions would mask which scenario actually failed.
func (r *Registry) Register(name string, factory func() Scenario) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[name]; exists {
		panic(fmt.Sprintf("loadgen: scenario %q is already registered", name))
	}
	r.factories[name] = factory
}

// Get returns a fresh Scenario for name. The second return is false
// when the name is unknown.
func (r *Registry) Get(name string) (Scenario, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	factory, ok := r.factories[name]
	if !ok {
		return nil, false
	}
	return factory(), true
}

// MustGet returns a fresh Scenario for name. Panics when the name is
// unknown.
func (r *Registry) MustGet(name string) Scenario {
	s, ok := r.Get(name)
	if !ok {
		panic(fmt.Sprintf("loadgen: scenario %q is not registered", name))
	}
	return s
}

// Names returns every registered scenario name in lexicographic order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for k := range r.factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Len returns the number of registered scenarios.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.factories)
}

var defaultRegistry = NewRegistry()

// DefaultRegistry returns the process-wide default Registry.
// Scenario subpackages register themselves against this Registry
// through their init() functions.
func DefaultRegistry() *Registry { return defaultRegistry }

// Register is a convenience that adds factory to the default Registry.
func Register(name string, factory func() Scenario) {
	defaultRegistry.Register(name, factory)
}
