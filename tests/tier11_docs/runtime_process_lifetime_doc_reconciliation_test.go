// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the runtime process lifetime.
//
// Every agent pod is a `keep` pod or a `restart` pod. A `keep` pod's runtime
// process lives as long as the pod unless `DemoteSDK` ends it, and a
// `restart` pod's runtime process lives from its launch to the next
// whole-pod scrub. In the sidecar deployment model the runtime supervisor,
// the runtime container's first process, launches the runtime's `command`
// when the adapter asks it to, after the manifest exists, and ends it at the
// adapter's request. Each process serves every session that starts while it
// is live over one connection, keyed by `sessionId`. No session's end,
// interrupt, or missed heartbeat signals or ends the process, no
// occupancy-zero boundary on a `keep` pod closes its connection, and a
// process that exits by itself is never relaunched: the pod retires.
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
	"os"
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
//	that ends the runtime on every pod. A `keep` pod's runtime process lives as
//	long as the pod and serves every later session on it, and the scrub does
//	not end it on a `keep` pod. A runtime author who reads the retired text
//	writes a runtime that exits after each session, and every recycled pod
//	then retires at the recycle boundary instead of serving its next session. The test also fails
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
//	runtime. The runtime supervisor launches the process in the sidecar model,
//	the adapter sends it no signal at a session's end, and no drain
//	coordination exists at pod exit.
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

// runtimeSupervisorHomeLabel opens the §4.7.10 paragraph that is the one home
// of the runtime supervisor's mechanism.
const runtimeSupervisorHomeLabel = "**Runtime supervisor.**"

// spec: 4.7.10 (Runtime supervisor)
// diagnosis: a specification chapter opens a second paragraph with the
//
//	runtime supervisor's label, or §4.7.10 lost the paragraph. The
//	supervisor's launch, environment, end, signal, exit, and liveness rules
//	have one home, and every other site cites it by name; a second paragraph
//	that restates them drifts from the first.
func TestRuntimeSupervisorParagraphHasOneSpecHome(t *testing.T) {
	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "spec"))
	if err != nil {
		t.Fatalf("read spec/: %v", err)
	}
	homes := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		lines := strings.Split(readRepoFile(t, root, "spec", e.Name()), "\n")
		for i, line := range lines {
			// A paragraph opens with the label after a blank line; a citation
			// wrapped onto the start of a line continues its paragraph.
			opensParagraph := i == 0 || strings.TrimSpace(lines[i-1]) == ""
			if !opensParagraph || !strings.HasPrefix(strings.TrimSpace(line), runtimeSupervisorHomeLabel) {
				continue
			}
			if e.Name() != "04_system-components.md" {
				t.Errorf("spec/%s line %d opens a paragraph with %q; cite the §4.7.10 paragraph instead", e.Name(), i+1, runtimeSupervisorHomeLabel)
				continue
			}
			homes++
		}
	}
	if homes != 1 {
		t.Errorf("spec/04_system-components.md: want exactly one paragraph opening with %q, found %d", runtimeSupervisorHomeLabel, homes)
	}
}

// retiredSidecarLaunchPhrases are the sentences the runtime supervisor
// retired from the documentation: a pod that keeps one runtime process on
// every recycling pool, the kubelet starting the runtime binary through its
// image entrypoint, an adapter that starts the runtime binary itself, an
// adapter that captures the runtime's stderr, and a
// scrub that leaves the runtime process untouched on every pod.
var retiredSidecarLaunchPhrases = []string{
	"keeps one runtime process",
	"keeps the runtime process that served",
	"no longer keeps runtime processes",
	"kubelet starts",
	"kubelet started",
	"captured by the adapter",
	"captured by adapter",
	"does not reach your runtime process",
	"does not reach the runtime process",
	"Session logs and runtime stderr",
	"its runtime process cannot serve the next session",
	"the adapter SIGKILLs",
	"the adapter starts the runtime binary",
}

