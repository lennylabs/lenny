# Test coverage

Project-wide rules for testing code changes. They apply to every change under `pkg/`, `cmd/`, `sdks/`, `migrations/`, and `charts/`, and to any agent or workflow that writes or modifies code in this repository.

## Top-level principle

Every code change ships with tests across all relevant tiers, not unit tests alone. Tests are first-class artifacts of the spec: each test cites the spec sections it exercises, and every behavioral spec section has at least one test. A change is not done until its relevant tiers run and pass; writing a test without running it does not count.

## The tier model

The suite runs in thirteen stages (`lenny-test --tier <name>`; `--max-tier <name>` runs 0 through that tier; `--changed` selects by git diff). Tier 7 has a local stage (7a) gating a Kind stage (7b); tier 12 is the cloud load tier.

| Tier | Name | Covers |
|:--|:--|:--|
| 0 | static | `go build`, `go vet`, `golangci-lint`, schema and codegen checks |
| 1 | unit | pure functions and single types, in-process |
| 2 | component | one component against a real kube-apiserver (envtest) or a single container |
| 3 | contract | wire contracts between components (proto, JSONL, HTTP) |
| 4 | integration | multi-service flows on the compose stack (Postgres, Redis, MinIO) |
| 5 | e2e (Kind) | full chart on Kind: pods, NetworkPolicy, admission, mTLS, lifecycle |
| 6 | e2e (cloud) | behaviors Kind cannot reproduce (gVisor nodes, Kata, managed LB, multi-zone) |
| 7 | load and SLO | concurrency, ordering, atomicity, error propagation (7a local, 7b Kind) |
| 8 | chaos | failure injection, partition, fail-open recovery |
| 9 | security | auth, isolation, network policy, credential handling |
| 10 | conformance | runtime adapter conformance battery |
| 11 | documentation | doc and runbook consistency (alert-to-runbook resolution, examples) |
| 12 | load and SLO (cloud) | sustained cloud-scale load |

## Which tiers a change must touch

Always run tier 0 and tier 1 on every touched package and its dependents. Add every higher tier whose surface the change touches:

- Pure function, type, or in-process logic: tier 1.
- Controller, reconciler, or anything reading or writing the kube-apiserver: tier 2 (envtest).
- A wire contract (a proto message, a JSONL frame, an HTTP request or response, a CRD schema): tier 3.
- A flow that crosses the gateway, a datastore, or another service: tier 4.
- A cluster behavior (pod lifecycle, NetworkPolicy, admission webhook, mTLS, drain): tier 5.
- Concurrency, ordering, atomicity, or rate behavior: tier 7a, and 7b when it needs real Kubernetes primitives.
- A failure or recovery path (datastore loss, partition, fail-open, leader failover): tier 8.
- Authentication, authorization, tenant isolation, egress, or credential delivery: tier 9.
- A runtime adapter contract: tier 10.
- Documentation, alert rules, or runbooks: tier 11.

A change usually touches several of these. Implement the tests for each tier the change reaches, not only the lowest one.

## Coverage target

New code carries at least 80% line coverage, measured by `go-cover` and enforced by CI (TESTING.md §12.1). The target is on new code; refactoring that does not change behavior is not penalized. Before declaring a change done, check the coverage of the lines you changed:

```
lenny-test coverage --diff <base-ref>
```

Coverage is a floor, not the goal. Cover the empty, error, concurrent, boundary, and spec-named-failure paths, not the happy path alone. A high coverage number over happy-path-only tests does not satisfy this rule.

## Test conventions

- Every test carries a `// spec:` annotation naming the spec sections it exercises (form: `// spec: 4.6.1 (warm pool controller), 12.3 (postgres ha)`). The harness maps tests to spec sections through this annotation.
- Every tier-2-and-higher test carries a `// diagnosis:` comment immediately above the function declaration, stating what a failure means. The harness surfaces it in the verdict.
- Name tests for the behavior and the spec section, not the function under test alone.

## How to run

