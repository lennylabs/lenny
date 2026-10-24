// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// PodWatch follows every update the API server sends for one pod. A poll can
// miss the short window between the kubelet's status update that reports a
// container's exit and the pod's removal; a watch receives that update.
type PodWatch struct {
	stream *LogStream
}

// WatchPod starts watching pod. Start it before the action whose outcome the
// test reads, and call States once the pod is gone.
func WatchPod(t testing.TB, c *kind.Cluster, pod string) *PodWatch {
	t.Helper()
	buf := &syncBuffer{}
	cmd := c.Kubectl("-n", Namespace, "get", "pod", pod, "--watch", "--output-watch-events", "-o", "json")
	cmd.Stdout = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("watch pod %s: %v", pod, err)
	}
	w := &PodWatch{stream: &LogStream{cmd: cmd, buf: buf}}
	t.Cleanup(func() { w.stream.Stop() })
	return w
}

// States stops the watch and returns every pod state it received, in order.
func (w *PodWatch) States() []PodState {
	return decodeWatchEvents(w.stream.Stop())
}

// decodeWatchEvents decodes the concatenated watch events kubectl prints
// with --output-watch-events -o json. A truncated trailing event, which a
// stop in the middle of a write leaves, is dropped.
func decodeWatchEvents(raw string) []PodState {
	dec := json.NewDecoder(strings.NewReader(raw))
	var out []PodState
	for {
		var ev struct {
			Type   string  `json:"type"`
			Object podJSON `json:"object"`
		}
		if err := dec.Decode(&ev); err != nil {
			return out // io.EOF, or a truncated trailing event
		}
		if ev.Type == "ERROR" {
			continue
		}
		out = append(out, ev.Object.toState())
	}
}

// FirstTerminated returns the first terminated state of container across
// states, and whether any state reported one.
func FirstTerminated(states []PodState, container string) (ContainerState, bool) {
	for _, s := range states {
		if cs, ok := s.Container[container]; ok && cs.Terminated {
			return cs, true
		}
	}
	return ContainerState{}, false
}
