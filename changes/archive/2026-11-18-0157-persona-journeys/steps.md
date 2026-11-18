# Steps: 0157 Persona journey tier

## Validation owed

| Item | Waits on it |
| :-- | :-- |
| The cost and the stability of a journey run, measured over the pilot's three journeys and three trials each. Measured on 2026-11-18, below. The budget for later runs is open decision 1 in `decisions.md`. | Adding journeys beyond the pilot. |

### Pilot measurements

The pilot ran in two runs, under decision 12: run `20261118T165838Z-1ae96b` ran trial 1 of each journey from `main` at `a10620ef5`, and run `20261118T185934Z-0a42f2` ran trials 2 and 3. Costs are the agent cost Claude Code reported, from the ledger. Infrastructure cost was $0 in every trial, because the target is Kind on the owner's laptop. Wall time runs from the trial's `startedAt` to its `finishedAt` in `result.json`, and covers creating the cluster, installing Lenny for the `lenny` setup, the agent, the postconditions, and the teardown. The first run's per-trial records were lost, so its wall times are not known.

| Journey | Trial | Run | Verdict | Agent cost | Wall time | Agent time |
| :-- | :-- | :-- | :-- | --: | --: | --: |
| `operator-install` | 1 | `20261118T165838Z-1ae96b` | pass | $2.9556 | not recorded | not recorded |
| `operator-install` | 2 | `20261118T185934Z-0a42f2` | pass | $1.8076 | 54m47s | 53m03s |
| `operator-install` | 3 | `20261118T185934Z-0a42f2` | pass | $3.2799 | 41m54s | 39m40s |
| `tenant-admin-onboarding` | 1 | `20261118T165838Z-1ae96b` | pass | $0.4514 | not recorded | not recorded |
| `tenant-admin-onboarding` | 2 | `20261118T185934Z-0a42f2` | fail | $0.4895 | 4m45s | 56s |
| `tenant-admin-onboarding` | 3 | `20261118T185934Z-0a42f2` | fail | $0.4233 | 3m30s | 45s |
| `end-user-first-session` | 1 | `20261118T165838Z-1ae96b` | fail | $0.1381 | not recorded | not recorded |
| `end-user-first-session` | 2 | `20261118T185934Z-0a42f2` | fail | $0.1086 | 3m21s | 47s |
| `end-user-first-session` | 3 | `20261118T185934Z-0a42f2` | fail | $0.1406 | 4m14s | 1m54s |

Trials 2 and 3 are trials 1 and 2 of the second run's records.

| Journey | Cost per trial (mean, range) | Wall time per trial (mean of two) | Agreement across the three trials |
| :-- | :-- | :-- | :-- |
| `operator-install` | $2.68, $1.81 to $3.28 | 48m20s | Agree: three passes. |
| `tenant-admin-onboarding` | $0.45, $0.42 to $0.49 | 4m08s | Disagree: one pass, then two failures of the same postcondition, `carol-is-tenant-admin`, for the same cause (ADMIN-I1, with ADMIAM-I1 and CLIADM-I1). |
| `end-user-first-session` | $0.13, $0.11 to $0.14 | 3m48s | Agree: three failures of the same two postconditions, `session-reached-a-live-state` and `transcript-holds-echo-reply`, for the same cause (CLIINST-I1). |

- **Cost.** The nine trials cost $9.7946. The ledger stands at $9.8538 of the $54 pilot cap, which adds $0.0593 for the two probe runs of 2026-11-17. No trial approached the $6 per-trial cap; the costliest used 55% of it. The `end-user-first-session` trials stopped early on CLIINST-I1, so the cost of a passing trial of that journey is not yet measured.
- **Time.** A run of the three journeys at three trials each takes about 2 hours 50 minutes of wall time on this host, from the second run's mean wall times. `operator-install` takes about 85% of it. Its second trial ran for 53 minutes of the journey's 60-minute time limit. Outside the agent, a trial spent between 2m20s and 3m49s with the `lenny` setup and about 2 minutes with the `prerequisites` setup, on the cluster, the install, the postconditions, and the teardown.
- **Stability.** Every trial was decided: none hit the cost cap or the time limit, and none was inconclusive. In the second run, whose records survive, every agent exited 0 and every teardown was verified. Two journeys gave three agreeing trials, and `tenant-admin-onboarding` did not. No page shows how to grant `tenant-admin` (ADMIAM-I1), so each trial has to find the way or guess it. The two failing trials guessed the bootstrap seed format and wrote `role` for `roles`, which the bootstrap accepted silently (ADMIN-I1). The record of the passing trial was lost, so how it assigned the role is not known. Decision 2's condition, three agreeing trials of each journey, is not met while `tenant-admin-onboarding` disagrees.

## Steps

### 0157.S1 The journey tier and three pilot journeys
- status: Landed
- lanes: [code, documentation]
- depends-on: [0155.S1]

The scenario format, the `journeys` tier, the restricted toolset, the postconditions, the records, the review procedure, and pilot journeys for an operator, a tenant admin, and an end user.
