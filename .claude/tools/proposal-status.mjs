// Read and write a proposal's status.
//
// The status used to be a prose bullet inside the proposal ("- **Status:**
// Approved for implementation as written (2026-08-14)."), which meant the
// security posture of spec/ turned on a regex over English. Sixteen distinct
// spellings are in proposals/ today. This tool is the only supported way to
// read or change it, and the spec-lease hook reads it through here.
//
// Usage:
//   proposal-status.mjs <proposal> --field status
//   proposal-status.mjs <proposal> --json
//   proposal-status.mjs <proposal> --set status=Approved --by alice --date 2026-09-02
//
// <proposal> is either a folder-layout directory (proposals/NNNN_kind_slug/)
// or a legacy single file (proposals/NNNN_kind_slug.md). Both are readable;
// only a folder-layout proposal is writable, because a legacy proposal has no
// .status.md to write and inventing one would leave two records that disagree.
//
// Exit codes: 0 read or wrote; 1 malformed or absent; 2 usage error.
//
// The frontmatter is flat rather than nested. The rework plan sketched nested
// inline maps (`drafted: { date: …, by: … }`); flat keys parse in ten lines
// without a YAML dependency, grep cleanly, and are editable by hand without
// getting the indentation wrong. Nothing downstream needs the nesting.

import { readFileSync, writeFileSync, existsSync, statSync, readdirSync } from "fs";
import { basename, join, resolve } from "path";

const STATES = ["Draft", "Reviewed", "Approved", "Implemented"];

// The state each recorded date belongs to, so --set stamps the right one.
const STAMP = { Draft: "drafted", Reviewed: "reviewed", Approved: "approved", Implemented: "implemented" };

// Library functions THROW; only the CLI exits. An earlier revision called
// process.exit here, which meant importing this module and passing a bad value
// terminated the caller instead of returning an error it could handle -- and
// the first thing that tripped over it was this tool's own test.
class StatusError extends Error {
  constructor(msg, code = 1) {
    super(msg);
    this.code = code;
  }
}
function die(msg, code = 1) {
  throw new StatusError(msg, code);
}

/**
 * Resolve a proposal reference to its layout and its status source.
 * Accepts the directory, the legacy file, or either without its extension.
 */
export function resolveProposal(ref) {
  const p = resolve(ref);
  if (existsSync(p) && statSync(p).isDirectory()) {
    const stem = basename(p);
    const status = join(p, stem + ".status.md");
    return { layout: "folder", dir: p, stem, statusFile: status };
  }
  if (existsSync(p) && statSync(p).isFile()) {
    return { layout: "legacy", file: p, stem: basename(p).replace(/\.md$/, ""), statusFile: p };
  }
  // A bare stem: prefer the directory, fall back to the file.
  if (existsSync(p + ".md")) {
    return { layout: "legacy", file: p + ".md", stem: basename(p), statusFile: p + ".md" };
  }
  return null;
}

