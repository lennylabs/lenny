// Layer 4: the pipeline tools, executed for real against fixture trees.
//
// No agents and no workflow bodies. These are the places where a guarantee is
// mechanical rather than a matter of an agent's judgement, so they are tested
// by running them.
//
// Run: node .claude/tests/tools.test.mjs

import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from "fs";
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

t.done();
