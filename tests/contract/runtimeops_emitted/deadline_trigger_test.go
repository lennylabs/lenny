//go:build contract

// SPDX-License-Identifier: MIT

package runtimeops_emitted_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 28.5.3 (CH-RUNTIMEOPS)
//
// diagnosis: the deadline_approaching contract admits a trigger other than
// session_age and budget. Either the published runtime-ops schema accepts a
// frame with another trigger (such as the removed idle trigger), or the
// adapter wrote one to the runtime, which then reads a frame the schema
// rejects. Check the trigger enum in schemas/runtime-ops-events.schema.json
// and the trigger check in RuntimeOps.SignalDeadlineApproaching.
func TestDeadlineApproachingAcceptsOnlySessionAgeAndBudget_spec_28_5_3(t *testing.T) {
	validator := schematest.Compile(t, runtimeOpsArtifact)
	frame := func(trigger any) map[string]any {
		f := map[string]any{"type": "deadline_approaching", "sessionId": sessionIDFixture, "remainingMs": 5000}
		if trigger != nil {
			f["trigger"] = trigger
		}
		return f
	}
	t.Run("schema", func(t *testing.T) {
		for _, ok := range []string{"session_age", "budget"} {
			if err := validator.Validate(frame(ok)); err != nil {
				t.Errorf("trigger %q rejected: %v", ok, err)
			}
		}
		for _, bad := range []any{"idle", "", "deadline", nil, 1} {
			if err := validator.Validate(frame(bad)); err == nil {
				t.Errorf("trigger %v accepted, want the schema to reject it", bad)
			}
		}
	})

	t.Run("adapter", func(t *testing.T) {
		lc, peer := startRuntimeOps(t)
		if err := lc.SignalDeadlineApproaching(sessionIDFixture, 5000, "idle"); !errors.Is(err, adapter.ErrUnknownDeadlineTrigger) {
			t.Fatalf("idle trigger: err = %v, want ErrUnknownDeadlineTrigger", err)
		}
		for _, trigger := range []string{"budget", "session_age"} {
			if err := lc.SignalDeadlineApproaching(sessionIDFixture, 5000, trigger); err != nil {
				t.Fatalf("trigger %q: %v", trigger, err)
			}
			// The refused idle frame was never written, so the next frame
			// read is this one.
			got := peer.read()
			if got["trigger"] != trigger {
				t.Fatalf("runtime read %v, want trigger %q", got, trigger)
			}
			if err := validator.Validate(got); err != nil {
				t.Errorf("emitted %q frame does not validate: %v", trigger, err)
			}
		}
	})
}
