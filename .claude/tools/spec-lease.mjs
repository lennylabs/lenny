// The spec/ write lease.
//
// spec/ is read-only unless a lease is open. The lease names ONE proposal, one
// checklist step, and the exact spec files that step's staged deliverables
// target, and it expires. It replaces a hook that grepped every proposal in the
// tree for the string "Status: Approved" and unlocked all of spec/ for
// everything if any one of them matched.
//
// What that bought, and why each part is here:
//
//   Independence. Only the leased proposal is consulted, so an agent working
//   on proposal A cannot write spec/ because proposal B happens to be approved.
//
//   A window that is the step, not the run. The lease is opened for one
//   spec-lane checklist step and released when that step ends, pass or fail.
//   Steps run sequentially, so no code-writing agent is ever alive while a
//   lease is held. An interleaved checklist has more windows, not weaker ones.
//
//   Fail closed. No lease, no writes. A malformed lease, an expired one, an
//   unreadable status, or a path outside the allow list are all refusals.
//
// One honest limit: the hook trusts the lease, and the lease file is not under
// spec/, so nothing stops an agent writing one. That raises the bar from "any
// approved proposal anywhere unlocks everything" to "an agent must forge a
// lease naming an Approved proposal", which is the right bar for a threat model
// of accidental writes. It is not a sandbox and is not claimed as one.
//
// Usage:
//   spec-lease.mjs check <path>          exit 0 to allow the write, 1 to block
//   spec-lease.mjs hook                  read a PreToolUse payload on stdin;
//                                        exit 0 to allow, 2 to block the call
//   spec-lease.mjs open <proposal> --step S3 --allow spec/a.md,spec/b.md
//                                        --allow is required: a lease with an
//                                        empty allow list grants nothing
//                                        [--ttl-hours 24] [--run-id X] [--now ISO]
//   spec-lease.mjs release [--step S3]   release, or release only if it is S3's
//   spec-lease.mjs status [--now ISO]    report the current lease and whether it
//                                        is still held

import { readFileSync, writeFileSync, existsSync, unlinkSync, realpathSync } from "fs";
import { resolve, relative, isAbsolute } from "path";
import { execFileSync } from "child_process";
import { fileURLToPath } from "url";
import { dirname } from "path";

const HERE = dirname(fileURLToPath(import.meta.url));
export const REPO = resolve(HERE, "..", "..");
export const LEASE = resolve(REPO, "proposals/.spec-lease.json");
const STATUS_TOOL = resolve(HERE, "proposal-status.mjs");

const DEFAULT_TTL_HOURS = 24;

/** Repo-relative, forward-slashed, for comparing against the allow list. */
export function relPath(p) {
  const abs = isAbsolute(p) ? p : resolve(REPO, p);
  return relative(REPO, realExisting(abs)).split("\\").join("/");
}

/**
 * Resolve symlinks as far as the path exists, keeping the unresolved tail.
 *
 * `resolve()` alone is pure string arithmetic, so a symlinked directory pointed
 * into the guarded tree read as outside it: an adversarial review created
 * `docs/lnk -> ../spec` and wrote `docs/lnk/28_x.md` straight through. The file
 * being written usually does not exist yet, which is why this walks up to the
 * nearest existing ancestor rather than calling realpathSync on the whole path.
 */
function realExisting(abs) {
  const parts = abs.split("/");
  for (let i = parts.length; i > 1; i--) {
    const head = parts.slice(0, i).join("/") || "/";
    try {
      const real = realpathSync(head);
      return i === parts.length ? real : real + "/" + parts.slice(i).join("/");
    } catch {
      // Not present yet; try its parent.
    }
  }
  return abs;
}

/**
 * True when `proposal` sits inside the directory holding the lease file.
 * The lease used to be trusted to name any path: a lease naming
 * "../.claude/jobs/.../proposals/0001_fix_a" resolved, its hand-written status
 * file was read, and spec/ opened. A proposal outside the directory the lease
 * sits in is under no hook and is not a proposal of this tree. The anchor is
 * the lease's own directory rather than REPO because that is `<repo>/proposals`
 * in production and the fixture's own proposals directory under test.
 */
function underLeaseDir(proposalAbs, leasePath) {
  const root = dirname(resolve(leasePath));
  const rel = relative(root, proposalAbs);
  return rel !== "" && !rel.startsWith("..") && !isAbsolute(rel);
}