// spec: 4.7.10 (Runtime supervisor, Runtime process lifetime), 5.2
// (Runtime-process lifetime, What the scrub reaches)
// diagnosis: a page again says that the kubelet starts the runtime binary,
//
//	that every recycling pod keeps one runtime process, or that the scrub
//	never reaches the runtime process. In the sidecar model the runtime
//	supervisor launches the runtime's `command`, a `restart` pod ends its
//	runtime process in each whole-pod scrub, and the runtime's stderr goes to
//	its container's log.
func TestRetiredSidecarLaunchPhrasesAbsentFromSpecAndDocs(t *testing.T) {
	root := repoRoot(t)
	for path, body := range specAndDocFiles(t, root) {
		requireNoneContainFold(t, path, body, retiredSidecarLaunchPhrases)
	}
}

// supervisedDocSection returns the named section of a documentation page with each
// whitespace run collapsed, so a pinned sentence wrapped across lines still
// matches.
func supervisedDocSection(t *testing.T, root, page, heading string) string {
	t.Helper()
	body := section(readDocPage(t, filepath.Join(root, "docs", page)), heading)
	if body == "" {
		t.Fatalf("docs/%s: section %q not found", page, heading)
	}
	return strings.Join(strings.Fields(body), " ")
}

// supervisedDocPage returns a whole documentation page with each whitespace run
// collapsed.
func supervisedDocPage(t *testing.T, root, page string) string {
	t.Helper()
	return strings.Join(strings.Fields(readDocPage(t, filepath.Join(root, "docs", page))), " ")
}

