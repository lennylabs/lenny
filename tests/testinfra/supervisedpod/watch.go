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

// ClaimWatch follows the SandboxClaim the gateway creates when it claims one
// pod, named claim-<pod>. It records whether that claim was created or
// updated while the watch ran, so a test can tell that a session start was
// placed on the pod even when the start failed and the claim was removed
// with the pod. spec: §4.6.1; §5.2 (Recycle lifecycle).
type ClaimWatch struct {
	stream *LogStream
	name   string
}

// WatchClaim starts watching pod's claim. Start it before the session start
// whose placement the test reads.
func WatchClaim(t testing.TB, c *kind.Cluster, pod string) *ClaimWatch {
	t.Helper()
	buf := &syncBuffer{}
	name := "claim-" + pod
	cmd := c.Kubectl("-n", Namespace, "get", "sandboxclaims", "--field-selector", "metadata.name="+name,
		"--watch", "--output-watch-events", "-o", "json")
	cmd.Stdout = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("watch claim %s: %v", name, err)
	}
	w := &ClaimWatch{stream: &LogStream{cmd: cmd, buf: buf}, name: name}
	t.Cleanup(func() { w.stream.Stop() })
	return w
}

// Claimed stops the watch and reports whether the claim was created or
// updated while it ran.
func (w *ClaimWatch) Claimed() bool {
	return claimSeen(w.stream.Stop(), w.name)
}

// claimSeen reports whether the concatenated watch events in raw carry an
// ADDED or MODIFIED event for the object named name.
func claimSeen(raw, name string) bool {
	dec := json.NewDecoder(strings.NewReader(raw))
	for {
		var ev struct {
			Type   string `json:"type"`
			Object struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
			} `json:"object"`
		}
		if err := dec.Decode(&ev); err != nil {
			return false // io.EOF, or a truncated trailing event
		}
		if (ev.Type == "ADDED" || ev.Type == "MODIFIED") && ev.Object.Metadata.Name == name {
			return true
		}
	}
}
