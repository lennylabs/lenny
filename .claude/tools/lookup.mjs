#!/usr/bin/env node
// Batch lookup over the working tree: exact terms, meaning, and sections, in one call.
//
// A reviewer checking a proposal against the spec asks the same two things over
// and over: "where does this identifier appear?" and "does anything else state
// this rule in other words?". Asked one grep and one sed at a time, each lookup
// is a model turn that re-reads the reviewer's whole context. This tool takes a
// list of lookups, runs them all, and returns each hit with the paragraph it sits
// in and the heading above it, so the follow-up read is already in the answer.
//
// It reads the working tree it is run in and keeps no index, so two worktrees at
// different commits each get their own answers with nothing to keep in sync.
//
// Usage:
//   lookup.mjs --json '<request>'        or        lookup.mjs < request.json
//   lookup.mjs --warm [scope ...]        embed every passage in the scopes (default: spec)
//   lookup.mjs --prune-cache <days>      drop cached vectors no lookup has used for <days> days
//
// Request (every field optional except that one of exact, meaning, sections, lines, related is given):
//   {
//     "scope":    ["spec"],             // any of: spec docs pkg cmd sdks charts schemas tests proposals
//     "paths":    ["spec/07_session-lifecycle.md"],   // narrows the scope to these files or directories
//     "exact":    ["maxSessionRetries", {"pattern": "idle[- ]terminat", "regex": true, "ignoreCase": true}],
//     "meaning":  [{"query": "an idle session is terminated", "context": "checking for restatements"}],
//     "sections": ["7.2", "28.5.3"],    // spec sections to return whole, by number
//     "lines":    ["spec/07_session-lifecycle.md:430-446"],   // line ranges to return verbatim
//     "related":  [{"text": "the passage being changed", "change": "what the edit does",
//                   "after": "the passage's new wording", "at": "spec/07_session-lifecycle.md:430",
//                   "min": 0.7}],       // passages that state or rely on what the change alters,
//                                       // judged by Jev. Pass the old passage as text, a one-line
//                                       // description as change, and the new wording as after.
//                                       // "also" takes extra queries (a string or a list). "via"
//                                       // picks candidate sources: meaning, also, sentidx, chunks
//                                       // (default), and links, terms, sentences, clauses, pairs,
//                                       // neighbors, all (measurement only)
//     "context":  "what the caller is trying to establish",   // narrows which exact hits print in full
//     "maxChars": 24000,                // output budget, capped at 26000
//     "exhaustive": true,               // meaning: judge every passage in scope. Default: only when the
//                                       // scope holds 3,000 passages or fewer; false forces a shortlist
//     "format": "text"                  // "json" returns every judged candidate, for tuning
//   }
//
// Exact search is complete: every hit is listed by location even when its text is
// not printed. Meaning search is a ranking and makes no completeness claim; the
// output says so. Printed text is verbatim from the file.
//
// Meaning search shortlists passages two ways, by word overlap and by embedding
// similarity, and judges the union. The embedding half runs lookup-embed.py through
// uv and caches vectors under the git common directory; without uv it is skipped
// and the output says so. `lookup.mjs --warm spec docs` embeds a scope ahead of
// time, which a workflow should do once before reviewers fan out.
//
// The embedding cache is shared by every worktree and every concurrent lookup on
// the machine. Operator-tunable environment variables:
//   LOOKUP_CACHE_DIR        cache directory (default: <git common dir>/lookup-cache).
//                           Holds embeddings.sqlite, the model files under models/,
//                           and in-flight request files under requests/.
//   LOOKUP_EMBED_THREADS    onnxruntime threads per lookup (default 2, or 1 on a
//                           one-core machine; --warm uses half the cores), so
//                           concurrent lookups do not each take every core.
//   LOOKUP_EMBED_LOCK_WAIT  seconds a lookup waits for another lookup's embedding
//                           run or model download (default 300). Past it the
//                           embedding shortlist covers only passages already
//                           cached, the output says "embedding busy", and the
//                           word-overlap shortlist still serves.
//   LOOKUP_UV               path to uv when it is not on PATH or in ~/.local/bin.
// Vectors are never invalidated, only superseded, so the cache grows as the text
// changes; --prune-cache removes rows unused for the given number of days.
//
// Meaning search, and narrowing by context, ask Jev (TypeSafe's System One model)
// one yes/no question per passage. They need a TypeSafe key, read from
// TYPESAFE_API_KEY or from ~/.config/typesafe/api_key (TYPESAFE_API_KEY set to the
// empty string turns judging off). Without one, or when Jev fails or is marked
// down (see "Jev availability" below), meaning and related searches return their
// shortlists unjudged, and the line under the header starts with
// "JEV UNAVAILABLE (<reason>)". Exact search, sections, and lines never ask Jev
// and stay complete.
//
// Exit codes: 0 ran; 1 failed; 2 usage error.

