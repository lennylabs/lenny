// Tests for .claude/tools/lookup.mjs: the parts that need no network.
//
// Exact search, section fetch, and the output budget run against a small
// repository built in a temporary directory. Meaning search never reaches the
// real Jev or the embedding model here: the Jev checks point LOOKUP_JEV_URL at a
// stub server in this process and keep the scope small enough to be judged
// whole, so no embedding runs. The embedding helper's
// concurrency paths that need no model (the bounded lock wait, request-file
// cleanup, and cache pruning) run when uv is installed and are skipped otherwise.
import { execFileSync, spawn, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { homedir, tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const TOOL = join(dirname(fileURLToPath(import.meta.url)), "..", "tools", "lookup.mjs");
let passed = 0;
let failed = 0;
function check(name, ok, detail) {
  if (ok) passed++;
  else {
    failed++;
    console.log(`  FAIL  ${name}${detail ? "\n        " + detail : ""}`);
  }
}

const root = mkdtempSync(join(tmpdir(), "lookup-test-"));
try {
  execFileSync("git", ["init", "-q"], { cwd: root });
  mkdirSync(join(root, "spec"));
  mkdirSync(join(root, "pkg", "gw"), { recursive: true });
  writeFileSync(
    join(root, "spec", "07_sessions.md"),
    [
      "# 7. Sessions",
      "",
      "## 7.1 Limits",
      "",
      "A session ends when `maxIdle` elapses with no client activity.",
      "",
      "| Setting | Default |",
      "|:--|:--|",
      "| `maxIdle` | 900s |",
      "| `maxAge` | 7200s |",
      "",
      "- First item mentions maxAge only.",
      "- Second item mentions `maxIdle` again.",
      "",
      "## 7.2 Resume",
      "",
      "Resume is unrelated text.",
      "",
      "### 7.2.1 Detail",
      "",
      "Nested detail under resume.",
      "",
      "## 7.3 Other",
      "",
      "```",
      "# not a heading: maxIdle inside a fence",
      "```",
      "",
    ].join("\n"),
  );
  writeFileSync(join(root, "pkg", "gw", "limits.go"), ["package gw", "", "// MaxIdle is the idle bound.", "const MaxIdle = 900", ""].join("\n"));

  const run = (req) => spawnSync("node", [TOOL, "--json", JSON.stringify(req)], { cwd: root, encoding: "utf8" });

  let r = run({ exact: ["maxIdle"] });
  check("exact search exits 0", r.status === 0, r.stderr);
  check("it counts every occurrence", /4 occurrence\(s\) in 4 passage\(s\), 1 file\(s\)/.test(r.stdout), r.stdout);
  check("a paragraph hit prints under its heading", r.stdout.includes("spec/07_sessions.md:5  [7.1 Limits]"));
  check("a table row prints with its header row", r.stdout.includes("| Setting | Default |\n| `maxIdle` | 900s |"));
  check("a list is split so only the matching item prints", r.stdout.includes("- Second item mentions") && !r.stdout.includes("- First item mentions"));
  check("a fenced line starting with # is not read as a heading", r.stdout.includes("[7.3 Other]") && !r.stdout.includes("[not a heading"));

  r = run({ exact: [{ pattern: "max(idle|age)", regex: true, ignoreCase: true }], scope: ["spec", "pkg"] });
  check("regex search spans scopes in the order given", r.stdout.indexOf("spec/07_sessions.md") < r.stdout.indexOf("pkg/gw/limits.go"), r.stdout);
  check("a code hit prints a line window", r.stdout.includes("const MaxIdle = 900"));

  r = run({ exact: ["absentTerm"] });
  check("a term with no hits reports zero", /absentTerm — 0 occurrence\(s\)/.test(r.stdout));

  r = run({ sections: ["7.2", "9.9"] });
  check("a section returns its subsections", r.stdout.includes("Resume is unrelated text.") && r.stdout.includes("Nested detail under resume."));
  check("a section stops at the next heading of its level", !r.stdout.includes("## 7.3 Other"));
  check("a missing section is reported", r.stdout.includes('no heading starting with "9.9"'));

  r = run({ exact: ["maxIdle"], maxChars: 150 });
  check("a hit that does not fit the budget is still listed by location", r.stdout.includes("Listed without text") && r.stdout.includes("output budget reached"), r.stdout);

  r = run({ exact: ["MaxIdle"], paths: ["pkg/gw/limits.go"] });
  check("paths reaches a single file outside the named scopes", /2 occurrence\(s\)/.test(r.stdout) && r.stdout.includes("pkg/gw/limits.go"), r.stdout);

  r = run({ lines: ["spec/07_sessions.md:16-17", "pkg/gw/limits.go:4", "spec/none.md:1-2", "garbage"] });
  check("lines returns a range verbatim under its heading", r.stdout.includes("## lines spec/07_sessions.md:16-17  [7.2 Resume]") && r.stdout.includes("Resume is unrelated text."), r.stdout);
  check("lines reads a file outside the scope", r.stdout.includes("const MaxIdle = 900"));
  check("lines reports a missing file and a malformed entry", r.stdout.includes("no such file") && r.stdout.includes('expected "path:from-to"'));

  r = run({ exact: ["maxIdle"], maxChars: 900000 });
  check("an oversized budget is capped and the cap is stated", r.stdout.includes("Output is capped at 26000 characters"));

  check("the header names the absolute tree searched", r.stdout.startsWith(`# lookup in ${root} over `), r.stdout.split("\n")[0]);

  r = run({});
  check("an empty request is a usage error", r.status === 2);
  r = spawnSync("node", [TOOL, "--prune-cache", "zero"], { cwd: root, encoding: "utf8" });
  check("--prune-cache without a day count is a usage error", r.status === 2 && r.stderr.includes("--prune-cache"));
  r = run({ exact: ["x"], scope: ["nope"] });
  check("an unknown scope is a usage error", r.status === 2 && r.stderr.includes("unknown scope"));

  await jevChecks(root);
  await embeddingChecks(root);
} finally {
  rmSync(root, { recursive: true, force: true });
}

// The Jev breakers, against a stub endpoint whose behavior each check sets.
// spec/20_many.md holds enough passages that a breaker that failed to open would
// send far more requests than the bounds below allow.
async function jevChecks(root) {
  const many = ["# 20. Many", ""];
  for (let i = 0; i < 200; i++) many.push(`## 20.${i + 1} Item ${i}`, "", `Passage ${i} says a session with no client activity is suspended after the idle bound.`, "");
  writeFileSync(join(root, "spec", "20_many.md"), many.join("\n"));

  let mode = "ok";
  let hits = 0;
  const server = createServer((req, res) => {
    let body = "";
    req.on("data", (d) => (body += d));
    req.on("end", () => {
      hits++;
      if (mode === "hang") return; // never answers
      if (mode === "500") return res.writeHead(503).end("overloaded");
      if (mode === "401") return res.writeHead(401).end("bad key");
      if (mode === "garbage") return res.writeHead(200, { "Content-Type": "application/json" }).end("<html>not json</html>");
      const qs = JSON.parse(body).questions || {};
      res.writeHead(200, { "Content-Type": "application/json" }).end(JSON.stringify({ answers: Object.fromEntries(Object.keys(qs).map((id) => [id, { noul: 0.9 }])) }));
    });
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const url = `http://127.0.0.1:${server.address().port}/v1/systemone`;
  let cacheN = 0;
  const freshCache = () => {
    const d = join(root, `jev-cache-${++cacheN}`);
    mkdirSync(d, { recursive: true });
    return d;
  };
  const envFor = (cache, extra = {}) => ({
    ...process.env,
    TYPESAFE_API_KEY: "test-key",
    LOOKUP_JEV_URL: url,
    LOOKUP_CACHE_DIR: cache,
    LOOKUP_JEV_CONCURRENCY: "4",
    LOOKUP_JEV_TIMEOUT: "2",
    LOOKUP_JEV_COOLDOWN: "600",
    ...extra,
  });
  const runJev = (req, env) =>
    new Promise((res) => {
      const t0 = Date.now();
      const c = spawn("node", [TOOL, "--json", JSON.stringify(req)], { cwd: root, env });
      let stdout = "";
      let stderr = "";
      c.stdout.on("data", (d) => (stdout += d));
      c.stderr.on("data", (d) => (stderr += d));
      c.on("close", (status) => res({ status, stdout, stderr, ms: Date.now() - t0 }));
    });
  const MEANING = { meaning: [{ query: "a session with no client activity is suspended after the idle bound" }], paths: ["spec/20_many.md"] };
  const state = (cache) => {
    try {
      return JSON.parse(readFileSync(join(cache, "jev-state.json"), "utf8"));
    } catch {
      return null;
    }
  };
  const line2 = (out) => out.split("\n")[1] || "";

  try {
    // A healthy endpoint judges, prints no marker, and records nothing.
    let cache = freshCache();
    mode = "ok";
    hits = 0;
    let r = await runJev(MEANING, envFor(cache));
    check("jev: a healthy endpoint judges the passages", r.status === 0 && r.stdout.includes("Jev judged 200 passage(s)") && hits === 200, `${hits} ${r.stdout.slice(0, 400)}${r.stderr}`);
    check("jev: a healthy call prints no marker and records no state", !r.stdout.includes("JEV UNAVAILABLE") && !existsSync(join(cache, "jev-state.json")));

    // A 5xx storm opens the per-call breaker after a few requests, well before
    // the 200 passages, and the call ends in seconds rather than minutes.
    cache = freshCache();
    mode = "500";
    hits = 0;
    r = await runJev(MEANING, envFor(cache));
    check("jev 5xx: the call still exits 0", r.status === 0, r.stderr);
    check("jev 5xx: the marker is the line under the header", line2(r.stdout).startsWith("JEV UNAVAILABLE (3 Jev requests in a row failed") && r.stdout.startsWith("# lookup in "), r.stdout.slice(0, 400));
    // Each of the 4 workers may send one request with its retries, and the two
    // workers whose failures come before the third may each start one more.
    check("jev 5xx: the breaker stops sending after a bounded number of requests", hits <= (4 + 2) * 3, `${hits} requests`);
    check("jev 5xx: the call takes seconds", r.ms < 10000, `${r.ms} ms`);
    check("jev 5xx: the shortlist is printed unjudged", r.stdout.includes("unjudged and unranked") && r.stdout.includes("shortlisted by words"), r.stdout.slice(0, 800));
    let s = state(cache);
    check("jev 5xx: the outage is recorded with a cooldown", s && s.down === true && s.auth === false && s.until - s.since === 600000, JSON.stringify(s));

    // A 401 trips on the first answer and is recorded as an auth failure.
    cache = freshCache();
    mode = "401";
    hits = 0;
    r = await runJev(MEANING, envFor(cache));
    check("jev 401: the marker names the rejected key", line2(r.stdout).startsWith("JEV UNAVAILABLE (Jev rejected the TypeSafe key (HTTP 401))"), line2(r.stdout));
    check("jev 401: no request is sent after the first answers", hits <= 4, `${hits} requests`);
    s = state(cache);
    check("jev 401: the shared state records an auth failure keyed to the key", s && s.down && s.auth === true && /^[0-9a-f]{16}$/.test(s.keyHash), JSON.stringify(s));

    // During the cooldown the next call skips Jev without sending anything.
    mode = "ok";
    hits = 0;
    r = await runJev(MEANING, envFor(cache));
    check("jev cooldown: the next call sends no request", hits === 0, `${hits} requests`);
    check("jev cooldown: it says it skipped and until when", line2(r.stdout).startsWith("JEV UNAVAILABLE (Jev rejected the TypeSafe key (HTTP 401); recorded by an earlier lookup at") && line2(r.stdout).includes("skipped until"), line2(r.stdout));

    // Exact search, sections, and lines never ask Jev, so a down state adds no marker.
    r = await runJev({ exact: ["maxIdle"], sections: ["7.2"] }, envFor(cache));
    check("jev cooldown: an exact-only request is complete and carries no marker", !r.stdout.includes("JEV UNAVAILABLE") && /4 occurrence\(s\)/.test(r.stdout) && hits === 0, r.stdout.slice(0, 300));
    r = await runJev({ exact: ["Passage"], context: "idle suspension", paths: ["spec/20_many.md"] }, envFor(cache));
    check("jev cooldown: context narrowing reports nothing was narrowed under the marker", line2(r.stdout).startsWith("JEV UNAVAILABLE") && r.stdout.includes("so nothing was narrowed") && hits === 0, r.stdout.slice(0, 500));
    r = await runJev({ related: [{ text: "Passage 3 says a session with no client activity is suspended after the idle bound.", change: "suspension becomes termination", via: ["terms", "sentidx"] }], paths: ["spec/20_many.md"] }, envFor(cache));
    check("jev cooldown: related candidates are reported as unranked leads", line2(r.stdout).startsWith("JEV UNAVAILABLE") && r.stdout.includes("unranked leads") && hits === 0, r.stdout.slice(0, 600));

    // A changed key is tried at once despite an auth failure on the old one.
    r = await runJev(MEANING, envFor(cache, { TYPESAFE_API_KEY: "another-key" }));
    check("jev auth: a changed key is tried without waiting for the cooldown", hits > 0 && !r.stdout.includes("JEV UNAVAILABLE"), `${hits} ${line2(r.stdout)}`);
    check("jev auth: its success clears the recorded outage", state(cache) === null, JSON.stringify(state(cache)));

    // After the cooldown, while another lookup holds the probe, a call keeps skipping.
    cache = freshCache();
    writeFileSync(join(cache, "jev-state.json"), JSON.stringify({ down: true, reason: "earlier outage", auth: false, since: Date.now() - 700000, until: Date.now() - 100000 }));
    writeFileSync(join(cache, "jev-probe"), "");
    hits = 0;
    r = await runJev(MEANING, envFor(cache));
    check("jev probe: a call skips while another lookup is probing", hits === 0 && line2(r.stdout).includes("another lookup is probing Jev now"), line2(r.stdout));
    // A probe claim left by a dead lookup goes stale and is taken over.
    const old = new Date(Date.now() - 600000);
    utimesSync(join(cache, "jev-probe"), old, old);
    r = await runJev(MEANING, envFor(cache));
    check("jev probe: a stale probe claim is taken over and the recovery clears the state", hits === 200 && !r.stdout.includes("JEV UNAVAILABLE") && state(cache) === null && !existsSync(join(cache, "jev-probe")), `${hits} ${line2(r.stdout)} ${JSON.stringify(state(cache))}`);

    // A failed probe records a new cooldown and releases its claim.
    writeFileSync(join(cache, "jev-state.json"), JSON.stringify({ down: true, reason: "earlier outage", auth: false, since: Date.now() - 700000, until: Date.now() - 100000 }));
    mode = "500";
    r = await runJev(MEANING, envFor(cache));
    s = state(cache);
    check("jev probe: a failed probe records a new cooldown and releases the claim", s && s.until > Date.now() && !existsSync(join(cache, "jev-probe")) && line2(r.stdout).startsWith("JEV UNAVAILABLE (3 Jev"), `${JSON.stringify(s)} ${line2(r.stdout)}`);

    // A hanging endpoint is cut off by the request timeout.
    cache = freshCache();
    mode = "hang";
    hits = 0;
    r = await runJev(MEANING, envFor(cache, { LOOKUP_JEV_TIMEOUT: "1" }));
    check("jev hang: the request timeout opens the breaker", line2(r.stdout).includes("timed out after 1s") && r.status === 0, line2(r.stdout) + r.stderr);
    check("jev hang: the call ends near the timeout", r.ms < 8000, `${r.ms} ms`);
    check("jev hang: no more than one round of requests is sent", hits <= 4 + 2, `${hits} requests`);

    // A 200 response that is not the expected JSON counts as a failure.
    cache = freshCache();
    mode = "garbage";
    hits = 0;
    r = await runJev(MEANING, envFor(cache));
    check("jev malformed: an unreadable response opens the breaker", line2(r.stdout).includes("not JSON") && hits <= 4 + 2, `${hits} ${line2(r.stdout)}`);

    // Concurrent lookups that all trip write one whole state and leave no temporary files.
    cache = freshCache();
    mode = "500";
    const rs = await Promise.all(Array.from({ length: 8 }, () => runJev(MEANING, envFor(cache))));
    check("jev concurrent: every lookup exits 0 with the marker", rs.every((x) => x.status === 0 && line2(x.stdout).startsWith("JEV UNAVAILABLE")), rs.map((x) => x.status + line2(x.stdout).slice(0, 40)).join(" | "));
    s = state(cache);
    check("jev concurrent: the state file is whole JSON", s && s.down === true, readdirSync(cache).join(","));
    check("jev concurrent: no temporary state file is left", readdirSync(cache).every((f) => !f.endsWith(".tmp")), readdirSync(cache).join(","));

    // Without a key the shortlist is unjudged under the same marker.
    cache = freshCache();
    hits = 0;
    r = await runJev(MEANING, envFor(cache, { TYPESAFE_API_KEY: "" }));
    check("jev no key: the marker names the missing key and nothing is sent", line2(r.stdout).startsWith("JEV UNAVAILABLE (no TypeSafe key is set): meaning/related results are unjudged shortlists") && hits === 0, line2(r.stdout));
    check("jev no key: no state is recorded", !existsSync(join(cache, "jev-state.json")));
  } finally {
    server.closeAllConnections();
    server.close();
    rmSync(join(root, "spec", "20_many.md"), { force: true });
  }
}

// The embedding helper's paths that need no model. Each runs against a cache
// directory of its own through LOOKUP_CACHE_DIR, so the shared cache is untouched.
async function embeddingChecks(root) {
  const uv = ["uv", join(homedir(), ".local", "bin", "uv")].find((u) => spawnSync(u, ["--version"]).status === 0);
  const sqlite = spawnSync("python3", ["-c", "import sqlite3"]).status === 0;
  if (!uv || !sqlite || spawnSync("flock", ["--version"]).status !== 0) {
    console.log("  SKIP  embedding checks: uv, python3, or flock is not installed");
    return;
  }
  const cache = join(root, "cache");
  mkdirSync(join(cache, "requests"), { recursive: true });
  const env = { ...process.env, LOOKUP_CACHE_DIR: cache, LOOKUP_UV: uv, LOOKUP_EMBED_LOCK_WAIT: "1", TYPESAFE_API_KEY: "" };
  const runAsync = (args) =>
    new Promise((res) => {
      const c = spawn("node", [TOOL, ...args], { cwd: root, env });
      let stdout = "";
      c.stdout.on("data", (d) => (stdout += d));
      c.on("close", (status) => res({ status, stdout }));
    });
  const db = (sql) => execFileSync("python3", ["-c", `import sqlite3,sys,time\ndb=sqlite3.connect(sys.argv[1])\n${sql}\ndb.commit()`, join(cache, "embeddings.sqlite")], { encoding: "utf8" });

  // A lookup killed mid-run leaves its request file; the next lookup removes
  // any older than an hour and leaves its own directory empty when it ends.
  const stale = join(cache, "requests", "request-1-1.json");
  writeFileSync(stale, "{}");
  utimesSync(stale, new Date(Date.now() - 7_200_000), new Date(Date.now() - 7_200_000));
  const fresh = join(cache, "requests", "request-2-2.json");
  writeFileSync(fresh, "{}");

  // Another process holds the embedding lock for longer than the wait bound.
  // The lookup gives up on the embedding half after the bound, says why, and
  // still answers from the word-overlap shortlist.
  const holder = spawn("flock", [join(cache, "embeddings.sqlite.lock"), "sleep", "20"], { stdio: "ignore" });
  await new Promise((res) => setTimeout(res, 300));
  const t0 = Date.now();
  const r = await runAsync(["--json", JSON.stringify({ meaning: [{ query: "a session ends when it has been idle too long" }] })]);
  const took = Date.now() - t0;
  holder.kill();
  if (r.stdout.includes("JEV UNAVAILABLE (no TypeSafe key is set)")) {
    check("a lookup behind a held embedding lock exits 0", r.status === 0, r.stdout);
    check("it reports the embedding as busy", r.stdout.includes("embedding busy"), r.stdout);
    check("it still serves the word-overlap shortlist", r.stdout.includes("shortlisted by words"), r.stdout);
    check("it stops waiting near the bound rather than behind the holder", took < 15000, `${took} ms`);
  } else check("a meaning lookup runs", false, r.stdout);
  check("a request file older than an hour is removed", !existsSync(stale));
  check("a recent request file is left for its lookup", existsSync(fresh));
  check("the lookup removes its own request file", readdirSync(join(cache, "requests")).length === 1, readdirSync(join(cache, "requests")).join(","));

  // Pruning drops rows unused for the given days. A cache written before the
  // last-used column existed is migrated with every row stamped today, so the
  // first prune after an upgrade removes nothing.
  rmSync(join(cache, "embeddings.sqlite"), { force: true });
  db("db.execute('CREATE TABLE vec (k BLOB PRIMARY KEY, v BLOB NOT NULL) WITHOUT ROWID')\nfor i in range(3): db.execute('INSERT INTO vec VALUES (?, ?)', (bytes([i]), b'x'))");
  let p = await runAsync(["--prune-cache", "30"]);
  check("a cache from before the last-used column is migrated, and nothing is pruned", p.status === 0 && p.stdout.includes("removed 0 vector(s)") && p.stdout.includes("3 kept"), p.stdout);
  db("db.execute('UPDATE vec SET used = ? WHERE k = ?', (int(time.time() // 86400) - 40, bytes([0])))");
  p = await runAsync(["--prune-cache", "30"]);
  check("a row unused past the cutoff is pruned and the rest kept", p.status === 0 && p.stdout.includes("removed 1 vector(s)") && p.stdout.includes("2 kept"), p.stdout);
}

if (failed) {
  console.log(`lookup: ${failed} check(s) failed, ${passed} passed.`);
  process.exit(1);
}
console.log(`lookup: all ${passed} check(s) passed.`);
