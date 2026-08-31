// Add or remove a residual change-graph coverage row.
//
// tests/registers/residual-change-graph-coverage.yaml needs a disposition row
// for every tracked file no change-graph glob covers, and .mjs and .sh are not
// among the exempt extensions, so every executable file added under .claude/
// needs one or the tier-0 residual gate fails. This keeps the rows sorted and
// correctly formatted rather than hand-editing YAML each time.
//
// Usage:
//   register-row.mjs --add <path> "<reason>"
//   register-row.mjs --remove <path>
//   register-row.mjs --check          list rows whose file no longer exists

import { readFileSync, writeFileSync, existsSync } from "fs";
import { resolve, dirname } from "path";
import { fileURLToPath } from "url";

const REPO = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
const REG = resolve(REPO, "tests/registers/residual-change-graph-coverage.yaml");

function parse() {
  const lines = readFileSync(REG, "utf8").split("\n");
  const head = [];
  const rows = [];
  let i = 0;
  while (i < lines.length && lines[i].trim() !== "entries:") head.push(lines[i++]);
  head.push(lines[i++]); // entries:
  while (i < lines.length) {
    if (lines[i].trim() === "- member: |-") {
      rows.push({ member: lines[i + 1].trim(), block: lines.slice(i, i + 6) });
      i += 6;
    } else {
      i++;
    }
  }
  return { head, rows };
}

function render({ head, rows }) {
  rows.sort((a, b) => (a.member < b.member ? -1 : a.member > b.member ? 1 : 0));
  return head.concat(rows.flatMap((r) => r.block)).join("\n").replace(/\n*$/, "\n");
}

const argv = process.argv.slice(2);
const { head, rows } = parse();

if (argv[0] === "--add") {
  const member = argv[1];
  const reason = argv[2];
  if (!member || !reason) {
    process.stderr.write('usage: register-row.mjs --add <path> "<reason>"\n');
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
} else {
  process.stderr.write("usage: register-row.mjs --add <path> <reason> | --remove <path> | --check\n");
  process.exit(2);
}