/** Parse the flat frontmatter of a .status.md. Returns null when absent. */
export function parseFrontmatter(text) {
  const m = /^---\n([\s\S]*?)\n---\n?/.exec(text);
  if (!m) return null;
  const out = {};
  for (const line of m[1].split("\n")) {
    if (!line.trim() || /^\s*#/.test(line)) continue;
    const i = line.indexOf(":");
    if (i < 0) continue;
    out[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  return out;
}

/**
 * The legacy reader. A single-file proposal states its status in a prose
 * bullet, in any of the spellings the tree has accumulated. This maps that
 * prose onto the four states, and is the ONLY place that regex lives.
 *
 * "Applied to spec" maps to Approved rather than to a fifth state: the spec
 * edits of an approved proposal having landed is a fact about its progress,
 * which the implementation checklist records per deliverable, not a status.
 */
export function legacyStatus(text) {
  // Both bullet spellings occur in the tree: `- **Status:** Approved …` and
  // `- **Status: SUPERSEDED …**`, where the emphasis wraps the whole clause.
  const m = /^-\s+\*\*Status:(?:\*\*)?\s*(.+)$/m.exec(text);
  if (!m) return null;
  const raw = m[1].replace(/\*\*/g, "").trim();
  const low = raw.toLowerCase();
  // Retired is not one of the four states and cannot be written. It exists
  // because a superseded or withdrawn proposal is neither a draft nor
  // implemented, and calling it either would be a false record: the four
  // states describe a proposal moving toward implementation, and these are not
  // moving. Every consumer that gates on a state gates on Approved, so a
  // Retired proposal is refused by all of them, which is the correct outcome.
  if (/^superseded|^withdrawn/.test(low)) return { status: "Retired", raw };
  if (/^implemented/.test(low)) return { status: "Implemented", raw };
  if (/^applied to spec|^approved/.test(low)) return { status: "Approved", raw };
  if (/^verified/.test(low)) return { status: "Reviewed", raw };
  if (/^draft|^early draft/.test(low)) return { status: "Draft", raw };
  return { status: null, raw };
}

export function readStatus(ref) {
  const r = resolveProposal(ref);
  if (!r) return { error: "no such proposal: " + ref };
  const text = readFileSync(r.statusFile, "utf8");
  if (r.layout === "legacy") {
    const l = legacyStatus(text);
    if (!l) return { error: "legacy proposal has no Status bullet: " + r.statusFile };
    if (!l.status) return { error: "unrecognised legacy status: " + l.raw };
    return { layout: "legacy", stem: r.stem, status: l.status, raw: l.raw, file: r.statusFile };
  }
  const fm = parseFrontmatter(text);
  if (!fm) return { error: "no frontmatter in " + r.statusFile };
  if (!STATES.includes(fm.status)) {
    return { error: "status must be one of " + STATES.join(", ") + "; found " + JSON.stringify(fm.status) };
  }
  return { layout: "folder", stem: r.stem, ...fm, file: r.statusFile };
}

export function writeStatus(ref, updates) {
  const r = resolveProposal(ref);
  if (!r) die("no such proposal: " + ref);
  if (r.layout !== "folder") {
    die("a legacy single-file proposal has no .status.md to write; migrate it first");
  }
  const text = readFileSync(r.statusFile, "utf8");
  const fm = parseFrontmatter(text);
  if (!fm) die("no frontmatter in " + r.statusFile);
  const body = text.replace(/^---\n[\s\S]*?\n---\n?/, "");

  if (updates.status !== undefined) {
    if (!STATES.includes(updates.status)) {
      die("status must be one of " + STATES.join(", ") + "; refused " + JSON.stringify(updates.status), 2);
    }
    fm.status = updates.status;
    const stamp = STAMP[updates.status];
    if (updates.date) fm[stamp + "-date"] = updates.date;
    if (updates.by) fm[stamp + "-by"] = updates.by;
  }
  for (const [k, v] of Object.entries(updates.raw || {})) fm[k] = v;

  // Preserve key order: known keys first in their canonical order, then any
  // the file already carried, so a hand-added field is not silently dropped.
  const order = ["proposal", "title", "kind", "status"];
  for (const st of ["drafted", "reviewed", "approved", "implemented"]) {
    order.push(st + "-date", st + "-by");
  }
  const keys = order.filter((k) => k in fm).concat(Object.keys(fm).filter((k) => !order.includes(k)));
  const out = "---\n" + keys.map((k) => k + ": " + (fm[k] ?? "")).join("\n") + "\n---\n" + body;
  writeFileSync(r.statusFile, out);
  return { file: r.statusFile, status: fm.status };
}

/** Every proposal under a directory, folder-layout or legacy. */
export function listProposals(root) {
  return readdirSync(root)
    .filter((n) => /^\d{4}[_-]/.test(n))
    .sort()
    .map((n) => join(root, n));
}

// ---- CLI -----------------------------------------------------------------

function main() {
  const argv = process.argv.slice(2);
  const ref = argv.find((a) => !a.startsWith("--"));
  if (!ref) die("usage: proposal-status.mjs <proposal> [--field F | --json | --set k=v --by W --date D]", 2);

  const flag = (name) => {
    const i = argv.indexOf("--" + name);
    return i >= 0 ? argv[i + 1] : undefined;
  };

  const sets = argv.filter((a, i) => argv[i - 1] === "--set");
  if (sets.length > 0) {
    const updates = { raw: {} };
    for (const s of sets) {
      const [k, ...rest] = s.split("=");
      const v = rest.join("=");
      if (k === "status") updates.status = v;
      else updates.raw[k] = v;
    }
    updates.by = flag("by");
    updates.date = flag("date");
    const w = writeStatus(ref, updates);
    process.stdout.write(w.status + "\n");
    process.exit(0);
  }

  const r = readStatus(ref);
  if (r.error) die(r.error);
  if (argv.includes("--json")) {
    process.stdout.write(JSON.stringify(r, null, 2) + "\n");
  } else {
    const field = flag("field") || "status";
    if (!(field in r)) die("no such field: " + field);
    process.stdout.write(String(r[field]) + "\n");
  }
  process.exit(0);
}

if (import.meta.url === "file://" + process.argv[1]) {
  try {
    main();
  } catch (e) {
    process.stderr.write("proposal-status: " + e.message + "\n");
    process.exit(e.code || 1);
  }
}

export { StatusError };
