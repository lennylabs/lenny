// Assert a proposal split lost nothing.
//
// The split of a legacy single-file proposal into the eight role-scoped files
// is agent work: deciding which design paragraphs are spec-facing and which
// are not needs judgement. The guarantee that the split lost no content is
// not, and it stays here so that "migrated" cannot come to mean "an agent said
// it was fine".
//
// The check is a partition test. Every non-blank line of the original must
// appear in at least one of the new files. It does NOT check the reverse: the
// split legitimately adds headings, and the migrator is expected to add them.
//
// Usage:
//   check-proposal-split.mjs <legacy.md> <proposal-dir>
//   check-proposal-split.mjs <legacy.md> <proposal-dir> --json
//
// Exit 0 when nothing was lost, 1 when something was, 2 on a usage error.

import { readFileSync, existsSync, readdirSync, statSync } from "fs";
import { join, resolve, basename } from "path";

// A line the split may introduce without it being present in the original.
// Anything else that appears only in the output is fine and unchecked; this
// list exists only to keep the failure report readable.
const ADDED = [
  /^#{1,3}\s/, // a heading the new file needs
  /^---$/, // frontmatter fences
  /^\s*$/,
];

/** Normalise for comparison: trailing whitespace is not content. */
const norm = (l) => l.replace(/\s+$/, "");

export function checkSplit(legacyPath, dirPath) {
  if (!existsSync(legacyPath)) return { error: "no such file: " + legacyPath };
  if (!existsSync(dirPath) || !statSync(dirPath).isDirectory()) {
    return { error: "no such directory: " + dirPath };
  }
  const original = readFileSync(legacyPath, "utf8").split("\n").map(norm);

  const parts = readdirSync(dirPath)
    .filter((f) => f.endsWith(".md"))
    .filter((f) => resolve(join(dirPath, f)) !== resolve(legacyPath))
    .sort();
  if (parts.length === 0) return { error: "no .md files in " + dirPath };

  // A multiset, so a line the original carries twice must appear twice.
  const have = new Map();
  for (const f of parts) {
    for (const l of readFileSync(join(dirPath, f), "utf8").split("\n").map(norm)) {
      have.set(l, (have.get(l) || 0) + 1);
    }
  }

  const lost = [];
  for (let i = 0; i < original.length; i++) {
    const l = original[i];
    if (l.trim() === "") continue;
    const n = have.get(l) || 0;
    if (n > 0) {
      have.set(l, n - 1);
      continue;
    }
    lost.push({ line: i + 1, text: l.length > 160 ? l.slice(0, 157) + "..." : l });
  }

  return {
    legacy: basename(legacyPath),
    dir: basename(dirPath),
    parts,
    originalLines: original.filter((l) => l.trim() !== "").length,
    lost,
    ok: lost.length === 0,
  };
}

if (import.meta.url === "file://" + process.argv[1]) {
  const argv = process.argv.slice(2);
  const files = argv.filter((a) => !a.startsWith("--"));
  if (files.length !== 2) {
    process.stderr.write("usage: check-proposal-split.mjs <legacy.md> <proposal-dir> [--json]\n");
    process.exit(2);
  }
  const r = checkSplit(resolve(files[0]), resolve(files[1]));
  if (r.error) {
    process.stderr.write("check-proposal-split: " + r.error + "\n");
    process.exit(2);
  }
  if (argv.includes("--json")) {
    process.stdout.write(JSON.stringify(r, null, 2) + "\n");
  } else if (r.ok) {
    process.stdout.write(
      "split ok: all " + r.originalLines + " content line(s) of " + r.legacy +
        " are present across " + r.parts.length + " file(s)\n",
    );
  } else {
    process.stdout.write(
      r.lost.length + " line(s) of " + r.legacy + " appear in none of the split files:\n",
    );
    for (const l of r.lost.slice(0, 40)) {
      process.stdout.write("  " + String(l.line).padStart(5) + ": " + l.text + "\n");
    }
    if (r.lost.length > 40) process.stdout.write("  ... and " + (r.lost.length - 40) + " more\n");
  }
  process.exit(r.ok ? 0 : 1);
}

// ADDED is exported so a test can assert the allowlist rather than infer it.
export { ADDED };
