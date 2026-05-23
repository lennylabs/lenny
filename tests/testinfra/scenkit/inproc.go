// SPDX-License-Identifier: MIT

package scenkit

import (
	"context"

	"github.com/lennylabs/lenny/tests/testinfra/inproc"
)

// InProcMixin is an embeddable helper for scenarios that need the
// in-process gateway harness. Scenarios that embed it gain
// SetupInProc / TeardownInProc / Env helpers and avoid duplicating
// the Start/Stop lifecycle.
//
// Usage:
//
//	type Scenario struct {
//	    scenkit.InProcMixin
//	    counters *scenkit.Counters
//	}
//
//	func (s *Scenario) Setup(ctx context.Context) error {
//	    s.counters = scenkit.NewCounters()
//	    return s.SetupInProc(ctx, inproc.Config{})
//	}
//
//	func (s *Scenario) Teardown(ctx context.Context) error {
//	    return s.TeardownInProc(ctx)
//	}
type InProcMixin struct {
	env *inproc.Env
}

// Env returns the underlying inproc.Env. nil before SetupInProc.
func (m *InProcMixin) Env() *inproc.Env { return m.env }

// SetupInProc creates and starts an inproc.Env with the supplied
// configuration.
func (m *InProcMixin) SetupInProc(ctx context.Context, c inproc.Config) error {
	m.env = inproc.New(c)
	return m.env.Start(ctx)
}

// TeardownInProc stops the embedded env. Safe to call when
// SetupInProc was never invoked.
func (m *InProcMixin) TeardownInProc(ctx context.Context) error {
	if m.env == nil {
		return nil
	}
	return m.env.Stop(ctx)
}
