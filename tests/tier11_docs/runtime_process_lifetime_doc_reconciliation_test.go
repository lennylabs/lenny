// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the runtime process lifetime.
//
// The runtime process lives as long as the pod. In the sidecar deployment
// model the kubelet starts it in the runtime container when the pod starts,
// the runtime dials the adapter, and the adapter accepts that connection at the
// pod's first session; every later session on the pod reaches the same
// process, keyed by `sessionId`. No session's end, interrupt, or missed
// heartbeat signals or ends the process, no occupancy-zero boundary closes its
// connection, the whole-pod scrub does not reach it, and the adapter sends it
// no drain frame at pod exit. A pod whose runtime cannot serve the next
// session retires at the recycle boundary.
//
// The retired readings are a fresh runtime process per session, an adapter
// that spawns the runtime binary, a session end, interrupt, or missed
// heartbeat that signals the runtime with SIGTERM, and a coordinated drain
// over CH-RUNTIMEOPS at session end or pod exit. A runtime author who follows
// any of them writes a runtime that exits after its session, which makes every
// recycled pod retire rather than serve its next session.
//
// The phrase lists below are the staged sentences the change replaced. They
// deliberately omit the sentences that describe the adapter's own `shutdown`
// write and SIGTERM escalation at pod drain, its exit-code reads, and the
// first-session manifest ordering, which keep their wording, so a later change
// to those sentences does not trip this test.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7.10 (Deployment Model), 5.2 (Pool Configuration and Execution
// Modes), 15.4.2 (RPC Lifecycle State Machine), 15.4.3 (Runtime Integration
// Levels), 28.5.3 (Intra-pod)

package tier11_docs_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// retiredRuntimeLifetimePhrases promise a per-session runtime process or a
// scrub that ends the runtime.
var retiredRuntimeLifetimePhrases = []string{
	"starts a fresh runtime process for each session",
	"terminates the SDK process along with all other session processes",
	"Adapter spawns runtime binary",
	"the runtime exits at each session end",
	"exits at each session end as in the default mode",
	"your binary exits at the end of each session",
}

// retiredPerSessionProcessPhrases state that a session end, an interrupt, or
// a missed heartbeat signals or ends the runtime process, that the adapter
// starts it, or that a session's end or the pod's exit coordinates a drain
// with the runtime.
var retiredPerSessionProcessPhrases = []string{
	"is treated as hung and is sent SIGTERM",
	"considers the process hung and sends SIGTERM",
	"within 10 seconds causes SIGTERM",
	"Same SIGTERM-based termination as Basic",
	"receives only `shutdown` at expiry",
	"receive only the `shutdown` message at expiry",
	"interrupt degrades to SIGTERM-based termination",
	"SIGTERM to agent, wait up to 10s",
	"by virtue of just having started",
	"The adapter spawns the runtime",
	"session ends normally",
	"exit on completion",
	"is spawning the runtime binary",
	"Agent binary is spawned with",
	"Your binary is spawned with its working directory",
	"Spawns your binary with stdin/stdout pipes",
	"On session end, sends `shutdown`",
	"the session has completed, or the budget is exhausted",
	"interrupt is SIGTERM-based",
	"an unimplemented interrupt becomes SIGTERM",
	"you only get SIGTERM",
	"you just get `shutdown` when time's up",
	"you receive only a `shutdown` message when the deadline is reached",
	"Failure to respond triggers SIGTERM",
	"Failure to ack triggers SIGTERM",
	"or the adapter sends SIGTERM",
	"gets SIGTERM after 10 seconds",
	"or get SIGTERM",
	"within 10 seconds, or SIGTERM",
	"heartbeat within 10 seconds, the adapter sends SIGTERM",
	"The adapter spawns your binary",
	"The adapter spawns this binary",
	"SIGTERM only, no safe stop point",
	"`shutdown` only, no advance notice",
	"setup belongs in the runtime's initialization",
	"belongs in the runtime's own initialization",
	"Adapter starts agent binary",
	"signals the agent to stop",
	"signals agent to stop",
	"with graceful shutdown coordination",
	"enables graceful shutdown coordination",
	"Coordinated via the CH-RUNTIMEOPS",
	"Coordinates graceful shutdown via a `DRAINING` state",
	"as the primary graceful shutdown path",
	"Coordinated draining when the pool is shutting down",
	"signal precedes that close",
}

// sweptFiles returns every file under root/dir whose extension is in exts,
// keyed by its repository-relative path, with each whitespace run collapsed to
// one space so a phrase wrapped across lines is still one match.
func sweptFiles(t *testing.T, root, dir string, exts ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	base := filepath.Join(root, dir)
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		for _, ext := range exts {
			if strings.HasSuffix(path, ext) {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				out[rel] = strings.Join(strings.Fields(readDocPage(t, path)), " ")
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", base, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s: no %v files found (moved or removed?)", dir, exts)
	}
	return out
}

