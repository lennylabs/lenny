// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// demotionReason returns the DemoteSDK reason with the decision, so Prepare
// sends the reason that matches the clause that fired: the restore clause
// takes precedence over a path match, and a path match names the path and
// the pattern.
//
// spec: §6.1; §7.1 (Derive restore semantics)
func TestDemotionReasonNamesTheClauseThatFired_spec_6_1(t *testing.T) {
	restore := func(context.Context) ([]adapterclient.ChunkGrant, error) { return nil, nil }
	plan := &adapterv1.WorkspacePlan{Sources: []*adapterv1.WorkspaceSource{{Type: "inlineFile", Path: "CLAUDE.md"}}}
	cases := []struct {
		name       string
		req        BindRequest
		wantReason string
		wantDemote bool
	}{
		{
			name:       "not preConnect",
			req:        BindRequest{RestoreChunks: restore, Plan: plan, SDKWarmBlockingPaths: []string{"CLAUDE.md"}},
			wantDemote: false,
		},
		{
			name:       "restore with blocking paths",
			req:        BindRequest{PreConnect: true, RestoreChunks: restore, Plan: plan, SDKWarmBlockingPaths: []string{"CLAUDE.md"}},
			wantReason: "workspace starts from a restored snapshot",
			wantDemote: true,
		},
		{
			name:       "path match",
			req:        BindRequest{PreConnect: true, Plan: plan, SDKWarmBlockingPaths: []string{"CLAUDE.md"}},
			wantReason: `workspace path "CLAUDE.md" matches sdkWarmBlockingPaths "CLAUDE.md"`,
			wantDemote: true,
		},
		{
			name:       "restore with an empty list",
			req:        BindRequest{PreConnect: true, RestoreChunks: restore},
			wantDemote: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, demote := demotionReason(tc.req)
			if demote != tc.wantDemote || reason != tc.wantReason {
				t.Errorf("demotionReason = (%q, %v), want (%q, %v)", reason, demote, tc.wantReason, tc.wantDemote)
			}
		})
	}
}