// spec: 4.7.10 (Runtime supervisor, Runtime process lifetime), 5.1
// (command, env)
// diagnosis: the runtime-author pages no longer state what the runtime
//
//	supervisor does to the runtime process: that it launches `command` after
//	the manifest exists, ignores the image's ENTRYPOINT, CMD, and ENV, ends
//	every process in the container, forwards no stop signal at pod
//	termination, ignores the signals a runtime sends it, never relaunches a
//	process that exits by itself, and fails no session for an exit within a
//	`shutdown` deadline; or the runtime configuration page lost the `command`
//	and `env` rows, or the `env` row no longer states that `env` is valid
//	only beside `command`. An author who misses them builds an image whose
//	entrypoint never runs, a runtime that exits at a session's end, or a
//	sidecar runtime launched without the variables its binary needs. The
//	test also fails when the Session End paragraph states one lifetime for
//	every pod instead of the `keep` and `restart` lifetimes, or when the pod
//	termination paragraph has the supervisor exit after the adapter's end
//	request rather than only on the close of the adapter's connection, or
//	when the publishing page's `preConnect` paragraph no longer has the
//	supervisor launch the process at warm-up only under a closed SDK-warm
//	circuit breaker and otherwise at workspace configuration. It also fails
//	when a page makes "no runtime process is live" the launch trigger, which
//	would have a session after a crash launch a new process where the
//	specification refuses the start and retires the pod; when the **Launch.**
//	paragraph omits the not-live condition or the refused start; when the
//	**Runtime exits.** table has the gateway rather than the adapter terminate
//	the sessions at the coordinator hold timeout; or when a warm-phase or
//	`shutdown`-deadline sentence names no launcher or ender. It also fails
//	when the lifecycle page no longer says that the higher **Runtime exits.**
//	row decides an end more than one row matches, or when the adapter contract
//	fails every session on an exit during the pod's termination, which
//	contradicts the precedence that gives an exit after the drain's `shutdown`
//	the `shutdown` row; and when the **Liveness.** paragraph omits the
//	adapter's failure to rebind its runtime sockets after an attested end,
//	which retires a `restart` pod with `runtime_not_live` at the scrub.
func TestRuntimeAuthorPagesStateTheRuntimeSupervisor(t *testing.T) {
	root := repoRoot(t)
	requireAllContain(t, "lifecycle.md Runtime Process Lifetime",
		supervisedDocSection(t, root, "runtime-author-guide/lifecycle.md", "Runtime Process Lifetime"), []string{
			"On a `keep` pod your runtime process lives as long as the pod, unless `DemoteSDK` ends it",
			"On a `restart` pod each runtime process lives from its launch to the next whole-pod scrub",
		})
	requireAllContain(t, "lifecycle.md Runtime Supervisor",
		supervisedDocSection(t, root, "runtime-author-guide/lifecycle.md", "Runtime Supervisor"), []string{
			"**Launch.**",
			"after the manifest your process reads exists",
			"**Environment.**",
			"The image's `ENTRYPOINT`, `CMD`, and `ENV` are not used",
			"**End.**",
			"only after two consecutive passes find no other process",
			"**Pod termination.**",
			"the supervisor forwards no stop signal to your runtime process",
			"**Signals.**",
			"SIGKILL and SIGSTOP are the only signals a process can make take effect on it",
			"a runtime that kills the supervisor ends every process in its own container",
			"A runtime that stops the supervisor freezes only the supervisor, and your runtime process and its children keep running",
			"In both cases the adapter loses its link to the supervisor, and the pod retires",
			"**Runtime exits.**",
			"A runtime does not exit at the end of a session",
			"A runtime that exits after `shutdown` within its `deadline_ms` fails no session",
			"When more than one row applies to one end, the row higher in the table decides, so an exit after `shutdown` during the pod's termination fails no session",
			"**Liveness.**",
			"or when the adapter fails to rebind its runtime sockets after the platform ends a runtime process",
			"when a launch at warm-up fails or its process does not connect within the adapter's wait",
			"on a `restart` pod, when the supervisor's startup self-test reports a failed gating property",
			"Before the pod becomes ready, and at any time on a service-mode pod, the adapter exits instead, and the platform replaces the pod",
		})
	requireRuntimeSupervisorLaunchCondition(t, root)
	requireAllContain(t, "lifecycle.md Runtime Supervisor",
		supervisedDocSection(t, root, "runtime-author-guide/lifecycle.md", "Runtime Supervisor"), []string{
			"until the adapter asks the supervisor to end it. When the adapter's connection to the supervisor closes, the supervisor exits",
		})
	requireNoneContain(t, "lifecycle.md Runtime Supervisor",
		supervisedDocSection(t, root, "runtime-author-guide/lifecycle.md", "Runtime Supervisor"), []string{
			"ends or freezes every process",
			"or the adapter's connection to the supervisor closes. The supervisor then exits",
		})
	sessionEnd := supervisedDocSection(t, root, "runtime-author-guide/lifecycle.md", "Session End")
	requireAllContain(t, "lifecycle.md Session End", sessionEnd, []string{
		"the process serves the pod's other sessions, and on a `keep` pod also later ones, while on a `restart` pod the next whole-pod scrub ends it",
	})
	requireNoneContain(t, "lifecycle.md Session End", sessionEnd, []string{
		"the process serves the pod's other sessions and later ones",
	})
	requireAllContain(t, "runtime-configuration.md Identity and image",
		supervisedDocSection(t, root, "runtime-author-guide/runtime-configuration.md", "Identity and image"), []string{
			"| `command` |",
			"| `env` |",
			"the admin registration payload and the bootstrap seed do not carry it",
			"Valid only on a runtime that carries `command`, which is a `type: agent` runtime in the sidecar deployment model, and rejected on every other runtime",
		})
	requireNoneContain(t, "runtime-configuration.md Identity and image",
		supervisedDocSection(t, root, "runtime-author-guide/runtime-configuration.md", "Identity and image"), []string{
			"rejected wherever `command` is",
		})
	requireAllContain(t, "publishing.md",
		supervisedDocPage(t, root, "runtime-author-guide/publishing.md"), []string{
			"the [runtime supervisor](lifecycle.md#runtime-supervisor) launches the runtime's `command` at warm-up when the pool's SDK-warm circuit breaker is closed at the moment the pod is created",
			"the supervisor launches the process when the session's workspace is configured",
		})
	requireAllContain(t, "runtime-configuration.md", supervisedDocPage(t, root, "runtime-author-guide/runtime-configuration.md"), []string{
		"Whether the runtime supervisor launches the agent SDK process during the warm phase",
	})
	requireAllContain(t, "testing.md", supervisedDocPage(t, root, "runtime-author-guide/testing.md"), []string{
		"the runtime supervisor sends the process SIGTERM and then SIGKILL 10 seconds later",
	})
	requireNoneContain(t, "publishing.md", supervisedDocPage(t, root, "runtime-author-guide/publishing.md"), []string{
		"the runtime process starts during the warm phase",
	})
	requireAllContain(t, "adapter-contract.md", supervisedDocPage(t, root, "reference/adapter-contract.md"), []string{
		"The first process of the agent container is the runtime supervisor",
		"whether the runtime can serve the next session, sampled after the scrub finishes",
		"**stderr** goes to the agent container's log",
		"An end during the pod's termination that does not follow the adapter's `shutdown` write",
		"A runtime that exits after `shutdown` within its `deadline_ms` fails no session, including during the pod's termination",
	})
	requireNoneContain(t, "adapter-contract.md", supervisedDocPage(t, root, "reference/adapter-contract.md"), []string{
		"An exit during the pod's termination also fails each session the process holds with `runtime_crash`",
	})
	stop := stopSessionRPCComment(t, readDocPage(t, filepath.Join(root, "docs", "api", "internal.md")))
	requireAllContain(t, "internal.md StopSession comment", stop, []string{
		"session_end",
		"On a keep pod the process stays alive for the pod's life unless DemoteSDK ends it",
		"on a restart pod the whole-pod scrub ends it",
	})
	requireNoneContainFold(t, "internal.md StopSession comment", stop, []string{"shutdown"})
}