// specAndDocFiles returns every specification and documentation page, every
// documentation diagram, and every schema, collapsed for sweeping.
func specAndDocFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := sweptFiles(t, root, "spec", ".md")
	for k, v := range sweptFiles(t, root, "docs", ".md", ".svg") {
		files[k] = v
	}
	for k, v := range sweptFiles(t, root, "schemas", ".json") {
		files[k] = v
	}
	return files
}

// spec: 4.7.10 (Deployment Model), 5.2 (Pool Configuration and Execution Modes)
// diagnosis: a page again promises a per-session runtime process or a scrub
//
//	that ends the runtime. The runtime process lives as long as the pod and
//	serves every later session on a recycled pod, and the whole-pod scrub does
//	not reach it. A runtime author who reads the retired text writes a runtime
//	that exits after each session, and every recycled pod then retires at the
//	recycle boundary instead of serving its next session. The test also fails
//	when the specification loses the paragraphs that state the lifetime, the
//	scrub's reach, or the deployer acknowledgment.
func TestRuntimeProcessLifetimeStatementsAgreeAcrossSpecAndDocs(t *testing.T) {
	root := repoRoot(t)

	for path, body := range specAndDocFiles(t, root) {
		requireNoneContainFold(t, path, body, retiredRuntimeLifetimePhrases)
	}

	requireAllContain(t, "spec/04_system-components.md",
		readRepoFile(t, root, "spec", "04_system-components.md"),
		[]string{"**Runtime process lifetime.**"})
	requireAllContain(t, "spec/05_runtime-registry-and-pool-model.md",
		readRepoFile(t, root, "spec", "05_runtime-registry-and-pool-model.md"),
		[]string{
			"**What the scrub reaches.**",
			"**Deployer acknowledgment (runtime process kept across sessions).**",
		})
}

// spec: 4.7.10 (Deployment Model), 15.4.2 (RPC Lifecycle State Machine),
// 15.4.3 (Runtime Integration Levels), 28.5.3 (Intra-pod)
// diagnosis: a page again states that a session end, an interrupt, or a missed
//
//	heartbeat signals or ends the runtime process, or that the adapter starts
//	it, or that a session's end or the pod's exit coordinates a drain with the
//	runtime. The runtime process lives as long as the pod, the kubelet starts
//	it in the sidecar model, the adapter sends it no signal at a session's end,
//	and no drain coordination exists at pod exit.
func TestPerSessionProcessStatementsRemovedFromStagedSites(t *testing.T) {
	root := repoRoot(t)
	for path, body := range specAndDocFiles(t, root) {
		requireNoneContainFold(t, path, body, retiredPerSessionProcessPhrases)
	}
}

// keptRuntimeRetireSpecFiles are the specification chapters that name the
// kept-runtime retire condition. The §5.2 Pod retirement policy is its one
// home, and every other site names it by its label.
var keptRuntimeRetireSpecFiles = []string{
	"04_system-components.md",
	"05_runtime-registry-and-pool-model.md",
	"06_warm-pod-model.md",
	"29_communication-scenarios.md",
}

// keptRuntimeRetireHomeLabel opens the §5.2 Pod retirement policy item that
// states the condition.
const keptRuntimeRetireHomeLabel = "**Runtime not live:**"

// keptRuntimeRetireRestatements are the phrasings a restatement of the
// condition would carry.
var keptRuntimeRetireRestatements = []string{
	"runtime_not_live",
	"cannot serve the next session",
}

// spec: 5.2 (Pod retirement policy)
// diagnosis: a site outside the §5.2 Pod retirement policy restates the
//
//	kept-runtime retire condition instead of naming it, so the copies can
//	drift; or the home item lost the reason value or the clause that retires a
//	pod whose report omits the liveness fact.
func TestKeptRuntimeRetireConditionHasOneSpecHome(t *testing.T) {
	root := repoRoot(t)

	homes := 0
	for _, name := range keptRuntimeRetireSpecFiles {
		body := readRepoFile(t, root, "spec", name)
		for i, line := range strings.Split(body, "\n") {
			if strings.Contains(line, keptRuntimeRetireHomeLabel) && name == "05_runtime-registry-and-pool-model.md" {
				homes++
				requireAllContain(t, "spec/"+name+" Runtime not live item", line,
					[]string{"runtime_not_live", "or omits that fact"})
				continue
			}
			for _, phrase := range keptRuntimeRetireRestatements {
				if strings.Contains(line, phrase) {
					t.Errorf("spec/%s line %d restates the kept-runtime retire condition (%q); name the §5.2 Pod retirement policy item %q instead", name, i+1, phrase, "Runtime not live")
				}
			}
		}
	}
	if homes != 1 {
		t.Errorf("spec/05_runtime-registry-and-pool-model.md: want exactly one line carrying %q, found %d (renamed, removed, or duplicated?)", keptRuntimeRetireHomeLabel, homes)
	}
}
