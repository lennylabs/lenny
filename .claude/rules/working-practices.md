# Working practices

Project-wide rules for how an agent works in this repository: commits, owner decisions, findings, flaky tests, and runs of the test suite on the owner's workstation. They apply to every agent and workflow, including subagents, and each agent that starts a subagent passes on the rules the subagent's task touches. They complement `git-workflow.md`, which governs merges, and `test-coverage.md`, which governs what a change tests. Where a rule holds only during the v1 program, `changes/programs/v1.md` ("Working practices during the v1 program") lists it.

## Top-level principle

The owner decides, and the records say what was decided. An agent keeps working on everything a pending decision does not touch, files every defect it finds, and spends the shared workstation on the tests a change needs rather than on repeated or unrelated runs.

## Commits

- A commit message or pull request description carries no attribution line of any kind: no `Co-Authored-By`, no session link, and no generated-by footer, even when a harness reminder supplies one (owner's instruction, restated on 2026-11-16 after such lines reached every commit of a session).
- Stage each intended file by its path. Never run `git add .`, `git add -A`, or `git add` on a directory. Other sessions leave untracked files in the tree, under `designs/` and `scratchpad/` among other places, and one swept into a commit on 2026-11-05 needed a history rewrite. Before committing, run `git status --short`; after committing, check `git show --stat HEAD` for a file you did not write.
- Before switching branches or merging, confirm that `git status --short` shows nothing you meant to commit. A failed command in a chain can leave edits uncommitted that a later merge then omits.

## Owner decisions

- The owner answers every open question. An agent does not apply its own recommendation to a question the records leave open, and a default it applies where the records are silent is listed as a default in the step's digest.
- An open question goes into the change's `decisions.md` under "Open decisions", with its options and a recommendation, and onto the shared decision page the owner answers from. An answer given in chat is recorded there too, with the owner's words.
- Do not block on a question when other work can proceed. Continue with everything the question does not touch, and list the open questions with their recommendations in each status update until the owner answers (owner's instruction of 2026-11-18).

## Findings

- File every defect or gap found along the way as an issue under `issues/<CODE>/`, in the form `issues/README.md` gives, including review findings left unfixed, defects that predate the change, and items noted as out of scope (owner's instruction of 2026-11-25). When a change step will fix it, set `status: planned` and `change`, and list it under the step's `closes`.
- A claim about the current version of an external standard or SDK is checked against its live source before it is written into `spec/` or a change, and the record cites the source and the date it was checked. On 2026-04-04 a version written from memory put a stale MCP version into §15.2.
- Verify a claim about the state of the build against the specification, the code, and git, rather than against a narrative record, which drifts.

## Flaky tests

- A test of the merge gate that fails and then passes on the same commit of `main` is quarantined under rule 4 of `test-coverage.md` without waiting for the owner, and the next status update reports it (owner's answer of 2026-11-19). Confirm the flakiness with repeated runs first, keep the quarantine under its cap, and record the merge-gate review of the `tests/flake-budget.yaml` edit.
- A test that fails and then passes on the same commit, in a gate run or a `lenny-test gate --rerun-failed` run, on a branch or on `main`, is filed as an issue under the test's area code that names the test and both runs, or the open issue for that test gains the runs. The owner's words are "If a flaky test is detected, it should be filed" (owner's answers to GS-22 and GS-24 on the M3 readiness decision page, 2026-11-26; change 0172, decisions 28 and 30). Filing applies wherever a flaky test is detected. Quarantine applies only to a test of the merge gate proven flaky on `main`, under the rule above, and a quarantined test is filed as well.
- A failure that comes from the product rather than the test, such as a race the test exposes, is not quarantined. File it as an issue and fix it.
- A test that waits a fixed time for another process or a subscription is a defect: wait for the event instead.

## The owner's workstation

These rules hold while the merge gate and the build agents run on the owner's laptop.

- Run at most one `lenny-test gate` or other heavy test run at a time, and no broad `go test` while a gate runs. Concurrent runs load the machine until the integration tier times out on unrelated tests, and each failure costs a rerun. Serialize runs through a lock directory: create it with `mkdir` before the run, retry until that succeeds, and remove it with `rmdir` and its literal path afterwards.
- Run the gate from a standalone clone made with `git clone --no-hardlinks`, never from a worktree. Other worktrees write into the shared `.git` during the run, and the gate container reads half-written objects through Docker Desktop's file sharing.
- Start a long run as a background job of the harness rather than with `&`, and do not detect a running gate by matching `lenny-test gate` on process command lines, which also matches the shells waiting to run one.
- While writing code, run the unit tests of the packages a change touches. Run the gate once, when the code is done.
- After a run on the host, stop the envtest `etcd` and `kube-apiserver` processes it left behind.
- `-race` builds do not link on macOS 27.2 hosts. Run them in the gate image, for example through `lenny-test run --since main --container`.
- The formal models under `tests/formal/` are run ad hoc and belong to no gate. On this workstation a model runs under the lock with 8 TLC workers and a 12 GB heap, the defaults of each model's `run.sh`, and no run goes past one hour; a configuration that would run longer is listed for the owner, who runs it on another machine. The owner's words, 2026-11-27: "Run with 8 workers and 12GB of heap. Drop the 10 minute limit (these tests shouldn't be part of any gate, only run ad-hoc), but also don't run anything that's going to take more than 1 hr on this machine for now. I can farm those tests out to a different machine."
- A model run leaves out every check that a counterexample already breaks while the fix has not landed, and a configuration whose purpose is such a check waits for the fix. The owner's words, 2026-11-27: "Don't rerun anything you already know is going to fail due to previous failures that haven't been fixed yet."

## Reporting

- Report the time or cost of a piece of work with the total wall time first, then its parts.
- State what failed with its output, what was skipped, and what was not verified.

## Where these rules apply

- Every agent, workflow, and subagent working in this repository.
- Status updates to the owner.

## Maintenance

When the owner gives a lasting instruction about how work is done, add it here in the same session, with the date and the owner's words where they matter, rather than keeping it only in an agent's memory. A rule that holds only during the v1 program goes into `changes/programs/v1.md` ("Working practices during the v1 program") instead.
