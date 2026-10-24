// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// spec: §4.7.10 (Runtime supervisor, Liveness)
//
// TestPodStateReadsReadinessDeletionAndContainerExits pins the pod reading
// the supervisor's cluster tests assert on: readiness, deletion, and each
// container's running and terminated state.
func TestPodStateReadsReadinessDeletionAndContainerExits(t *testing.T) {
	raw := `{"metadata":{"name":"p","deletionTimestamp":"2026-10-24T00:00:00Z","labels":{"a":"b"}},
	"spec":{"nodeName":"n1"},
	"status":{"conditions":[{"type":"Ready","status":"True","lastTransitionTime":"2026-10-24T00:00:05Z"}],
	"containerStatuses":[
	 {"name":"adapter","containerID":"containerd://abc","restartCount":0,"state":{"terminated":{"exitCode":1,"reason":"Error"}}},
	 {"name":"runtime","containerID":"containerd://def","restartCount":2,"state":{"running":{"startedAt":"2026-10-24T00:00:01Z"}}}]}}`
	var p podJSON
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	s := p.toState()
	if s.Name != "p" || s.Node != "n1" || !s.Ready || !s.Deleting || s.Labels["a"] != "b" || s.ReadyAt.IsZero() {
		t.Fatalf("state = %+v", s)
	}
	a, r := s.Container["adapter"], s.Container["runtime"]
	if !a.Terminated || a.ExitCode != 1 || a.Reason != "Error" || a.Running {
		t.Errorf("adapter = %+v; want terminated with exit 1", a)
	}
	if !r.Running || r.RestartCount != 2 || r.StartedAt.IsZero() || r.ID != "containerd://def" {
		t.Errorf("runtime = %+v; want running with two restarts", r)
	}
	if d := s.Describe(); !strings.Contains(d, "exit=1") || !strings.Contains(d, "deleting=true") {
		t.Errorf("Describe = %q", d)
	}
}

// spec: §4.7.10 (Runtime supervisor, Launch)
//
// TestProbeReplyHelpersReadTheTextPartAndEnvironment pins the reply helpers:
// the first text part of a message response, the last line of kubectl
// output, a variable of a NAME=VALUE list, and the serving probes of a
// process list.
func TestProbeReplyHelpersReadTheTextPartAndEnvironment(t *testing.T) {
	text, err := OutputText(json.RawMessage(`[{"type":"data","text":"x"},{"type":"text","text":"{\"ok\":true}"}]`))
	if err != nil || text != `{"ok":true}` {
		t.Fatalf("OutputText = %q, %v", text, err)
	}
	for _, bad := range []string{`{`, `[{"type":"data"}]`} {
		if _, err := OutputText(json.RawMessage(bad)); err == nil {
			t.Errorf("OutputText(%s) succeeded", bad)
		}
	}
	if got := lastLine("Warning: x\n{\"a\":1}\n\n"); got != `{"a":1}` {
		t.Errorf("lastLine = %q", got)
	}
	if v, ok := EnvValue([]string{"A=1", "B=x=y"}, "B"); !ok || v != "x=y" {
		t.Errorf("EnvValue = %q, %v", v, ok)
	}
	if _, ok := EnvValue([]string{"A=1"}, "C"); ok {
		t.Error("EnvValue found an absent variable")
	}
	procs := []superviseprobe.Process{
		{PID: 1, Cmdline: []string{"/lenny-supervisor/lenny-supervisor", "run"}},
		{PID: 7, Cmdline: []string{ProbeExe}, State: "S"},
		{PID: 9, Cmdline: []string{ProbeExe, superviseprobe.SubProcesses}, State: "R"},
	}
	if got := ServingProbes(procs); len(got) != 1 || got[0].PID != 7 {
		t.Errorf("ServingProbes = %+v; want PID 7", got)
	}
}
