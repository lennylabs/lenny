// Parse-check a workflow script the way the runtime does: the body is wrapped in
// an async function, so a top-level `return` is legal and `export const meta` is
// stripped first. `node --check` treats the file as a module and misses errors
// that only surface in that wrapping, which shipped a broken script once.
import { readFileSync } from "fs";
let bad = 0;
for (const f of process.argv.slice(2)) {
  const src = readFileSync(f, "utf8").replace(/^export\s+const\s+meta/m, "const meta");
  try {
    new Function("args", "agent", "parallel", "pipeline", "phase", "log", "workflow", "budget",
      "return (async () => {\n" + src + "\n})();");
    console.log("PARSE OK   " + f);
  } catch (e) {
    console.log("PARSE FAIL " + f + " :: " + e.message.split("\n")[0]);
    bad++;
  }
}
process.exit(bad ? 1 : 0);