- `lenny-test --changed --max-tier <tier>` runs every tier up to `<tier>` for the packages your diff touches; start here.
- `lenny-test --tier <name>` runs one tier; `--pkg <path>` scopes to a package.
- Bring infrastructure up with `lenny-test infra up --profile compose|kind|all` before integration and higher tiers; see the AVAILABLE INFRASTRUCTURE the build loops document for standing clusters and stacks.
- Check the environment before running any tier above 1, and read a failure there as an environment failure until you have ruled that out. The checks are in "Environment preflight" below.
- `lenny-test stress --test <Name> --runs <N>` exercises a flake budget for concurrency-sensitive tests.

## How to apply when editing

1. Identify every tier the change reaches from the table above before writing code.
2. Write the tests for those tiers alongside the code, covering the non-happy paths.
3. Run tier 0 and tier 1 plus each reached tier; fix the code until they pass.
4. Run `lenny-test coverage --diff <base-ref>` and raise coverage of changed lines to the target.
5. Add the `// spec:` annotation to every test and the `// diagnosis:` comment to tier-2-and-up tests.

## Environment preflight

A tier above 1 runs against a cluster or a compose stack whose images were built from some earlier revision
of this tree. When the two drift apart the tests fail for reasons that have nothing to do with the change
under test, and the failure looks exactly like a code defect: the container exits immediately, the pod never
reaches Ready, and every `kubectl wait` burns its full timeout. One run spent 1400 seconds waiting on pods
that had already failed 2,741 times because the pod spec rendered a flag the loaded image did not define.
Run these checks first, and when one fails, fix the environment rather than the code.

**1. The images match the renderer.** The pod spec is rendered from the current tree and the image is not.
Any argument the renderer passes that the image's binary does not define makes the container exit
immediately with status 2 from Go's flag package, and the first line of its log names the flag. Compare what
`pkg/controller/sandbox/podspec` renders against the flags the runtime and adapter binaries define, and
rebuild and reload when a flag was added, removed, or renamed since the images were built. A rename such as
`--workspace-root` to `--workspace-base` is the common case, because it changes both sides and neither fails
to compile.

**2. The cluster is not carrying wreckage from an earlier run.** Count the agent pods by phase before
starting:

```
kubectl -n lenny-agents get pods --no-headers | awk '{print $3}' | sort | uniq -c | sort -rn
```

Terminal pods are not harmless. A test that waits on a label selector waits on every pod the selector
matches, so a pile of `Error` pods makes each wait run to its timeout. Delete them with
`kubectl -n lenny-agents delete pods --field-selector=status.phase=Failed --wait=false`. An
`ImagePullBackOff` pod usually means a fixture pinned an image digest that was never pushed; delete the pool
that owns it, not only the pod, or the controller recreates it.

**3. The warm pools can actually reach Ready.** A pool whose `READY` column is zero while its desired count
is not will recreate a failing pod indefinitely, so the pile in check 2 rebuilds itself. Read the pool list
and, for any pool at zero ready, read one of its pods' logs before running anything.

**4. No orphaned test infrastructure is still running.** An envtest run killed part-way leaves its etcd and
kube-apiserver behind, holding memory and ports. Any such process reparented to init (`ppid` of 1) is
orphaned; reap them with `pkill -f kubebuilder-envtest` before starting, and again after each tier-2
package.

**5. The failure you are looking at is yours.** Before treating a tier failure above 1 as a defect in the
change, confirm the same tier fails on a clean checkout of the merge base. A failure that predates the
change is a pre-existing condition to record, not work for this step, and rebuilding a landed step to make
somebody else's broken fixture pass is how a step enters a loop it cannot leave.

When a check fails and fixing the environment is out of scope for the work in hand, record the precondition
and skip the affected case with a reason, in the register at `tests/registers/skip-reasons.yaml`. A test
skipped with a recorded reason is honest; one that fails for an environmental reason teaches everyone to
ignore red.

## Escape hatches

- A pure refactor with no behavior change does not need new tests and is not held to the new-code coverage target; the existing tests must still pass.
- Tier 6 and tier 12 require operator-provisioned cloud resources (managed Kubernetes, gVisor nodes, sustained load infrastructure). When those are unavailable locally, implement the tests, state the dependency, and run the tiers that do run locally; do not let the cloud tiers block the rest.
- Generated code (codegen output, mocks) is exercised through the code that uses it; it does not need its own tests.

## Maintenance

When a new tier or a new required-tier mapping surfaces, add it to the table or the selection list. Keep the mappings concrete so an author can decide which tiers a change reaches without re-reading TESTING.md.