/**
 * Read the lease and say whether it is still held. The expiry used to be
 * evaluated only inside decide(), so `status` printed an expired lease as
 * present with no expiry signal, and the compile gate that reads that output
 * aborted a run with "lease-leaked" over a lease that granted nothing.
 */
export function readLease(leasePath = LEASE, now = new Date()) {
  if (!existsSync(leasePath)) return { present: false };
  let raw;
  try {
    raw = JSON.parse(readFileSync(leasePath, "utf8"));
  } catch (e) {
    return { present: true, malformed: String(e.message) };
  }
  if (!raw || typeof raw !== "object" || !raw.proposal || !raw.expires) {
    return { present: true, malformed: "missing proposal or expires" };
  }
  const expired = !(new Date(raw.expires) > now);
  return { present: true, expired, held: !expired, lease: raw };
}

/**
 * Decide whether a write to `target` is allowed.
 * Every failure path returns allow:false; there is no path that returns
 * allow:true on an error, which is the property the tests pin.
 */
export function decide(target, opts = {}) {
  const leasePath = opts.leasePath || LEASE;
  const now = opts.now ? new Date(opts.now) : new Date();
  const rel = relPath(target);

  // `git checkout -- spec/` names the directory, and relative() drops the
  // trailing slash, so a bare "spec" has to be in scope too: measured, that one
  // command was the only one of the four Bash write forms in the audit that
  // still walked through a scanner delegating here.
  if (rel !== "spec" && !rel.startsWith("spec/")) {
    return { allow: true, why: "not under spec/" };
  }

  const r = readLease(leasePath, now);
  if (!r.present) {
    return { allow: false, why: "spec/ is read-only: no spec lease is open" };
  }
  if (r.malformed) {
    return { allow: false, why: "spec lease is malformed (" + r.malformed + "); refusing" };
  }
  const lease = r.lease;

  if (r.expired) {
    return {
      allow: false,
      why: "spec lease expired at " + lease.expires + "; release it or re-run the step that opened it",
    };
  }

  const proposalAbs = resolve(REPO, lease.proposal);
  if (!underLeaseDir(proposalAbs, leasePath)) {
    return {
      allow: false,
      why: "the leased proposal " + lease.proposal + " is outside " + dirname(resolve(leasePath)) + "; refusing",
    };
  }

  let status;
  try {
    status = execFileSync("node", [STATUS_TOOL, proposalAbs, "--field", "status"], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  } catch (e) {
    return { allow: false, why: "cannot read the status of " + lease.proposal + "; refusing" };
  }
  if (status !== "Approved") {
    return {
      allow: false,
      why: "the leased proposal " + lease.proposal + " is " + status + ", not Approved",
    };
  }

  // An empty allow list grants nothing. It used to grant all of spec/: the
  // guard read `allow.length > 0 && !allow.includes(rel)`, so `open <proposal>
  // --step S1` with no --allow let spec/04, spec/28 and a spec/ file that did
  // not exist yet all through, against a header that says the lease names the
  // exact files the step's deliverables target.
  const allow = Array.isArray(lease.allow) ? lease.allow.map((p) => relPath(p)) : [];
  if (allow.length === 0) {
    return {
      allow: false,
      why:
        "the spec lease for " + lease.proposal + " has an empty allow list, which grants nothing; " +
        "re-open it with --allow naming the files the step writes",
    };
  }
  if (!allow.includes(rel)) {
    return {
      allow: false,
      why: rel + " is not in the lease's allow list (" + allow.join(", ") + ")",
    };
  }

  return { allow: true, why: "leased by " + lease.proposal + (lease.step ? " step " + lease.step : "") };
}

// The four forms the audit measured as ungated when the hook matched only the
// file-editing tools: `sed -i spec/...`, `cat > spec/...`, `rm spec/...`, and
// `git checkout -- spec/`. A read (`cat spec/x`, `grep -rn x spec/`) has to stay
// allowed or the pipeline cannot read its own source, so a bare mention of a
// spec path is not a block; only a span that carries write intent is scanned.
// What has to precede a verb for it to be RUN rather than merely mentioned: the
// start of the command, or a separator, optionally behind `sudo` or an
// environment prefix. Written as a string because the spans below are built
// with RegExp so they can share it.
const CMD_POS = "(?:^|[;&|\\n(){}]|\\|\\||&&)\\s*(?:sudo\\s+)?(?:[A-Za-z_][A-Za-z0-9_]*=\\S*\\s+)*";

const WRITE_SPANS = [
  // Redirection target. The optional `|` is the noclobber override (`>|`),
  // which the previous pattern excluded along with the pipe it shares a
  // character with, so a bare shell redirect -- the very form this exists to
  // catch -- was missed.
  />>?\|?\s*[^\s;&|<>]+/g,
  // In-place edit, short (`-i`, `-pi`) and long (`--in-place`) forms. The long
  // form was unreachable: the pattern required a single dash.
  new RegExp(
    CMD_POS + "(?:sed|perl|ruby)\\b[^;&|\\n]*?(?:\\s-[A-Za-z]*i\\b|\\s--in-place\\b)[^;&|\\n]*",
    "g",
  ),
  // COMMAND POSITION, not anywhere in the string. Matching the verb as a bare
  // word blocked `grep -rn tee <path>` -- a pure read whose search TERM is a
  // write verb -- and any commit message mentioning a patch or an install.
  // A write verb is a write only when something is about to run it: at the
  // start, or after a separator, optionally behind sudo or an env prefix.
  new RegExp(CMD_POS + "(?:rm|mv|cp|tee|truncate|install|patch|shred|ln|dd|ed|ex)\\b[^;&|\\n]*", "g"),
  new RegExp(CMD_POS + "git\\s+(?:checkout|restore|apply|rm|mv|clean|stash|reset)\\b[^;&|\\n]*", "g"),
];

// A path-shaped token inside a write span: either one containing `spec/`, or a
// bare `spec` naming the directory itself.
const SPEC_TOKEN = /[^\s"'`;&|<>()]*spec\/[^\s"'`;&|<>()]*|[^\s"'`;&|<>()]*\bspec\b(?=\s|$)/g;

/**
 * The first blocking decision for any spec/ path a shell command writes to, or
 * null when the command writes to none.
 *
 * THE LIMIT, stated plainly because it is easy to over-read this guard. The
 * scan is TEXTUAL and allow-by-default: it recognises a fixed verb list and a
 * redirection pattern. An interpreter invocation (`python -c`, `node -e`,
 * `perl -e`), a target assembled from a shell variable, a write inside a script
 * this only sees the name of, or a write through a symlink into the tree, are
 * all outside what a scanner of this kind can see. An adversarial review
 * demonstrated every one of them.
 *
 * This is a bar against an agent writing to the guarded tree by ACCIDENT while
 * doing ordinary work, and it is not a sandbox. An agent with a shell can
 * always reach the filesystem. Do not add a check here and conclude the tree is
 * protected; the protection is that the ordinary ways of writing a file are
 * caught and reported.
 */
export function scanCommand(command, opts = {}) {
  if (typeof command !== "string" || command === "") return null;
  // A heredoc body is DATA, not a command. Scanning it blocked this
  // repository's own commit describing this guard, because the message quoted
  // the write commands the guard catches. The body is dropped and the line that
  // opens it is still scanned, so a redirection into a guarded path is caught
  // while the text it would write is ignored.
  command = stripHeredocBodies(command);
  for (const span of WRITE_SPANS) {
    span.lastIndex = 0;
    let m;
    while ((m = span.exec(command)) !== null) {
      SPEC_TOKEN.lastIndex = 0;
      let t;
      while ((t = SPEC_TOKEN.exec(m[0])) !== null) {
        const cand = t[0].replace(/^["'`]+|["'`]+$/g, "").replace(/\/+$/, "");
        if (!cand) continue;
        const d = decideTarget(cand, opts);
        if (!d.allow) return d;
      }
    }
  }
  return null;
}