// launchTriggerPages are the documentation pages that state when the runtime
// supervisor launches the runtime process.
var launchTriggerPages = []string{
	"runtime-author-guide/lifecycle.md",
	"runtime-author-guide/echo-runtime.md",
	"getting-started/concepts.md",
	"reference/state-machines.md",
}

// requireRuntimeSupervisorLaunchCondition fails when a page states the launch
// trigger as "no runtime process is live" rather than the specification's
// launch condition, when the **Launch.** paragraph omits the not-live
// condition or the refused start, or when the **Runtime exits.** table names
// the gateway as the terminator at the coordinator hold timeout.
//
// spec: 4.7.10 (Runtime supervisor: Launch, Liveness, Runtime exits), 10.1
// (Hold state timeout)
func requireRuntimeSupervisorLaunchCondition(t *testing.T, root string) {
	t.Helper()
	for _, page := range launchTriggerPages {
		requireNoneContainFold(t, page, supervisedDocPage(t, root, page), []string{
			"when no runtime process is live",
			"finds no live process",
		})
		requireAllContain(t, page, supervisedDocPage(t, root, page), []string{
			"the pod has launched no runtime process since its creation or since the platform last ended one",
		})
	}
	supervisor := supervisedDocSection(t, root, "runtime-author-guide/lifecycle.md", "Runtime Supervisor")
	requireAllContain(t, "lifecycle.md Runtime Supervisor", supervisor, []string{
		"and while the not-live state ([Liveness](#runtime-supervisor)) is clear",
		"A session that starts when no process is live and these conditions permit no launch, such as after your runtime process exits by itself, fails its start, and the pod retires",
		"| The adapter's close of the connection at the coordinator hold timeout | None fails with `runtime_crash`; the adapter terminates each one with `coordinator_lost` |",
	})
	requireNoneContain(t, "lifecycle.md Runtime Supervisor", supervisor, []string{
		"The gateway terminates each one",
	})
}

// stopSessionRPCComment returns the comment lines directly above the
// `rpc StopSession` declaration of a page's protobuf block, joined with each
// whitespace run collapsed.
func stopSessionRPCComment(t *testing.T, body string) string {
	t.Helper()
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "rpc StopSession(") {
			continue
		}
		var comment []string
		for j := i - 1; j >= 0 && strings.HasPrefix(strings.TrimSpace(lines[j]), "//"); j-- {
			comment = append([]string{strings.TrimPrefix(strings.TrimSpace(lines[j]), "//")}, comment...)
		}
		return strings.Join(strings.Fields(strings.Join(comment, " ")), " ")
	}
	t.Fatalf("no `rpc StopSession(` declaration found")
	return ""
}

