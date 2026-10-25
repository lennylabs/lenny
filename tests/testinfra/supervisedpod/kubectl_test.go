// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// fakeKubectlScript stands in for kubectl on PATH. Each rule is a file
// NN.match holding a substring of the argument line; the first rule, in
// file-name order, whose substring the line contains prints NN.out.K on its
// Kth call when that file exists and NN.out otherwise, and exits with the
// status in NN.code (0 when absent). Every call is appended to calls.log.
const fakeKubectlScript = `#!/bin/sh
dir="$FAKE_KUBECTL_DIR"
printf '%s\n' "$*" >> "$dir/calls.log"
for m in "$dir"/*.match; do
  [ -e "$m" ] || continue
  pat=$(cat "$m")
  case "$*" in
    *"$pat"*)
      base="${m%.match}"
      n=$(( $(cat "$base.count" 2>/dev/null || echo 0) + 1 ))
      echo "$n" > "$base.count"
      if [ -e "$base.out.$n" ]; then cat "$base.out.$n"; else cat "$base.out" 2>/dev/null; fi
      exit "$(cat "$base.code" 2>/dev/null || echo 0)"
      ;;
  esac
done
exit 0
`

// kubectlRule is one fake kubectl response: the outputs are printed on the
// first, second, ... call, the last one repeating.
type kubectlRule struct {
	match   string
	outputs []string
	code    int
}

