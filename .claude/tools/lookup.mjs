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
//
// Request (every field optional except that one of exact, meaning, sections, lines is given):
//   {
//     "scope":    ["spec"],             // any of: spec docs pkg cmd sdks charts schemas tests proposals
//     "paths":    ["spec/07_session-lifecycle.md"],   // narrows the scope to these files or directories
//     "exact":    ["maxSessionRetries", {"pattern": "idle[- ]terminat", "regex": true, "ignoreCase": true}],
//     "meaning":  [{"query": "an idle session is terminated", "context": "checking for restatements"}],
//     "sections": ["7.2", "28.5.3"],    // spec sections to return whole, by number
//     "lines":    ["spec/07_session-lifecycle.md:430-446"],   // line ranges to return verbatim
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
// Meaning search, and narrowing by context, ask Jev (TypeSafe's System One model)
// one yes/no question per passage. They need a TypeSafe key, read from
// TYPESAFE_API_KEY or from ~/.config/typesafe/api_key. Without one, meaning search
// returns its shortlist unjudged and says that it did.
//
// Exit codes: 0 ran; 1 failed; 2 usage error.

import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { homedir } from "node:os";
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
const JEV_URL = "https://api.typesafe.ai/v1/systemone";
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
  if (process.env.TYPESAFE_API_KEY) return process.env.TYPESAFE_API_KEY.trim();
  try {
    return readFileSync(join(homedir(), ".config", "typesafe", "api_key"), "utf8").trim();
  } catch {
    return "";
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
function embedShortlist(blocks, queries, top) {
  const script = join(dirname(fileURLToPath(import.meta.url)), "lookup-embed.py");
  let common;
  try {
    common = resolve(execFileSync("git", ["rev-parse", "--git-common-dir"], { encoding: "utf8" }).trim());
  } catch {
    return { reason: "not in a git repository, so there is nowhere to cache vectors" };
  }
  const dir = join(common, "lookup-cache");
  mkdirSync(dir, { recursive: true });
  const input = JSON.stringify({
    cache: join(dir, "embeddings.sqlite"),
    top,
    queries,
    passages: blocks.map((b, id) => ({ id, text: (b.heading ? b.heading + "\n" : "") + b.text })),
  });
  const r = spawnSync("uv", ["run", "--quiet", script], { input, encoding: "utf8", maxBuffer: 1 << 28, timeout: 1_500_000 });
  if (r.error) return { reason: r.error.code === "ENOENT" ? "uv is not installed" : r.error.message };
  if (r.status !== 0) return { reason: `lookup-embed.py failed: ${(r.stderr || "").trim().split("\n").pop()}` };
  try {
    const out = JSON.parse(r.stdout);
    return { model: out.model, embedded: out.embedded, lists: out.results.map((l) => l.map(([id, score]) => ({ block: blocks[id], score }))) };
  } catch {
    return { reason: "lookup-embed.py returned unreadable output" };
  }
}

async function jevAsk(key, state, questions) {
  for (let attempt = 0; ; attempt++) {
    let res;
    try {
      res = await fetch(JEV_URL, {
        method: "POST",
        headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
        body: JSON.stringify({ state, model: JEV_MODEL, questions }),
      });
    } catch (e) {
      if (attempt >= 4) throw e;
      await new Promise((r) => setTimeout(r, 400 * 2 ** attempt));
      continue;
    }
    if (res.ok) return (await res.json()).answers;
    if ((res.status === 429 || res.status === 529 || res.status >= 500) && attempt < 5) {
      const wait = Number(res.headers.get("retry-after")) * 1000 || 400 * 2 ** attempt;
      await new Promise((r) => setTimeout(r, wait));
      continue;
    }
    throw new Error(`Jev returned ${res.status}: ${(await res.text()).slice(0, 300)}`);
  }
}

// One request per passage. Jev's accuracy falls as the state fills with text
// unrelated to the question, so a passage is judged alone with its heading. Jev
// answers every question in a request against the same state, so when several
// queries shortlist one passage they travel together and the passage is sent once.
// `items` is [{ block, questions: { id: question } }]; the result maps each id to
// its probability, per item, or null where the request failed.
async function judgeMany(key, items) {
  const out = new Array(items.length).fill(null);
  let next = 0;
  let failed = 0;
  const worker = async () => {
    for (;;) {
      const i = next++;
      if (i >= items.length) return;
      const b = items[i].block;
      try {
        const a = await jevAsk(key, { section_heading: b.heading, passage: b.text.slice(0, 12000) }, items[i].questions);
        out[i] = Object.fromEntries(Object.entries(a).map(([id, v]) => [id, v.noul]));
      } catch {
        failed++;
      }
    }
  };
  await Promise.all(Array.from({ length: Math.min(JEV_CONCURRENCY, items.length) }, worker));
  return { answers: out, failed };
}

async function judge(key, blocks, question) {
  const { answers, failed } = await judgeMany(key, blocks.map((block) => ({ block, questions: { q: question } })));
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

const loc = (b) => `${b.file}:${b.from}${b.to > b.from ? "-" + b.to : ""}`;
const where = (b) => (b.heading ? `${loc(b)}  [${b.heading}]` : loc(b));

async function main() {
  const req = readRequest();
  const exact = Array.isArray(req.exact) ? req.exact : [];
  const meaning = (Array.isArray(req.meaning) ? req.meaning : []).map((m) => (typeof m === "string" ? { query: m } : m));
  const sections = Array.isArray(req.sections) ? req.sections.map(String) : [];
  if (!req.warm && !exact.length && !meaning.length && !sections.length && !(Array.isArray(req.lines) && req.lines.length))
    usage("give at least one of exact, meaning, sections, lines");
  const key = typesafeKey();
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
    const r = embedShortlist(all, [], SHORTLIST);
    process.stdout.write(r.reason ? `warm failed: ${r.reason}\n` : `warm: ${all.length} passage(s) in scope, ${r.embedded} embedded this run (${r.model})\n`);
    process.exit(r.reason ? 1 : 0);
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
  const lookups = exact.length + meaning.length || 1;
  let done = 0;
  let base = 0;
  const fits = (s) => used + s.length < base + ((budget - base) * (done + 1)) / lookups;
  emit(`# lookup over ${files.length} file(s) under: ${(req.scope && req.scope.length ? req.scope : ["spec"]).join(", ")}${req.paths ? " (paths: " + req.paths.join(", ") + ")" : ""}`);

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
    if (req.context && key && hits.length > 3) {
      judged = await judge(key, hits, contextQuestion(typeof term === "string" ? term : term.pattern, req.context));
      hits.forEach((h, i) => (h.p = judged.probs[i]));
      show = hits.filter((h) => h.p === null || h.p >= NARROW);
      emit(`Narrowed by context with Jev: ${show.length} of ${hits.length} passage(s) printed. Every passage is listed below either way.${judged.failed ? ` ${judged.failed} passage(s) could not be judged and are printed.` : ""}`);
    } else if (req.context && !key) {
      emit("Context given but no TypeSafe key is set, so nothing was narrowed.");
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
    const whole = key ? req.exhaustive === true || (req.exhaustive !== false && all.length <= JUDGE_ALL_BELOW) : false;
    // One embedding run serves every query in the call.
    const emb = whole || !all.length ? null : embedShortlist(all, meaning.map((m) => m.query), SHORTLIST);
    if (emb && emb.reason) emit(`\nEmbedding shortlist unavailable (${emb.reason}); meaning search used word overlap only.`);
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
    if (key) {
      const byBlock = new Map();
      meaning.forEach((m, mi) => {
        const q = meaningQuestion(m.query, m.ctx);
        for (const c of m.cands) {
          if (!byBlock.has(c.block)) byBlock.set(c.block, {});
          byBlock.get(c.block)["q" + mi] = q;
        }
      });
      const items = [...byBlock].map(([block, questions]) => ({ block, questions }));
      const { answers, failed } = await judgeMany(key, items);
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
      if (!key) {
        emit(`No TypeSafe key is set, so these are shortlisted candidates (${mix}), unjudged and unranked.`);
        // Without a judge there is no common score, so the two lists are interleaved.
        const order = [...cands.filter((c) => c.via === "both"), ...cands.filter((c) => c.via === "embedding"), ...cands.filter((c) => c.via === "words")];
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

  // "format": "json" returns every judged candidate with its probability, matched
  // or not. It exists for measuring and tuning the search, not for reviewers.
  if (req.format === "json") process.stdout.write(JSON.stringify({ meaning: records }) + "\n");
  else process.stdout.write(out.join("\n") + "\n");
}

main().catch((e) => {
  process.stderr.write(`lookup.mjs: ${e.stack || e.message}\n`);
  process.exit(1);
});
