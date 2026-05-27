// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §7.5 line 475, §7.3 line 387, §16.1 line 124 — F-7.5.9.
//
// recordSetupCommandFailed must fire the §16.1 warmup_failure_total
// counter with error_type=setup_command_failed and append the §11.7
// audit row with the failing command's cmd / exit code / stderr excerpt
// pulled from the adapter-side partial outputs.

type captureSetupAudit struct{ events []SessionLifecycleEvent }

func (c *captureSetupAudit) EmitSessionLifecycle(_ context.Context, ev SessionLifecycleEvent) {
	c.events = append(c.events, ev)
}

func TestRecordSetupCommandFailedFiresMetricAndAudit(t *testing.T) {
	var (
		gotErrorType string
		sink         captureSetupAudit
	)
	s := &Server{
		clock:                    func() time.Time { return time.Unix(0, 0) },
		incWarmpoolWarmupFailure: func(et string) { gotErrorType = et },
		lifecycleAudit:           &sink,
	}
	s.recordSetupCommandFailed(&podsession.SetupCommandFailure{
		Pod:   "sbx-abc",
		Cause: errors.New("rpc: FailedPrecondition"),
		Outputs: []*adapterv1.SetupCommandOutput{
			{Cmd: "echo ok", ExitCode: 0},
			{Cmd: "exit 7", ExitCode: 7, Stderr: "command failed"},
		},
	})
	if gotErrorType != "setup_command_failed" {
		t.Errorf("metric error_type = %q, want setup_command_failed", gotErrorType)
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit events len = %d, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.EventType != auditSessionSetupCommandFailed {
		t.Errorf("audit event type = %q, want %q", ev.EventType, auditSessionSetupCommandFailed)
	}
	if ev.FailureClass != "setup_command_failed" {
		t.Errorf("audit failure_class = %q, want setup_command_failed", ev.FailureClass)
	}
	if !strings.Contains(ev.Detail, `"exit 7"`) || !strings.Contains(ev.Detail, "command failed") {
		t.Errorf("audit Detail = %q, want to contain the failing cmd + stderr excerpt", ev.Detail)
	}
}

// recordSetupCommandFailed must be safe to call with no outputs, no
// metric hook, no audit sink, or a nil failure.
func TestRecordSetupCommandFailedSafeFallbacks(t *testing.T) {
	s := &Server{clock: func() time.Time { return time.Unix(0, 0) }}
	// nil failure is a no-op (no panic).
	s.recordSetupCommandFailed(nil)

	// No hooks: still safe.
	s.recordSetupCommandFailed(&podsession.SetupCommandFailure{Cause: errors.New("x")})

	// No outputs: Detail falls back to the underlying Cause string.
	var sink captureSetupAudit
	s.lifecycleAudit = &sink
	s.recordSetupCommandFailed(&podsession.SetupCommandFailure{Cause: errors.New("rpc broke")})
	if len(sink.events) != 1 || !strings.Contains(sink.events[0].Detail, "rpc broke") {
		t.Errorf("no-outputs Detail should fall back to Cause; got %+v", sink.events)
	}
}

// spec: §7.5 line 475 — F-7.5.4. setupOutputsFromBind round-trips every
// captured field from the adapter wire form to the session-row form.
func TestSetupOutputsFromBindRoundTrip(t *testing.T) {
	if got := setupOutputsFromBind(nil); got != nil {
		t.Errorf("setupOutputsFromBind(nil) = %v, want nil", got)
	}
	in := []*adapterv1.SetupCommandOutput{
		{Cmd: "npm ci", ExitCode: 0, Stdout: "ok", Stderr: "", DurationMs: 1234, Truncated: false},
		{Cmd: "exit 7", ExitCode: 7, Stdout: "", Stderr: "boom", DurationMs: 12, Truncated: true},
	}
	got := setupOutputsFromBind(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Cmd != "npm ci" || got[0].ExitCode != 0 || got[0].Stdout != "ok" || got[0].DurationMs != 1234 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].ExitCode != 7 || got[1].Stderr != "boom" || !got[1].Truncated {
		t.Errorf("got[1] = %+v", got[1])
	}
}
