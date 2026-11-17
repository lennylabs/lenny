# Session isolation design: supervisor prototype

This folder holds the prototype runtime supervisor that `0145-session-isolation`, `0153-workspace-materialization`, and `0131-incremental-checkpoint` measure and cite. It is a record of throwaway spike code, not product code. The product supervisor stays in `supervisor/`, and an implementation proposal ports these changes there with its own tests.

## Contents

- `supervisor-prototype/`: the full source tree of the final prototype, 4,915 lines of C and headers excluding `test/`. It is the repository's `supervisor/` (3,585 lines at the start) plus four patches built during spikes on 2026-11-12:
  - the file-descriptor broker: the `fs` request with named roots and `statat`;
  - the `setup` request carrying descriptors (`fdin.c`), used by `lenny-materialize`;
  - the asynchronous setup and cleanup worker;
  - three rounds of fixes from an independent security review. The final review found nothing at Medium severity or above.
- `supervisor-prototype-vs-tree.diff`: the unified diff of `supervisor-prototype/` against the repository's `supervisor/` as of this commit, with `test/` excluded.

## How it was built and tested

- The tree builds freestanding with `-Werror` (`make` in `supervisor-prototype/`), on arm64 (gcc 14) and amd64.
- It ran under runc and gVisor `release-20261109.0` in local kind clusters, and under runc, gVisor, and Kata 4.3.1 on AWS `m8i.2xlarge`.
- The async worker suite passed 101 of 101 checks on runc and on gVisor.
- The C unit harness in `test/` passes 89 cases. The same 6 fail as on the unpatched base, because the harness ran as root.

The measured results are recorded in the designs' validation records. The spike harnesses, logs, and AWS scripts are not kept here.

## Line counts

| Stage | Source lines (no `test/`) |
| :-- | --: |
| Repository `supervisor/` | 3,585 |
| With the broker and the setup-with-descriptors patch | about 4,364 |
| Final prototype (this folder) | 4,915 |

The async worker and its review fixes added 487 code lines and removed 111, net +376, excluding comments and blank lines.
