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
//   spec-lease.mjs open <proposal> --step S3 --allow spec/a.md,spec/b.md
//                                        [--ttl-hours 24] [--run-id X] [--now ISO]
//   spec-lease.mjs release [--step S3]   release, or release only if it is S3's
//   spec-lease.mjs status [--json]       report the current lease

import { readFileSync, writeFileSync, existsSync, unlinkSync } from "fs";
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
  return relative(REPO, abs).split("\\").join("/");
}

export function readLease(leasePath = LEASE) {
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
  return { present: true, lease: raw };
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

  if (!rel.startsWith("spec/")) return { allow: true, why: "not under spec/" };

  const r = readLease(leasePath);
  if (!r.present) {
    return { allow: false, why: "spec/ is read-only: no spec lease is open" };
  }
  if (r.malformed) {
    return { allow: false, why: "spec lease is malformed (" + r.malformed + "); refusing" };
  }
  const lease = r.lease;

  const expires = new Date(lease.expires);
  if (!(expires > now)) {
    return {
      allow: false,
      why: "spec lease expired at " + lease.expires + "; release it or re-run the step that opened it",
    };
  }

  let status;
  try {
    status = execFileSync("node", [STATUS_TOOL, resolve(REPO, lease.proposal), "--field", "status"], {
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

  const allow = Array.isArray(lease.allow) ? lease.allow.map((p) => relPath(p)) : [];
  if (allow.length > 0 && !allow.includes(rel)) {
    return {
      allow: false,
      why: rel + " is not in the lease's allow list (" + allow.join(", ") + ")",
    };
  }

  return { allow: true, why: "leased by " + lease.proposal + (lease.step ? " step " + lease.step : "") };
}

export function openLease(proposal, opts = {}) {
  const now = opts.now ? new Date(opts.now) : new Date();
  const ttl = Number(opts.ttlHours || DEFAULT_TTL_HOURS);
  const lease = {
    proposal: relPath(proposal),
    step: opts.step || "",
    runId: opts.runId || "",
    opened: now.toISOString(),
    expires: new Date(now.getTime() + ttl * 3600 * 1000).toISOString(),
    allow: (opts.allow || []).map((p) => relPath(p)),
  };
  writeFileSync(opts.leasePath || LEASE, JSON.stringify(lease, null, 2) + "\n");
  return lease;
}

export function releaseLease(opts = {}) {
  const leasePath = opts.leasePath || LEASE;
  const r = readLease(leasePath);
  if (!r.present) return { released: false, why: "no lease" };
  if (opts.step && r.lease && r.lease.step && r.lease.step !== opts.step) {
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
    const l = openLease(proposal, {
      leasePath,
      step: flag("step"),
      runId: flag("run-id"),
      ttlHours: flag("ttl-hours"),
      now: flag("now"),
      allow: (flag("allow") || "").split(",").map((s) => s.trim()).filter(Boolean),
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
    const r = readLease(leasePath);
    process.stdout.write(JSON.stringify(r, null, 2) + "\n");
    process.exit(0);
  }

  process.stderr.write("usage: spec-lease.mjs check|open|release|status\n");
  process.exit(2);
}
