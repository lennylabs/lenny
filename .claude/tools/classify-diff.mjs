// Decide, deterministically, whether a diff is comment-only, doc-only, or
// test-only.
//
// The change-class classifier decides which test tiers a fix warrants, and its
// output is what NOT to run. These three classes carry the most risk if they
// are wrong -- comment-only skips everything above tier 0 -- and all three are
// mechanically decidable, so they do not go to an agent at all. Its verdict is
// AUTHORITATIVE where it fires: if this says a diff is not comment-only, no
// agent may classify it as comment-only, whatever the fixer reported.
//
// That closes the failure mode this exists for: a fixer adjusts one line of
// logic while editing the comment above it, self-reports "updated a comment",
// and every test that would have caught it is skipped.
//
// Everything else needs judgement and stays with the agent.
//
// Usage:
//   classify-diff.mjs <range>            e.g. HEAD~1..HEAD
//   classify-diff.mjs --diff-file <path> a saved unified diff, for testing
//   [--repo <root>] [--json]
//
// Prints JSON: { classes: [...], files: [...], addedRemoved: n, reason: "..." }
// Exit 0 always when it could read a diff; 2 on a usage or read failure.

import { readFileSync } from "fs";
import { execFileSync } from "child_process";
import { resolve, dirname } from "path";
import { fileURLToPath } from "url";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_DEFAULT = resolve(HERE, "..", "..");

const CODE_EXT = [".go", ".mjs", ".js", ".ts", ".py", ".sh", ".proto", ".sql"];
const DOC_EXT = [".md", ".txt", ".rst"];
const DATA_EXT = [".json", ".yaml", ".yml", ".toml"];

const isTestPath = (p) => /(^|\/)tests\//.test(p) || /_test\.go$/.test(p) || /\.test\.(mjs|js|ts)$/.test(p) || /\.test\.sh$/.test(p);
const isRunbookish = (p) => /docs\/runbooks\//.test(p) || /docs\/reference\/metrics\.md$/.test(p) || /alert/i.test(p);
const ext = (p) => {
  const i = p.lastIndexOf(".");
  return i < 0 ? "" : p.slice(i).toLowerCase();
};

/**
 * Is every added and removed line in a Go-style file a comment or blank?
 *
 * Block comments need a small state machine, and the machine is run per file
 * over the ADDED lines and the REMOVED lines separately, because a hunk can
 * open a block comment in one and close it in the other.
 */
function onlyComments(lines) {
  let inBlock = false;
  for (const raw of lines) {
    const l = raw.trim();
    if (l === "") continue;
    let i = 0;
    let sawCode = false;
    while (i < l.length) {
      if (inBlock) {
        const close = l.indexOf("*/", i);
        if (close < 0) {
          i = l.length;
        } else {
          inBlock = false;
          i = close + 2;
        }
        continue;
      }
      const rest = l.slice(i);
      const ws = rest.match(/^\s+/);
      if (ws) {
        i += ws[0].length;
        continue;
      }
      if (rest.startsWith("//")) {
        i = l.length;
        continue;
      }
      if (rest.startsWith("/*")) {
        inBlock = true;
        i += 2;
        continue;
      }
      sawCode = true;
      break;
    }
    if (sawCode) return false;
  }
  return true;
}

export function classify(diffText) {
  const files = [];
  const perFile = new Map();
  let current = null;
  let added = 0;
  const note = (name) => {
    current = name;
    if (current !== "/dev/null" && !files.includes(current)) {
      files.push(current);
      perFile.set(current, { add: [], del: [] });
    }
  };
  // A "---" or "+++" line is a file header only where a header can stand. Past
  // the header block every line carries a +/-/space marker, so removing the SQL
  // comment "-- drop the old index" arrives here as "--- drop the old index".
  // Deciding by prefix rather than by position dropped that line from the
  // removed set: a migrations/*.sql diff deleting only "--" lines classified as
  // comment-only with addedRemoved 0, and comment-only skips every tier above
  // 0. The body is entered at the "+++" header as well as at "@@", because a
  // diff assembled without hunk headers would otherwise present an empty
  // add/del set, which onlyComments answers true for and which lands on the
  // same wrong verdict.
  let inBody = false;
  for (const line of diffText.split("\n")) {
    if (line.startsWith("diff --git ")) {
      inBody = false;
      const m = /^diff --git a\/\S+ b\/(.+)$/.exec(line);
      if (m) note(m[1].trim());
      continue;
    }
    if (!inBody) {
      if (line.startsWith("+++ ")) {
        const m = /^\+\+\+ b\/(.+)$/.exec(line);
        if (m) note(m[1].trim());
        inBody = true;
      } else if (line.startsWith("@@")) {
        inBody = true;
      }
      continue;
    }
    if (line.startsWith("@@")) continue;
    if (!current || !perFile.has(current)) continue;
    if (line.startsWith("+")) {
      perFile.get(current).add.push(line.slice(1));
      added++;
    } else if (line.startsWith("-")) {
      perFile.get(current).del.push(line.slice(1));
      added++;
    }
  }

  const classes = [];
  const reasons = [];

  if (files.length === 0) {
    return { classes: ["empty"], files, addedRemoved: 0, reason: "the diff changes no file" };
  }

  // doc-only: every changed file is prose.
  if (files.every((f) => DOC_EXT.includes(ext(f)))) {
    classes.push(files.some(isRunbookish) ? "doc-only-operator" : "doc-only");
    reasons.push("every changed file is prose");
  }

  // test-only: every changed file is a test.
  if (files.every(isTestPath)) {
    classes.push("test-only");
    reasons.push("every changed file is a test");
  }

  // comment-only: every changed file is code, and every added and removed line
  // in it is a comment or blank.
  if (
    files.every((f) => CODE_EXT.includes(ext(f))) &&
    files.every((f) => {
      const p = perFile.get(f);
      return onlyComments(p.add) && onlyComments(p.del);
    })
  ) {
    classes.push("comment-only");
    reasons.push("every added and removed line in every changed code file is a comment or blank");
  }

  if (classes.length === 0) {
    reasons.push(
      "the diff is none of comment-only, doc-only or test-only; an agent classifies it",
    );
  }
  return { classes, files, addedRemoved: added, reason: reasons.join("; ") };
}

if (import.meta.url === "file://" + process.argv[1]) {
  const argv = process.argv.slice(2);
  const flag = (n) => {
    const i = argv.indexOf("--" + n);
    return i >= 0 ? argv[i + 1] : undefined;
  };
  const repo = flag("repo") || REPO_DEFAULT;
  const diffFile = flag("diff-file");
  const range = argv.find((a) => !a.startsWith("--") && argv[argv.indexOf(a) - 1] !== "--repo" && argv[argv.indexOf(a) - 1] !== "--diff-file");

  let text;
  try {
    text = diffFile
      ? readFileSync(resolve(diffFile), "utf8")
      : execFileSync("git", ["diff", "--no-color", "-U0", range || "HEAD~1..HEAD"], {
          cwd: repo,
          encoding: "utf8",
          maxBuffer: 64 * 1024 * 1024,
        });
  } catch (e) {
    process.stderr.write("classify-diff: cannot read the diff: " + e.message + "\n");
    process.exit(2);
  }
  const r = classify(text);
  process.stdout.write(JSON.stringify(r) + "\n");
  process.exit(0);
}
