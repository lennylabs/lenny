// SPDX-License-Identifier: MIT

package loadctl

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ProgressSink persists RunnerProgress events beyond the 64-event
// in-memory hub backlog so post-run analysis surfaces the full
// per-scenario telemetry. Implementations MUST be safe for
// concurrent Append calls from multiple goroutines.
type ProgressSink interface {
	// Append writes one progress event to the sink. The runID
	// scopes the storage location.
	Append(runID string, p RunnerProgress) error
	// Open returns a reader over the persisted JSON-lines stream
	// for runID. The caller closes it. Returns ErrNoProgress when
	// the run has no persisted events yet.
	Open(runID string) (io.ReadCloser, error)
}

// ErrNoProgress is returned by ProgressSink.Open when the run has no
// persisted events yet.
var ErrNoProgress = fmt.Errorf("loadctl: no persisted progress")

// noopSink discards every event and reports ErrNoProgress on Open.
// Returned by newProgressSink when no ProgressDir is configured so
// the server keeps working in the pure-in-memory dev path.
type noopSink struct{}

func (noopSink) Append(string, RunnerProgress) error { return nil }
func (noopSink) Open(string) (io.ReadCloser, error)  { return nil, ErrNoProgress }

// fileSink writes one JSONL file per run under a base directory.
// Concurrent Appends for the same run are serialised by a per-run
// mutex; concurrent Appends for different runs proceed in parallel.
type fileSink struct {
	dir string

	mu     sync.Mutex
	locks  map[string]*sync.Mutex
}

func newFileSink(dir string) (*fileSink, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("loadctl: progress sink mkdir %q: %w", dir, err)
	}
	return &fileSink{dir: dir, locks: map[string]*sync.Mutex{}}, nil
}

func (s *fileSink) runLock(runID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.locks[runID]; ok {
		return m
	}
	m := &sync.Mutex{}
	s.locks[runID] = m
	return m
}

func (s *fileSink) pathFor(runID string) string {
	// runIDs come from generateID() and are kebab-style; defensively
	// reject any path traversal attempt.
	clean := filepath.Base(runID)
	return filepath.Join(s.dir, clean+".jsonl")
}

// Append serialises p as one JSON line and fsyncs the file. fsync on
// every event is conservative; a real production deployment may want
// a batched-write background flusher.
func (s *fileSink) Append(runID string, p RunnerProgress) error {
	if runID == "" {
		return fmt.Errorf("loadctl: empty runID")
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	body = append(body, '\n')

	m := s.runLock(runID)
	m.Lock()
	defer m.Unlock()
	f, err := os.OpenFile(s.pathFor(runID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return err
	}
	return f.Sync()
}

// Open returns the persisted stream. ErrNoProgress when the file is
// absent.
func (s *fileSink) Open(runID string) (io.ReadCloser, error) {
	if runID == "" {
		return nil, ErrNoProgress
	}
	f, err := os.Open(s.pathFor(runID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoProgress
		}
		return nil, err
	}
	return f, nil
}

// newProgressSink resolves the ProgressDir config value to a
// concrete ProgressSink. An empty value selects the in-memory noop
// sink; an absolute path or "file://" prefix selects fileSink.
//
// Cloud sinks (s3://, gs://, azureblob://) are recognised by name
// and will land alongside the existing dispatch.* cloud SDK wiring;
// for now they fall back to noopSink with a warning so the server
// boots cleanly against a placeholder configuration.
func newProgressSink(dir string) (ProgressSink, error) {
	switch {
	case dir == "":
		return noopSink{}, nil
	case strings.HasPrefix(dir, "file://"):
		return newFileSink(strings.TrimPrefix(dir, "file://"))
	case strings.HasPrefix(dir, "s3://"),
		strings.HasPrefix(dir, "gs://"),
		strings.HasPrefix(dir, "azureblob://"):
		// Cloud sinks are out-of-scope for the initial cut; the
		// dispatch package wires the SDKs and an object-storage
		// sink belongs alongside the report uploader.
		return noopSink{}, nil
	default:
		return newFileSink(dir)
	}
}
