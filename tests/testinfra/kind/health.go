// SPDX-License-Identifier: MIT

package kind

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tier-5 suite runs against a standing Kind cluster that is reused
// across days of test runs. Two failure modes make that cluster serve
// misleading results, and both look identical to "Lenny is not installed"
// through the plain phase/Ready gate in install.go:
//
//   - The Lenny control plane crash-loops. Every dependent suite skips
//     with the generic install hint, so a broken control plane reads as
//     an un-provisioned workstation.
//   - The CNI crash-loops. NetworkPolicy is then not enforced at all,
//     which is worse than a skip: a policy assertion that expects a
//     connection to be refused can pass for the wrong reason.
//
// The helpers below separate "nothing is deployed" (skip) from "it is
// deployed and is in a restart loop" (fail with the object counts and
// the container termination reason), and prune the terminal-phase
// Sandbox debris whose accumulation drives the informer caches that
// cause the restart loop in the first place.

// cniSelectors are the label selectors of the CNI DaemonSets the
// harness knows about, in the order they are probed. Kind's default CNI
// is kindnet; TESTING.md's cluster profile documents Calico, and Cilium
// is the third CNI §13.2 names as satisfying the NetworkPolicy
// requirement. The first selector that matches at least one pod in
// kube-system is taken as the cluster's CNI.
var cniSelectors = []string{"app=kindnet", "k8s-app=calico-node", "k8s-app=cilium"}

// kubeSystemNamespace holds the CNI DaemonSet pods.
const kubeSystemNamespace = "kube-system"

// crashLoopRestartThreshold is the restart count above which a pod that
// is not Ready is treated as looping rather than as still settling. A
// freshly installed pod restarts a handful of times while its
// dependencies come up; a pod past this count that still is not Ready is
// not converging on its own.
const crashLoopRestartThreshold = 5

// sandboxDebrisMinAge is how long a Sandbox must have existed before the
// per-run prune deletes it. Terminal-phase Sandboxes younger than this
// may still be the subject of a test that is running concurrently, and
// leaving a bounded recent window in place keeps the prune from racing
// an in-flight assertion.
const sandboxDebrisMinAge = time.Hour

// envSkipPrune disables the per-run debris prune. It mirrors
// install.sh's LENNY_SKIP_PRUNE escape hatch for the image prune.
const envSkipPrune = "LENNY_KIND_SKIP_PRUNE"

// terminalSandboxPhases are the §6.2 terminal values of
// Sandbox.status.phase. A Sandbox in either phase serves no session and
// will never serve another, so its object is debris once it ages out.
var terminalSandboxPhases = map[string]bool{"failed": true, "terminated": true}

// podStatus is the per-pod health the bootstrap gate reads. Restarts is
// the highest restart count across the pod's containers; the reason
// slices carry one entry per container that reports one.
type podStatus struct {
	Name             string
	Phase            string
	Ready            bool
	Restarts         int
	WaitingReasons   []string
	TerminatedReason []string
}

// runningAndReady reports whether the pod is serving.
func (p podStatus) runningAndReady() bool {
	return p.Phase == "Running" && p.Ready
}

// inRestartLoop reports whether the pod is cycling rather than settling.
// A serving pod is never in a restart loop no matter how many times it
// has restarted in the past: the gateway on a long-lived cluster carries
// double-digit restart counts and is perfectly healthy.
func (p podStatus) inRestartLoop() bool {
	if p.runningAndReady() {
		return false
	}
	if containsString(p.WaitingReasons, "CrashLoopBackOff") {
		return true
	}
	if containsString(p.TerminatedReason, "OOMKilled") {
		return true
	}
	return p.Restarts > crashLoopRestartThreshold
}

// containsString reports whether haystack contains want.
func containsString(haystack []string, want string) bool {
	for _, s := range haystack {
		if s == want {
			return true
		}
	}
	return false
}

