// The proposal-edit audit's pathspec, checked against a real git repository.
//
// The audit exists to report edits to the proposal's authored text. Under the
// folder layout the proposal is a directory, and the run itself writes two
// files inside it (the implementation checklist and the deviations file), so a
// bare directory pathspec reports the run's own bookkeeping as the forbidden
// edit. These cases assert the BEHAVIOUR of the pathspec the workflow builds
// rather than its wording: the command is lifted out of the audit prompt and
// executed against a scratch repository.
//
// Run: node .claude/tests/proposal-edit-audit.test.mjs

import { execFileSync, execSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runWorkflow, suite } from "./harness.mjs";

const t = suite("proposal-edit audit");
const WF = ".claude/workflows/implement-proposal-build.js";
const STEP = {
  id: "S1", title: "the counter", work: "emit it", targets: ["pkg/adapter"],
  tiers: ["unit"], specRefs: ["16.1"], checklistStep: "S1", dependsOn: [],
};

const base = (over = {}) => ({
  "checklist-ticks": { ticked: [] },
  "compile:*": { compiles: true, errors: [] },
  "lease-check:*": { leaseHeld: false },
  "build:*": { implemented: true, testsPassed: true, tiersRun: ["unit"], commit: "c1", filesChanged: ["pkg/a.go"], testsAddedOrModified: [] },
  "review:*": { findings: [] },
  "verify:*": { green: true, tiersRun: ["unit"], failures: [] },
  "tick:*": "DONE",
  "compile-guard:*": { clean: true, compiles: true },
  "proposal-edit-audit": { edited: false, commits: [] },
  default: {},
  ...over,
});

const git = (cwd, ...a) => execFileSync("git", a, { cwd, encoding: "utf8" }).trim();

/** A scratch repo with one commit carrying `files`; returns its path and HEAD. */
function scratchRepo(files) {
  const dir = mkdtempSync(join(tmpdir(), "audit-"));
  git(dir, "init", "-q", "-b", "main");
  git(dir, "config", "user.email", "t@example.com");
  git(dir, "config", "user.name", "t");
  write(dir, files);
  git(dir, "add", "-A");
  git(dir, "commit", "-q", "-m", "A");
  return { dir, base: git(dir, "rev-parse", "HEAD") };
}

function write(dir, files) {
  for (const [rel, body] of Object.entries(files)) {
    const abs = join(dir, rel);
    mkdirSync(abs.replace(/\/[^/]*$/, ""), { recursive: true });
    writeFileSync(abs, body);
  }
}

function commit(dir, files, msg) {
  write(dir, files);
  execFileSync("git", ["add", "-A"], { cwd: dir });
  execFileSync("git", ["commit", "-q", "-m", msg], { cwd: dir });
  return git(dir, "rev-parse", "HEAD");
}

/** Run the workflow against `dir` and return the audit prompt it built. */
async function auditPrompt(dir, proposalPath, baseSha) {
  const { calls } = await runWorkflow(
    WF,
    { proposalPath, repoRoot: dir, date: "2026-08-31", plan: { blastRadius: [], steps: [STEP] } },
    base({ "baseline": { sha: baseSha }, "build:S1:base": { sha: baseSha } }),
  );
  const call = calls.find((c) => c.label === "proposal-edit-audit");
  if (!call) throw new Error("the audit never ran");
  return call.prompt;
}

const logCmd = (prompt) => prompt.match(/`(git log --oneline [^`]+)`/)[1];
const showCmd = (prompt, sha) => prompt.match(/`(git show <sha> -- [^`]+)`/)[1].replace("<sha>", sha);

/** Execute a command the prompt names, through a real shell. Throws on error. */
const sh = (dir, cmd) => execSync(cmd, { cwd: dir, shell: "/bin/bash", encoding: "utf8" });

const DIR = "proposals/0081_fix_x";
const F = (role) => DIR + "/0081_fix_x." + role + ".md";
const SEED = {
  "pkg/a.go": "package a\n",
  [F("implementation-checklist")]: "- [ ] S1 the counter\n",
  [F("deviations")]: "# Deviations\n",
  [F("summary")]: "The counter is emitted by the adapter.\n",
};

t.section("C14. the run's own checklist and deviations writes are not reported as edits");
{
  const { dir, base: baseSha } = scratchRepo(SEED);
  const sweep = commit(
    dir,
    {
      "pkg/a.go": "package a\n\nfunc A() {}\n",
      [F("implementation-checklist")]: "- [x] S1 the counter\n",
      [F("deviations")]: "# Deviations\n\n- S1: the tier ran narrower than planned.\n",
    },
    "Mark step S1 done in proposal 0081",
  );
  const prompt = await auditPrompt(dir, DIR, baseSha);
  let out = null;
  let threw = null;
  try {
    out = sh(dir, logCmd(prompt));
  } catch (e) {
    threw = e;
  }
  t.check("the log command runs in a real shell", threw === null, threw && String(threw.message).split("\n")[0]);
  t.check("a commit sweeping only the tick and the deviation is not reported", out !== null && out.trim() === "", JSON.stringify(out));
  let showOut = null;
  try {
    showOut = sh(dir, showCmd(prompt, sweep) + " --stat");
  } catch (e) {
    showOut = "THREW: " + e.message;
  }
  t.check("and its diff names no file under the proposal directory", !showOut.includes(DIR), showOut);
}

t.section("an edit to the proposal's authored text is still reported");
{
  const { dir, base: baseSha } = scratchRepo(SEED);
  commit(dir, { [F("implementation-checklist")]: "- [x] S1 the counter\n" }, "tick S1");
  const real = commit(dir, { [F("summary")]: "The counter is emitted by the gateway.\n" }, "Reword the summary");
  const prompt = await auditPrompt(dir, DIR, baseSha);
  const out = sh(dir, logCmd(prompt)).trim().split("\n").filter(Boolean);
  t.check("exactly the summary edit is reported", out.length === 1 && out[0].startsWith(real.slice(0, 7)), JSON.stringify(out));
}

t.section("a legacy single-file proposal is not blinded by the exclusion");
{
  const { dir, base: baseSha } = scratchRepo({
    "pkg/a.go": "package a\n",
    "proposals/0081_fix_x.md": "# 0081\n\n- [ ] S1 the counter\n",
  });
  const edit = commit(dir, { "proposals/0081_fix_x.md": "# 0081\n\n- [x] S1 the counter\n" }, "tick S1");
  const prompt = await auditPrompt(dir, "proposals/0081_fix_x.md", baseSha);
  t.check("the legacy pathspec carries no exclusion", !prompt.includes(":(exclude)"), prompt.slice(0, 0));
  const out = sh(dir, logCmd(prompt)).trim().split("\n").filter(Boolean);
  t.check("the edit is still reported", out.length === 1 && out[0].startsWith(edit.slice(0, 7)), JSON.stringify(out));
  t.check("and the prompt tells the auditor the tick may be the run's own", /own bookkeeping/.test(prompt));
}

t.done();
