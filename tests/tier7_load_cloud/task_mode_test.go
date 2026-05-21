// SPDX-License-Identifier: MIT

//go:build load_cloud

// task mode cloud-load tests. §5.2's `task` mode runs one-shot task
// executions on pods that cycle through multiple tasks (up to
// taskPolicy.maxTasksPerPod) before retirement, with a between-task
// workspace scrub. Each task runs to completion in a single HTTP
// call; there is no persistent session. The scenarios exercise the
// task ingestion path, the between-task scrub, and the §13.1
// MaxTasksPerPod retirement.

package tier7_load_cloud_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/load"
)

const taskModeRuntime = "load-task-runtime"

// spec: 5.2 + 12.7 (task throughput across pod-reuse cycles)
// diagnosis: TestCloudTaskThroughput drives sustained one-shot
// task execution against the task pool. The gateway picks a free
// task slot on a warm pod, runs the task, scrubs, and returns the
// pod to the pool. A failure means task dispatch regressed, the
// between-task scrub exhausted the cleanup budget, or the
// §13.1 maxTasksPerPod retirement loop did not keep up with
// arrival.
func TestCloudTaskThroughput(t *testing.T) {
	_ = requireCloudLoad(t)
	s := loadScale(t)
	res := load.RunScenario(t, "task_throughput", cloudLoadOptions(s, gatewayBaseURL(t), map[string]string{
		"LENNY_RUNTIME": taskModeRuntime,
	}))
	assertScenarioRan(t, "task_throughput", res)
}

// spec: 5.2 + 12.7 (task scrub latency between tasks)
// diagnosis: TestCloudTaskScrubLatency measures the wall-clock cost
// of the between-task scrub. The scenario hammers tasks against a
// single pod and observes the inter-task latency floor; a
// regression in pkg/sandbox or the §5.2 cleanupCommands path
// surfaces as elevated p99.
func TestCloudTaskScrubLatency(t *testing.T) {
	_ = requireCloudLoad(t)
	s := loadScale(t)
	res := load.RunScenario(t, "task_scrub_latency", cloudLoadOptions(s, gatewayBaseURL(t), map[string]string{
		"LENNY_RUNTIME": taskModeRuntime,
	}))
	assertScenarioRan(t, "task_scrub_latency", res)
}
