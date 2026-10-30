// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// AdapterEventTimes returns the kubelet timestamp of every line of pod's
// adapter log that records event, one of the adapter's log event names such
// as adapter.LogSupervisorReady. The timestamps come from `kubectl logs
// --timestamps`, so they are the node's clock at the moment the line was
// written, comparable with the pod's condition transition times on a Kind
// cluster whose nodes share the host clock. spec: §4.7.10 (Runtime
// supervisor, Launch; Liveness).
func AdapterEventTimes(t testing.TB, c *kind.Cluster, pod, event string) []time.Time {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", Namespace, "logs", pod, "-c", "adapter", "--timestamps")
	if err != nil {
		t.Fatalf("read the adapter log of %s: %v\n%s", pod, err, out)
	}
	return eventTimes(out, event)
}

// eventTimes parses `kubectl logs --timestamps` output and returns the
// timestamp of every line that records event. A line whose timestamp does
// not parse is skipped.
func eventTimes(log, event string) []time.Time {
	var out []time.Time
	for _, line := range strings.Split(log, "\n") {
		stamp, rest, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !recordsEvent(rest, event) {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			continue
		}
		out = append(out, at)
	}
	return out
}

// recordsEvent reports whether a log line's message is event, in the text
// layouts slog writes: the default logger's `LEVEL event`, the text
// handler's `msg=event`, and the JSON handler's `"msg":"event"`.
func recordsEvent(line, event string) bool {
	if strings.Contains(line, `"msg":"`+event+`"`) {
		return true
	}
	for _, f := range strings.Fields(line) {
		if f == event || f == "msg="+event {
			return true
		}
	}
	return false
}

// CheckReadyAfterSupervisor returns an error when a pod whose Ready
// condition last turned true at readyAt has no adapter.LogSupervisorReady
// record in connects, or turned Ready before the latest record. The API
// server stores a condition transition time at whole-second precision,
// truncated, so the record's time is truncated the same way before the
// comparison; a Ready in the same second as the connection passes, and one
// in an earlier second fails. spec: §4.7.10 (Runtime supervisor, Launch);
// §4.7.9 (step 4).
func CheckReadyAfterSupervisor(readyAt time.Time, connects []time.Time) error {
	if readyAt.IsZero() {
		return errors.New("the pod carries no Ready transition time")
	}
	if len(connects) == 0 {
		return errors.New("the adapter log records no supervisor ready: the adapter turned Ready without " +
			"accepting the supervisor's ready")
	}
	connected := connects[len(connects)-1]
	if readyAt.Before(connected.Truncate(time.Second)) {
		return fmt.Errorf("the pod turned Ready at %s, before the adapter accepted the supervisor's ready at %s",
			readyAt.UTC().Format(time.RFC3339), connected.UTC().Format(time.RFC3339Nano))
	}
	return nil
}