// fleetVerdict is the classification of a set of pods that make up one
// logical component (the Lenny control plane, the CNI DaemonSet).
type fleetVerdict int

const (
	// fleetAbsent — the selector matched nothing. The component is not
	// deployed, which is a skip rather than a failure.
	fleetAbsent fleetVerdict = iota
	// fleetHealthy — every pod is Running with Ready=True.
	fleetHealthy
	// fleetSettling — at least one pod is not Ready, but nothing is
	// cycling. The component may still converge, so this is a skip.
	fleetSettling
	// fleetRestartLoop — at least one pod is cycling (CrashLoopBackOff,
	// OOMKilled, or past the restart threshold while not Ready). The
	// component will not converge without operator action.
	fleetRestartLoop
)

// classifyPods reduces a component's pods to a single verdict. A restart
// loop outranks settling: one OOMKilled pod among several healthy ones
// still means the component is broken, and reporting it as "still coming
// up" is what let the condition persist unnoticed.
func classifyPods(pods []podStatus) fleetVerdict {
	if len(pods) == 0 {
		return fleetAbsent
	}
	settling := false
	for _, p := range pods {
		if p.inRestartLoop() {
			return fleetRestartLoop
		}
		if !p.runningAndReady() {
			settling = true
		}
	}
	if settling {
		return fleetSettling
	}
	return fleetHealthy
}

// podHealthJSONPath renders one tab-separated line per pod carrying the
// name, phase, Ready condition, per-container restart counts, per-container
// waiting reasons, and per-container last-termination reasons. Fields that
// are absent render empty, so every line carries the same separator count.
const podHealthJSONPath = `{range .items[*]}` +
	`{.metadata.name}{"\t"}` +
	`{.status.phase}{"\t"}` +
	`{.status.conditions[?(@.type=="Ready")].status}{"\t"}` +
	`{.status.containerStatuses[*].restartCount}{"\t"}` +
	`{.status.containerStatuses[*].state.waiting.reason}{"\t"}` +
	`{.status.containerStatuses[*].lastState.terminated.reason}` +
	`{"\n"}{end}`

// parsePodStatuses parses the podHealthJSONPath output. A line that does
// not carry the expected field count is skipped rather than guessed at:
// the gate's callers treat an empty result as "absent", which is a skip,
// so a parse that silently drops a malformed line cannot turn a broken
// cluster into a passing one.
func parsePodStatuses(out string) []podStatus {
	var pods []podStatus
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 6 {
			continue
		}
		pods = append(pods, podStatus{
			Name:             strings.TrimSpace(fields[0]),
			Phase:            strings.TrimSpace(fields[1]),
			Ready:            strings.TrimSpace(fields[2]) == "True",
			Restarts:         maxRestartCount(fields[3]),
			WaitingReasons:   splitReasons(fields[4]),
			TerminatedReason: splitReasons(fields[5]),
		})
	}
	return pods
}

// maxRestartCount returns the highest restart count in a space-separated
// per-container list. A pod is in a restart loop when any one of its
// containers is, so the maximum is the right reduction.
func maxRestartCount(field string) int {
	high := 0
	for _, tok := range strings.Fields(field) {
		n, err := strconv.Atoi(tok)
		if err != nil {
			continue
		}
		if n > high {
			high = n
		}
	}
	return high
}

// splitReasons splits a space-separated per-container reason list.
func splitReasons(field string) []string {
	return strings.Fields(field)
}

// podStatusesFor reads the health of the pods matching selector in
// namespace. An error (kubectl missing, namespace absent) yields no pods,
// which classifies as fleetAbsent and therefore as a skip.
func podStatusesFor(c *Cluster, namespace, selector string) []podStatus {
	args := []string{"-n", namespace, "get", "pods"}
	if selector != "" {
		args = append(args, "-l", selector)
	}
	args = append(args, "-o", "jsonpath="+podHealthJSONPath)
	out, err := c.Kubectl(args...).Output()
	if err != nil {
		return nil
	}
	return parsePodStatuses(string(out))
}

