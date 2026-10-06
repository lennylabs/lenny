// SPDX-License-Identifier: MIT

package runtimekit

import (
	"testing"
	"time"
)

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 5.2 (Slot-identifier
// reclaim hold)
//
// The default acknowledgement wait leaves room inside the 10 s
// ConfigureWorkspace timeout to write session_end and answer, and stays
// below the 10 s graceful window that bounds the hold-timeout
// termination's wait for the slot serialization. A wait of zero would fail
// every start that waits.
func TestDefaultSessionStartAckTimeoutFitsTheAdapterBounds_spec_28_5_3(t *testing.T) {
	const configureWorkspaceTimeout = 10 * time.Second
	const holdTimeoutGraceWindow = 10 * time.Second
	if DefaultSessionStartAckTimeout <= 0 {
		t.Fatalf("DefaultSessionStartAckTimeout = %s, want a positive wait", DefaultSessionStartAckTimeout)
	}
	if DefaultSessionStartAckTimeout > configureWorkspaceTimeout-2*time.Second {
		t.Fatalf("DefaultSessionStartAckTimeout = %s, want room below the %s ConfigureWorkspace timeout", DefaultSessionStartAckTimeout, configureWorkspaceTimeout)
	}
	if DefaultSessionStartAckTimeout >= holdTimeoutGraceWindow {
		t.Fatalf("DefaultSessionStartAckTimeout = %s, want it below the %s hold-timeout graceful window", DefaultSessionStartAckTimeout, holdTimeoutGraceWindow)
	}
}