import { execFileSync, spawnSync } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { closeSync, existsSync, mkdirSync, openSync, readFileSync, readdirSync, renameSync, statSync, unlinkSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { availableParallelism, homedir } from "node:os";
import { fileURLToPath } from "node:url";

const SCOPES = {
  spec: { dir: "spec", kind: "md" },
  docs: { dir: "docs", kind: "md" },
  proposals: { dir: "proposals", kind: "md" },
  pkg: { dir: "pkg", kind: "code" },
  cmd: { dir: "cmd", kind: "code" },
  sdks: { dir: "sdks", kind: "code" },
  charts: { dir: "charts", kind: "code" },
  schemas: { dir: "schemas", kind: "code" },
  tests: { dir: "tests", kind: "code" },
};
const CODE_EXT = /\.(go|proto|ya?ml|json|tpl|ts|py|sh|sql|md)$/;
const SKIP_DIR = new Set(["node_modules", ".git", "vendor", "testdata", "dist", "bin"]);
const MAX_FILE_BYTES = 6_000_000;
const MAX_BUDGET = 26000;
const JEV_URL = process.env.LOOKUP_JEV_URL || "https://api.typesafe.ai/v1/systemone";
const JEV_MODEL = process.env.LOOKUP_JEV_MODEL || "jev-latest";
const JEV_CONCURRENCY = Number(process.env.LOOKUP_JEV_CONCURRENCY) || 32;
// A passage Jev rates at or above this is treated as relevant. Tune against real
// lookups before relying on the boundary; the probability is printed with each hit.
const RELEVANT = 0.5;
// Narrowing an exact search by context uses a lower bar: the caller asked for
// every occurrence, so a passage is dropped from the printed text only when Jev
// rates it clearly unrelated. It stays in the location list either way.
const NARROW = 0.35;
// Passages each shortlist contributes to what Jev judges. Measured against Jev
// judging every passage of five spec files for eight paraphrase queries, 80 per
// list reached 81% of the passages Jev rates 0.8 or higher and 160 reached 89%.
const SHORTLIST = Number(process.env.LOOKUP_SHORTLIST) || 160;
// At or below this many passages in scope, meaning search skips the shortlists and
// judges everything: Jev judges about 2,000 passages in 9 seconds, so a scope of a
// few files is cheaper to judge whole than to shortlist and miss.
const JUDGE_ALL_BELOW = Number(process.env.LOOKUP_JUDGE_ALL_BELOW) || 3000;

function usage(msg) {
  process.stderr.write(`lookup.mjs: ${msg}\nSee the header of .claude/tools/lookup.mjs for the request format.\n`);
  process.exit(2);
}

function readRequest() {
  const pr = process.argv.indexOf("--prune-cache");
  if (pr !== -1) {
    const days = Number(process.argv[pr + 1]);
    if (!Number.isInteger(days) || days < 1) usage("--prune-cache takes a whole number of days, at least 1");
    return { prune: days };
  }
  const w = process.argv.indexOf("--warm");
  if (w !== -1) {
    const scope = process.argv.slice(w + 1).filter((a) => !a.startsWith("-"));
    return { warm: true, scope: scope.length ? scope : ["spec"] };
  }
  const i = process.argv.indexOf("--json");
  let raw;
  if (i !== -1) raw = process.argv[i + 1];
  else if (!process.stdin.isTTY) raw = readFileSync(0, "utf8");
  if (!raw || !raw.trim()) usage("no request given");
  try {
    return JSON.parse(raw);
  } catch (e) {
    usage(`request is not valid JSON: ${e.message}`);
  }
}

// The key comes from the environment, or from a file outside the repository so
// that it reaches every session, worktree, and workflow agent without being
// exported in each one.
function typesafeKey() {
  // Set but empty means no key: a test or an operator can turn judging off
  // without moving the key file.
  if (process.env.TYPESAFE_API_KEY !== undefined) return process.env.TYPESAFE_API_KEY.trim();
  try {
    return readFileSync(join(homedir(), ".config", "typesafe", "api_key"), "utf8").trim();
  } catch {
    return "";
  }
}

// The cache directory every worktree of the repository shares, or a reason there
// is none. LOOKUP_CACHE_DIR overrides it, such as for a test against a copy.
function cacheDir() {
  if (process.env.LOOKUP_CACHE_DIR) return { dir: resolve(process.env.LOOKUP_CACHE_DIR) };
  try {
    const common = resolve(execFileSync("git", ["rev-parse", "--git-common-dir"], { encoding: "utf8" }).trim());
    return { dir: join(common, "lookup-cache") };
  } catch {
    return { reason: "not in a git repository, so there is nowhere to cache vectors" };
  }
}

// uv's installer puts it in ~/.local/bin, which a non-login shell or a workflow
// subagent often lacks on PATH. Spawning a bare "uv" there reports uv as missing
// while it is installed.
function uvCommand() {
  if (process.env.LOOKUP_UV) return process.env.LOOKUP_UV;
  for (const d of (process.env.PATH || "").split(":")) if (d && existsSync(join(d, "uv"))) return "uv";
  for (const p of [join(homedir(), ".local", "bin", "uv"), join(homedir(), ".cargo", "bin", "uv")]) if (existsSync(p)) return p;
  return "uv";
}

// A request file is removed when its lookup ends, but a lookup killed mid-run
// leaves it behind, and a file can be tens of megabytes. Files older than this
// belong to no live lookup: the spawn timeout is shorter.
const STALE_REQUEST_MS = 3_600_000;
function sweepStaleRequests(dir) {
  let names = [];
  try {
    names = readdirSync(dir);
  } catch {
    return;
  }
  const now = Date.now();
  for (const n of names) {
    try {
      const p = join(dir, n);
      if (now - statSync(p).mtimeMs > STALE_REQUEST_MS) unlinkSync(p);
    } catch {
      /* another lookup removed it first */
    }
  }
}

// Runs lookup-embed.py on one request object and returns its parsed output, or
// a reason it could not run.
function runEmbed(request) {
  const script = join(dirname(fileURLToPath(import.meta.url)), "lookup-embed.py");
  const c = cacheDir();
  if (c.reason) return { reason: c.reason };
  const reqDir = join(c.dir, "requests");
  mkdirSync(reqDir, { recursive: true });
  sweepStaleRequests(reqDir);
  // The request is passed as a file. Piped on stdin, a request of tens of
  // megabytes twice left the helper blocked on a read that never completed.
  const reqFile = join(reqDir, `request-${process.pid}-${Date.now()}.json`);
  writeFileSync(reqFile, JSON.stringify({ cache: join(c.dir, "embeddings.sqlite"), models: join(c.dir, "models"), ...request }));
  const r = spawnSync(uvCommand(), ["run", "--quiet", script, reqFile], { stdio: ["ignore", "pipe", "pipe"], encoding: "utf8", maxBuffer: 1 << 28, timeout: 1_500_000 });
  try {
    unlinkSync(reqFile);
  } catch {
    /* already gone */
  }
  if (r.error) return { reason: r.error.code === "ENOENT" ? "uv is not installed" : r.error.message };
  if (r.status !== 0) return { reason: `lookup-embed.py failed: ${(r.stderr || "").trim().split("\n").pop()}` };
  try {
    return JSON.parse(r.stdout);
  } catch {
    return { reason: "lookup-embed.py returned unreadable output" };
  }
}

function repoRoot() {
  try {
    return execFileSync("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8" }).trim();
  } catch {
    return process.cwd();
  }
}

