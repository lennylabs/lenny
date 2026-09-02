// Layer 4: the pipeline tools, executed for real against fixture trees.
//
// No agents and no workflow bodies. These are the places where a guarantee is
// mechanical rather than a matter of an agent's judgement, so they are tested
// by running them.
//
// Run: node .claude/tests/tools.test.mjs

import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, copyFileSync } from "fs";
import { spawnSync } from "child_process";
import { tmpdir } from "os";
import { join } from "path";
import { suite, REPO } from "./harness.mjs";
import { readStatus, writeStatus, legacyStatus, parseFrontmatter } from "../tools/proposal-status.mjs";
import { checkSplit } from "../tools/check-proposal-split.mjs";

const t = suite("tools");
const TMP = mkdtempSync(join(tmpdir(), "lenny-tools-"));
process.on("exit", () => rmSync(TMP, { recursive: true, force: true }));

const write = (p, s) => {
  mkdirSync(join(TMP, p, ".."), { recursive: true });
  writeFileSync(join(TMP, p), s);
  return join(TMP, p);
};

function folderProposal(stem, status, extra = "") {
  mkdirSync(join(TMP, "proposals", stem), { recursive: true });
  write(
    "proposals/" + stem + "/" + stem + ".status.md",
    "---\nproposal: " + stem + "\ntitle: Fixture\nkind: fix\nstatus: " + status +
      "\ndrafted-date: 2026-08-01\ndrafted-by: change-proposal\n" + extra + "---\n\nBody prose.\n",
  );
  return join(TMP, "proposals", stem);
}

// ---- T1..T2: proposal-status ---------------------------------------------

t.section("T1. the four states read, and a fifth is refused");
for (const s of ["Draft", "Reviewed", "Approved", "Implemented"]) {
  const p = folderProposal("0100_fix_" + s.toLowerCase(), s);
  t.check(s + " reads back", readStatus(p).status === s);
}
{
  const p = folderProposal("0101_fix_bogus", "Shipped");
  const r = readStatus(p);
  t.check("an unknown state is an error, not a value", !!r.error, JSON.stringify(r));
}

t.section("T2. --set rewrites one field and preserves the rest");
{
  const p = folderProposal("0102_fix_setme", "Reviewed");
  writeStatus(p, { status: "Approved", by: "alice", date: "2026-09-02" });
  const after = readStatus(p);
  t.check("status changed", after.status === "Approved");
  t.check("the stamp for that state landed", after["approved-by"] === "alice" && after["approved-date"] === "2026-09-02");
  t.check("an earlier state's stamp survives", after["drafted-by"] === "change-proposal");
  t.check("the body survives", /Body prose\./.test(readFileSync(after.file, "utf8")));
  let threw = false;
  try {
    writeStatus(p, { status: "Shipped" });
  } catch (e) {
    threw = true;
  }
  t.check("an invalid state is refused rather than written", threw || readStatus(p).status === "Approved");
}

t.section("T2b. the legacy prose reader covers every spelling in the tree");
{
  const cases = [
    ["- **Status:** Approved for implementation as written (2026-08-14).", "Approved"],
    ["- **Status:** **Applied to spec (2026-08-03).** Approved (2026-08-02).", "Approved"],
    ["- **Status: SUPERSEDED (2026-07-27). Do not implement.** Its spec edits...", "Retired"],
    ["- **Status:** **WITHDRAWN (2026-07-30).**", "Retired"],
    ["- **Status:** Implemented green (2026-08-16), independently verified.", "Implemented"],
    ["- **Status:** Verified (2026-08-01). Converged after 9 rounds; awaiting sign-off.", "Reviewed"],
    ["- **Status:** Draft for review.", "Draft"],
  ];
  for (const [line, want] of cases) {
    const got = legacyStatus("# P\n\n" + line + "\n");
    t.check(want + " <- " + line.slice(0, 44), got && got.status === want, got && got.status);
  }
  t.check("Retired is not writable: it is not one of the four", !["Draft", "Reviewed", "Approved", "Implemented"].includes("Retired"));
}

t.section("T2c. every proposal in the real tree parses");
{
  const { listProposals } = await import("../tools/proposal-status.mjs");
  const all = listProposals(join(REPO, "proposals"));
  const bad = all.map((p) => [p, readStatus(p)]).filter(([, r]) => r.error);
  t.check(all.length + " proposal(s) all parse", bad.length === 0, bad.map(([p]) => p).join(", "));
}

// ---- T3..T5: check-proposal-split ----------------------------------------

const LEGACY = [
  "# Proposal: fixture",
  "",
  "- **Status:** Draft for review.",
  "",
  "## 1. Problem",
  "The gateway drops the frame.",
  "",
  "## 2. Proposed spec changes",
  "Replace the row beginning `slotId`.",
  "",
  "## 3. Testing",
  "Add a tier-3 contract test.",
  "",
].join("\n");

