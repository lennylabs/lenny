// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// F-7.4.15: publishWorkspaceWarnings emits one SSE workspace_plan_warning
// frame per §14 advisory the adapter returned from FinalizeWorkspace.
// spec: §7.4 line 459.
func TestPublishWorkspaceWarnings_EmitsOnePerWarning_spec_7_4_15(t *testing.T) {
	bus := sessionevents.NewBus(0)
	srv := New(memstore.New(), Options{Events: bus})

	result := &podsession.BindResult{
		TenantID:  "default",
		SessionID: "sess_w",
		WorkspacePlanWarnings: []*adapterv1.WorkspacePlanWarning{
			{Code: "workspace_plan_strip_components_skip", SourceIndex: 1, Entry: "a.txt", Message: "skipped a.txt"},
			{Code: "workspace_plan_strip_components_skip", SourceIndex: 1, Entry: "b.txt", Message: "skipped b.txt"},
		},
	}
	srv.publishWorkspaceWarnings(result)

	events := bus.History("sess_w", 0)
	if len(events) != 2 {
		t.Fatalf("events: got %d, want 2; events=%+v", len(events), events)
	}
	for i, ev := range events {
		if ev.Type != "workspace_plan_warning" {
			t.Errorf("event[%d].Type = %q, want workspace_plan_warning", i, ev.Type)
		}
	}
}

// F-7.4.15: a nil result or empty warnings is a no-op.
func TestPublishWorkspaceWarnings_NilSafe_spec_7_4_15(t *testing.T) {
	bus := sessionevents.NewBus(0)
	srv := New(memstore.New(), Options{Events: bus})

	srv.publishWorkspaceWarnings(nil)
	srv.publishWorkspaceWarnings(&podsession.BindResult{TenantID: "default", SessionID: "sess_w"})

	if len(bus.History("sess_w", 0)) != 0 {
		t.Errorf("expected no events for nil/empty warnings")
	}
}

// silence unused import warning.
var _ = context.Background
