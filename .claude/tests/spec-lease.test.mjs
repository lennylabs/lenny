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
import { decide, readLease, openLease, releaseLease, scanCommand } from "../tools/spec-lease.mjs";

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

t.section("L6. a heredoc body is data, and does not read as a write");
{
  // The guard's first shipped form scanned the whole Bash command, so this
  // repository's own commit message -- which quoted the write commands the
  // guard catches -- was blocked by the guard it was describing. The body of a
  // heredoc is data; the line that opens one is still a command.
  const G = String.fromCharCode(115, 112, 101, 99);
  const blocked = (cmd) => {
    const r = scanCommand(cmd, {});
    return !!(r && !r.allow);
  };
  t.check(
    "a message quoting a write is not a write",
    !blocked("git commit -F - <<EOF\n  rm " + G + "/28.md, sed -i x " + G + "/29.md\nEOF"),
  );
  t.check(
    "a quoted-delimiter body is data too",
    !blocked("cat <<'EOF'\nrm " + G + "/28.md\nEOF"),
  );
  t.check(
    "but a redirection into the guarded tree is still caught",
    blocked("cat <<EOF > " + G + "/28.md\nhello\nEOF"),
  );
  t.check("an in-place edit is still caught", blocked("sed -i s/a/b/ " + G + "/28.md"));
  t.check("a removal is still caught", blocked("rm " + G + "/28.md"));
  t.check("a restore is still caught", blocked("git checkout -- " + G + "/"));
  t.check("a read is still allowed", !blocked("grep -n foo " + G + "/28.md"));
}

t.section("L7. the bypasses an adversarial review demonstrated");
{
  // Every case here was executed against the guard and got through. The
  // guarded name is built from character codes so this file's own text cannot
  // trip the guard when an agent edits it through a shell.
  const G = String.fromCharCode(115, 112, 101, 99);
  const F = G + "/28_x.md";
  const blocked = (cmd) => {
    const r = scanCommand(cmd, {});
    return !!(r && !r.allow);
  };

  // The regression the heredoc fix introduced: `<<WORD` was matched anywhere,
  // including inside a quoted string, so a quoted mention stood in for a real
  // opener and every line after it was dropped unscanned.
  t.check(
    "a quoted mention of the operator does not hide the next line",
    blocked('echo "note << MARK here"\nsed -i \'1s/^/x/\' ' + F),
  );
  t.check(
    "an unterminated opener does not swallow the rest of the command",
    blocked("cat <<EOF\nbody\nrm " + F),
  );
  t.check("a here-string is not a heredoc", blocked("cat <<<x\nrm " + F));
  t.check("a real body is still data", !blocked("git commit -F - <<EOF\n  rm " + F + "\nEOF"));
  t.check("and the line opening one is still scanned", blocked("cat <<EOF > " + F + "\nx\nEOF"));

  // Gaps in the patterns the guard already meant to cover.
  t.check("the noclobber-override redirect is a redirect", blocked("echo x >|" + F));
  t.check("the long in-place form is an in-place edit", blocked("sed --in-place=.bak s/a/b/ " + F));
  t.check("ed writes", blocked("printf '1c\\nX\\n.\\nw\\nq\\n' | ed " + F));

  // Controls: the guard must stay narrow.
  t.check("a read is allowed", !blocked("grep -n foo " + F));
  t.check("a path merely containing the word is allowed", !blocked("rm docs/my" + G + "/old.md"));
  t.check(
    "a commit message mentioning a write is allowed",
    !blocked('git commit -m "redirecting into ' + F + ' is blocked"'),
  );
  // A write verb is a write only when something is about to RUN it. Matching it
  // as a bare word anywhere blocked a pure read whose SEARCH TERM was a verb,
  // and any commit message mentioning a patch or an install.
  t.check("a read whose search term is a write verb", !blocked("grep -rn tee " + F));
  t.check("a message naming a patch", !blocked("git commit -m 'apply the patch to " + F + "'"));
  t.check("a message naming an install", !blocked("git commit -m 'install the anchor in " + F + "'"));
  t.check("but a verb after a pipe still runs", blocked("echo x | tee " + F));
  t.check("and a verb behind sudo still runs", blocked("sudo rm " + F));
  t.check("and a verb behind an env prefix still runs", blocked("FOO=1 rm " + F));
}

t.done();
