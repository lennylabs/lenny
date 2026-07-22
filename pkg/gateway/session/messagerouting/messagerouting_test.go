// SPDX-License-Identifier: MIT

package messagerouting

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// TestClassify_spec_7_2_paths covers every §7.2 lines 313-331 path the
// coordinating replica can select from observable state, including the
// path-3-over-path-4 precedence overlap and the pre-running
// external-vs-inter-session branch (line 339).
func TestClassify_spec_7_2_paths(t *testing.T) {
	cases := []struct {
		name          string
		state         session.State
		inputRequired bool
		immediate     bool
		src           Source
		wantPath      int
		wantAction    Action
		wantStatus    session.DeliveryStatus
	}{
		{
			name: "path2_running_delivers", state: session.StateRunning,
			src: SourceExternal, wantPath: 2, wantAction: ActionDeliver,
			wantStatus: session.DeliveryStatusDelivered,
		},
		{
			name: "path3_input_required_state_buffers", state: session.StateInputRequired,
			src: SourceExternal, wantPath: 3, wantAction: ActionBufferInbox,
			wantStatus: session.DeliveryStatusQueued,
		},
		{
			// §7.2 line 319: input_required buffering applies even when
			// delivery:"immediate" is set — path 3 wins over path 4.
			name: "path3_wins_over_immediate", state: session.StateRunning,
			inputRequired: true, immediate: true, src: SourceExternal,
			wantPath: 3, wantAction: ActionBufferInbox, wantStatus: session.DeliveryStatusQueued,
		},
		{
			name: "path3_pending_input_via_flag", state: session.StateRunning,
			inputRequired: true, src: SourceInterSession,
			wantPath: 3, wantAction: ActionBufferInbox, wantStatus: session.DeliveryStatusQueued,
		},
		{
			// §7.2 path 6 line 330: a suspended target without
			// delivery:"immediate" remains buffered (inbox, queued).
			name: "path6_suspended_buffers_inbox", state: session.StateSuspended,
			src: SourceExternal, wantPath: 6, wantAction: ActionBufferInbox,
			wantStatus: session.DeliveryStatusQueued,
		},
		{
			// §7.2 path 6 line 327: a delivery:"immediate" message to a
			// pod-held suspended session selects the atomic resume-and-deliver,
			// returning delivered.
			name: "path6_suspended_immediate_resumes", state: session.StateSuspended,
			immediate: true, src: SourceInterSession,
			wantPath: 6, wantAction: ActionResumeAndDeliver, wantStatus: session.DeliveryStatusDelivered,
		},
		{
			// §7.2 path 6: the resume-and-deliver selection is source-agnostic —
			// an external-client immediate message to a suspended session also
			// resumes-and-delivers.
			name: "path6_suspended_immediate_external_resumes", state: session.StateSuspended,
			immediate: true, src: SourceExternal,
			wantPath: 6, wantAction: ActionResumeAndDeliver, wantStatus: session.DeliveryStatusDelivered,
		},
		{
			name: "path7_resume_pending_dlq", state: session.StateResumePending,
			src: SourceInterSession, wantPath: 7, wantAction: ActionBufferDLQ,
			wantStatus: session.DeliveryStatusQueued,
		},
		{
			name: "path7_awaiting_client_action_dlq", state: session.StateAwaitingClientAction,
			src: SourceExternal, wantPath: 7, wantAction: ActionBufferDLQ,
			wantStatus: session.DeliveryStatusQueued,
		},
		{
			name: "path7_terminal_completed_rejects", state: session.StateCompleted,
			src: SourceExternal, wantPath: 7, wantAction: ActionRejectTerminal,
		},
		{
			name: "path7_terminal_failed_rejects", state: session.StateFailed,
			src: SourceInterSession, wantPath: 7, wantAction: ActionRejectTerminal,
		},
		{
			name: "path7_terminal_cancelled_rejects", state: session.StateCancelled,
			src: SourceExternal, wantPath: 7, wantAction: ActionRejectTerminal,
		},
		{
			name: "path7_terminal_expired_rejects", state: session.StateExpired,
			src: SourceExternal, wantPath: 7, wantAction: ActionRejectTerminal,
		},
		{
			// §7.2 line 339 pre-running: external client retries.
			name: "prerunning_external_rejects_not_ready", state: session.StateCreated,
			src: SourceExternal, wantPath: 7, wantAction: ActionRejectNotReady,
		},
		{
			// §7.2 line 339 pre-running: inter-session parent→child buffers.
			name: "prerunning_intersession_buffers_dlq", state: session.StateStarting,
			src: SourceInterSession, wantPath: 7, wantAction: ActionBufferDLQ,
			wantStatus: session.DeliveryStatusQueued,
		},
		{
			name: "prerunning_finalizing_external_rejects", state: session.StateFinalizing,
			src: SourceExternal, wantPath: 7, wantAction: ActionRejectNotReady,
		},
		{
			name: "prerunning_ready_intersession_dlq", state: session.StateReady,
			src: SourceInterSession, wantPath: 7, wantAction: ActionBufferDLQ,
			wantStatus: session.DeliveryStatusQueued,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.state, tc.inputRequired, tc.immediate, tc.src)
			if got.Path != tc.wantPath {
				t.Errorf("path = %d, want %d", got.Path, tc.wantPath)
			}
			if got.Action != tc.wantAction {
				t.Errorf("action = %d, want %d", got.Action, tc.wantAction)
			}
			if got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tc.wantStatus)
			}
		})
	}
}
