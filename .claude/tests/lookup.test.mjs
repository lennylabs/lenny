// Tests for .claude/tools/lookup.mjs: the parts that need no network.
//
// Exact search, section fetch, and the output budget run against a small
// repository built in a temporary directory. Meaning search is not exercised
// here: it calls Jev and an embedding model, and a test that depends on either
// would fail for reasons unrelated to the tool. The embedding helper's
// concurrency paths that need no model (the bounded lock wait, request-file
// cleanup, and cache pruning) run when uv is installed and are skipped otherwise.
import { execFileSync, spawn, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, utimesSync, writeFileSync } from "node:fs";
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

  await embeddingChecks(root);
} finally {
  rmSync(root, { recursive: true, force: true });
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
  if (r.stdout.includes("No TypeSafe key") || r.stdout.includes("Jev judged")) {
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