// cniPodStatuses returns the CNI DaemonSet's pods and the selector that
// matched them. When no known selector matches, it returns no pods and
// an empty selector: the cluster runs a CNI the harness does not
// recognize, and the gate makes no assertion about it.
func cniPodStatuses(c *Cluster) ([]podStatus, string) {
	for _, sel := range cniSelectors {
		if pods := podStatusesFor(c, kubeSystemNamespace, sel); len(pods) > 0 {
			return pods, sel
		}
	}
	return nil, ""
}

// RequireEnforcingCNI fails the calling test when the cluster's CNI
// DaemonSet is in a restart loop.
//
// spec: §13.2 (network isolation) — "The cluster CNI must support
// NetworkPolicy enforcement including egress rules." A CNI whose pods are
// crash-looping enforces no policy at all, so every NetworkPolicy
// assertion in tiers 5, 8, and 9 runs against a cluster that cannot
// satisfy the requirement under test. Such a run must fail loudly:
// a deny assertion on an unenforced cluster can pass for the wrong reason,
// which is a worse outcome than a skip.
func RequireEnforcingCNI(t testing.TB, c *Cluster) {
	t.Helper()
	pods, selector := cniPodStatuses(c)
	if classifyPods(pods) == fleetRestartLoop {
		t.Fatal(restartLoopDiagnostic(c,
			fmt.Sprintf("the cluster CNI (%s/%s)", kubeSystemNamespace, selector), pods))
	}
}

// RequireHealthyControlPlane skips when the Lenny control plane is absent
// or still settling, and fails when it is in a restart loop.
//
// spec: §13.2 (network isolation), §4.6.1 (warm pool controller). The
// skip path preserves install.go's contract that a workstation without an
// install reports a clean skip. The failure path is the change: a control
// plane that is deployed and cycling is an actionable cluster defect, and
// reporting it through the same generic install hint hid an OOM-killing
// restart loop behind dozens of skipped suites.
func RequireHealthyControlPlane(t testing.TB, c *Cluster) {
	t.Helper()
	if controlPlaneReady(c) {
		return
	}
	pods := podStatusesFor(c, lennySystemNamespace, lennyControlPlaneSelector)
	if classifyPods(pods) == fleetRestartLoop {
		t.Fatal(restartLoopDiagnostic(c,
			fmt.Sprintf("the Lenny control plane (%s)", lennySystemNamespace), pods))
	}
	t.Skip(installHint + " (Helm release present but control-plane pods are not Ready)")
}

// restartLoopDiagnostic renders the message the gate fails with. It names
// the cycling pods and their termination reasons, and appends the cluster
// object counts, because an OOMKilled control-plane or CNI pod on a
// standing cluster is normally caused by informer-cache growth over
// accumulated test objects rather than by a code defect.
func restartLoopDiagnostic(c *Cluster, component string, pods []podStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is in a restart loop; the cluster cannot serve this tier.\n", component)
	for _, p := range pods {
		if !p.inRestartLoop() {
			continue
		}
		fmt.Fprintf(&b, "  %s: phase=%s ready=%t restarts=%d waiting=%s lastTerminated=%s\n",
			p.Name, p.Phase, p.Ready, p.Restarts,
			joinReasons(p.WaitingReasons), joinReasons(p.TerminatedReason))
	}
	b.WriteString(clusterObjectCounts(c))
	b.WriteString("An OOMKilled control-plane or CNI pod on a standing cluster is normally " +
		"informer-cache pressure from accumulated test objects. Prune the terminal-phase " +
		"Sandboxes (the harness does this per run unless " + envSkipPrune + "=1 is set) and " +
		"re-run; if the counts above are already small, raise the component's memory limit.\n")
	return b.String()
}

// joinReasons renders a per-container reason list for the diagnostic,
// naming the absence explicitly so an empty column is not read as a
// truncated line.
func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return "none"
	}
	return strings.Join(reasons, ",")
}

