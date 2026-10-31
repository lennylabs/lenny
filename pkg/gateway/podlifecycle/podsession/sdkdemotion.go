// SPDX-License-Identifier: MIT

package podsession

import (
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/sdkwarm"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// SDKDemotionNotSupported is returned by Bind when a §6.1 preConnect pod's
// workspace plan requires demotion but the pod's adapter does not
// implement the DemoteSDK RPC (it returns UNIMPLEMENTED). Per §6.1
// the gateway fails the session with SDK_DEMOTION_NOT_SUPPORTED rather than
// serving the session with stale SDK state.
type SDKDemotionNotSupported struct {
	Pod string
}

func (e *SDKDemotionNotSupported) Error() string {
	return fmt.Sprintf("podsession: pod %s requires SDK demotion but its adapter does not implement DemoteSDK (SDK_DEMOTION_NOT_SUPPORTED)", e.Pod)
}

// workspacePlanPaths returns the relative workspace paths the plan places,
// one per §14 WorkspaceSource, for matching against sdkWarmBlockingPaths
// (§6.1).
func workspacePlanPaths(plan *adapterv1.WorkspacePlan) []string {
	if plan == nil {
		return nil
	}
	paths := make([]string, 0, len(plan.GetSources()))
	for _, src := range plan.GetSources() {
		if p := src.GetPath(); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// isUnimplemented reports whether err is a gRPC UNIMPLEMENTED status, the
// §6.1 signal that a preConnect pod's adapter cannot DemoteSDK.
func isUnimplemented(err error) bool {
	return status.Code(err) == codes.Unimplemented
}

// restoreDemotionReason is the DemoteSDK reason for a preConnect session whose
// workspace starts from a restored snapshot. spec: §6.1.
const restoreDemotionReason = "workspace starts from a restored snapshot"

// demotionReason makes the §6.1 SDK-warm demotion decision for a bind request
// and returns the DemoteSDK reason with it. A non-preConnect request never
// demotes. A session whose workspace starts from a restored snapshot matches
// every non-empty sdkWarmBlockingPaths list, because the restored paths are not
// known before materialization. Otherwise the plan's placed paths are matched
// against the list. spec: §7.1 (Derive restore semantics); §6.1.
func demotionReason(req BindRequest) (string, bool) {
	if !req.PreConnect {
		return "", false
	}
	if req.RestoreChunks != nil && len(req.SDKWarmBlockingPaths) > 0 {
		return restoreDemotionReason, true
	}
	mp, pat, requires := sdkwarm.RequiresDemotion(workspacePlanPaths(req.Plan), req.SDKWarmBlockingPaths)
	if !requires {
		return "", false
	}
	return fmt.Sprintf("workspace path %q matches sdkWarmBlockingPaths %q", mp, pat), true
}

// RequiresDemotion reports the §6.1 SDK-warm demotion decision for a bind
// request: whether the request's workspace forces a still-SDK-warm
// (preConnect) pod to be demoted to pod-warm before the workspace is
// materialized. The decision is a pure function of the plan's placed paths,
// whether the session starts from a restored snapshot, and the runtime's
// sdkWarmBlockingPaths glob list, so the finalize-time Prepare (which makes
// the decision) and the launch-only /start path (which needs it without
// re-running Prepare) compute the identical answer from the same demotionReason
// rather than the gateway persisting the boolean. spec: §6.1, §4.3, §4.4,
// §7.1 (Derive restore semantics).
func RequiresDemotion(req BindRequest) bool {
	_, requires := demotionReason(req)
	return requires
}