function splitTree(stem, parts) {
  const dir = join(TMP, "proposals", stem);
  mkdirSync(dir, { recursive: true });
  for (const [name, body] of Object.entries(parts)) {
    writeFileSync(join(dir, stem + "." + name + ".md"), body);
  }
  return dir;
}

t.section("T3. a faithful split passes");
{
  const legacy = write("legacy/0200.md", LEGACY);
  const dir = splitTree("0200_fix_ok", {
    "problem-statement": "# Problem: fixture\n\n## Statement\nThe gateway drops the frame.\n",
    "status": "---\nstatus: Draft\n---\n\n- **Status:** Draft for review.\n",
    "spec-changes": "# Spec changes\n\n## Staged edits\nReplace the row beginning `slotId`.\n",
    "non-spec-changes": "# Non-spec changes\n\n## Testing\nAdd a tier-3 contract test.\n",
    "summary": "# Proposal: fixture\n\n## 1. Problem\n\n## 2. Proposed spec changes\n\n## 3. Testing\n",
  });
  const r = checkSplit(legacy, dir);
  t.check("ok", r.ok, JSON.stringify(r.lost));
}

t.section("T4. a lossy split fails and names the lines");
{
  const legacy = write("legacy/0201.md", LEGACY);
  const dir = splitTree("0201_fix_lossy", {
    "problem-statement": "# Problem: fixture\n\n## Statement\nThe gateway drops the frame.\n",
    "spec-changes": "# Spec changes\n",
    "summary": "# Proposal: fixture\n\n## 1. Problem\n\n## 2. Proposed spec changes\n\n## 3. Testing\n",
  });
  const r = checkSplit(legacy, dir);
  t.check("not ok", !r.ok);
  t.check("names the dropped staged edit", r.lost.some((l) => /slotId/.test(l.text)), JSON.stringify(r.lost));
  t.check("names the dropped test line", r.lost.some((l) => /tier-3 contract test/.test(l.text)));
  t.check("reports a line number", r.lost.every((l) => Number.isInteger(l.line) && l.line > 0));
}

t.section("T5. added headings and whitespace are tolerated");
{
  const legacy = write("legacy/0202.md", "## A\ncontent line\n\n\n   \n");
  const dir = splitTree("0202_fix_ws", {
    "spec-changes": "# A brand new heading the split adds\n\n## A\ncontent line   \n\n",
  });
  const r = checkSplit(legacy, dir);
  t.check("trailing whitespace is not content", r.ok, JSON.stringify(r.lost));
}

t.section("T5b. a duplicated line must appear as many times as it did");
{
  const legacy = write("legacy/0203.md", "repeated\nrepeated\n");
  const dir = splitTree("0203_fix_dup", { "spec-changes": "repeated\n" });
  const r = checkSplit(legacy, dir);
  t.check("one copy does not satisfy two", !r.ok, JSON.stringify(r));
}

// ---- T6: register-row ----------------------------------------------------
//
// The tool is run for real as a subprocess against a fixture tree. It resolves
// its repository root from import.meta.url, so a copy of it at
// <case>/.claude/tools/register-row.mjs reads <case>/tests/registers/... and no
// production flag or environment override is needed to redirect it.

const REG_REL = "tests/registers/residual-change-graph-coverage.yaml";
const REG_HEAD =
  "# SPDX-License-Identifier: MIT\n" +
  "kind: residual-register\nversion: 1\nclass: change-graph-coverage\n";

function row(member, reason) {
  return (
    "  - member: |-\n" +
    member.split("\n").map((l) => "      " + l + "\n").join("") +
    "    class: change-graph-coverage\n    disposition: excluded\n    reason: |-\n" +
    reason.split("\n").map((l) => "      " + l + "\n").join("")
  );
}

let caseNo = 0;
function tree(registerText) {
  const dir = join(TMP, "reg-case-" + ++caseNo);
  mkdirSync(join(dir, ".claude", "tools"), { recursive: true });
  copyFileSync(join(REPO, ".claude/tools/register-row.mjs"), join(dir, ".claude/tools/register-row.mjs"));
  if (registerText !== null) {
    mkdirSync(join(dir, "tests", "registers"), { recursive: true });
    writeFileSync(join(dir, REG_REL), registerText);
  }
  return dir;
}

const run = (dir, ...args) =>
  spawnSync(process.execPath, [join(dir, ".claude/tools/register-row.mjs"), ...args], { encoding: "utf8" });