// fakeKubectl installs a fake kubectl answering with rules, in order, and
// returns a cluster bound to it and a reader of the argument lines it saw.
func fakeKubectl(t *testing.T, rules ...kubectlRule) (*kind.Cluster, func() string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(fakeKubectlScript), 0o755); err != nil {
		t.Fatal(err)
	}
	for i, r := range rules {
		base := filepath.Join(dir, fmt.Sprintf("%02d", i))
		write(t, base+".match", r.match)
		last := ""
		for k, out := range r.outputs {
			write(t, fmt.Sprintf("%s.out.%d", base, k+1), out)
			last = out
		}
		write(t, base+".out", last)
		if r.code != 0 {
			write(t, base+".code", fmt.Sprint(r.code))
		}
	}
	t.Setenv("FAKE_KUBECTL_DIR", dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	calls := func() string {
		b, _ := os.ReadFile(filepath.Join(dir, "calls.log"))
		return string(b)
	}
	return &kind.Cluster{Name: "fake", KubeconfigPath: filepath.Join(dir, "kubeconfig")}, calls
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// podDoc renders a pod object as kubectl -o json prints it.
func podDoc(name string, ready, deleting bool) string {
	status := "False"
	if ready {
		status = "True"
	}
	del := ""
	if deleting {
		del = `"deletionTimestamp":"2026-10-24T00:00:00Z",`
	}
	return fmt.Sprintf(`{"metadata":{"name":%q,%s"labels":{"lenny.dev/pool":"p"}},"spec":{"nodeName":"n1"},`+
		`"status":{"conditions":[{"type":"Ready","status":%q,"lastTransitionTime":"2026-10-24T00:00:00Z"}],`+
		`"containerStatuses":[{"name":"runtime","containerID":"containerd://abc","restartCount":1,`+
		`"state":{"running":{"startedAt":"2026-10-24T00:00:00Z"}}}]}}`, name, del, status)
}

// podList renders pods as kubectl get pods -o json prints them.
func podList(docs ...string) string {
	return `{"items":[` + strings.Join(docs, ",") + `]}`
}

func podNames(pods []PodState) []string {
	var names []string
	for _, p := range pods {
		names = append(names, p.Name)
	}
	return names
}

// spec: §4.7.10 (Runtime supervisor, Liveness)
//
// TestPodReadersDecodeWhatKubectlPrints pins the pod and pool readers the
// supervisor's cluster tests assert on against kubectl's JSON output.
func TestPodReadersDecodeWhatKubectlPrints(t *testing.T) {
	c, calls := fakeKubectl(
		t,
		kubectlRule{match: "get pod gone", outputs: []string{""}},
		kubectlRule{match: "get pod a", outputs: []string{podDoc("a", true, false)}},
		kubectlRule{match: "get pods -l lenny.dev/pool=p", outputs: []string{podList(podDoc("a", true, false), podDoc("b", false, false), podDoc("c", true, true))}},
	)
	st, found := GetPod(t, c, "a")
	if !found || !st.Ready || st.Deleting || st.Node != "n1" || st.Container["runtime"].RestartCount != 1 || !st.Container["runtime"].Running {
		t.Fatalf("GetPod(a) = %+v, %t", st, found)
	}
	if !strings.Contains(st.Describe(), "a ready=true deleting=false") || !strings.Contains(st.Describe(), "runtime{running=true") {
		t.Fatalf("Describe() = %q", st.Describe())
	}
	if _, found := GetPod(t, c, "gone"); found {
		t.Fatal("GetPod reported a pod kubectl did not print")
	}
	pods := PoolPods(t, c, "p")
	if got := strings.Join(podNames(pods), ","); got != "a,b,c" || !pods[2].Deleting {
		t.Fatalf("PoolPods = %v", pods)
	}
	if ready := WaitReadyPods(t, c, "p", 1, time.Second); strings.Join(podNames(ready), ",") != "a" {
		t.Fatalf("WaitReadyPods = %v, want the Ready pod not being deleted", podNames(ready))
	}
	if !strings.Contains(calls(), "--kubeconfig") {
		t.Fatalf("kubectl ran without the cluster's kubeconfig: %s", calls())
	}
}

// spec: §5.2 (PoolWarmingUp condition), §4.6.1
//
// TestClaimableReadersSkipClaimedPodsAndAWarmingPool pins the claimability
// readers: a pod holding a SandboxClaim is not idle, and a pool whose
// template reports PoolWarmingUp is not available.
func TestClaimableReadersSkipClaimedPodsAndAWarmingPool(t *testing.T) {
	c, _ := fakeKubectl(
		t,
		kubectlRule{match: "sandboxwarmpool p -o jsonpath={.spec.minWarm}", outputs: []string{"1 3"}},
		kubectlRule{match: "sandboxwarmpool p", outputs: []string{"tmpl-p"}},
		kubectlRule{match: "sandboxtemplate tmpl-p", outputs: []string{"True", "False"}},
		kubectlRule{match: "sandboxclaim claim-a --ignore-not-found", outputs: []string{"sandboxclaim/claim-a"}},
		kubectlRule{match: "sandboxclaim claim-a -o jsonpath={.status.phase}", outputs: []string{"bound", "reserved"}},
		kubectlRule{match: "sandboxclaim", outputs: []string{""}},
		kubectlRule{match: "get pods -l lenny.dev/pool=p", outputs: []string{podList(podDoc("a", true, false), podDoc("b", true, false), podDoc("c", true, false))}},
	)
	if !PoolWarmingUp(t, c, "p") {
		t.Fatal("PoolWarmingUp = false on a template reporting True")
	}
	WaitPoolAvailable(t, c, "p", time.Second)
	if !ClaimExists(t, c, "a") || ClaimExists(t, c, "b") {
		t.Fatal("ClaimExists misread the claims")
	}
	if got := strings.Join(podNames(WaitClaimablePods(t, c, "p", 2, time.Second)), ","); got != "b,c" {
		t.Fatalf("WaitClaimablePods = %s, want the unclaimed Ready pods", got)
	}
	if !FirstIdleInClaimOrder(t, c, "p", "b") || FirstIdleInClaimOrder(t, c, "p", "c") {
		t.Fatal("FirstIdleInClaimOrder misordered the idle pods")
	}
	WaitClaimPhase(t, c, "a", "reserved", 5*time.Second)
	requirePinnedIdleBound(t, c, "p")
}

// spec: §5.2 (Recycle lifecycle), §4.6.1
//
// TestFreshIdlePodsReturnsOnlyPodsCreatedAfterTheReplacement pins that the
// idle pods a case reads were created after the pool's earlier idle pods
// were deleted, and that a claimed pod is left in place.
func TestFreshIdlePodsReturnsOnlyPodsCreatedAfterTheReplacement(t *testing.T) {
	c, calls := fakeKubectl(
		t,
		kubectlRule{match: "sandboxclaim claim-held", outputs: []string{"sandboxclaim/claim-held"}},
		kubectlRule{match: "sandboxclaim", outputs: []string{""}},
		kubectlRule{match: "get pods -l lenny.dev/pool=p", outputs: []string{
			podList(podDoc("old", true, false), podDoc("held", true, false)),
			podList(podDoc("held", true, false), podDoc("new", true, false)),
		}},
	)
	if got := strings.Join(podNames(FreshIdlePods(t, c, "p", 1, time.Second)), ","); got != "new" {
		t.Fatalf("FreshIdlePods = %s, want only the replacement pod", got)
	}
	log := calls()
	if !strings.Contains(log, "delete pod old") || strings.Contains(log, "delete pod held") {
		t.Fatalf("deleted the wrong pods:\n%s", log)
	}
}

// spec: §4.7.10 (Runtime supervisor, Signals)
//
// TestPodLifecycleHelpersRunTheirKubectlCommands pins the deletion, log,
// sweep, and exec helpers against the commands they issue and the output
// they read back.
func TestPodLifecycleHelpersRunTheirKubectlCommands(t *testing.T) {
	c, calls := fakeKubectl(
		t,
		kubectlRule{match: "get pod p1", outputs: []string{podDoc("p1", false, true), ""}},
		kubectlRule{match: "logs -f p1", outputs: []string{"streamed line\n"}},
		kubectlRule{match: "logs p1 -c runtime --previous", outputs: []string{"previous log"}},
		kubectlRule{match: "logs p1 -c runtime", outputs: []string{"current log"}},
		kubectlRule{match: "delete pods -l " + TestLabel, outputs: []string{"pod/left deleted\n"}},
		kubectlRule{match: "processes", outputs: []string{`Warning: x` + "\n" + `[{"pid":1,"cmdline":["/lenny-supervisor/supervisor"]},{"pid":7,"cmdline":["` + ProbeExe + `"],"state":"S"}]`}},
		kubectlRule{match: "attack", outputs: []string{`{"attack":"bind","ok":false,"errno":"EADDRINUSE"}`}},
	)
	last, gone := WaitGone(t, c, "p1", 5*time.Second)
	if !gone || !last.Deleting {
		t.Fatalf("WaitGone = %+v, %t; want the deleting state then gone", last, gone)
	}
	DeletePod(t, c, "p1")
	if got := Logs(t, c, "p1", "runtime", true); got != "previous log" {
		t.Fatalf("Logs(previous) = %q", got)
	}
	if got := Logs(t, c, "p1", "runtime", false); got != "current log" {
		t.Fatalf("Logs = %q", got)
	}
	SweepTestObjects(t, c)
	s := StreamLogs(t, c, "p1", "runtime")
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(s.buf.String(), "streamed line") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := s.Stop(); !strings.Contains(got, "streamed line") {
		t.Fatalf("Stop() = %q", got)
	}
	procs := Processes(t, c, "p1")
	if serving := ServingProbes(procs); len(serving) != 1 || serving[0].PID != 7 {
		t.Fatalf("ServingProbes(%v) = %v, want the launched probe only", procs, serving)
	}
	if r := Attack(t, c, "p1", "bind"); r.OK || r.Errno != "EADDRINUSE" {
		t.Fatalf("Attack = %+v", r)
	}
	log := calls()
	for _, want := range []string{"delete pod p1 --wait=false --ignore-not-found", "exec p1 -c runtime -- " + ProbeExe} {
		if !strings.Contains(log, want) {
			t.Errorf("kubectl calls lack %q:\n%s", want, log)
		}
	}
}

// spec: §4.7.10 (Runtime supervisor, Signals)
//
// TestSweepTestObjectsNeverFailsTheRun pins that the start-time sweep logs a
// kubectl failure instead of failing the test whose subject is intact.
func TestSweepTestObjectsNeverFailsTheRun(t *testing.T) {
	c, calls := fakeKubectl(t, kubectlRule{match: "delete pods", outputs: []string{"forbidden"}, code: 1})
	SweepTestObjects(t, c)
	if !strings.Contains(calls(), TestLabel+"=true") {
		t.Fatalf("the sweep did not select the test label: %s", calls())
	}
}
