// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// livenessEvents are the adapter log records a missed pong produces: the
// CH-SUPERVISE link failure and the not-live cause. spec: §4.7.10 (Runtime
// supervisor, Liveness).
var livenessEvents = []string{adapter.LogSuperviseLinkFailure, adapter.LogRuntimeNotLive}

// RequireSupervisorStillLive asserts that pod's supervisor survived the
// signals a test sent it and kept answering ping. The pod exists, is not
// being deleted, and runs both containers; PID 1 of the runtime container is
// present and neither stopped nor a zombie; and the adapter logged neither
// a CH-SUPERVISE link failure nor a not-live cause. The adapter-log check is
// the one that sees a missed pong: on an idle keep pod a missed pong goes
// down the Liveness Start channel, which leaves the adapter running and the
// pod Ready until the next start, so the pod and process state alone pass a
// supervisor that stopped answering. spec: §4.7.10 (Runtime supervisor,
// Liveness).
func RequireSupervisorStillLive(t testing.TB, c *kind.Cluster, pod string) {
	t.Helper()
	st, found := GetPod(t, c, pod)
	if !found || st.Deleting || !st.Container["adapter"].Running || !st.Container["runtime"].Running {
		t.Fatalf("pod %s did not survive the signals to its supervisor: found=%v %s", pod, found, st.Describe())
	}
	events := make(map[string][]time.Time, len(livenessEvents))
	for _, event := range livenessEvents {
		events[event] = AdapterEventTimes(t, c, pod, event)
	}
	if err := CheckSupervisorStillLive(Processes(t, c, pod), events); err != nil {
		t.Errorf("pod %s: %v", pod, err)
	}
}

// CheckSupervisorStillLive returns an error when procs, the runtime
// container's process list, has no PID 1 or a PID 1 that is stopped or a
// zombie, or when events, the adapter log records keyed by event name, holds
// a record of a CH-SUPERVISE link failure or a not-live cause. spec: §4.7.10
// (Runtime supervisor, Liveness).
func CheckSupervisorStillLive(procs []superviseprobe.Process, events map[string][]time.Time) error {
	var problems []string
	if err := checkPID1Running(procs); err != nil {
		problems = append(problems, err.Error())
	}
	for _, event := range livenessEvents {
		if at := events[event]; len(at) != 0 {
			problems = append(problems, fmt.Sprintf("the adapter logged %s at %v after the signals to the supervisor", event, at))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

// checkPID1Running returns an error when procs has no PID 1 or its PID 1 is
// stopped, traced-stopped, or a zombie.
func checkPID1Running(procs []superviseprobe.Process) error {
	for _, p := range procs {
		if p.PID != 1 {
			continue
		}
		switch p.State {
		case "T", "t", "Z":
			return fmt.Errorf("the supervisor (PID 1) is in state %s after the signals", p.State)
		}
		return nil
	}
	return fmt.Errorf("the runtime container has no PID 1 after the signals: %+v", procs)
}
