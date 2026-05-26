// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"sort"
	"strings"

	nodev1 "k8s.io/api/node/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RuntimeClassRequirement names a §5.3 isolation profile and the
// RuntimeClass object that profile maps to. The lenny-preflight Job
// passes one requirement per enabled, externally-managed
// runtimeClasses.profiles entry.
type RuntimeClassRequirement struct {
	// Profile is the §5.3 isolation profile (standard | sandboxed |
	// microvm) that requires the RuntimeClass.
	Profile string
	// Name is the RuntimeClass object name the profile maps to (for
	// example gvisor, kata, runc).
	Name string
}

// CheckRuntimeClasses verifies every required RuntimeClass exists in the
// cluster, failing the preflight fail-closed when any is absent so the
// install aborts before the first warm pod create would be rejected by
// the API server.
//
// The §17.9 check table sources the required RuntimeClasses from
// `.Values.bootstrap.pools`. Pools are runtime admin-API objects created
// after the gateway is up, so they do not exist at pre-install time when
// the fail-closed guarantee matters most: an operator who selected
// `sandboxed` (gVisor) without installing gVisor. The install-time source
// of "required RuntimeClasses" is the chart's enabled
// runtimeClasses.profiles set, so a miss is attributed to the isolation
// profile rather than to a pool name. The core "RuntimeClass '<name>' not
// found" wording from §17.9 is preserved.
//
// spec: §5.3 line 676 ("checks for required RuntimeClasses and all other
// infrastructure dependencies before installation proceeds"); §17.9
// line 478 (fail-closed before any Lenny component is deployed).
func CheckRuntimeClasses(required []RuntimeClassRequirement, existing map[string]bool) Decision {
	var missing []string
	seen := map[string]bool{}
	for _, req := range required {
		if req.Name == "" || existing[req.Name] {
			continue
		}
		msg := fmt.Sprintf("RuntimeClass '%s' not found; required by isolation profile '%s'",
			req.Name, req.Profile)
		if seen[msg] {
			continue
		}
		seen[msg] = true
		missing = append(missing, msg)
	}
	if len(missing) == 0 {
		return Decision{Passed: true}
	}
	sort.Strings(missing)
	return Decision{Passed: false, Reason: strings.Join(missing, "; ")}
}

// gatherRuntimeClasses lists the cluster's node.k8s.io/v1 RuntimeClass
// objects and projects them onto a name set for CheckRuntimeClasses.
func gatherRuntimeClasses(ctx context.Context, reader client.Reader) (map[string]bool, error) {
	var list nodev1.RuntimeClassList
	if err := reader.List(ctx, &list); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(list.Items))
	for i := range list.Items {
		out[list.Items[i].Name] = true
	}
	return out, nil
}
