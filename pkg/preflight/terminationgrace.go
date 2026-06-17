// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// nodeDrainTimeoutWarnSeconds is the §5.2 line 516 commonly-configured
// node drain timeout (600s). A pool whose terminationGracePeriodSeconds
// exceeds it risks the kubelet SIGKILLing the pod before checkpoints
// complete, so the preflight emits a warning.
const nodeDrainTimeoutWarnSeconds = 600

// PoolGracePeriod is one pool's deployer-set
// terminationGracePeriodSeconds, projected from a SandboxTemplate.
type PoolGracePeriod struct {
	// Pool is the SandboxTemplate (pool) name.
	Pool string
	// TerminationGracePeriodSeconds is the pool's deployer-set grace
	// period, or nil when unset (the pool uses the §4.6.1 default base).
	TerminationGracePeriodSeconds *int64
}

// CheckTerminationGracePeriods emits an advisory warning for every pool
// whose terminationGracePeriodSeconds exceeds the commonly-configured
// node drain timeout (600s). The check never fails the install: §5.2
// line 516 specifies a preflight warning, not a rejection, mirroring the
// warning-only treatment of the SandboxWarmPool CRD validation webhook
// for the same condition. A fresh install carries no pools and the check
// passes cleanly; on upgrade, existing pools are evaluated.
//
// spec: §5.2 line 516 — "The lenny-preflight Job also checks for this
// condition and emits a preflight warning."
func CheckTerminationGracePeriods(pools []PoolGracePeriod) Decision {
	var warnings []string
	for _, p := range pools {
		if p.TerminationGracePeriodSeconds == nil ||
			*p.TerminationGracePeriodSeconds <= nodeDrainTimeoutWarnSeconds {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"pool '%s' terminationGracePeriodSeconds (%ds) exceeds %ds; verify that the "+
				"cluster node drain timeout is configured to accommodate this value or the "+
				"kubelet will SIGKILL the pod before checkpoints complete",
			p.Pool, *p.TerminationGracePeriodSeconds, nodeDrainTimeoutWarnSeconds,
		))
	}
	if len(warnings) == 0 {
		return Decision{Passed: true}
	}
	sort.Strings(warnings)
	return Decision{Passed: true, Reason: "WARNING: " + strings.Join(warnings, "; ")}
}

// gatherPoolGracePeriods lists the cluster's SandboxTemplate pools and
// projects each onto its deployer-set terminationGracePeriodSeconds. On a
// fresh install the SandboxTemplate CRD may not be applied yet; a
// missing kind or absent CRD is treated as "no pools to check" so the
// advisory warning never blocks a first install.
func gatherPoolGracePeriods(ctx context.Context, reader client.Reader) ([]PoolGracePeriod, error) {
	var list lennyv1.SandboxTemplateList
	if err := reader.List(ctx, &list); err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]PoolGracePeriod, 0, len(list.Items))
	for i := range list.Items {
		t := &list.Items[i]
		out = append(out, PoolGracePeriod{
			Pool:                          t.Name,
			TerminationGracePeriodSeconds: t.Spec.TerminationGracePeriodSeconds,
		})
	}
	return out, nil
}
