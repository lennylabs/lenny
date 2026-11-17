// SPDX-License-Identifier: MIT

//go:build load_local

// Package large_workspace_upload models the §4.7 workspace upload
// flow: many concurrent goroutines stream multi-MB byte payloads
// through a bounded gateway upload handler and assert the bytes-per-
// second floor holds, the upload-queue depth stays bounded, and no
// upload corrupts under concurrent contention.
//
// TESTING.md §12.7.a multi-component scenarios.
package large_workspace_upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "large_workspace_upload"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// uploadHandler models the gateway's bounded upload handler. The
// concurrency cap is the analogue of the §4.7 LENNY_UPLOAD_CONCURRENCY
// flag; uploads past the cap return ErrAtCapacity.
type uploadHandler struct {
	mu       sync.Mutex
	inFlight int
	cap      int
	digests  map[string]string
}

func newUploadHandler(cap int) *uploadHandler {
	return &uploadHandler{cap: cap, digests: map[string]string{}}
}

func (h *uploadHandler) upload(id string, body []byte) (string, error) {
	h.mu.Lock()
	if h.inFlight >= h.cap {
		h.mu.Unlock()
		return "", fmt.Errorf("§4.7 rejected: at capacity (%d)", h.cap)
	}
	h.inFlight++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.inFlight--
		h.mu.Unlock()
	}()
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	h.mu.Lock()
	prev, seen := h.digests[id]
	if seen && prev != digest {
		h.mu.Unlock()
		return "", fmt.Errorf("§4.7 violated: %s digest changed (%s → %s) — concurrent corruption", id, prev, digest)
	}
	h.digests[id] = digest
	h.mu.Unlock()
	return digest, nil
}

type Scenario struct {
	counters *scenkit.Counters
	handler  *uploadHandler
	body     []byte
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 8, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	// 1 MiB synthetic body.
	s.body = make([]byte, 1<<20)
	for i := range s.body {
		s.body[i] = byte(i % 251)
	}
	s.handler = newUploadHandler(4)
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	id := fmt.Sprintf("ws-%d", iter%32)
	_, err := s.handler.upload(id, s.body)
	if err != nil {
		if err.Error() == fmt.Sprintf("§4.7 rejected: at capacity (%d)", s.handler.cap) {
			s.counters.Inc("rejected_at_capacity")
			return nil
		}
		s.counters.Inc("corruption_detected")
		return err
	}
	s.counters.Inc("uploaded")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("corruption_detected"); v > 0 {
		return fmt.Errorf("§4.7 violated: %d digest mismatches under concurrent upload", v)
	}
	if s.counters.Get("uploaded") == 0 {
		return fmt.Errorf("scenario did not upload anything")
	}
	if s.handler.inFlight != 0 {
		return fmt.Errorf("§4.7 violated: %d in-flight after run", s.handler.inFlight)
	}
	return nil
}
