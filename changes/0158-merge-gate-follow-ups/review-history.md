# Review history: 0158 Merge gate follow-ups

- 2026-11-17, revision 1. Created.
- 2026-11-17, revision 1. Created to hold the owner's answers to the questions the security review of 0154 raised, and the work two of them need.
- 2026-11-17, revision 1. The owner settled the isolation question: the gate keeps the host socket and documents it (decision 4). Status `Decided`.
- 2026-11-17, build. 0158.S1 built, reviewed, and merged; gate run `a9c87256d92ccb1982a87c52`. Three questions the build raised are open decisions 1 to 3; the change stays `In progress` until the owner answers them. [`digests/0158.S1.md`](digests/0158.S1.md) records the result.
- 2026-11-17, revision 2. The owner settled the three questions 0158.S1 raised (decisions 5 to 7), which step 0158.S2 carries out. The questions as framed:

  **Open decisions of revision 1**

  Raised by the build and review of 0158.S1:

  1. **gitleaks's default global allowlist.** Extending the default rules also brings in a default allowlist that skips any path containing `gitleaks.toml` or under `node_modules/`, images, lockfiles, and `go.sum`, and carries a loose pattern, `(?i)^true|false|null$`, that lets through any value containing "false". Options: (a) copy the default rules into `.gitleaks.toml` with a trimmed allowlist; (b) keep extending the defaults, and fail the check when a tracked path matches those skipped path patterns; (c) accept as is. Recommendation: (b).
  2. **Wider review rule.** Scripts, `tests/static/`, `tests/registers/`, the packages `lenny-test` imports, and `go.mod` and `go.sum` can also weaken checks, or could put a fake `gitleaks` on the path through an `init()` function. Options: (a) add them to the reviewed paths; (b) keep the four paths. Recommendation: (a) for `tests/static/`, `tests/registers/`, and `go.mod`/`go.sum`, which are few and decisive; the broad sets (every script, every imported package) would make most changes reviewable and dilute the rule.
  3. **Archives and binary files.** Tracked archives and binary files are not scanned, and the upload fixtures include archive bombs. Options: (a) a reviewed list of tracked binary files, which the scan checks against so a new one fails until listed; (b) leave them unscanned. Recommendation: (a).
- 2026-11-17, review of 0158.S2 (general and security). Verified: every file `.gitleaks.toml` allowlists was skipped whole, because gitleaks applies a global allowlist's paths without its AND condition (from 0158.S1); text files under skipped paths, archive and binary classification, symlinks, and version drift left tracked content unscanned. Fixes need no decision. Two questions went to the owner and were settled as decisions 8 and 9.