t.section("T6a. a member written across two lines survives a round trip");
{
  const dir = tree(REG_HEAD + "entries:\n" + row("a/one.mjs\nb/two.mjs", "wrapped member") + row("z/last.mjs", "plain"));
  const r = run(dir, "--add", ".claude/tools/register-row.mjs", "the tool itself");
  t.check("added", r.status === 0, r.stdout + r.stderr);
  const out = readFileSync(join(dir, REG_REL), "utf8");
  t.check("the first body line survives", out.includes("      a/one.mjs\n"));
  t.check("the continuation line survives", out.includes("      b/two.mjs\n"), out);
  t.check("the whole row round-trips", out.includes(row("a/one.mjs\nb/two.mjs", "wrapped member")), out);
}

t.section("T6b. a reason body is not silently eaten");
{
  const dir = tree(REG_HEAD + "entries:\n" + row("a/one.mjs", "first line of reason\nsecond line of reason"));
  const r = run(dir, "--add", ".claude/tools/register-row.mjs", "the tool itself");
  t.check("added", r.status === 0, r.stdout + r.stderr);
  const out = readFileSync(join(dir, REG_REL), "utf8");
  t.check("the second reason line survives", out.includes("      second line of reason\n"), out);
}

t.section("T6c. a register the canonical writer emits for an empty class");
{
  const dir = tree(REG_HEAD + "entries: []\n");
  const r = run(dir, "--add", ".claude/tools/register-row.mjs", "the tool itself");
  t.check("added", r.status === 0, r.stdout + r.stderr);
  const out = readFileSync(join(dir, REG_REL), "utf8");
  const lines = out.split("\n");
  t.check("the empty-list form is gone", !out.includes("entries: []"), out);
  t.check("exactly one entries line at column 0", lines.filter((l) => l === "entries:").length === 1, out);
  const at = lines.indexOf("entries:");
  t.check("the first row follows immediately", lines[at + 1] === "  - member: |-", JSON.stringify(lines.slice(at, at + 3)));
  t.check(
    "nothing sits at document root after it",
    lines.slice(at + 1).every((l) => l === "" || /^ /.test(l)),
    out,
  );
}

t.section("T6d. removing the last row leaves the empty list, not a bare key");
{
  const dir = tree(REG_HEAD + "entries:\n" + row("a/one.mjs", "only row"));
  const r = run(dir, "--remove", "a/one.mjs");
  t.check("removed", r.status === 0, r.stdout + r.stderr);
  const out = readFileSync(join(dir, REG_REL), "utf8");
  t.check("the entries line declares an empty list", /^entries: \[\]$/m.test(out), out);
  t.check("no bare entries key survives", !/^entries:$/m.test(out), out);
}

t.section("T6e. a missing register prints usage or names the file, without a stack");
{
  const dir = tree(null);
  const bare = run(dir);
  t.check("no arguments exits 2", bare.status === 2, String(bare.status) + bare.stderr);
  t.check("no arguments prints usage", bare.stderr.startsWith("usage:"), bare.stderr);
  t.check("no arguments raises no stack", !/ENOENT|at readFileSync/.test(bare.stderr), bare.stderr);
  const chk = run(dir, "--check");
  t.check("--check exits 2", chk.status === 2, String(chk.status) + chk.stderr);
  t.check("--check names the register", chk.stderr.includes(REG_REL), chk.stderr);
  t.check("--check raises no stack", !/\n\s+at |node:fs/.test(chk.stderr), chk.stderr);
}

t.section("T6f. --add refuses what --check would call stale");
{
  const before = REG_HEAD + "entries:\n" + row("a/one.mjs", "only row");
  const dir = tree(before);
  const r = run(dir, "--add", ".claude/tools/ghost.mjs", "r");
  t.check("exits 2", r.status === 2, String(r.status) + r.stdout + r.stderr);
  t.check("names the missing file", r.stderr.includes("no such file"), r.stderr);
  t.check("the register is untouched", readFileSync(join(dir, REG_REL), "utf8") === before);
}

t.section("T6g. --add of a path that exists is followed by a clean --check");
{
  const dir = tree(REG_HEAD + "entries: []\n");
  const add = run(dir, "--add", ".claude/tools/register-row.mjs", "the tool itself");
  t.check("added", add.status === 0, add.stdout + add.stderr);
  const chk = run(dir, "--check");
  t.check("--check is clean", chk.status === 0, chk.stdout + chk.stderr);
  t.check("--check reports nothing", chk.stdout === "", chk.stdout);
}

t.section("T6h. a no-op --add over the live register is byte-stable");
{
  const live = readFileSync(join(REPO, REG_REL), "utf8");
  const dir = tree(live);
  const r = run(dir, "--add", ".claude/tools/register-row.mjs", "a reason that must not be written");
  t.check("already present", r.stdout.startsWith("already present:"), r.stdout + r.stderr);
  t.check("the live rows are unperturbed", readFileSync(join(dir, REG_REL), "utf8") === live);
}

t.done();