// clusterObjectCounts renders the Sandbox and pod counts in the agent
// namespace, broken down by phase. It is best-effort: a cluster whose API
// server is not answering yields an empty section rather than an error,
// because the caller is already reporting a failure.
func clusterObjectCounts(c *Cluster) string {
	var b strings.Builder
	b.WriteString("cluster object counts (informer-cache pressure):\n")
	for _, kind := range []string{"sandboxes", "pods"} {
		counts, total := phaseHistogram(c, agentNamespace, kind)
		if total == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s in %s: total=%d%s\n", kind, agentNamespace, total, counts)
	}
	return b.String()
}

// phaseHistogram returns a rendered per-phase breakdown and the total
// object count for a resource kind in a namespace. Each line carries the
// object name before the phase so that an object with no phase set is
// still a non-empty line and is counted, and so that the trailing newline
// the jsonpath emits is not mistaken for one.
func phaseHistogram(c *Cluster, namespace, resource string) (string, int) {
	out, err := c.Kubectl(
		"-n", namespace, "get", resource,
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\t"}{.status.phase}{"\n"}{end}`,
	).Output()
	if err != nil {
		return "", 0
	}
	counts, total := tallyPhases(string(out))
	phases := make([]string, 0, len(counts))
	for phase := range counts {
		phases = append(phases, phase)
	}
	sort.Strings(phases)
	var b strings.Builder
	for _, phase := range phases {
		fmt.Fprintf(&b, " %s=%d", phase, counts[phase])
	}
	return b.String(), total
}

// tallyPhases counts name/phase lines by phase, rendering an unset phase
// as "(none)". It returns the per-phase counts and the total.
func tallyPhases(out string) (map[string]int, int) {
	counts := map[string]int{}
	total := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		_, phase, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		phase = strings.TrimSpace(phase)
		if phase == "" {
			phase = "(none)"
		}
		counts[phase]++
		total++
	}
	return counts, total
}

// sandboxRecord is one Sandbox as the prune sees it.
type sandboxRecord struct {
	Name     string
	Phase    string
	Created  time.Time
	Deleting bool
}

// pruneOnce guards the per-process debris prune.
var pruneOnce sync.Once

// pruneClusterDebrisOnce prunes aged terminal-phase Sandboxes once per
// test process. It is best-effort: a prune that cannot reach the API
// server logs and returns, leaving the health gate to report the real
// problem.
func pruneClusterDebrisOnce(t testing.TB, c *Cluster) {
	t.Helper()
	if os.Getenv(envSkipPrune) == "1" {
		return
	}
	pruneOnce.Do(func() {
		res, err := PruneTerminalSandboxes(c, time.Now())
		if err != nil {
			t.Logf("prune terminal Sandboxes: %v", err)
		}
		if len(res.Selected) > 0 {
			t.Logf("pruned %d of %d terminal-phase Sandbox object(s) older than %s from %s",
				len(res.Selected)-len(res.Survivors), len(res.Selected),
				sandboxDebrisMinAge, agentNamespace)
		}
	})
}

// PruneResult reports what one prune pass selected and what outlived it.
//
// The two are reported separately because the selection is anchored at the
// cutoff instant the caller passes in, while the cluster keeps moving: a
// pool that is churning retires more pods while the prune runs, and those
// objects age past sandboxDebrisMinAge moments later. Re-listing the
// cluster after a prune and treating whatever is aged by then as a prune
// failure therefore reports a failure on every actively-churning cluster.
// Survivors is the honest measure: of the objects this pass selected, the
// ones the delete and the finalizer-clearing pass did not remove.
type PruneResult struct {
	// Selected are the Sandbox names the pass chose: §6.2 terminal phase
	// and older than sandboxDebrisMinAge as of the cutoff instant.
	Selected []string
	// Survivors are the Selected names still present in the cluster when
	// the pass returned.
	Survivors []string
}

