// Tests for .claude/tools/lookup.mjs: the parts that need no network.
//
// Exact search, section fetch, and the output budget run against a small
// repository built in a temporary directory. Meaning search is not exercised
// here: it calls Jev and an embedding model, and a test that depends on either
// would fail for reasons unrelated to the tool.
import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
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

  r = run({});
  check("an empty request is a usage error", r.status === 2);
  r = run({ exact: ["x"], scope: ["nope"] });
  check("an unknown scope is a usage error", r.status === 2 && r.stderr.includes("unknown scope"));
} finally {
  rmSync(root, { recursive: true, force: true });
}

if (failed) {
  console.log(`lookup: ${failed} check(s) failed, ${passed} passed.`);
  process.exit(1);
}
console.log(`lookup: all ${passed} check(s) passed.`);
