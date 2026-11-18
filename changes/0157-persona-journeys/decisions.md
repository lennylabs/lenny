# Decisions: 0157 Persona journey tier

## Open decisions

Raised on 2026-11-18 by the pilot of 0157.S1, under decision 3. The measurements are in `steps.md`, "Validation owed".

1. **Budget for later journey runs.** The pilot's nine trials cost $9.7946 in agent spend and nothing in infrastructure. The costliest trial was an `operator-install` trial at $3.2799, which is 55% of the $6 per-trial cap. Per trial, `operator-install` cost $1.81 to $3.28, `tenant-admin-onboarding` $0.42 to $0.49, and `end-user-first-session` $0.11 to $0.14. A run of the three journeys at three trials each takes about 2 hours 50 minutes of wall time on this host, of which `operator-install` takes about 85%. One `operator-install` trial ran for 53 minutes of its 60-minute time limit. The `end-user-first-session` trials stopped early on CLIINST-I1, so the cost of a passing trial of that journey is not yet measured.

   - (a) Keep the $6 per-trial cap (`--run-cap-usd`). Give each later run its own ledger with a cap of $25 (`--pilot <run> --pilot-cap-usd 25`) for the three pilot journeys at three trials each, which is about 2.5 times the pilot's measured nine-trial cost and leaves room for the ledger's pending charge of $6 before the last trial. A journey added later adds $18 (three trials at the $6 cap) to the run's cap until its own three trials are measured, and then twice its measured three-trial cost. Raise `operator-install`'s `timeLimit` from `60m` to `90m`, so a slow trial is decided rather than stopped at the time limit. At the measured cost, a weekly run costs about $42 a month; the cadence is set with the schedule.
   - (b) As (a), with the per-trial cap lowered to $4, about 1.2 times the costliest trial. A slower `operator-install` trial is then more likely to stop at the cap and be recorded as inconclusive.
   - (c) Keep the pilot's figures for every later run: $6 per trial and $54 per run, with no per-journey accounting and the time limits unchanged.

   **Recommendation: (a).** It keeps the per-trial cap that no pilot trial approached, bounds a run at about 2.5 times its measured cost, sizes a new journey's share from its own trials, and removes the one time limit the pilot came close to.

## Decisions

Settled by the owner on 2026-11-16, answering "ok" to each recommendation; the questions as framed in revision 1 are in `review-history.md`.

1. **What the agent reads.** Only `docs/` and what the persona would see: CLI help, API responses, and the user interface.
2. **What a failing journey blocks.** During the pilot, a failing journey opens an issue and blocks nothing. Once three trials of each journey agree consistently, a failing journey blocks its area's milestone exit.
3. **Budget.** The pilot runs at most nine journey runs (three journeys, three trials each), and the budget for later runs is set from its measurements.

Settled by the owner on 2026-11-17:

4. **How the journey agent runs.** Claude Code in headless mode (`claude -p`) on the owner's Claude login, with a restricted tool list (a shell limited to `kubectl`, `helm`, and `lenny-ctl`, and read access to `docs/` alone), using Claude Sonnet 5.5 (`claude-sonnet-5-5`).
5. **Spending cap.** A dollar cap: $6 per run and $54 for the pilot's nine runs, about two million tokens per run at Claude Sonnet 5.5's rates ($2 per million input tokens, $10 per million output tokens). The harness stops a run when the cost Claude Code reports reaches the cap and records the run as inconclusive.
6. **Cluster.** A dedicated Kind cluster, `lenny-journeys`, created for each run and deleted afterwards; journeys never use the shared `lenny-e2e` cluster.
7. **Runtime inside Lenny.** The echo reference runtime to start, so Lenny makes no LLM calls in the pilot. In the owner's words: "the echo reference runtime may not be enough for all tests. I'm ok using that to start with, but as we add more journeys we may need additional test runtimes". A journey that needs behavior the echo runtime lacks adds a test runtime in the change that adds the journey.
8. **The pilot journeys.** An operator installs Lenny with Helm and configures a pool; a tenant admin sets up a tenant, a user, and a credential; an end user creates a session, sends a message, and reads the result, each following only the documentation.
9. **Records.** Transcripts and verdicts go under `tests/results/journeys/`, which is not committed, and each run's summary goes into the step's digest.

Settled by the owner on 2026-11-17, on questions the review of 0157.S1 raised:

10. **Isolation of the journey agent.** The agent runs in a container that holds only the `claude` CLI, `kubectl`, `helm`, `lenny-ctl`, a read-only copy of the journey's documentation, and the trial cluster's read-only internal kubeconfig, attached to the Kind cluster's network, with no host home directory, Docker socket, host kubeconfig, or cloud credentials. It authenticates with a long-lived token on the owner's subscription from `claude setup-token`, which the harness reads from a file and never records. A later cloud target supplies the network and the credentials its scenario names to the same container.
11. **End-user identity in the pilot.** The end-user journey uses the platform-admin token, because the development install cannot issue a user token. The scenario and `TESTING.md` state this limit, and an issue tracks issuing user tokens in the development install.

Settled by the owner on 2026-11-18, on the order of the pilot's trials; the owner's question and the explanation the answer followed are in `review-history.md`:

12. **Order of the pilot's trials.** The pilot runs one trial of each journey first, then the other two trials of each journey in a second run. Decisions 2 and 3 stand: each journey gets three trials, decision 2's agreement is read over all three, and decision 3's budget is set from all nine. In the owner's words: "1 now, 2 more later".

Owner constraint recorded on 2026-11-17: later journeys will deploy to AWS, GCP, and Azure. The harness keeps cluster provisioning behind an interface with Kind as the only target, records infrastructure cost on its own ledger line, verifies teardown on every exit path, and passes credentials per scenario.

