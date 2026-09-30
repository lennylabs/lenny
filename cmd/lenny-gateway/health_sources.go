// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"sync/atomic"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
)

// alertHealthSource implements health.AlertStatusSource over this
// replica's in-process §25.13 alert tracker. For a component it returns
// the worst severity among firing §16.5 alerts mapped to it: any firing
// critical alert reports unhealthy, otherwise a firing warning reports
// degraded. ok is false when no firing alert maps to the component, in
// which case the dependency probe's verdict stands.
// spec: §25.3.
type alertHealthSource struct {
	eval *atomic.Pointer[evaluator.Evaluator]
}

func (s alertHealthSource) ComponentStatus(component string) (health.Status, []string, bool) {
	e := s.eval.Load()
	if e == nil {
		return "", nil, false
	}
	var firing []string
	hasCritical := false
	for _, al := range e.FiringAlerts() {
		comp, ok := rules.HealthComponentFor(al.Rule.Name)
		if !ok || comp != component {
			continue
		}
		firing = append(firing, al.Rule.Name)
		if al.Rule.Severity == rules.SeverityCritical {
			hasCritical = true
		}
	}
	if len(firing) == 0 {
		return "", nil, false
	}
	if hasCritical {
		return health.StatusUnhealthy, firing, true
	}
	return health.StatusDegraded, firing, true
}

// staticHealthy returns a §25.3 health Checker that always reports
// the named component healthy. The minimal gateway uses these
// because every subsystem is an in-process in-memory store with no
// failure mode; production swaps in checkers that probe Postgres /
// Redis / MinIO connectivity.
func staticHealthy(name string) health.Checker {
	return health.CheckerFunc{
		ComponentName: name,
		Fn: func(context.Context) health.Component {
			return health.Component{Name: name, Status: health.StatusHealthy}
		},
	}
}
