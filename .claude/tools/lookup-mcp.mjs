#!/usr/bin/env node
// MCP server that exposes lookup.mjs as one tool.
//
// A reviewer told about lookup.mjs in prose still reaches for grep most of the
// time: in a replay, lenses made 34 lookup calls and 85 plain shell calls against
// the spec. A tool in the agent's own tool list, with a typed schema, is where an
// agent looks first, so this wraps the same script as an MCP tool. It adds no
// behavior: each call runs lookup.mjs with the arguments as its request.
//
// Registered in .mcp.json as the server "lookup", so the tool's full name is
// mcp__lookup__lookup. The protocol is MCP over stdio: one JSON-RPC message per
// line. Only what a tool server needs is implemented.

import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const SCRIPT = join(dirname(fileURLToPath(import.meta.url)), "lookup.mjs");

const DESCRIPTION = [
  "Batch lookup over the repository's spec, docs, and code. Use this instead of grep and sed for every search or read of spec/, docs/, pkg/, cmd/, and the other scopes.",
  "One call takes a whole list of lookups and returns each hit with the paragraph, list item, or table row it sits in, its section heading, and its file and line. Printed text is verbatim from the file and can be cited as evidence.",
  "Put every term, statement, section, and line range you want at this point into ONE call; ten or twenty entries is normal.",
  "exact is complete: every occurrence is listed by location. meaning is a ranking with a match probability, reads markdown only, and is complete only when the output says every passage in scope was judged (narrow paths to a few files for that).",
  "Output is capped at 26000 characters; a passage that does not fit is listed by location and can be fetched with lines.",
  "When the line under the output header starts with JEV UNAVAILABLE, meaning and related results are unjudged shortlists: do not retry the tool for them; search with exact terms, grep, and Read instead.",
].join(" ");

const TOOL = {
  name: "lookup",
  description: DESCRIPTION,
  inputSchema: {
    type: "object",
    properties: {
      exact: {
        type: "array",
        description: "Terms to find. A string is a literal, case-sensitive term. An object gives a regex.",
        items: {
          anyOf: [
            { type: "string" },
            {
              type: "object",
              properties: { pattern: { type: "string" }, regex: { type: "boolean" }, ignoreCase: { type: "boolean" } },
              required: ["pattern"],
            },
          ],
        },
      },
      meaning: {
        type: "array",
        description: "Plain-language statements of a rule or behavior, to find passages that state it in other words.",
        items: {
          type: "object",
          properties: {
            query: { type: "string", description: "The rule or behavior, stated specifically, naming its subject." },
            context: { type: "string", description: "What you are trying to establish with this query." },
          },
          required: ["query"],
        },
      },
      sections: { type: "array", items: { type: "string" }, description: 'Spec section numbers to return whole, such as "7.3".' },
      lines: {
        type: "array",
        items: { type: "string" },
        description: 'Line ranges to return verbatim, such as "spec/07_session-lifecycle.md:430-446". Use in place of sed.',
      },
      related: {
        type: "array",
        description:
          "Before or after editing a passage: find other passages that state, restate, or rely on what the edit changes, so they can be updated too. Searches by section links, identifiers, and meaning, then a judge model ranks each candidate. Use scope [\"spec\", \"docs\", \"proposals\"] with paths narrowed to the proposal directory.",
        items: {
          type: "object",
          properties: {
            text: { type: "string", description: "The passage being changed, verbatim, or the new text being written." },
            change: { type: "string", description: "What the edit does. Recall falls by about half without it." },
            at: { type: "string", description: 'Location of the passage, such as "spec/07_session-lifecycle.md:430", so the whole passage is used and excluded from results.' },
            also: {
              anyOf: [{ type: "string" }, { type: "array", items: { type: "string" } }],
              description: "The new wording, or a list of short queries naming each fact the edit changes. Each is searched on its own.",
            },
            after: { type: "string", description: "The passage as it reads after the edit. Searched as a query and given to the judge with change." },
            reviewLogs: { type: "boolean", description: "Include review-log files, which are left out by default." },
            min: { type: "number", description: "Score threshold, default 0.7. Lower it for more recall, raise it for fewer results." },
          },
          required: ["text"],
        },
      },
      scope: {
        type: "array",
        items: { type: "string", enum: ["spec", "docs", "proposals", "pkg", "cmd", "sdks", "charts", "schemas", "tests"] },
        description: "Where to search. Default: spec.",
      },
      paths: { type: "array", items: { type: "string" }, description: "Files or directories that narrow the scope." },
      context: {
        type: "string",
        description: "What you are trying to establish. For an exact term with many hits, only passages that bear on it print in full; the rest stay listed by location.",
      },
      exhaustive: { type: "boolean", description: "meaning: judge every passage in scope. Default: only for a scope of 3000 passages or fewer." },
      root: {
        type: "string",
        description:
          "Absolute path of the repository or worktree to search. Pass it whenever the task names a repository: without it the call searches the session's working directory, which in a workflow is often a different checkout of the same repository. The first output line names the tree searched.",
      },
    },
  },
};

function runLookup(args) {
  const { root, ...request } = args || {};
  return new Promise((resolve) => {
    const child = spawn("node", [SCRIPT], { cwd: root || process.cwd(), stdio: ["pipe", "pipe", "pipe"] });
    let out = "";
    let err = "";
    child.stdout.on("data", (d) => (out += d));
    child.stderr.on("data", (d) => (err += d));
    child.on("error", (e) => resolve({ text: `lookup could not start: ${e.message}`, isError: true }));
    child.on("close", (code) => resolve(code === 0 ? { text: out, isError: false } : { text: (err || out || `lookup exited ${code}`).trim(), isError: true }));
    child.stdin.end(JSON.stringify(request));
  });
}

const send = (msg) => process.stdout.write(JSON.stringify(msg) + "\n");

async function handle(msg) {
  const { id, method, params } = msg;
  const reply = (result) => id !== undefined && send({ jsonrpc: "2.0", id, result });
  switch (method) {
    case "initialize":
      return reply({
        protocolVersion: params?.protocolVersion || "2024-11-05",
        capabilities: { tools: {} },
        serverInfo: { name: "lookup", version: "1.0.0" },
      });
    case "ping":
      return reply({});
    case "tools/list":
      return reply({ tools: [TOOL] });
    case "tools/call": {
      if (params?.name !== TOOL.name) return send({ jsonrpc: "2.0", id, error: { code: -32602, message: `unknown tool ${params?.name}` } });
      const r = await runLookup(params.arguments);
      return reply({ content: [{ type: "text", text: r.text }], isError: r.isError });
    }
    default:
      // Notifications carry no id and need no answer; an unknown request gets an error.
      if (id !== undefined) send({ jsonrpc: "2.0", id, error: { code: -32601, message: `method not found: ${method}` } });
  }
}

createInterface({ input: process.stdin }).on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch {
    return send({ jsonrpc: "2.0", id: null, error: { code: -32700, message: "parse error" } });
  }
  handle(msg).catch((e) => msg.id !== undefined && send({ jsonrpc: "2.0", id: msg.id, error: { code: -32603, message: e.message } }));
});