/**
 * Remove every heredoc body, keeping the line that opens it.
 *
 * The unquoted, quoted and dash forms all delimit on a line holding the word
 * alone, with leading tabs stripped for the dash form. An unterminated heredoc
 * runs to the end of the command, which is the safe reading here: the body is
 * data either way.
 */
export function stripHeredocBodies(command) {
  const lines = String(command).split("\n");
  const out = [];
  for (let i = 0; i < lines.length; i++) {
    out.push(lines[i]);
    const opener = heredocDelimiter(lines[i]);
    if (!opener) continue;
    let j = i + 1;
    let closed = false;
    while (j < lines.length) {
      const probe = opener.dash ? lines[j].replace(/^\t+/, "") : lines[j];
      if (probe === opener.delim) { closed = true; break; }
      j++;
    }
    // UNTERMINATED means this was probably not a heredoc at all. Dropping to the
    // end of the command was the fail-OPEN reading, and it was exploitable:
    // `echo "note << MARK here"` followed by a real write on the next line hid
    // the write entirely. When no delimiter line closes it, keep every line.
    if (!closed) continue;
    i = j;
  }
  return out.join("\n");
}

/**
 * The heredoc delimiter a line opens, or null.
 *
 * Quote-aware, because `<<` inside a quoted string is text rather than a
 * redirection. Scanning for the operator anywhere on the line let a quoted
 * mention of it stand in for a real one.
 */