// spec: 5.2 (Runtime-process lifetime, Deployer acknowledgment (runtime
// process kept across sessions)), 17.8.1, 24.2
// diagnosis: an operator page no longer documents `recycle.runtimeProcess`,
//
//	where `cleanupCommands` run on a supervised pod, the acknowledgment rule
//	keyed on `keep`, or the warm-pool-exhaustion step for a supervised pool
//	whose pods never reach idle, with its `lenny-ctl preflight supervisor`
//	invocation and its `controller.tuning` keys. An operator then cannot
//	diagnose a pool whose adapters exit before READY. The test also fails
//	when the execution-modes page limits the post-update refusal to `keep`
//	pods for an update that leaves the pool not reusing pods, which refuses
//	every pod that served an earlier session, or when the troubleshooting
//	remedy for the acknowledgment error omits that `restart` needs a
//	restartable runtime or that the non-reusing alternatives need
//	`recycle.runtimeProcess` removed, which pool admission otherwise rejects.
//	It also fails when the troubleshooting remedy or the execution-modes
//	decision guide offers `restart` without stating that pool admission
//	currently refuses it on every pool; that pin is removed together with the
//	pool-admission refusal of `restart` and the interim doc sentences.
func TestOperatorPagesStateTheRuntimeProcessLifetimeAndSupervisorRemedies(t *testing.T) {
	root := repoRoot(t)
	requireAllContain(t, "configuration.md", supervisedDocPage(t, root, "reference/configuration.md"), []string{
		"| `sessionPolicy.recycle.runtimeProcess` |",
		"the runtime supervisor runs them in the runtime image at the agent UID",
	})
	exec := supervisedDocSection(t, root, "reference/execution-modes.md", "Runtime process lifetime")
	requireAllContain(t, "execution-modes.md Runtime process lifetime", exec, []string{
		"**Runtime process kept across sessions.**",
		"A sequential pool under `restart` does not require it",
		"the gateway places no later session on any pod that served an earlier session, `restart` pods included",
		"After a sequential pool's change from `keep` to `restart`, the gateway places no later session on a `keep` pod that served an earlier session",
	})
	requireNoneContain(t, "execution-modes.md Runtime process lifetime", exec, []string{
		"takes effect at the next session: the gateway places no later session on a `keep` pod",
	})
	requireAllContain(t, "execution-modes.md", supervisedDocPage(t, root, "reference/execution-modes.md"), []string{
		"its runtime cannot serve the next session",
	})
	requireAllContain(t, "warm-pool-exhaustion.md", supervisedDocPage(t, root, "runbooks/warm-pool-exhaustion.md"), []string{
		"lenny-ctl preflight supervisor --runtime-class <runtime-class> --config <values.yaml>",
		"controller.tuning.superviseWaitTimeout",
		"controller.tuning.warmUpTimeout",
	})
	requireAllContain(t, "troubleshooting.md Recycling Pool Does Not Reuse Pods",
		supervisedDocSection(t, root, "operator-guide/troubleshooting.md", "Recycling Pool Does Not Reuse Pods"), []string{
			"`recycle.runtimeProcess: keep`",
			"also follows every other cause of the adapter's not-live state",
			"The `restart` value requires a restartable runtime, which carries `command`, runs in `session` mode, and does not declare `capabilities.preConnect: true`",
			"alternatives also require removing `recycle.runtimeProcess`, because pool admission rejects the field on a pool that does not reuse pods",
		})
	requireRestartAdmissionRefusalStated(t, root)
	requireAllContain(t, "lenny-ctl.md", supervisedDocPage(t, root, "operator-guide/lenny-ctl.md"), []string{
		"| `lenny-ctl preflight supervisor --runtime-class <name> --config <values.yaml>` |",
	})
	requireAllContain(t, "architecture.md", supervisedDocPage(t, root, "getting-started/architecture.md"), []string{
		"**Session logs:**",
	})
}

// restartAdmissionRefusal is the interim sentence an operator page states
// beside every remedy that offers `recycle.runtimeProcess: restart`, while
// pool admission refuses that value on every pool.
const restartAdmissionRefusal = "Pool admission currently refuses `recycle.runtimeProcess: restart` on every pool with `400 VALIDATION_ERROR`, so the remedies available today are `acknowledgeProcessLevelIsolation: true`, `recycle.maxSessionsPerPod: 1`, and `recycle.scrubProfile: vm-restart`."

// requireRestartAdmissionRefusalStated fails when the troubleshooting remedy
// for the acknowledgment error or the execution-modes decision guide offers
// `restart` without the interim refusal sentence.
//
// spec: 5.2 (Runtime-process lifetime (`recycle.runtimeProcess`))
func requireRestartAdmissionRefusalStated(t *testing.T, root string) {
	t.Helper()
	requireAllContain(t, "troubleshooting.md Recycling Pool Does Not Reuse Pods",
		supervisedDocSection(t, root, "operator-guide/troubleshooting.md", "Recycling Pool Does Not Reuse Pods"),
		[]string{restartAdmissionRefusal})
	requireAllContain(t, "execution-modes.md Decision guide",
		supervisedDocSection(t, root, "reference/execution-modes.md", "Decision guide"),
		[]string{restartAdmissionRefusal})
}