// PruneTerminalSandboxes deletes every Sandbox in the agent namespace
// whose §6.2 phase is terminal (`failed` or `terminated`) and which is
// older than sandboxDebrisMinAge, returning the number it removed.
//
// The agent pod carries an ownerReference to its Sandbox, so deleting the
// Sandbox cascades to the pod and the prune does not have to walk pods
// separately.
//
// A Sandbox carries the §4.6.1 `lenny.dev/session-cleanup` finalizer,
// which the WarmPoolController removes once no claim references the pod.
// The prune issues an ordinary delete first and gives the controller a
// window to do that. Objects still holding the finalizer after the window
// have theirs cleared directly: a terminal-phase Sandbox references no
// live session, so the invariant the finalizer protects (never delete a
// pod whose session is still active) does not apply to it, and a cluster
// whose controller is itself down from this debris cannot otherwise
// escape the deadlock.
//
// The result names the objects the pass selected and the ones that
// outlived it. A pass that removes everything it selected returns an empty
// Survivors list even on a cluster that is producing fresh debris the whole
// time, which is what makes the result assertable.
func PruneTerminalSandboxes(c *Cluster, now time.Time) (PruneResult, error) {
	stale, err := AgedTerminalSandboxes(c, now)
	if err != nil {
		return PruneResult{}, err
	}
	res := PruneResult{Selected: stale}
	if len(stale) == 0 {
		return res, nil
	}
	staleSet := make(map[string]bool, len(stale))
	for _, name := range stale {
		staleSet[name] = true
	}
	deleteSandboxes(c, stale)

	// Give the controller its normal finalizer-removal window before
	// clearing finalizers directly.
	remaining, err := waitForSandboxesGone(c, staleSet, finalizerGrace)
	if err != nil {
		return res, err
	}
	if len(remaining) == 0 {
		return res, nil
	}
	var stuck []string
	for _, r := range remaining {
		if r.Deleting {
			stuck = append(stuck, r.Name)
		}
	}
	clearSandboxFinalizers(c, stuck)

	// Clearing a finalizer lets the API server finish a delete that was
	// already accepted, so the objects go away asynchronously; poll once
	// more before declaring what survived.
	remaining, err = waitForSandboxesGone(c, staleSet, finalizerGrace)
	if err != nil {
		return res, err
	}
	for _, r := range remaining {
		res.Survivors = append(res.Survivors, r.Name)
	}
	return res, nil
}

// waitForSandboxesGone polls the agent namespace until none of the wanted
// Sandboxes remain or the window elapses, returning the records still
// present. A list error is returned rather than swallowed: the caller
// cannot distinguish "nothing left" from "could not look" otherwise.
func waitForSandboxesGone(c *Cluster, want map[string]bool, window time.Duration) ([]sandboxRecord, error) {
	deadline := time.Now().Add(window)
	for {
		records, err := listSandboxes(c)
		if err != nil {
			return nil, err
		}
		var remaining []sandboxRecord
		for _, r := range records {
			if want[r.Name] {
				remaining = append(remaining, r)
			}
		}
		if len(remaining) == 0 || !time.Now().Before(deadline) {
			return remaining, nil
		}
		time.Sleep(pruneSettlePollInterval)
	}
}

// pruneSettlePollInterval is how often the prune re-lists the namespace
// while it waits for the deletes it issued to complete.
const pruneSettlePollInterval = 2 * time.Second

// AgedTerminalSandboxes returns the names of the Sandboxes in the agent
// namespace that the per-run prune selects: §6.2 terminal phase
// (`failed` or `terminated`) and older than sandboxDebrisMinAge. It is
// the read-only half of PruneTerminalSandboxes, so a test can assert on
// what a prune would remove without removing it.
func AgedTerminalSandboxes(c *Cluster, now time.Time) ([]string, error) {
	records, err := listSandboxes(c)
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, r := range records {
		if !terminalSandboxPhases[r.Phase] {
			continue
		}
		if now.Sub(r.Created) < sandboxDebrisMinAge {
			continue
		}
		stale = append(stale, r.Name)
	}
	return stale, nil
}

