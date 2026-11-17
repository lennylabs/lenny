# Decisions: 0157 Persona journey tier

## Open decisions

None.

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

Owner constraint recorded on 2026-11-17: later journeys will deploy to AWS, GCP, and Azure. The harness keeps cluster provisioning behind an interface with Kind as the only target, records infrastructure cost on its own ledger line, verifies teardown on every exit path, and passes credentials per scenario.

