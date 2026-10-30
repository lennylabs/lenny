// Add or remove a residual change-graph coverage row.
//
// tests/registers/residual-change-graph-coverage.yaml needs a disposition row
// for every tracked file no change-graph glob covers, and .mjs and .sh are not
// among the exempt extensions, so every executable file added under .claude/
// needs one or the tier-0 residual gate fails. This keeps the rows sorted and
// correctly formatted rather than hand-editing YAML each time.
//
// The file this writes is also written by scripts/specshift/register.Render, so
// the parse and render here reproduce that writer rather than a reading of one
// sample of its output.
//
// Usage:
//   register-row.mjs --add <path> "<reason>"    refuses a path that does not
//                                               exist and a reason spanning
//                                               more than one line
//   register-row.mjs --remove <path>
//   register-row.mjs --check          list rows whose file no longer exists

import { readFileSync, writeFileSync, existsSync } from "fs";
import { resolve, dirname } from "path";
import { fileURLToPath } from "url";

const REPO = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
const REG = resolve(REPO, "tests/registers/residual-change-graph-coverage.yaml");

function parse() {
  let text;
  try {
    text = readFileSync(REG, "utf8");
  } catch (err) {
    // An unguarded read made an invocation with no arguments exit 1 with an
    // ENOENT stack out of node:fs, so the usage branch below was unreachable
    // whenever the register was absent. scripts/specshift/register.Read refuses
    // a missing register for the same reason rather than loading it as empty.
    process.stderr.write("cannot read " + REG + ": " + err.message + "\n");
    process.exit(2);
  }
  const lines = text.split("\n");
  const head = [];
  const rows = [];
  let i = 0;
  // register.Render writes `entries: []` for a class with no entries, so a scan
  // for the literal `entries:` ran off the end of that file and pushed
  // undefined into the header. The entries line itself is not kept: render()
  // re-derives it from the row count, as Render does.
  let entriesEmpty = null;
  while (i < lines.length) {
    const t = lines[i].trim();
    if (t === "entries:" || t === "entries: []") {
      entriesEmpty = t === "entries: []";
      i++;
      break;
    }
    head.push(lines[i++]);
  }
  if (entriesEmpty === null) {
    process.stderr.write(REG + ": carries no entries block\n");
    process.exit(2);
  }
  while (i < lines.length) {
    if (lines[i].trim() !== "- member: |-") {
      i++;
      continue;
    }
    // A row is bounded by indentation rather than by a line count. The previous
    // fixed six-line slice dropped every line after the first of a block
    // scalar, so a member written across two lines, which register's
    // validMemberText explicitly allows, came back from --add with its
    // continuation deleted, exit 0 and no warning.
    const start = i++;
    while (i < lines.length && lines[i].trim() !== "- member: |-" && /^ {4}\S|^ {6}/.test(lines[i])) i++;
    const block = lines.slice(start, i);
    const body = [];
    for (let j = 1; j < block.length && /^ {6}/.test(block[j]); j++) body.push(block[j].trim());
    // register.MemberKey folds the continuation join back into a space, so the
    // sort key and the --add/--remove match here are the key the Go side uses.
    rows.push({ member: body.join(" "), block });
  }
  return { head, rows };
}

function render({ head, rows }) {
  rows.sort((a, b) => (a.member < b.member ? -1 : a.member > b.member ? 1 : 0));
  // Same rule as register.Render's empty-class branch. Emitting the entries
  // line the file was read with left a bare `entries:` behind after --remove of
  // the last row, which register.Load refuses with "carries no entries block".
  const entriesLine = rows.length ? "entries:" : "entries: []";
  return head.concat([entriesLine], rows.flatMap((r) => r.block)).join("\n").replace(/\n*$/, "\n");
}

const argv = process.argv.slice(2);
const CMDS = new Set(["--add", "--remove", "--check"]);
if (!CMDS.has(argv[0])) {
  process.stderr.write("usage: register-row.mjs --add <path> <reason> | --remove <path> | --check\n");
  process.exit(2);
}
const { head, rows } = parse();

if (argv[0] === "--add") {
  const member = argv[1];
  const reason = argv[2];
  if (!member || !reason) {
    process.stderr.write('usage: register-row.mjs --add <path> "<reason>"\n');
    process.exit(2);
  }
  // --add of a path that does not exist exited 0 with "added" and the very next
  // --check printed the row it had just written as stale, so the tool wrote a
  // row its own gate rejects. The predicate is the one --check already uses.
  if (!existsSync(resolve(REPO, member))) {
    process.stderr.write("no such file: " + member + "\n");
    process.exit(2);
  }
  // register.ValidEntry refuses a reason carrying a line break with "a reason
  // is one line", so a row written with one is a document the loader refuses.
  if (/[\n\r]/.test(reason)) {
    process.stderr.write("a reason is one line: " + member + "\n");
    process.exit(2);
  }
  if (rows.some((r) => r.member === member)) {
    process.stdout.write("already present: " + member + "\n");
    process.exit(0);
  }
  rows.push({
    member,
    block: [
      "  - member: |-",
      "      " + member,
      "    class: change-graph-coverage",
      "    disposition: excluded",
      "    reason: |-",
      "      " + reason,
    ],
  });
  writeFileSync(REG, render({ head, rows }));
  process.stdout.write("added: " + member + "\n");
} else if (argv[0] === "--remove") {
  const member = argv[1];
  const before = rows.length;
  const kept = rows.filter((r) => r.member !== member);
  if (kept.length === before) {
    process.stdout.write("not present: " + member + "\n");
    process.exit(0);
  }
  writeFileSync(REG, render({ head, rows: kept }));
  process.stdout.write("removed: " + member + "\n");
} else if (argv[0] === "--check") {
  const stale = rows.filter((r) => !existsSync(resolve(REPO, r.member)));
  for (const r of stale) process.stdout.write("stale row (file is gone): " + r.member + "\n");
  process.exit(stale.length ? 1 : 0);
}