function heredocDelimiter(line) {
  let quote = null;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quote) {
      if (c === "\\" && quote !== "'") i++;
      else if (c === quote) quote = null;
      continue;
    }
    if (c === "'" || c === '"' || c === "`") { quote = c; continue; }
    if (c === "\\") { i++; continue; }
    if (c === "<" && line[i + 1] === "<") {
      const rest = line.slice(i + 2);
      // `<<<` is a here-STRING: one line, no body to drop.
      if (rest.startsWith("<")) return null;
      const m = /^-?\s*(["']?)([A-Za-z_][A-Za-z0-9_]*)\1/.exec(rest);
      if (!m) return null;
      return { delim: m[2], dash: rest.startsWith("-") };
    }
  }
  return null;
}

/**
 * Decide one target, resolving it against the repo root and, when the payload
 * carries a different working directory, against that too. Checking both is the
 * fail-closed reading: a relative `spec/x.md` is still a spec write when the
 * agent's cwd is elsewhere in the tree.
 */
function decideTarget(target, opts = {}) {
  const d = decide(target, opts);
  if (!d.allow) return d;
  if (opts.cwd && !isAbsolute(target)) {
    const viaCwd = decide(resolve(opts.cwd, target), opts);
    if (!viaCwd.allow) return viaCwd;
  }
  return d;
}

const EDIT_TOOLS = new Set(["Edit", "Write", "MultiEdit", "NotebookEdit"]);

/**
 * Decide a PreToolUse payload. The shell wrapper this replaced classified paths
 * itself with two `case` statements and never reached relPath(), so measured,
 * `$REPO/./spec/x.md`, `$REPO/docs/../spec/x.md` and `$REPO//spec/x.md` were all
 * allowed. Parsing here puts every path through the same resolve()-based
 * normalisation the lease already uses.
 */
export function decideHook(rawPayload, opts = {}) {
  let payload;
  try {
    payload = JSON.parse(rawPayload);
  } catch (e) {
    // The shell hook ran `jq ... 2>/dev/null` and then `[ -n "$f" ] || exit 0`:
    // measured, a jq that exits 127 allowed a write to spec/28_x.md. An input
    // this tool cannot read is a refusal, like every other one in this file.
    return { allow: false, why: "the hook payload is not JSON; refusing" };
  }
  if (!payload || typeof payload !== "object") {
    return { allow: false, why: "the hook payload is not an object; refusing" };
  }
  const input = payload.tool_input && typeof payload.tool_input === "object" ? payload.tool_input : {};
  const o = { ...opts, cwd: typeof payload.cwd === "string" ? payload.cwd : "" };

  if (EDIT_TOOLS.has(payload.tool_name)) {
    const target = input.file_path ?? input.notebook_path;
    if (typeof target !== "string" || target === "") {
      return { allow: false, why: "the payload names no path; refusing" };
    }
    return decideTarget(target, o);
  }

  if (payload.tool_name === "Bash") {
    if (typeof input.command !== "string") {
      return { allow: false, why: "the Bash payload names no command; refusing" };
    }
    return scanCommand(input.command, o) || { allow: true, why: "no spec/ write in the command" };
  }

  // The matcher routes only the tools above, so anything else means the payload
  // is not what this hook was configured for.
  return { allow: false, why: "unrecognised tool " + String(payload.tool_name) + "; refusing" };
}

export const GUIDANCE =
  "Stage edits via the change-proposal skill, record approval, then apply with " +
  "the implement-proposal skill, which opens a lease for the step that writes them.";

