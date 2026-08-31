// Layer 2: the lazy per-proposal migrator.
//
// The real workflow body runs; only `agent` is stubbed. What matters here is
// what the script decides: whether to migrate at all, whether to trust the
// split, and whether to land a tree whose inbound references it could not fix.
//
// Run: node .claude/tests/migrate-proposal.test.mjs

import { runWorkflow, suite, labels, never, matching, firstIndex } from "./harness.mjs";

const t = suite("migrate-proposal");
const WF = ".claude/workflows/migrate-proposal.js";
const ARGS = {
  proposalPath: "proposals/0076_fix_scope-the-generation.md",
  repoRoot: "/repo",
  date: "2026-08-31",
};

// A stub table for the happy path; individual cases override one entry.
const ok = (over = {}) => ({
  assess: { state: "legacy", status: "Draft", title: "Scope the generation", kind: "fix" },
  split: { written: ["problem-statement", "summary", "status", "spec-changes"], notes: "" },
  "check-split": { ok: true, lost: [] },
  "retarget-refs": { sites: [{ file: "BUILD-GAPS.md", was: "a.md", now: "a/", why: "a finding points at it" }], unresolved: [] },
  "drop-legacy": "DONE",
  "commit-migration": "committed",
  ...over,
});

t.section("M1. it migrates a legacy proposal and lands it");
{
  const { result, calls } = await runWorkflow(WF, ARGS, ok());
  t.check("status migrated", result.status === "migrated", JSON.stringify(result).slice(0, 160));
  t.check("names the directory", result.dir === "proposals/0076_fix_scope-the-generation");
  t.check("reports all eight role files", (result.files || []).length === 8, String((result.files || []).length));
  t.check("the legacy file is dropped only after the check", firstIndex(calls, "drop-legacy") > firstIndex(calls, "check-split"));
  t.check("and only after references are retargeted", firstIndex(calls, "drop-legacy") > firstIndex(calls, "retarget-refs"));
  t.check("it commits", !never(calls, "commit-migration"));
}

t.section("M2. the split prompt names all eight roles and forbids rewriting");
{
  const { calls } = await runWorkflow(WF, ARGS, ok());
  const p = calls.find((c) => c.label === "split").prompt;
  for (const role of [
    "problem-statement", "summary", "status", "implementation-checklist",
    "spec-changes", "non-spec-changes", "review-log", "deviations",
  ]) {
    t.check("names ." + role + ".md", p.includes("." + role + ".md"));
  }
  t.check("calls it a relocation, not a rewrite", /RELOCATION, not a rewrite/.test(p));
  t.check("forbids rewording", /may not reword, summarise, merge, drop/.test(p));
  t.check("warns that a mechanical check follows", /mechanical check runs immediately after you/.test(p));
  t.check("does not let the split delete the source", /Do NOT delete the legacy file/.test(p));
  t.check("pins the status to what the tool read", /status must be\s+exactly Draft|status: Draft/.test(p));
}

t.section("M3. a failing partition check repairs, and never proceeds while lossy");
{
  let n = 0;
  const { result, calls } = await runWorkflow(WF, ARGS, ok({
    "check-split": () => (++n <= 2 ? { ok: false, lost: ["  42: the dropped line"] } : { ok: true, lost: [] }),
  }));
  t.check("it repaired rather than proceeding", matching(calls, "repair-split").length === 2, String(matching(calls, "repair-split").length));
  t.check("the repair prompt carries the exact lost lines", /42: the dropped line/.test(calls.find((c) => c.label.startsWith("repair-split")).prompt));
  t.check("and it lands once clean", result.status === "migrated");
}
{
  const { result, calls } = await runWorkflow(WF, ARGS, ok({
    "check-split": { ok: false, lost: ["  7: gone"] },
  }));
  t.check("a check that never clears stops the run", result.status === "lost-content", result.status);
  t.check("the legacy file is NOT dropped", never(calls, "drop-legacy"));
  t.check("nothing is committed", never(calls, "commit-migration"));
  t.check("the lost lines are reported", (result.lost || []).join().includes("7: gone"));
}

t.section("M4. an Implemented or Retired proposal is refused");
for (const s of ["Implemented", "Retired"]) {
  const { result, calls } = await runWorkflow(WF, ARGS, ok({
    assess: { state: "legacy", status: s, title: "T", kind: "fix" },
  }));
  t.check(s + " is refused", result.status === "refused-implemented", result.status);
  t.check(s + ": no split runs", never(calls, "split"));
  t.check(s + ": the legacy file survives", never(calls, "drop-legacy"));
}

t.section("M5. idempotence and resume");
{
  const { result, calls } = await runWorkflow(WF, ARGS, ok({
    assess: { state: "already-migrated", status: "Draft", title: "T", kind: "fix" },
  }));
  t.check("already-migrated returns at once", result.status === "already", result.status);
  t.check("no split agent runs", never(calls, "split"));
  t.check("only the assessment ran", labels(calls).filter((l) => l !== "assess").length === 0, labels(calls).join(","));
}
{
  const { calls } = await runWorkflow(WF, ARGS, ok({
    assess: { state: "partial", status: "Draft", title: "T", kind: "fix" },
  }));
  const p = calls.find((c) => c.label === "split").prompt;
  t.check("a partial split is resumed, not restarted", /PREVIOUS RUN DIED PART-WAY/.test(p));
  t.check("and is told not to duplicate what landed", /Do not duplicate content that already landed/.test(p));
}
{
  const { result } = await runWorkflow(WF, ARGS, ok({
    assess: { state: "absent", status: "Draft", title: "T", kind: "fix" },
  }));
  t.check("an absent proposal is reported, not invented", result.status === "absent");
}

t.section("M6. an unresolvable inbound reference stops the migration");
{
  const { result, calls } = await runWorkflow(WF, ARGS, ok({
    "retarget-refs": { sites: [], unresolved: ["tests/tier11_docs/x_test.go:12 reads the file but for what is unclear"] },
  }));
  t.check("the run stops", result.status === "unresolved-references", result.status);
  t.check("the legacy file is NOT dropped", never(calls, "drop-legacy"));
  t.check("nothing is committed", never(calls, "commit-migration"));
  t.check("the unresolved site is reported", (result.unresolved || []).join().includes("x_test.go"));
}
{
  const { calls } = await runWorkflow(WF, ARGS, ok());
  const p = calls.find((c) => c.label === "retarget-refs").prompt;
  t.check("it greps before editing", /git grep -n/.test(p));
  t.check("a prefix match is left alone", /path-PREFIX match .* needs no change/s.test(p));
  t.check("a content reader takes a role file", /reads staged spec text takes \.spec-changes\.md/.test(p));
  t.check("guessing is forbidden", /do not guess/.test(p));
}

t.section("M7. a dead agent never lands a partial migration");
for (const dead of ["assess", "split", "check-split", "retarget-refs"]) {
  const { result, calls } = await runWorkflow(WF, ARGS, ok({ [dead]: null }));
  t.check(dead + " dying stops the run", result.status !== "migrated", result.status);
  t.check(dead + " dying leaves the legacy file", never(calls, "drop-legacy"));
}

t.done();
