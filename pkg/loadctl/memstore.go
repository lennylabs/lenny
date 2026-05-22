// SPDX-License-Identifier: MIT

package loadctl

import (
	"context"
	"sort"
	"sync"
)

// memStore is the in-memory implementation of Store. Selected when
// DatabaseURL starts with "memory://". Sufficient for the dev loop
// and the tier-12 dry-run path.
type memStore struct {
	mu        sync.RWMutex
	runs      map[string]*Run
	baselines map[string]string
}

func newMemStore() *memStore {
	return &memStore{
		runs:      make(map[string]*Run),
		baselines: make(map[string]string),
	}
}

func (s *memStore) CreateRun(_ context.Context, r *Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dup := *r
	s.runs[r.ID] = &dup
	return nil
}

func (s *memStore) GetRun(_ context.Context, id string) (*Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, ErrRunNotFound
	}
	dup := *r
	return &dup, nil
}

func (s *memStore) UpdateRun(_ context.Context, r *Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[r.ID]; !ok {
		return ErrRunNotFound
	}
	dup := *r
	s.runs[r.ID] = &dup
	return nil
}

func (s *memStore) ListRuns(_ context.Context) ([]*Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Run, 0, len(s.runs))
	for _, r := range s.runs {
		dup := *r
		out = append(out, &dup)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

func (s *memStore) PinBaseline(_ context.Context, name, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[runID]; !ok {
		return ErrRunNotFound
	}
	s.baselines[name] = runID
	return nil
}

func (s *memStore) Close() error { return nil }
