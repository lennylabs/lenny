// SPDX-License-Identifier: MIT

package poolstore

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// TestProcessAcknowledgmentMessageByLifetime_spec_5_2 pins the wording of the
// acknowledgment rejection by lifetime. On a pool under `restart` the clause
// stating that the pod keeps one runtime process is false, so it is absent;
// it is present only under `keep`. The restart message is reached only
// through this builder, because validateRecyclePolicy refuses a concurrent
// unacknowledged pool earlier and a sequential `restart` pool requires no
// acknowledgment.
//
// spec: §5.2 (Deployer acknowledgment (runtime process kept across sessions))
func TestProcessAcknowledgmentMessageByLifetime_spec_5_2(t *testing.T) {
	const kept = "the pod keeps one runtime process across the sessions it serves"
	cases := []struct {
		lifetime runtimestore.RuntimeProcessLifetime
		wantKept bool
	}{
		{runtimestore.RuntimeProcessKeep, true},
		{runtimestore.RuntimeProcessRestart, false},
		{"", false},
	}
	for _, tc := range cases {
		msg := processAcknowledgmentError(&runtimestore.RecyclePolicy{RuntimeProcess: tc.lifetime}).Error()
		if strings.Contains(msg, kept) != tc.wantKept {
			t.Errorf("lifetime %q: message %q, want kept clause %v", tc.lifetime, msg, tc.wantKept)
		}
		if !strings.Contains(msg, "acknowledgeProcessLevelIsolation=true") || !strings.HasSuffix(msg, "(§5.2)") {
			t.Errorf("lifetime %q: message %q does not name the requirement and §5.2", tc.lifetime, msg)
		}
	}
	if strings.Contains(processAcknowledgmentError(nil).Error(), kept) {
		t.Error("a nil recycle block carries the kept-process clause")
	}
}