export function openLease(proposal, opts = {}) {
  const now = opts.now ? new Date(opts.now) : new Date();
  const ttl = Number(opts.ttlHours || DEFAULT_TTL_HOURS);
  const leasePath = opts.leasePath || LEASE;
  const lease = {
    proposal: relPath(proposal),
    step: opts.step || "",
    runId: opts.runId || "",
    opened: now.toISOString(),
    expires: new Date(now.getTime() + ttl * 3600 * 1000).toISOString(),
    allow: (opts.allow || []).map((p) => relPath(p)),
  };
  // Refused here as well as in decide(), so an out-of-tree proposal fails at
  // the opener rather than silently producing a lease nothing will honour.
  if (!underLeaseDir(resolve(REPO, lease.proposal), leasePath)) {
    throw new Error("proposal " + proposal + " is outside " + dirname(resolve(leasePath)));
  }
  writeFileSync(leasePath, JSON.stringify(lease, null, 2) + "\n");
  return lease;
}

export function releaseLease(opts = {}) {
  const leasePath = opts.leasePath || LEASE;
  const r = readLease(leasePath);
  if (!r.present) return { released: false, why: "no lease" };
  // A lease opened without --step carries step "", and the guard used to read
  // `r.lease.step && r.lease.step !== opts.step`, so `release --step S9` freed
  // it. A step-scoped release frees only its own step; an unscoped release
  // still frees anything, which is the operator's cleanup path. The `r.lease`
  // conjunct stays: a malformed lease has no `lease` and must stay deletable.
  if (opts.step && r.lease && r.lease.step !== opts.step) {
    return { released: false, why: "lease belongs to step " + r.lease.step + ", not " + opts.step };
  }
  unlinkSync(leasePath);
  return { released: true };
}

// ---- CLI -----------------------------------------------------------------

if (import.meta.url === "file://" + process.argv[1]) {
  const argv = process.argv.slice(2);
  const cmd = argv[0];
  const flag = (n) => {
    const i = argv.indexOf("--" + n);
    return i >= 0 ? argv[i + 1] : undefined;
  };
  const leasePath = flag("lease-file") ? resolve(flag("lease-file")) : LEASE;

  if (cmd === "hook") {
    let raw;
    try {
      raw = readFileSync(0, "utf8");
    } catch (e) {
      raw = "";
    }
    const d = decideHook(raw, { leasePath, now: flag("now") });
    if (d.allow) process.exit(0);
    process.stderr.write("blocked: " + d.why + " " + GUIDANCE + "\n");
    process.exit(2);
  }

  if (cmd === "check") {
    const target = argv[1];
    if (!target) {
      process.stderr.write("usage: spec-lease.mjs check <path>\n");
      process.exit(2);
    }
    const d = decide(target, { leasePath, now: flag("now") });
    if (!d.allow) process.stderr.write("blocked: " + d.why + "\n");
    process.exit(d.allow ? 0 : 1);
  }

  if (cmd === "open") {
    const proposal = argv[1];
    if (!proposal) {
      process.stderr.write("usage: spec-lease.mjs open <proposal> --step S --allow a,b\n");
      process.exit(2);
    }
    const allowList = (flag("allow") || "").split(",").map((s) => s.trim()).filter(Boolean);
    if (allowList.length === 0) {
      process.stderr.write("open requires --allow: a lease with no allow list grants nothing\n");
      process.exit(2);
    }
    const l = openLease(proposal, {
      leasePath,
      step: flag("step"),
      runId: flag("run-id"),
      ttlHours: flag("ttl-hours"),
      now: flag("now"),
      allow: allowList,
    });
    process.stdout.write(JSON.stringify(l) + "\n");
    process.exit(0);
  }

  if (cmd === "release") {
    const r = releaseLease({ leasePath, step: flag("step") });
    process.stdout.write(JSON.stringify(r) + "\n");
    process.exit(0);
  }

  if (cmd === "status") {
    const r = readLease(leasePath, flag("now") ? new Date(flag("now")) : new Date());
    process.stdout.write(JSON.stringify({ held: !!r.held, ...r }, null, 2) + "\n");
    process.exit(0);
  }

  process.stderr.write("usage: spec-lease.mjs check|hook|open|release|status\n");
  process.exit(2);
}
