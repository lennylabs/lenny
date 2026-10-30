#!/usr/bin/env node
// The change-proposal workflow's persisted state, on the disk side of the
// workflow sandbox.
//
// A workflow script cannot touch a file, so its state reaches the disk only
// through an agent, and an agent moves text only by generating it. This tool is
// what those agents run, and what the launching session runs, so that as little
// text as possible has to be generated and every transfer is checkable.
//
// Lengths, checksums and digests count Unicode code points, which is what the
// workflow computes on its side with `for (const ch of s)`.
//
//   sig <file>...                       per part file: "<name> <len> <sum>", read as `part` reads it
//   join <out> <len> <sum> <part>...    rebuild the state from the parts' transfer lines, verify the
//                                       length and checksum of its state text, then write <out>
//                                       atomically; prints "OK <len> <sum>" or "ERR <why>"
//   meta <file> <chunkSize>             JSON: {len, sum, chunks:[{len, sum}]}, or "MISSING"
//   slice <file> <index> <chunkSize>    the chunk between CP_SLICE_BEGIN and CP_SLICE_END markers
//   record-name <id>                    the record file name the decisions workflow uses for an item
//   migrate-records <state> <dir>       move record text out of a state written before record files
//                                       existed, into <dir>, and slim the state in place
//   format <state>                      rewrite a state file one entry to a line, in place
//   launch-copy <state> <workflow> <out>
//                                       write <workflow> to <out> with the saved decisions state
//                                       embedded, so a relaunch reads it with no agent at all
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { basename, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const read = (p) => readFileSync(p, "utf8").replace(/\n$/, "");
// A part file as the verification reads it: every line's leading whitespace is
// dropped. The Workflow harness indents every line of a script-computed prompt,
// so an agent asked to copy a chunk sees each line two spaces in and, measured on
// one run, copied the indentation into the file on every attempt: 38 lines, 76
// extra characters, and a checksum that never matched. A state line never starts
// with whitespace (`stateText` writes each one starting at `{`, `}` or a JSON
// string), so dropping it restores the chunk exactly and the checksum still
// catches any other change.
export const unindent = (s) => s.split("\n").map((l) => l.replace(/^[ \t]+/, "")).join("\n");
const part = (p) => unindent(read(p));
const writeAtomic = (p, text) => {
  mkdirSync(dirname(p), { recursive: true });
  writeFileSync(p + ".tmp", text);
  renameSync(p + ".tmp", p);
};

export function sig(s) {
  let len = 0;
  let sum = 0;
  for (const ch of s) {
    len++;
    sum = (sum + ch.codePointAt(0)) % 4294967296;
  }
  return { len, sum };
}

// The workflow writes the state one entry to a line, and a chunk is whole lines
// joined by newlines, so a chunk never ends mid-token: an agent copying a
// fragment that stopped inside a key completed the key. Parts rejoin with a
// newline. These two functions are the ones change-proposal.js carries.
export function stateText(obj) {
  const keys = Object.keys(obj);
  const lines = ["{"];
  keys.forEach((k, i) => {
    const v = obj[k];
    const comma = i < keys.length - 1 ? "," : "";
    if (v && typeof v === "object" && !Array.isArray(v) && Object.keys(v).length > 0) {
      const ek = Object.keys(v);
      lines.push(JSON.stringify(k) + ":{");
      ek.forEach((e, j) => lines.push(JSON.stringify(e) + ":" + JSON.stringify(v[e]) + (j < ek.length - 1 ? "," : "")));
      lines.push("}" + comma);
    } else {
      lines.push(JSON.stringify(k) + ":" + JSON.stringify(v) + comma);
    }
  });
  lines.push("}");
  return lines.join("\n");
}
// The transfer format the part writers copy: one complete JSON array a line,
// `[key, value]` or `[key, entry, value]`. change-proposal.js carries the same
// function. `fromTransfer` is its inverse and throws on a line that is not one.
export function transferText(obj) {
  const lines = [];
  for (const k of Object.keys(obj)) {
    const v = obj[k];
    if (v && typeof v === "object" && !Array.isArray(v) && Object.keys(v).length > 0) {
      for (const e of Object.keys(v)) lines.push(JSON.stringify([k, e, v[e]]));
    } else {
      lines.push(JSON.stringify([k, v]));
    }
  }
  return lines.join("\n");
}
export function fromTransfer(text) {
  const obj = {};
  for (const line of text.split("\n")) {
    if (!line.trim()) continue;
    const a = JSON.parse(line);
    if (!Array.isArray(a) || (a.length !== 2 && a.length !== 3) || typeof a[0] !== "string") {
      throw new Error("not a transfer line: " + line.slice(0, 60));
    }
    if (a.length === 2) obj[a[0]] = a[1];
    else {
      if (!obj[a[0]] || typeof obj[a[0]] !== "object" || Array.isArray(obj[a[0]])) obj[a[0]] = {};
      obj[a[0]][a[1]] = a[2];
    }
  }
  return obj;
}
export function chunks(text, size) {
  const out = [];
  let cur = [];
  let n = 0;
  for (const line of text.split("\n")) {
    const len = Array.from(line).length + 1;
    if (cur.length > 0 && n + len > size) {
      out.push(cur.join("\n"));
      cur = [];
      n = 0;
    }
    cur.push(line);
    n += len;
  }
  if (cur.length > 0) out.push(cur.join("\n"));
  return out;
}

// The digest change-proposal-decisions.js names record files by: FNV-1a over
// code points, twice with different offsets, as 16 hex digits. The two are one
// statement; the decisions test holds them equal.
export function textDigest(text) {
  let a = 0x811c9dc5;
  let b = 0x01000193 ^ 0x5bd1e995;
  for (const ch of String(text)) {
    const c = ch.codePointAt(0);
    a = Math.imul(a ^ c, 0x01000193) >>> 0;
    b = Math.imul(b ^ c, 0x01000193) >>> 0;
  }
  return a.toString(16).padStart(8, "0") + b.toString(16).padStart(8, "0");
}
export const recordName = (id) => textDigest(id) + ".md";

// The record file an Apply agent writes, in the same form.
export function recordText(id, where, wrote) {
  return "ID: " + id + "\nWHERE:\n" + (where || []).map((w) => "- " + w).join("\n") + "\nWROTE:\n" + (wrote || "") + "\n";
}

// A state written before record files existed carries the text inline. The
// text moves to the record files, the state keeps whether a file exists, a short
// question and an impact row's digest, and the corpus inventory is dropped.
export function migrateRecords(state, dir) {
  const recs = state.itemRecords || {};
  let moved = 0;
  for (const rec of Object.values(recs)) {
    const wrote = rec.wrote || (rec.contested && rec.contested.wrote) || "";
    const where = (Array.isArray(rec.where) && rec.where.length ? rec.where : rec.contested && rec.contested.where) || [];
    if (wrote && !rec.hasRecord) {
      writeAtomic(dir + "/" + recordName(rec.id), recordText(rec.id, where, wrote));
      rec.hasRecord = true;
      moved++;
    }
    if (rec.hasRecord === undefined) rec.hasRecord = false;
    if (rec.rowText) rec.rowTextDigest = textDigest(rec.rowText);
    if (rec.rowTextDigest === undefined) rec.rowTextDigest = "";
    delete rec.rowText;
    delete rec.wrote;
    delete rec.where;
    if (rec.contested) {
      delete rec.contested.wrote;
      delete rec.contested.where;
    }
    rec.question = String(rec.question || "").slice(0, 120);
  }
  delete state.corpus;
  return moved;
}

// The line change-proposal.js carries for the launcher to replace. Exactly one
// must exist, so a renamed sentinel fails loudly rather than launching a copy
// that silently reads nothing.
export const EMBED_SENTINEL = "const CP_EMBEDDED_DECISIONS_STATE = null;";
// The Workflow tool refuses a script larger than this many bytes. The workflow
// alone is close to it, so the copy drops its comment-only lines, which the run
// never reads, before the state goes in; a copy still over the limit is refused
// here rather than by the launch.
export const MAX_SCRIPT_BYTES = 524288;
export function launchCopy(src, state) {
  const n = src.split(EMBED_SENTINEL).length - 1;
  if (n !== 1) throw new Error("expected exactly one embedding sentinel, found " + n);
  const stripped = src
    .split("\n")
    .filter((line) => !/^\s*\/\//.test(line))
    .join("\n");
  const out = stripped.replace(EMBED_SENTINEL, "const CP_EMBEDDED_DECISIONS_STATE = " + JSON.stringify(state) + ";");
  const bytes = Buffer.byteLength(out, "utf8");
  if (bytes > MAX_SCRIPT_BYTES) {
    throw new Error(
      "the launch copy is " + bytes + " bytes, over the Workflow limit of " + MAX_SCRIPT_BYTES +
        "; launch the workflow itself with resumeState, which reads the state back in verified chunks",
    );
  }
  // The runtime wraps the body in an async function after removing the meta
  // export, so the copy is parsed the same way.
  new vm.Script("(async function () {\n" + out.replace(/^export\s+const\s+meta/m, "const meta") + "\n})");
  return out;
}

function main([cmd, ...args]) {
  if (cmd === "sig") {
    for (const p of args) {
      if (!existsSync(p)) {
        console.log(basename(p) + " MISSING");
        continue;
      }
      const s = sig(part(p));
      console.log(basename(p) + " " + s.len + " " + s.sum);
    }
  } else if (cmd === "join") {
    const [out, wantLen, wantSum, ...parts] = args;
    const missing = parts.filter((p) => !existsSync(p));
    if (missing.length) return console.log("ERR missing " + missing.map((p) => basename(p)).join(","));
    let text;
    try {
      text = stateText(fromTransfer(parts.map(part).join("\n")));
    } catch (e) {
      return console.log("ERR json " + e.message);
    }
    const s = sig(text);
    if (String(s.len) !== wantLen || String(s.sum) !== wantSum) return console.log("ERR mismatch " + s.len + " " + s.sum);
    writeAtomic(out, text);
    console.log("OK " + s.len + " " + s.sum);
  } else if (cmd === "meta") {
    const [file, size] = args;
    if (!existsSync(file)) return console.log("MISSING");
    const text = read(file);
    console.log(JSON.stringify({ ...sig(text), chunks: chunks(text, Number(size)).map(sig) }));
  } else if (cmd === "slice") {
    const [file, index, size] = args;
    const part = chunks(read(file), Number(size))[Number(index)] ?? "";
    process.stdout.write("CP_SLICE_BEGIN" + part + "CP_SLICE_END\n");
  } else if (cmd === "record-name") {
    console.log(recordName(args[0]));
  } else if (cmd === "migrate-records") {
    const [file, dir] = args;
    const state = JSON.parse(read(file));
    const before = JSON.stringify(state).length;
    const moved = migrateRecords(state, dir);
    const text = stateText(state);
    writeAtomic(file, text);
    console.log("moved " + moved + " record(s) to " + dir + "; state " + before + " -> " + text.length + " chars");
  } else if (cmd === "format") {
    const text = stateText(JSON.parse(read(args[0])));
    writeAtomic(args[0], text);
    console.log(args[0] + " " + text.split("\n").length + " line(s)");
  } else if (cmd === "launch-copy") {
    const [stateFile, workflow, out] = args;
    const state = existsSync(stateFile) ? JSON.parse(read(stateFile)) : null;
    if (!state) {
      console.error("no saved state at " + stateFile + "; launch the workflow itself");
      process.exit(1);
    }
    writeAtomic(out, launchCopy(readFileSync(workflow, "utf8"), state));
    console.log(out + " (" + Object.keys(state.itemRecords || {}).length + " record(s) embedded)");
  } else {
    console.error("usage: cp-state.mjs sig|join|meta|slice|record-name|migrate-records|format|launch-copy ...");
    process.exit(2);
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main(process.argv.slice(2));
