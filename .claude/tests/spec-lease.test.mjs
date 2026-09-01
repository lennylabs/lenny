// Layer 4: the spec/ write lease, executed for real against a fixture tree.
//
// The hook tests in hook.test.sh drive the command line; these drive the
// exported decision functions directly, because what is pinned here is the
// verdict rather than any prose. Each case names the behaviour the lease
// claimed in its own header and did not hold.
//
// Run: node .claude/tests/spec-lease.test.mjs

import { mkdtempSync, mkdirSync, writeFileSync, existsSync, rmSync } from "fs";
import { tmpdir } from "os";
import { join } from "path";
import { suite } from "./harness.mjs";
import { decide, readLease, openLease, releaseLease } from "../tools/spec-lease.mjs";

const t = suite("spec-lease");
const TMP = mkdtempSync(join(tmpdir(), "lenny-lease-"));
process.on("exit", () => rmSync(TMP, { recursive: true, force: true }));

// The fixture mirrors hook.test.sh: proposals beside the lease file, plus one
// proposal deliberately outside that directory with the same Approved
// frontmatter, which is what a forged lease points at.
function approvedProposal(dir, stem) {
  const root = join(TMP, dir, stem);
  mkdirSync(root, { recursive: true });
  writeFileSync(
    join(root, stem + ".status.md"),
    "---\nproposal: " + stem + "\ntitle: Fixture\nkind: fix\nstatus: Approved\n" +
      "approved-date: 2026-08-31\napproved-by: alice\n---\n",
  );
  return root;
}

const APPROVED = approvedProposal("proposals", "0001_fix_approved");
const OUTSIDE = approvedProposal("outside", "0001_evil");
mkdirSync(join(TMP, "spec"), { recursive: true });
writeFileSync(join(TMP, "spec", "04_control-plane.md"), "");
const leasePath = join(TMP, "proposals", ".spec-lease.json");
const FOUR = "spec/04_control-plane.md";
const rmLease = () => rmSync(leasePath, { force: true });

t.section("L1. an empty allow list grants nothing");
{
  rmLease();
  openLease(APPROVED, { leasePath, step: "S1", allow: [] });
  for (const p of [FOUR, "spec/28_communication-channels.md", "spec/99_anything.md"]) {
    const d = decide(p, { leasePath });
    t.check(p + " is refused", d.allow === false, d.why);
  }
}

t.section("L2. a populated allow list grants exactly what it lists");
{
  rmLease();
  openLease(APPROVED, { leasePath, step: "S1", allow: [FOUR] });
  t.check("the listed file passes", decide(FOUR, { leasePath }).allow === true);
  const d = decide("spec/28_communication-channels.md", { leasePath });
  t.check("a sibling spec file is refused", d.allow === false, d.why);
}

t.section("L3. a proposal outside the lease's own directory is refused");
{
  rmLease();
  // Written by hand rather than through openLease, which is what a forger does.
  writeFileSync(
    leasePath,
    JSON.stringify({
      proposal: OUTSIDE,
      step: "S1",
      runId: "test",
      opened: "2026-08-31T00:00:00.000Z",
      expires: "2099-01-01T00:00:00.000Z",
      allow: [FOUR],
    }),
  );
  const d = decide(FOUR, { leasePath });
  t.check("an out-of-tree proposal does not open spec/", d.allow === false, d.why);

  let threw = false;
  try {
    openLease(OUTSIDE, { leasePath, step: "S1", allow: [FOUR] });
  } catch (e) {
    threw = true;
  }
  t.check("opening one throws", threw);
}

t.section("L4. a step-scoped release frees only its own step");
{
  rmLease();
  openLease(APPROVED, { leasePath, allow: [FOUR] });
  const r = releaseLease({ leasePath, step: "S9" });
  t.check("another step's release is refused", r.released === false, JSON.stringify(r));
  t.check("the lease file survives", existsSync(leasePath));
  t.check("it still grants its file", decide(FOUR, { leasePath }).allow === true);
  t.check("an unscoped release frees it", releaseLease({ leasePath }).released === true);
}

t.section("L5. an expired lease reads as present and not held");
{
  rmLease();
  openLease(APPROVED, { leasePath, step: "S1", allow: [FOUR], ttlHours: -100 });
  const r = readLease(leasePath);
  t.check("present", r.present === true);
  t.check("not held", r.held === false, JSON.stringify(r));
  t.check("expired", r.expired === true);
  const d = decide(FOUR, { leasePath });
  t.check("and it grants nothing", d.allow === false, d.why);
}

t.done();
