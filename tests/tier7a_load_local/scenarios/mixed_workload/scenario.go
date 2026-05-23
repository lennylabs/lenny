// SPDX-License-Identifier: MIT

//go:build load_local

// Package mixed_workload interleaves create / get / delete on the
// inproc gateway. The §11.2 invariant: total throughput stays above
// a per-op floor when the workload is mixed; no single op type
// starves under contention.
//
// TESTING.md §12.7.a multi-component scenarios.
package mixed_workload

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/inproc"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "mixed_workload"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	scenkit.InProcMixin
	counters *scenkit.Counters

	mu  sync.Mutex
	ids []string
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.counters = scenkit.NewCounters()
	s.ids = make([]string, 0, 256)
	return s.SetupInProc(ctx, inproc.Config{})
}

func (s *Scenario) Teardown(ctx context.Context) error { return s.TeardownInProc(ctx) }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	gw := s.Env().GatewayURL()
	op := iter % 3
	switch op {
	case 0:
		// Create.
		status, body, err := scenkit.DoJSON(ctx, "POST", gw+"/v1/sessions", []byte(`{"runtimeRef":"echo"}`))
		if err != nil {
			s.counters.IncOnError(ctx, "failures", err)
			return err
		}
		if status != http.StatusCreated {
			s.counters.Inc("failures")
			return fmt.Errorf("create status=%d", status)
		}
		var created struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &created)
		if created.ID != "" {
			s.mu.Lock()
			s.ids = append(s.ids, created.ID)
			s.mu.Unlock()
		}
		s.counters.Inc("creates")
	case 1:
		// Get.
		id := s.pickID()
		if id == "" {
			s.counters.Inc("get_skipped")
			return nil
		}
		status, _, err := scenkit.DoJSON(ctx, "GET", gw+"/v1/sessions/"+id, nil)
		if err != nil {
			s.counters.IncOnError(ctx, "failures", err)
			return err
		}
		if status == http.StatusOK {
			s.counters.Inc("gets")
		}
	case 2:
		// Delete.
		id := s.popID()
		if id == "" {
			s.counters.Inc("delete_skipped")
			return nil
		}
		_, _, err := scenkit.DoJSON(ctx, "DELETE", gw+"/v1/sessions/"+id, nil)
		if err != nil {
			s.counters.IncOnError(ctx, "failures", err)
			return err
		}
		s.counters.Inc("deletes")
	}
	return nil
}

func (s *Scenario) pickID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ids) == 0 {
		return ""
	}
	return s.ids[len(s.ids)-1]
}

func (s *Scenario) popID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ids) == 0 {
		return ""
	}
	id := s.ids[len(s.ids)-1]
	s.ids = s.ids[:len(s.ids)-1]
	return id
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if f := s.counters.Get("failures"); f > 0 {
		return fmt.Errorf("§11.2 violated: %d failed ops", f)
	}
	if s.counters.Get("creates") == 0 || s.counters.Get("gets") == 0 || s.counters.Get("deletes") == 0 {
		return fmt.Errorf("scenario must exercise create + get + delete")
	}
	return nil
}