// spec: 5.2 (Runtime-process lifetime (`recycle.runtimeProcess`))
// diagnosis: a pool example in the specification or the documentation sets
//
//	`recycle.runtimeProcess` on a pool whose `recycle.enabled` is not true.
//	The field is accepted only on a reusing recycling pool, so the pool
//	controller rejects every other pool that sets it, and a deployer who
//	copies the example gets a pool that fails admission. An example that
//	shows the key on such a pool writes it commented out.
func TestRuntimeProcessKeySetOnlyOnRecyclingPoolExamples(t *testing.T) {
	root := repoRoot(t)
	keys := 0
	for _, dir := range []string{"spec", "docs"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			body := readDocPage(t, path)
			keys += strings.Count(body, "runtimeProcess:")
			for _, site := range runtimeProcessKeysOutsideEnabledRecycle(body) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s line %d: `runtimeProcess:` is set in a recycle block whose `enabled` is not true; comment the key out", rel, site)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if keys == 0 {
		t.Fatalf("no `runtimeProcess:` key found in spec/ or docs/; the pool examples moved and this check reads nothing")
	}
}

// runtimeProcessKeysOutsideEnabledRecycle returns the 1-based line numbers of
// each uncommented `runtimeProcess:` key whose sibling keys at the same
// indentation do not include `enabled: true`.
func runtimeProcessKeysOutsideEnabledRecycle(body string) []int {
	lines := strings.Split(body, "\n")
	var sites []int
	for i, line := range lines {
		key := strings.TrimLeft(line, " ")
		if !strings.HasPrefix(key, "runtimeProcess:") {
			continue
		}
		if siblingKeyValue(lines, i, len(line)-len(key), "enabled") != "true" {
			sites = append(sites, i+1)
		}
	}
	return sites
}

// siblingKeyValue returns the value, without its trailing comment, of the
// YAML key named key at the given indentation in the block that contains
// line i, or "" when the block has no such key. The block ends at the first
// non-blank line indented less than indent.
func siblingKeyValue(lines []string, i, indent int, key string) string {
	inBlock := func(l string) bool {
		trimmed := strings.TrimLeft(l, " ")
		return trimmed == "" || len(l)-len(trimmed) >= indent
	}
	start, end := i, i
	for start > 0 && inBlock(lines[start-1]) {
		start--
	}
	for end+1 < len(lines) && inBlock(lines[end+1]) {
		end++
	}
	for _, l := range lines[start : end+1] {
		trimmed := strings.TrimLeft(l, " ")
		if len(l)-len(trimmed) != indent || !strings.HasPrefix(trimmed, key+":") {
			continue
		}
		value, _, _ := strings.Cut(strings.TrimPrefix(trimmed, key+":"), "#")
		return strings.TrimSpace(value)
	}
	return ""
}

// spec: 4.7.11 (item 8 Runtime supervisor), 13.1
// diagnosis: the security page no longer states the runtime supervisor's
//
//	trust basis, that a runtime can kill or freeze its own pod's supervisor
//	where the namespace init is not immune, that a sent fault signal has no
//	effect, the property self-test and `lenny-ctl preflight supervisor`, the
//	supervisor image under cosign verification, or the process-limit note
//	that bounds a fork bomb on runc without user namespaces.
func TestSecurityPageStatesTheRuntimeSupervisorTrustBasis(t *testing.T) {
	root := repoRoot(t)
	requireAllContain(t, "security.md Runtime Supervisor",
		supervisedDocSection(t, root, "operator-guide/security.md", "Runtime Supervisor"), []string{
			"**Trust basis.**",
			"A runtime can kill or freeze its own pod's supervisor with SIGKILL or SIGSTOP",
			"is blocked and has no effect, while a fault in the supervisor itself ends it",
			"**Property self-test.**",
			"lenny-ctl preflight supervisor --runtime-class <name> --config <values.yaml>",
			"`controller.supervisorImage`",
			"`podPidsLimit`",
		})
}