// finalizerGrace is how long the prune waits for the WarmPoolController
// to remove the session-cleanup finalizer before clearing it directly.
const finalizerGrace = 30 * time.Second

// listSandboxes reads every Sandbox in the agent namespace with the
// fields the prune keys on.
func listSandboxes(c *Cluster) ([]sandboxRecord, error) {
	out, err := c.Kubectl(
		"-n", agentNamespace, "get", "sandboxes",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\t"}{.status.phase}{"\t"}`+
			`{.metadata.creationTimestamp}{"\t"}{.metadata.deletionTimestamp}{"\n"}{end}`,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("list sandboxes in %s: %w", agentNamespace, err)
	}
	return parseSandboxRecords(string(out)), nil
}

// parseSandboxRecords parses the listSandboxes jsonpath output. A record
// whose creation timestamp does not parse is reported with a zero time,
// which the age filter treats as arbitrarily old; the phase filter still
// gates it, so only a terminal Sandbox can be selected that way.
func parseSandboxRecords(out string) []sandboxRecord {
	var records []sandboxRecord
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			continue
		}
		created, _ := time.Parse(time.RFC3339, strings.TrimSpace(fields[2]))
		records = append(records, sandboxRecord{
			Name:     strings.TrimSpace(fields[0]),
			Phase:    strings.TrimSpace(fields[1]),
			Created:  created,
			Deleting: strings.TrimSpace(fields[3]) != "",
		})
	}
	return records
}

// deleteBatchSize caps how many object names go into one kubectl delete
// invocation, and pruneWorkers bounds how many invocations run at once.
// A cluster that has already accumulated enough debris to OOM its
// controller answers each object's API call slowly, so a serial prune of
// several thousand objects takes tens of minutes; batching and bounded
// parallelism bring it under a few. The worker bound keeps the prune
// inside the API server's inflight-mutating-request budget.
const (
	deleteBatchSize = 50
	pruneWorkers    = 16
)

// runKubectlPerBatch splits names into batches of at most batchSize and
// runs `kubectl <argsFor(batch)>` for each, pruneWorkers at a time.
// Errors are tolerated: an object another actor removed first is not a
// prune failure, and the caller re-lists to find what actually remains.
func runKubectlPerBatch(c *Cluster, names []string, batchSize int, argsFor func(batch []string) []string) {
	if len(names) == 0 {
		return
	}
	batches := make(chan []string)
	var wg sync.WaitGroup
	for i := 0; i < pruneWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range batches {
				_ = c.Kubectl(argsFor(batch)...).Run()
			}
		}()
	}
	for start := 0; start < len(names); start += batchSize {
		end := start + batchSize
		if end > len(names) {
			end = len(names)
		}
		batches <- names[start:end]
	}
	close(batches)
	wg.Wait()
}

// deleteSandboxes issues non-blocking deletes for every named Sandbox.
func deleteSandboxes(c *Cluster, names []string) {
	runKubectlPerBatch(c, names, deleteBatchSize, func(batch []string) []string {
		return append([]string{
			"-n", agentNamespace, "delete", "sandbox",
			"--wait=false", "--ignore-not-found",
		}, batch...)
	})
}

// clearSandboxFinalizers removes the metadata.finalizers list from each
// named Sandbox. kubectl patch addresses one object per invocation, so
// the batch size is one. A missing object or an already-empty finalizer
// list is not an error.
func clearSandboxFinalizers(c *Cluster, names []string) {
	runKubectlPerBatch(c, names, 1, func(batch []string) []string {
		return []string{
			"-n", agentNamespace, "patch", "sandbox", batch[0],
			"--type=merge", "-p", `{"metadata":{"finalizers":null}}`,
		}
	})
}