function walk(dir, kind, out) {
  let entries;
  try {
    entries = readdirSync(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const e of entries) {
    const p = join(dir, e.name);
    if (e.isDirectory()) {
      if (!SKIP_DIR.has(e.name)) walk(p, kind, out);
    } else if (e.isFile()) {
      if (kind === "md" ? e.name.endsWith(".md") : CODE_EXT.test(e.name)) out.push(p);
    }
  }
}

function collectFiles(root, req) {
  const scopes = Array.isArray(req.scope) && req.scope.length ? req.scope : ["spec"];
  const files = [];
  for (const s of scopes) {
    const def = SCOPES[s];
    if (!def) usage(`unknown scope "${s}"; known: ${Object.keys(SCOPES).join(" ")}`);
    const found = [];
    walk(join(root, def.dir), def.kind, found);
    files.push(...found.sort());
  }
  // Files keep the order of the scopes as given, so with ["spec", "pkg"] the
  // spec's statement of a term prints before the code that implements it.
  let rel = files.map((f) => relative(root, f).split(sep).join("/"));
  if (Array.isArray(req.paths) && req.paths.length) {
    const want = req.paths.map((p) => p.replace(/^\.\//, "").replace(/\/$/, ""));
    rel = rel.filter((f) => want.some((w) => f === w || f.startsWith(w + "/")));
    // A path outside the named scopes is still honored when it is a single file.
    for (const w of want) {
      if (!rel.includes(w)) {
        try {
          if (statSync(join(root, w)).isFile()) rel.push(w);
        } catch {
          /* not a file; ignore */
        }
      }
    }
  }
  return rel;
}

// A markdown file as blocks, each carrying the heading above it. A block is the
// unit a hit is reported in and the unit Jev judges, so it has to be small enough
// to quote: a paragraph, one list item, one table row (printed under its header
// row), or a fenced block.
function mdBlocks(file, text) {
  const lines = text.split("\n");
  const blocks = [];
  let heading = "(before first heading)";
  let start = -1;
  let fence = false;
  const push = (from, to, extra) =>
    blocks.push({ file, heading, from: from + 1, to, text: lines.slice(from, to).join("\n"), ...extra });
  const isItem = (l) => /^\s{0,3}([-*+]|\d+[.)])\s+/.test(l);
  const flush = (end) => {
    if (start === -1) return;
    const run = lines.slice(start, end);
    if (run.length > 2 && run[0].trimStart().startsWith("|") && /^\s*\|?[\s:|-]+\|?\s*$/.test(run[1])) {
      const tableHeader = run[0];
      for (let i = start + 2; i < end; i++) push(i, i + 1, { tableHeader });
    } else if (!/^\s*(```|~~~)/.test(run[0]) && run.filter(isItem).length > 1) {
      let from = start;
      for (let i = start + 1; i < end; i++) {
        if (isItem(lines[i])) {
          push(from, i);
          from = i;
        }
      }
      push(from, end);
    } else push(start, end);
    start = -1;
  };
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i];
    if (/^\s*(```|~~~)/.test(l)) fence = !fence;
    const h = !fence && /^(#{1,6})\s+(.*)$/.exec(l);
    if (h) {
      flush(i);
      heading = h[2].trim();
      blocks.push({ file, heading, from: i + 1, to: i + 1, text: l, isHeading: true, level: h[1].length });
      continue;
    }
    if (!fence && l.trim() === "") flush(i);
    else if (start === -1) start = i;
  }
  flush(lines.length);
  return blocks;
}

// What to print for a block. A long block (a fenced listing, a wide table row)
// is cut to the lines that matched plus their neighbors, and the cut is stated.
const LONG_BLOCK = 1200;
function render(b, re) {
  const head = b.tableHeader ? b.tableHeader + "\n" : "";
  if (b.text.length <= LONG_BLOCK) return head + b.text;
  const ls = b.text.split("\n");
  if (ls.length === 1 || !re) {
    // One long line (a wide table row): keep the text around the match.
    const at = re ? Math.max(0, (re.exec(b.text)?.index ?? 0) - LONG_BLOCK / 2) : 0;
    return head + (at ? "[...] " : "") + b.text.slice(at, at + LONG_BLOCK) + `\n[cut: ${b.text.length} characters in this passage]`;
  }
  const keep = new Set();
  ls.forEach((l, i) => {
    if (re.test(l)) for (let j = Math.max(0, i - 1); j <= Math.min(ls.length - 1, i + 1); j++) keep.add(j);
  });
  const out = [];
  let last = -2;
  for (const i of [...keep].sort((a, c) => a - c)) {
    if (i !== last + 1) out.push(`[line ${b.from + i}]`);
    out.push(ls[i].length > 900 ? ls[i].slice(0, 900) + " [cut]" : ls[i]);
    last = i;
  }
  return head + out.join("\n") + `\n[excerpt: matching lines of a ${ls.length}-line passage]`;
}

// A code file has no paragraphs, so a hit is reported with a window of lines.
function codeBlocksForHits(file, text, hitLines) {
  const lines = text.split("\n");
  const blocks = [];
  let cur = null;
  for (const n of hitLines) {
    const from = Math.max(1, n - 2);
    const to = Math.min(lines.length, n + 3);
    if (cur && from <= cur.to + 1) cur.to = to;
    else {
      cur = { file, heading: "", from, to };
      blocks.push(cur);
    }
  }
  for (const b of blocks) b.text = lines.slice(b.from - 1, b.to).join("\n");
  return blocks;
}

function matcher(term) {
  const t = typeof term === "string" ? { pattern: term } : term;
  if (!t || typeof t.pattern !== "string" || !t.pattern) usage("each exact entry needs a non-empty pattern");
  const src = t.regex ? t.pattern : t.pattern.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  let re;
  try {
    re = new RegExp(src, t.ignoreCase ? "i" : "");
  } catch (e) {
    usage(`bad regex "${t.pattern}": ${e.message}`);
  }
  return { label: t.pattern + (t.regex ? " (regex)" : ""), re };
}

function tokens(s) {
  return (s.toLowerCase().match(/[a-z0-9_]{3,}/g) || []).filter((w) => !STOP.has(w));
}
const STOP = new Set(
  "the and for that this with from are was were has have not but its into than then when which while each any all can may must shall will does did such also only one two per via".split(" "),
);

// BM25 over blocks, to shortlist candidates for Jev. It is a recall device: the
// query and the caller's context both contribute terms.
function shortlist(blocks, text, k) {
  const q = [...new Set(tokens(text))];
  if (!q.length) return [];
  const docs = blocks.map((b) => tokens(b.text + " " + b.heading));
  const N = docs.length || 1;
  const avg = docs.reduce((a, d) => a + d.length, 0) / N || 1;
  const df = new Map();
  for (const d of docs) for (const w of new Set(d)) if (q.includes(w)) df.set(w, (df.get(w) || 0) + 1);
  const scored = [];
  docs.forEach((d, i) => {
    if (!d.length) return;
    const tf = new Map();
    for (const w of d) if (df.has(w)) tf.set(w, (tf.get(w) || 0) + 1);
    let s = 0;
    for (const [w, f] of tf) {
      const idf = Math.log(1 + (N - df.get(w) + 0.5) / (df.get(w) + 0.5));
      s += (idf * f * 2.2) / (f + 1.2 * (0.25 + (0.75 * d.length) / avg));
    }
    if (s > 0) scored.push([s, i]);
  });
  scored.sort((a, b) => b[0] - a[0]);
  return scored.slice(0, k).map(([, i]) => blocks[i]);
}

// The embedding shortlist: passages whose meaning is close to the query whatever
// words they use. lookup-embed.py computes it and caches vectors by passage text
// under the git common directory, which every worktree of the repository shares.
// Returns one list of blocks per query, or a reason it could not run.
// The text embedded for a passage. Warming and searching must agree on it,
// because the cache is keyed by this text.
const passageText = (b) => (b.heading ? b.heading + "\n" : "") + b.text;

function embedShortlist(blocks, queries, top) {
  const out = runEmbed({ top, queries, passages: blocks.map((b, id) => ({ id, text: passageText(b) })) });
  if (out.reason) return out;
  if (!Array.isArray(out.results)) return { reason: "lookup-embed.py returned unreadable output" };
  return { model: out.model, embedded: out.embedded, note: out.note, lists: out.results.map((l) => l.map(([id, score]) => ({ block: blocks[id], score }))) };
}

// JEV AVAILABILITY. Jev can stop answering for reasons this tool cannot fix: the
// network is down, the key is wrong or revoked (401, 403), the service is
// overloaded (429, 5xx), a request hangs, or a response cannot be read. Before
// this guard a lookup retried each of thousands of passages with backoff, so an
// outage turned one call into minutes, and the output read like a judged
// ranking with every passage missing. Two breakers bound that.
//
// Per call: every request has a timeout, a 429 or 5xx is retried at most
// LOOKUP_JEV_RETRIES times with a short capped backoff, and once
// LOOKUP_JEV_TRIP requests in a row fail (or one 401 or 403 arrives) the call
// sends nothing more to Jev and reports its Jev-backed results as unjudged.
//
// Across calls: a trip is recorded in jev-state.json in the shared cache
// directory, so every worktree and process on the machine sees it. Calls during
// the cooldown skip Jev at once. After the cooldown one call claims the probe
// (an exclusively created jev-probe file) and tries Jev; a success clears the
// state and a failure records a new cooldown. An auth failure is keyed to a hash
// of the key, so a changed key is tried at once rather than after the cooldown.
//
// Operator-tunable environment variables:
//   LOOKUP_JEV_URL       Jev endpoint (default the TypeSafe API); tests point it at a stub.
//   LOOKUP_JEV_TIMEOUT   seconds before one request is abandoned (default 20).
//   LOOKUP_JEV_RETRIES   retries of a 429, 5xx, or network error per request (default 2).
//   LOOKUP_JEV_TRIP      consecutive failed requests that trip the call's breaker (default 3).
//   LOOKUP_JEV_COOLDOWN  seconds Jev is skipped after a trip (default 600).
const JEV_TIMEOUT_MS = (Number(process.env.LOOKUP_JEV_TIMEOUT) || 20) * 1000;
const JEV_RETRIES = Number.isInteger(Number(process.env.LOOKUP_JEV_RETRIES)) && process.env.LOOKUP_JEV_RETRIES !== "" ? Number(process.env.LOOKUP_JEV_RETRIES) : 2;
const JEV_TRIP = Number(process.env.LOOKUP_JEV_TRIP) || 3;
const JEV_COOLDOWN_MS = (Number(process.env.LOOKUP_JEV_COOLDOWN) || 600) * 1000;
// A probe file older than this belongs to a lookup that died mid-probe.
const JEV_PROBE_STALE_MS = Math.max(60_000, 3 * JEV_TIMEOUT_MS);

// The text every caller and agent instruction keys on. Keep it stable.
const JEV_MARKER = "JEV UNAVAILABLE";

// A failure that ends a request and counts toward the breaker. `auth` trips at once.
class JevError extends Error {
  constructor(message, auth = false) {
    super(message);
    this.auth = auth;
  }
}

const keyHash = (key) => createHash("sha256").update(key).digest("hex").slice(0, 16);

function readJevState(dir) {
  try {
    const s = JSON.parse(readFileSync(join(dir, "jev-state.json"), "utf8"));
    return s && typeof s === "object" ? s : null;
  } catch {
    return null;
  }
}

// Writes go to a private temporary file and are renamed into place, so a reader
// never sees a partial file and concurrent writers leave one whole state.
function writeJevState(dir, state) {
  try {
    mkdirSync(dir, { recursive: true });
    const tmp = join(dir, `jev-state.json.${process.pid}.${randomBytes(4).toString("hex")}.tmp`);
    writeFileSync(tmp, JSON.stringify(state) + "\n");
    renameSync(tmp, join(dir, "jev-state.json"));
  } catch {
    /* the cross-call breaker is advisory; the per-call breaker still holds */
  }
}

const isoTime = (ms) => new Date(ms).toISOString().replace(/\.\d+Z$/, "Z");

// One Jev client per lookup call. `reason` is null while Jev may be used, and
// the reason it may not be once that is known.
class Jev {
  constructor(key) {
    this.key = key;
    this.reason = key ? null : "no TypeSafe key is set";
    this.checked = !key;
    this.failStreak = 0;
    this.successes = 0;
    this.failures = 0;
    this.probe = null;
    this.startedAt = Date.now();
    const c = key ? cacheDir() : {};
    this.dir = c.dir || null;
  }

  // Whether Jev may be asked. The first call reads the shared state, so a lookup
  // that never needs Jev never touches it.
  usable() {
    if (!this.checked) {
      this.checked = true;
      this.consultSharedState();
    }
    return this.reason === null;
  }

  consultSharedState() {
    if (!this.dir) return;
    const s = readJevState(this.dir);
    if (!s || !s.down) return;
    if (s.auth && s.keyHash !== keyHash(this.key)) return; // the key changed since the auth failure
    const until = Number(s.until) || 0;
    if (Date.now() < until) {
      this.reason = `${s.reason || "Jev failed"}; recorded by an earlier lookup at ${isoTime(Number(s.since) || 0)}, skipped until ${isoTime(until)}`;
      return;
    }
    // The cooldown has passed: one call probes, the rest keep skipping until it ends.
    const probe = join(this.dir, "jev-probe");
    for (let attempt = 0; attempt < 2; attempt++) {
      try {
        closeSync(openSync(probe, "wx"));
        this.probe = probe;
        return;
      } catch (e) {
        if (e.code !== "EEXIST") return; // cannot claim; probe anyway rather than stay down forever
        try {
          if (Date.now() - statSync(probe).mtimeMs > JEV_PROBE_STALE_MS) {
            unlinkSync(probe);
            continue;
          }
        } catch {
          continue; // the prober finished between the open and the stat
        }
        this.reason = `${s.reason || "Jev failed"}; the cooldown has passed and another lookup is probing Jev now`;
        return;
      }
    }
  }

  // Ends the call's use of Jev: releases a probe claim and, when this call
  // reached Jev, clears a recorded outage older than the call.
  finish() {
    if (this.dir && this.successes && !this.reason) {
      const s = readJevState(this.dir);
      if (s && s.down && (Number(s.since) || 0) <= this.startedAt) {
        try {
          unlinkSync(join(this.dir, "jev-state.json"));
        } catch {
          /* already cleared */
        }
      }
    }
    if (this.probe)
      try {
        unlinkSync(this.probe);
      } catch {
        /* already released */
      }
    this.probe = null;
  }

  trip(why, auth) {
    if (this.reason) return;
    this.reason = why;
    if (this.dir)
      writeJevState(this.dir, { down: true, reason: why, auth: Boolean(auth), keyHash: keyHash(this.key), since: Date.now(), until: Date.now() + JEV_COOLDOWN_MS, pid: process.pid });
  }

  record(err) {
    this.failures++;
    this.failStreak++;
    if (err instanceof JevError && err.auth) this.trip(err.message, true);
    else if (this.failStreak >= JEV_TRIP) this.trip(`${this.failStreak} Jev requests in a row failed, the last with: ${err.message}`, false);
  }

  // One request. Throws when the breaker is open or the request failed.
  async ask(state, questions) {
    if (!this.usable()) throw new JevError(this.reason);
    try {
      const answers = await this.attempt(state, questions);
      this.successes++;
      this.failStreak = 0;
      return answers;
    } catch (e) {
      if (!this.reason) this.record(e);
      throw e;
    }
  }

  async attempt(state, questions) {
    for (let attempt = 0; ; attempt++) {
      if (this.reason) throw new JevError(this.reason);
      let res;
      try {
        res = await fetch(JEV_URL, {
          method: "POST",
          headers: { Authorization: `Bearer ${this.key}`, "Content-Type": "application/json" },
          body: JSON.stringify({ state, model: JEV_MODEL, questions }),
          signal: AbortSignal.timeout(JEV_TIMEOUT_MS),
        });
      } catch (e) {
        // A timeout is not retried: a hanging service would hang the retry too.
        if (e.name === "TimeoutError" || e.name === "AbortError") throw new JevError(`a Jev request timed out after ${JEV_TIMEOUT_MS / 1000}s`);
        if (attempt >= JEV_RETRIES) throw new JevError(`Jev is unreachable (${e.cause?.code || e.message})`);
        await sleep(300 * 2 ** attempt);
        continue;
      }
      if (res.status === 401 || res.status === 403) {
        await res.body?.cancel();
        throw new JevError(`Jev rejected the TypeSafe key (HTTP ${res.status})`, true);
      }
      if (res.ok) {
        let body;
        try {
          body = await res.json();
        } catch (e) {
          if (e.name === "TimeoutError" || e.name === "AbortError") throw new JevError(`a Jev response timed out after ${JEV_TIMEOUT_MS / 1000}s`);
          throw new JevError("Jev returned a response that is not JSON");
        }
        const a = body && body.answers;
        if (!a || typeof a !== "object" || Object.keys(questions).some((id) => !a[id] || typeof a[id].noul !== "number"))
          throw new JevError("Jev returned a response without the expected answers");
        return a;
      }
      const text = await res.text().catch(() => "");
      if ((res.status === 429 || res.status >= 500) && attempt < JEV_RETRIES && !this.reason) {
        // Honour Retry-After, but never so long that an outage stalls the call.
        const wait = Math.min(Number(res.headers.get("retry-after")) * 1000 || 300 * 2 ** attempt, 3000);
        await sleep(wait);
        continue;
      }
      throw new JevError(`Jev returned HTTP ${res.status}${text ? ": " + text.replace(/\s+/g, " ").slice(0, 120) : ""}`);
    }
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// One request per passage. Jev's accuracy falls as the state fills with text
// unrelated to the question, so a passage is judged alone with its heading. Jev
// answers every question in a request against the same state, so when several
// queries shortlist one passage they travel together and the passage is sent once.
// `items` is [{ block, questions: { id: question } }]; the result maps each id to
// its probability, per item, or null where the request failed or was not sent
// because the breaker opened.
async function judgeMany(jev, items) {
  const out = new Array(items.length).fill(null);
  let next = 0;
  let failed = 0;
  const worker = async () => {
    for (;;) {
      const i = next++;
      if (i >= items.length) return;
      if (!jev.usable()) {
        failed++;
        continue;
      }
      const b = items[i].block;
      try {
        const a = await jev.ask({ section_heading: b.heading, passage: b.text.slice(0, 12000) }, items[i].questions);
        out[i] = Object.fromEntries(Object.entries(a).map(([id, v]) => [id, v.noul]));
      } catch {
        failed++;
      }
    }
  };
  await Promise.all(Array.from({ length: Math.min(JEV_CONCURRENCY, items.length) }, worker));
  return { answers: out, failed };
}

async function judge(jev, blocks, question) {
  const { answers, failed } = await judgeMany(jev, blocks.map((block) => ({ block, questions: { q: question } })));
  return { probs: answers.map((a) => (a ? a.q : null)), failed };
}

// The wording was chosen by comparing variants on hand-labelled spec passages.
// Asking for "the same rule about the same subject" ranks analogous rules about
// other subjects lower than asking whether the passage "restates" the query; a
// graded Score in place of the yes/no question ranked them worse than either.
function meaningQuestion(query, context) {
  const instructions = { looking_for: query };
  if (context) instructions.caller_goal = context;
  instructions.question =
    "Does `passage` state the same rule, limit, or behavior that `looking_for` describes, about the same subject and under the same condition" +
    (context ? ", in a way that bears on `caller_goal`" : "") +
    "? The wording may differ from `looking_for`.";
  return {
    type: "noul",
    instructions,
    criteria: {
      true: "The passage states that same rule, limit, or behavior, for the same subject as `looking_for`, in any wording.",
      false:
        "The passage states a different rule, states a similar rule about a different subject, or only shares vocabulary with `looking_for`.",
    },
  };
}

function contextQuestion(term, context) {
  return {
    type: "noul",
    instructions: {
      term,
      caller_goal: context,
      question: "Would a reviewer pursuing `caller_goal` need to read `passage`, which mentions `term`?",
    },
    criteria: {
      true: "The passage's use of `term` bears on `caller_goal`: it states behavior, a rule, a value, or a cross-reference the goal depends on.",
      false: "The passage mentions `term` in passing, in a context unrelated to `caller_goal`.",
    },
  };
}

// ---- related: passages that state or depend on what a changed passage says ----
//
// A fix that changes one statement leaves every other statement of the same fact
// wrong, and the review loop found those one round at a time. "related" takes the
// changed text and collects candidates from the sources "via" names. The default
// sources are meaning (the word-overlap and embedding shortlists for the text),
// also (the same for the new wording), sentidx (word overlap against single
// sentences), and chunks (embeddings of short sentence windows). Links and terms
// stay available for measurement. Jev then judges each candidate against the
// changed text.

// Measured with the default sources on 132 isolated cases: 0.7 returned 65% of
// targets with 72% of labelled results needed or useful, at a median of 16 per
// call; 0.75 returned 54% at 73% and 10. On 13 targets from real fixes, 0.7
// returned 7 and 0.75 returned 4, both at 61%.
const RELATED_MIN = Number(process.env.LOOKUP_RELATED_MIN) || 0.7;
// Each meaning shortlist contributes this many candidates. Smaller than SHORTLIST,
// because related search is judged for precision as well as recall.
const RELATED_SHORTLIST = Number(process.env.LOOKUP_RELATED_SHORTLIST) || 80;
// A term in more passages than this names a common concept rather than the
// changed statement, and is dropped from the term source.
const TERM_MAX_PASSAGES = Number(process.env.LOOKUP_TERM_MAX_PASSAGES) || 40;
const RELATED_MAX_CANDIDATES = 600;

const sectionNumber = (heading) => (/^§?\s*(\d+(?:\.\d+)*)\.?\s/.exec(heading || "") || [])[1] || null;
const slug = (h) =>
  h
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s_-]/gu, "")
    .trim()
    .replace(/\s+/g, "-");
const LABEL_ID = /\b[A-Z]{2,}-\d+[a-z]?\d*\b/g;

// Per markdown block: the numbered section it sits in, and the sections and
// labelled items it cites. Built once per call over every block in scope.
function linkIndex(blocksOf) {
  const bySlug = new Map();
  for (const bl of blocksOf.values()) for (const b of bl) if (b.isHeading) bySlug.set(slug(b.heading), b.heading);
  for (const bl of blocksOf.values()) {
    const stack = [];
    const body = bl.filter((b) => !b.isHeading);
    body.forEach((b, i) => {
      b.prev = body[i - 1] || null;
      b.next = body[i + 1] || null;
    });
    for (const b of bl) {
      if (b.isHeading) {
        while (stack.length && stack[stack.length - 1].level >= b.level) stack.pop();
        stack.push(b);
      }
      // The section a block belongs to: the nearest numbered heading above it, or,
      // for a proposal's staged edit, the sections its heading names ("§ 7.2").
      const numbered = [...stack].reverse().find((h) => sectionNumber(h.heading) || /§\s?\d/.test(h.heading));
      b.sections = new Set();
      if (numbered) {
        const n = sectionNumber(numbered.heading);
        if (n) b.sections.add(n);
        else for (const m of numbered.heading.matchAll(/§\s?(\d+(?:\.\d+)*)/g)) b.sections.add(m[1]);
      }
      b.labelHead = (stack.map((h) => h.heading.match(LABEL_ID)).filter(Boolean).pop() || [])[0] || null;
      b.cites = citesOf(b.text, bySlug);
    }
  }
  return bySlug;
}

function citesOf(text, bySlug) {
  const sections = new Set();
  for (const m of text.matchAll(/§\s?(\d+(?:\.\d+)*)/g)) sections.add(m[1].replace(/\.$/, ""));
  for (const m of text.matchAll(/\]\(([^)\s]*?)#([\w%-]+)\)/g)) {
    const h = bySlug.get(m[2].toLowerCase());
    const n = h && sectionNumber(h);
    if (n) sections.add(n);
  }
  return { sections, labels: new Set(text.match(LABEL_ID) || []) };
}

const within = (n, target) => n === target || n.startsWith(target + ".");

function identifiers(text) {
  const out = new Set();
  for (const m of text.matchAll(/`([^`\n]{3,60})`/g)) out.add(m[1].trim());
  for (const m of text.matchAll(/\b([A-Za-z]+(?:[A-Z][a-z0-9]+){1,}[A-Za-z0-9]*)\b/g)) if (/[a-z][A-Z]/.test(m[1])) out.add(m[1]);
  for (const m of text.matchAll(/\b([a-z][a-z0-9]*(?:[_.][a-z0-9]+)+)\b/g)) out.add(m[1]);
  return [...out].filter((t) => t.length >= 3);
}

async function relatedSearch(r, ctx) {
  const { all, blocksOf, bySlug, jev, root } = ctx;
  // Default sources, measured on 132 cases (197 targets): meaning, also, sentidx,
  // and chunks reach 86% of targets as candidates with a median of 281 per call;
  // the earlier default of links, terms, and meaning reached 76% with 292. Link-
  // only candidates were mostly noise in blind labelling.
  const via = new Set(Array.isArray(r.via) && r.via.length ? r.via : ["meaning", "also", "sentidx", "chunks"]);
  const text = String(r.text || "");
  // "after" is the passage's new wording. It is searched like "also" and is
  // given to Jev with the change: on 56 landed spec changes, Jev returned 73% of
  // the other edited sites at 0.7 with it and 67% without, at the same share of
  // relevant results.
  const after = String(r.after || "").trim();
  const change = String(r.change || "") + (after ? "\nThe passage then reads:\n" + after.slice(0, 3000) : "");
  // The passage the changed text sits in: named by "at", or found by its text.
  let home = null;
  if (r.at) {
    const m = /^(.+?):(\d+)/.exec(r.at);
    if (m) home = (blocksOf.get(m[1]) || []).find((b) => !b.isHeading && Number(m[2]) >= b.from && Number(m[2]) <= b.to) || null;
  }
  const probe = text.replace(/\s+/g, " ").slice(0, 80);
  if (!home && probe.length >= 40) home = all.find((b) => b.text.replace(/\s+/g, " ").includes(probe)) || null;
  // Search with the whole changed passage: a caller often quotes only the clause
  // it changes, and the clause alone shares too little with the passages that
  // restate it.
  const passage = home && home.text.length > text.length ? home.text : text;
  const cands = new Map();
  // Review logs record past decisions rather than current statements, so a fix
  // never makes one wrong. They made up about half of the returned passages on
  // proposal 0092 before they were left out. "reviewLogs": true keeps them.
  const skipFile = (f) => r.reviewLogs !== true && /(^|\/)[^/]*review-log[^/]*\.md$/.test(f);
  const add = (b, src) => {
    if (b === home || b.isHeading || skipFile(b.file)) return;
    if (!cands.has(b)) cands.set(b, new Set());
    cands.get(b).add(src);
  };
  const notes = [];

  if (via.has("links")) {
    const own = citesOf(passage, bySlug);
    const targets = new Set(own.sections);
    const labels = new Set(own.labels);
    if (home) {
      for (const n of home.sections) targets.add(n);
      if (home.labelHead) labels.add(home.labelHead);
    }
    // Passages that cite a target section or labelled item, and the passages of a
    // cited section that share the most words with the changed text.
    for (const b of all) {
      if ([...b.cites.sections].some((n) => [...targets].some((t) => within(n, t)))) add(b, "links");
      else if ([...b.cites.labels].some((l) => labels.has(l)) && b.labelHead !== (home && home.labelHead)) add(b, "links");
    }
    for (const n of own.sections) {
      const inSection = all.filter((b) => [...b.sections].some((x) => within(x, n)));
      for (const b of shortlist(inSection, passage, 10)) add(b, "links");
    }
    notes.push(`links: sections ${[...targets].join(", ") || "none"}; labels ${[...labels].join(", ") || "none"}`);
  }

  if (via.has("terms")) {
    const used = [];
    for (const t of identifiers(passage)) {
      const hits = all.filter((b) => b.text.includes(t));
      if (!hits.length || hits.length > TERM_MAX_PASSAGES) continue;
      used.push(t);
      for (const b of hits) add(b, "terms");
    }
    notes.push(`terms: ${used.join(", ") || "none"}`);
  }

  // "also" is one text or a list of short queries, each searched on its own.
  const alsoList = [...(Array.isArray(r.also) ? r.also : [r.also]), r.after].map((x) => String(x || "").trim()).filter(Boolean);
  const also = alsoList.join("\n");
  const k = Number(r.shortlist) > 0 ? Number(r.shortlist) : RELATED_SHORTLIST;
  const meaningTop = [];
  if (via.has("meaning")) {
    const q = passage + (change ? " " + change : "");
    const words = shortlist(all, q, k);
    for (const b of words) add(b, "meaning");
    const emb = embedShortlist(all, [q], k);
    if (emb.reason) notes.push(`embedding shortlist unavailable (${emb.reason})`);
    else if (emb.note) notes.push(`embedding shortlist partial (${emb.note})`);
    if (!emb.reason) for (const { block } of emb.lists[0]) add(block, "meaning");
    meaningTop.push(...words.slice(0, 20), ...(emb.lists ? emb.lists[0].slice(0, 20).map((x) => x.block) : []));
  }

  // The sources below exist for measurement; the documented request uses the
  // three above. "also" is a second text, such as the new wording a fix writes.
  const units = (t, re, min) => t.split(re).map((x) => x.trim()).filter((x) => x.length >= min);
  const multi = (queries, kk, src) => {
    if (!queries.length) return;
    for (const q of queries) for (const b of shortlist(all, q, kk)) add(b, src);
    const emb = embedShortlist(all, queries, kk);
    if (!emb.reason) for (const l of emb.lists) for (const { block } of l) add(block, src);
  };
  if (via.has("also") && alsoList.length) {
    for (const q of alsoList) for (const b of shortlist(all, q, k)) add(b, "also");
    const emb = embedShortlist(all, alsoList, k);
    if (!emb.reason) for (const l of emb.lists) for (const { block } of l) add(block, "also");
  }
  if (via.has("sentences")) multi(units(passage + (also ? "\n" + also : ""), /(?<=[.!?])\s+(?=[A-Z`*(])|\n+/, 30).slice(0, 24), 15, "sentences");
  if (via.has("clauses"))
    multi(units(passage + (also ? "\n" + also : ""), /(?<=[.!?;:])\s+|\n+|,\s+(?=(?:and|or|but|so|which|while|because|unless|until|when)\b)/, 25).slice(0, 40), 8, "clauses");
  if (via.has("pairs")) {
    const ids = identifiers(passage + " " + also);
    if (ids.length >= 2)
      for (const b of all) {
        let n = 0;
        for (const t of ids) if (b.text.includes(t) && ++n >= 2) break;
        if (n >= 2) add(b, "pairs");
      }
  }
  if (via.has("sentidx")) {
    // Reverse direction: rank document sentences rather than whole passages, so a
    // one-sentence restatement inside a long passage is not diluted by the rest.
    const sents = [];
    for (const b of all) for (const s of units(b.text, /(?<=[.!?])\s+(?=[A-Z`*(])|\n+/, 30)) sents.push({ block: b, text: s, heading: "" });
    for (const s of shortlist(sents, passage + " " + also, k)) add(s.block, "sentidx");
  }
  if (via.has("chunks")) {
    // Embedding at sentence grain: each passage is cut into windows of adjacent
    // sentences (about 120 to 400 characters) and each window is embedded on its
    // own, so one restating sentence inside a long passage is not averaged away.
    // A window that matches stands for its passage.
    const chunks = [];
    for (const b of all) {
      if (b.text.length <= 400) {
        chunks.push({ block: b, text: b.text, heading: b.heading });
        continue;
      }
      let cur = "";
      for (const s of units(b.text, /(?<=[.!?])\s+(?=[A-Z`*(\[])|\n+/, 1)) {
        if (cur && cur.length + s.length > 400) {
          chunks.push({ block: b, text: cur, heading: b.heading });
          cur = "";
        }
        cur = cur ? cur + " " + s : s;
        if (cur.length >= 120 && /[.!?]$/.test(cur)) {
          chunks.push({ block: b, text: cur, heading: b.heading });
          cur = "";
        }
      }
      if (cur.trim().length >= 20) chunks.push({ block: b, text: cur, heading: b.heading });
    }
    const qs = [passage + (also ? "\n" + also : ""), ...units(passage + (also ? "\n" + also : ""), /(?<=[.!?])\s+(?=[A-Z`*(])|\n+/, 30).slice(0, 16)];
    if (alsoList.length > 1) qs.push(...alsoList);
    const emb = embedShortlist(chunks, qs, Number(r.chunkTop) > 0 ? Number(r.chunkTop) : 20);
    if (emb.reason) notes.push(`chunk embedding unavailable (${emb.reason})`);
    else if (emb.note) notes.push(`chunk embedding partial (${emb.note})`);
    if (!emb.reason) for (const l of emb.lists) for (const { block } of l) add(block.block, "chunks");
  }
  if (via.has("neighbors")) for (const b of [home, ...meaningTop]) if (b) for (const n of [b.prev, b.next]) if (n) add(n, "neighbors");
  if (via.has("all")) for (const b of all) add(b, "all");

  let list = [...cands].map(([block, src]) => ({ block, via: [...src].sort() }));
  const cap = Number(r.cap) > 0 ? Number(r.cap) : RELATED_MAX_CANDIDATES;
  if (list.length > cap) {
    notes.push(`${list.length} candidates; the ${list.length - cap} with the least word overlap were not judged`);
    const keep = new Set(shortlist(list.map((c) => c.block), passage + " " + change + " " + also, cap));
    list = list.filter((c) => keep.has(c.block));
  }
  let failed = 0;
  const wantJudge = list.length > 0 && r.judge !== false;
  if (wantJudge && jev.usable()) {
    // For tuning, "format": "json" also asks the question without the change.
    const questions = { q: relatedQuestion(passage, change, text) };
    if (ctx.tune && change) questions.q0 = relatedQuestion(passage, "", text);
    const { answers, failed: f } = await judgeMany(jev, list.map((c) => ({ block: c.block, questions })));
    failed = f;
    list.forEach((c, i) => {
      c.p = answers[i] ? answers[i].q : null;
      if (questions.q0) c.p0 = answers[i] ? answers[i].q0 : null;
    });
  }
  // A breaker that opened during this search leaves a partial ranking, so the
  // whole list is reported unjudged rather than as a ranking with holes in it.
  const judged = wantJudge && jev.usable();
  return { home, list, notes, sources: [...via], failed, judged, unjudgedReason: wantJudge && !judged ? jev.reason : null };
}

// The wording was chosen by comparing three on 141 known targets and 279
// blind-labelled results: asking whether an editor making the change must also
// read the passage kept 57% of targets at 0.75 with 12 of 148 noise results,
// against 45% at 0.7 with 14 for "states or depends on the same fact" and 43%
// with 14 for "same subject and condition".
function relatedQuestion(text, change, part) {
  const instructions = { changed_text: text.slice(0, 6000) };
  if (part && part.length < text.length) instructions.changed_part = part.slice(0, 2000);
  if (change) instructions.change = change.slice(0, 3000);
  instructions.question = change
    ? "An editor is about to make `change` to `changed_text`. To keep the documents consistent, must the editor also read `passage`, because it states, restates, or relies on something `change` alters?"
    : "An editor is about to change `changed_text`. To keep the documents consistent, must the editor also read `passage`, because it states, restates, or relies on what `changed_text` says?";
  return {
    type: "noul",
    instructions,
    criteria: {
      true: "`passage` states, restates, or relies on a fact, rule, value, outcome, or identifier that the change alters, so it may now be wrong or inconsistent.",
      false: "`passage` is about something the change does not alter, even if it is about the same mechanism or shares words.",
    },
  };
}

const loc = (b) => `${b.file}:${b.from}${b.to > b.from ? "-" + b.to : ""}`;
const where = (b) => (b.heading ? `${loc(b)}  [${b.heading}]` : loc(b));

async function main() {
  const req = readRequest();
  const exact = Array.isArray(req.exact) ? req.exact : [];
  const meaning = (Array.isArray(req.meaning) ? req.meaning : []).map((m) => (typeof m === "string" ? { query: m } : m));
  const sections = Array.isArray(req.sections) ? req.sections.map(String) : [];
  const related = (Array.isArray(req.related) ? req.related : []).map((r) => (typeof r === "string" ? { text: r } : r));
  for (const r of related) if (!r || typeof r.text !== "string" || r.text.trim().length < 20) usage("each related entry needs a text of at least 20 characters");
  if (req.prune) {
    const r = runEmbed({ prune: req.prune });
    process.stdout.write(r.reason ? `prune failed: ${r.reason}\n` : `prune: removed ${r.pruned} vector(s) unused for ${req.prune} day(s); ${r.kept} kept\n`);
    process.exit(r.reason ? 1 : 0);
  }
  if (!req.warm && !exact.length && !meaning.length && !sections.length && !related.length && !(Array.isArray(req.lines) && req.lines.length))
    usage("give at least one of exact, meaning, sections, lines, related");
  const key = typesafeKey();
  // Constructed after the prune and warm paths, which never ask Jev. The exit
  // hook releases a probe claim even when a usage error ends the call.
  const jev = new Jev(key);
  process.on("exit", () => jev.finish());
  // Set when a part of this request that asks Jev went without it.
  let jevMissed = false;
  const lineReqs = Array.isArray(req.lines) ? req.lines.map(String) : [];
  // The harness that runs this tool for an agent saves any output past roughly
  // 30,000 characters to a file, and the agent then spends turns reading the file
  // back. The budget is capped below that, whatever the caller asks for; what does
  // not fit is listed by location and can be fetched with "lines".
  const asked = Number(req.maxChars) > 0 ? Number(req.maxChars) : 24000;
  const budget = Math.min(asked, MAX_BUDGET);
  const root = repoRoot();
  const files = collectFiles(root, req);

  const texts = new Map();
  const blocksOf = new Map();
  for (const f of files) {
    let t;
    try {
      if (statSync(join(root, f)).size > MAX_FILE_BYTES) continue;
      t = readFileSync(join(root, f), "utf8");
    } catch {
      continue;
    }
    texts.set(f, t);
    if (f.endsWith(".md")) blocksOf.set(f, mdBlocks(f, t));
  }

  if (req.warm) {
    const all = [];
    for (const bl of blocksOf.values()) for (const b of bl) if (!b.isHeading && b.text.trim().length > 40) all.push(b);
    // Warming is one process run ahead of a fan-out, so it takes half the cores
    // where a concurrent lookup takes two. LOOKUP_EMBED_THREADS still wins.
    const r = runEmbed({ top: 0, queries: [], threads: Math.max(2, Math.floor(availableParallelism() / 2)), passages: all.map((b, id) => ({ id, text: passageText(b) })) });
    process.stdout.write(
      r.reason
        ? `warm failed: ${r.reason}\n`
        : `warm: ${root}: ${all.length} passage(s) in scope, ${r.embedded} embedded this run (${r.model})${r.note ? `; ${r.note}` : ""}\n`,
    );
    process.exit(r.reason || r.note ? 1 : 0);
  }

  const out = [];
  const records = [];
  let used = 0;
  // Prints text while the budget lasts; a hit that does not fit is still listed
  // by location, so the caller knows it exists and can ask for it.
  const emit = (s) => {
    out.push(s);
    used += s.length + 1;
  };
  // A requested section prints first and whole while the budget lasts. The rest
  // is shared evenly: each search may spend its share plus whatever earlier
  // searches left, so the first term cannot starve the others.
  const lookups = exact.length + meaning.length + related.length || 1;
  let done = 0;
  let base = 0;
  const fits = (s) => used + s.length < base + ((budget - base) * (done + 1)) / lookups;
  // The tree is named absolutely. A workflow agent runs in the session's working
  // directory, which is often not the worktree its task is about, and a lookup
  // without "root" then searches the wrong tree with nothing else to show it.
  emit(`# lookup in ${root} over ${files.length} file(s) under: ${(req.scope && req.scope.length ? req.scope : ["spec"]).join(", ")}${req.paths ? " (paths: " + req.paths.join(", ") + ")" : ""}`);

  if (asked > MAX_BUDGET) emit(`Output is capped at ${MAX_BUDGET} characters (maxChars ${asked} asked). Passages that do not fit are listed by location; fetch them with "lines".`);
  // "lines" returns named line ranges verbatim: the follow-up for a passage that
  // an earlier call listed without text, several ranges to a call.
  for (const spec of lineReqs) {
    const m = /^(.+?):(\d+)(?:-(\d+))?$/.exec(spec.trim());
    if (!m) {
      emit(`\n## lines ${spec}: expected "path:from-to"`);
      continue;
    }
    const from = Number(m[2]);
    const to = Math.max(from, Number(m[3] || m[2]));
    let t = texts.get(m[1]);
    if (t === undefined) {
      try {
        t = readFileSync(join(root, m[1]), "utf8");
      } catch {
        emit(`\n## lines ${spec}: no such file`);
        continue;
      }
    }
    const ls = t.split("\n");
    const body = ls.slice(from - 1, to).join("\n");
    const bl = blocksOf.get(m[1]) || (m[1].endsWith(".md") ? mdBlocks(m[1], t) : []);
    const h = [...bl].reverse().find((b) => b.isHeading && b.from <= from);
    emit(`\n## lines ${m[1]}:${from}-${Math.min(to, ls.length)}${h ? `  [${h.heading}]` : ""}`);
    const room = Math.floor(budget - used - 200);
    if (body.length <= room) emit(body);
    else {
      emit(body.slice(0, Math.max(0, room)));
      emit(`[truncated: ${body.length - Math.max(0, room)} characters not shown; ask for fewer lines per call.]`);
    }
  }

  if (sections.length) {
    for (const s of sections) {
      const num = s.replace(/^§\s*/, "").trim();
      const re = new RegExp(`^${num.replace(/\./g, "\\.")}(\\s|$)`);
      let found = false;
      for (const [f, bl] of blocksOf) {
        const i = bl.findIndex((b) => b.isHeading && re.test(b.heading));
        if (i === -1) continue;
        found = true;
        let j = i + 1;
        while (j < bl.length && !(bl[j].isHeading && bl[j].level <= bl[i].level)) j++;
        const lines = texts.get(f).split("\n");
        const endLine = j < bl.length ? bl[j].from - 1 : lines.length;
        const body = lines.slice(bl[i].from - 1, endLine).join("\n");
        emit(`\n## section ${num}: ${f}:${bl[i].from}-${endLine}  [${bl[i].heading}]`);
        if (used + body.length < budget) emit(body);
        else {
          const room = Math.max(0, Math.floor(budget - used - 200));
          emit(body.slice(0, room));
          emit(`\n[truncated: section is ${body.length} characters; ${body.length - room} not shown. Raise "maxChars" or ask for a subsection.]`);
        }
      }
      if (!found) emit(`\n## section ${num}: no heading starting with "${num}" in scope`);
    }
  }
  // What the sections left is shared evenly among the searches.
  base = used;

  for (const term of exact) {
    const { label, re } = matcher(term);
    const hits = [];
    for (const [f, t] of texts) {
      if (!re.test(t)) continue;
      const lines = t.split("\n");
      const hitLines = [];
      for (let i = 0; i < lines.length; i++) if (re.test(lines[i])) hitLines.push(i + 1);
      if (blocksOf.has(f)) {
        for (const b of blocksOf.get(f)) {
          const n = hitLines.filter((x) => x >= b.from && x <= b.to).length;
          if (n) hits.push({ ...b, n });
        }
      } else {
        for (const b of codeBlocksForHits(f, t, hitLines))
          hits.push({ ...b, n: hitLines.filter((x) => x >= b.from && x <= b.to).length });
      }
    }
    const total = hits.reduce((a, h) => a + h.n, 0);
    emit(`\n## exact: ${label} — ${total} occurrence(s) in ${hits.length} passage(s), ${new Set(hits.map((h) => h.file)).size} file(s)`);
    if (!hits.length) {
      done++;
      continue;
    }
    let show = hits;
    let judged = null;
    if (req.context && hits.length > 3 && jev.usable()) {
      judged = await judge(jev, hits, contextQuestion(typeof term === "string" ? term : term.pattern, req.context));
      if (jev.usable()) {
        hits.forEach((h, i) => (h.p = judged.probs[i]));
        show = hits.filter((h) => h.p === null || h.p >= NARROW);
        emit(`Narrowed by context with Jev: ${show.length} of ${hits.length} passage(s) printed. Every passage is listed below either way.${judged.failed ? ` ${judged.failed} passage(s) could not be judged and are printed.` : ""}`);
      }
    }
    if (req.context && hits.length > 3 && !jev.usable()) {
      jevMissed = true;
      emit(`Context given but Jev is unavailable (${jev.reason}), so nothing was narrowed; every passage is printed while the budget lasts.`);
    }
    const shown = new Set(show);
    const skipped = [];
    for (const h of hits) {
      const head = `\n### ${where(h)}${h.p != null ? `  (relevance ${h.p.toFixed(2)})` : ""}`;
      const body = render(h, re);
      if (shown.has(h) && fits(head + "\n" + body)) emit(head + "\n" + body);
      else skipped.push(h);
    }
    if (skipped.length) {
      emit(`\nListed without text (${skipped.length}):`);
      for (const h of skipped)
        emit(`- ${where(h)}${h.p != null ? `  (relevance ${h.p.toFixed(2)})` : ""}${shown.has(h) ? "  (output budget reached)" : ""}`);
    }
    done++;
  }

  if (meaning.length) {
    const all = [];
    for (const bl of blocksOf.values()) for (const b of bl) if (!b.isHeading && b.text.trim().length > 40) all.push(b);
    for (const m of meaning) if (!m || typeof m.query !== "string" || !m.query.trim()) usage("each meaning entry needs a query");
    // A small scope is judged whole; so is any scope when the caller asks. Without
    // a key there is no judge, so the shortlists are all there is to show.
    const whole = jev.usable() ? req.exhaustive === true || (req.exhaustive !== false && all.length <= JUDGE_ALL_BELOW) : false;
    // One embedding run serves every query in the call.
    const emb = whole || !all.length ? null : embedShortlist(all, meaning.map((m) => m.query), SHORTLIST);
    if (emb && emb.reason) emit(`\nEmbedding shortlist unavailable (${emb.reason}); meaning search used word overlap only.`);
    if (emb && emb.note) emit(`\nEmbedding shortlist partial (${emb.note}); the word-overlap shortlist is complete.`);
    else if (emb && emb.lists && emb.embedded) emit(`\nEmbedding shortlist: ${emb.embedded} of ${all.length} passage(s) embedded by this lookup, the rest read from the cache.`);
    meaning.forEach((m, mi) => {
      m.ctx = m.context || req.context || "";
      if (whole) m.cands = all.map((b) => ({ block: b, via: "all" }));
      else {
        const byBlock = new Map();
        for (const b of shortlist(all, m.query + " " + m.ctx, SHORTLIST)) byBlock.set(b, "words");
        if (emb && emb.lists) for (const { block } of emb.lists[mi]) byBlock.set(block, byBlock.has(block) ? "both" : "embedding");
        m.cands = [...byBlock].map(([block, via]) => ({ block, via }));
      }
    });
    // Judge every query's candidates in one pass, one request per distinct passage.
    let judgedFailed = 0;
    if (jev.usable()) {
      const byBlock = new Map();
      meaning.forEach((m, mi) => {
        const q = meaningQuestion(m.query, m.ctx);
        for (const c of m.cands) {
          if (!byBlock.has(c.block)) byBlock.set(c.block, {});
          byBlock.get(c.block)["q" + mi] = q;
        }
      });
      const items = [...byBlock].map(([block, questions]) => ({ block, questions }));
      const { answers, failed } = await judgeMany(jev, items);
      judgedFailed = failed;
      const at = new Map(items.map((it, i) => [it.block, answers[i]]));
      meaning.forEach((m, mi) => (m.probs = m.cands.map((c) => at.get(c.block)?.["q" + mi] ?? null)));
    }
    for (const m of meaning) {
      const ctx = m.ctx;
      const cands = m.cands;
      emit(`\n## meaning: ${m.query}${ctx ? `  (context: ${ctx})` : ""}`);
      if (!cands.length) {
        emit("No candidate passages in scope.");
        done++;
        continue;
      }
      const n = (v) => cands.filter((c) => c.via === v).length;
      const mix = whole ? "every passage in scope" : `${n("words")} by word overlap, ${n("embedding")} by embedding, ${n("both")} by both, of ${all.length} in scope`;
      // A breaker that opened during the pass leaves a partial ranking, so the
      // query is shown as its unjudged shortlist instead.
      if (!jev.usable()) {
        jevMissed = true;
        // A scope that was to be judged whole has no shortlist, so one is made here.
        const shown = whole ? shortlist(all, m.query + " " + ctx, SHORTLIST).map((block) => ({ block, via: "words" })) : cands;
        const from = whole ? `${shown.length} by word overlap, of ${all.length} in scope` : mix;
        emit(`Jev is unavailable (${jev.reason}), so these are shortlisted candidates (${from}), unjudged and unranked. They are leads, not matches, and the shortlist is not complete: search with exact terms and read the passages yourself.`);
        // Without a judge there is no common score, so the two lists are interleaved.
        const order = [...shown.filter((c) => c.via === "both"), ...shown.filter((c) => c.via === "embedding"), ...shown.filter((c) => c.via === "words")];
        for (const { block: b, via } of order.slice(0, 16)) {
          const head = `\n### ${where(b)}  (shortlisted by ${via})`;
          if (fits(head + "\n" + render(b))) emit(head + "\n" + render(b));
          else emit(`- ${where(b)}  (shortlisted by ${via}; output budget reached)`);
        }
        done++;
        continue;
      }
      const probs = m.probs;
      const failed = probs.filter((p) => p === null).length && judgedFailed ? probs.filter((p) => p === null).length : 0;
      records.push({ query: m.query, context: ctx, inScope: all.length, candidates: cands.map((c, i) => ({ file: c.block.file, from: c.block.from, to: c.block.to, heading: c.block.heading, via: c.via, p: probs[i], text: c.block.text })) });
      const ranked = cands
        .map((c, i) => ({ ...c.block, via: c.via, p: probs[i] }))
        .filter((b) => b.p !== null && b.p >= RELEVANT)
        .sort((a, b) => b.p - a.p);
      emit(`Jev judged ${cands.length} passage(s) (${mix}); ${ranked.length} matched.${failed ? ` ${failed} could not be judged.` : ""} ${whole ? "Every passage in scope was judged, so a passage missing here was rated below the threshold." : 'This is a ranking over a shortlist, not a complete list; narrow "paths" or set "exhaustive": true to judge every passage.'}`);
      const skipped = [];
      for (const b of ranked) {
        const head = `\n### ${where(b)}  (match ${b.p.toFixed(2)}${b.via === "all" ? "" : ", shortlisted by " + b.via})`;
        if (fits(head + "\n" + render(b))) emit(head + "\n" + render(b));
        else skipped.push(b);
      }
      if (skipped.length) {
        emit(`\nListed without text (${skipped.length}, output budget reached):`);
        for (const b of skipped) emit(`- ${where(b)}  (match ${b.p.toFixed(2)})`);
      }
      done++;
    }
  }

  const relatedRecords = [];
  if (related.length) {
    const all = [];
    for (const bl of blocksOf.values()) for (const b of bl) if (!b.isHeading && b.text.trim().length > 40) all.push(b);
    const bySlug = linkIndex(blocksOf);
    const ctx = { all, blocksOf, bySlug, jev, root, tune: req.format === "json" };
    for (const r of related) {
      const res = await relatedSearch(r, ctx);
      relatedRecords.push({
        text: r.text,
        change: r.change || "",
        home: res.home ? { file: res.home.file, from: res.home.from, to: res.home.to } : null,
        notes: res.notes,
        candidates: res.list.map((c) => ({ file: c.block.file, from: c.block.from, to: c.block.to, heading: c.block.heading, via: c.via, p: c.p ?? null, p0: c.p0 ?? null, text: c.block.text })),
      });
      emit(`\n## related: ${r.text.replace(/\s+/g, " ").slice(0, 120)}${r.text.length > 120 ? " [...]" : ""}`);
      if (res.home) emit(`Changed passage: ${where(res.home)}`);
      emit(`Candidate sources: ${res.sources.join(", ")}${res.notes.length ? ` (${res.notes.join("; ")})` : ""}.`);
      if (!res.judged) {
        if (res.unjudgedReason) {
          jevMissed = true;
          emit(`Jev is unavailable (${res.unjudgedReason}), so these ${res.list.length} candidates are unranked leads, unjudged and in no order of relevance. Read each one, and search by exact identifiers and paraphrases yourself; this list is not a sweep.`);
        } else emit(`Judging was turned off ("judge": false), so these ${res.list.length} candidates are unjudged.`);
        for (const c of res.list.slice(0, 30)) emit(`- ${where(c.block)}  (via ${c.via.join("+")})`);
        done++;
        continue;
      }
      const min = Number(r.min) > 0 ? Number(r.min) : Number(req.min) > 0 ? Number(req.min) : RELATED_MIN;
      const ranked = res.list.filter((c) => c.p !== null && c.p >= min).sort((a, b) => b.p - a.p);
      emit(`Jev judged ${res.list.length} candidate(s); ${ranked.length} state or depend on the changed text (threshold ${min}).${res.failed ? ` ${res.failed} could not be judged.` : ""} This is a ranking over the candidates those sources found, not a proof that nothing else depends on it.`);
      const skipped = [];
      for (const c of ranked) {
        const head = `\n### ${where(c.block)}  (related ${c.p.toFixed(2)}, via ${c.via.join("+")})`;
        if (fits(head + "\n" + render(c.block))) emit(head + "\n" + render(c.block));
        else skipped.push(c);
      }
      if (skipped.length) {
        emit(`\nListed without text (${skipped.length}, output budget reached):`);
        for (const c of skipped) emit(`- ${where(c.block)}  (related ${c.p.toFixed(2)})`);
      }
      done++;
    }
  }

  // "format": "json" returns every judged candidate with its probability, matched
  // or not. It exists for measuring and tuning the search, not for reviewers.
  // The marker goes right under the header line, so an agent reading only the
  // top of the output sees that the Jev-backed parts are unjudged.
  const marker = jevMissed ? `${JEV_MARKER} (${jev.reason}): meaning/related results are unjudged shortlists; use grep/exact search and read the passages yourself. Exact search, sections, and lines are unaffected and complete.` : null;
  if (marker) out.splice(1, 0, marker);
  if (req.format === "json") process.stdout.write(JSON.stringify({ jevUnavailable: marker, meaning: records, related: relatedRecords }) + "\n");
  else process.stdout.write(out.join("\n") + "\n");
}

main().catch((e) => {
  process.stderr.write(`lookup.mjs: ${e.stack || e.message}\n`);
  process.exit(1);
});
