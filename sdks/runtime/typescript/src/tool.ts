// SPDX-License-Identifier: MIT

// This module implements the §28.5.3 adapter-local tool surface. An
// adapter-local tool call emits a stdout tool_call frame and resolves
// when the matching tool_result frame arrives on stdin, correlating by
// id. Unlike the platform MCP tools (which require Standard level),
// adapter-local tools (read_file, write_file, list_dir, delete_file)
// are resolved inside the adapter process with no MCP server.

import { randomBytes } from "node:crypto";
import type { FrameWriter } from "./transport.js";
import type { AdapterTools, MessagePart, ToolResult } from "./types.js";

// InboundToolResult is the decoded §28.5.3 tool_result frame.
export interface InboundToolResult {
  type: string;
  id: string;
  content?: MessagePart[];
  isError?: boolean;
  sessionId?: string;
}

// ToolCallOwner is the session a tool_call belongs to: id is the
// session's sessionId, and ended is the drop mark the frame loop sets
// when it reads the session's session_end.
export interface ToolCallOwner {
  readonly id: string;
  ended: boolean;
}

// SessionEndedError is the rejection of a tool_call whose session ended
// before its result arrived, or whose frame was dropped because the
// session had already ended.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2).
export class SessionEndedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SessionEndedError";
  }
}

// PendingToolCall is the in-flight bookkeeping for one tool_call: the
// session that issued it and the callbacks that settle when the
// correlated tool_result arrives.
interface PendingToolCall {
  owner: ToolCallOwner;
  resolve(result: InboundToolResult): void;
  reject(err: Error): void;
}

// ToolCallRegistry correlates outbound §28.5.3 tool_call frames with
// inbound tool_result frames. The §28.5.3 frame loop delivers a
// tool_result here; an AdapterTools call registers and awaits one.
export class ToolCallRegistry {
  private readonly pending = new Map<string, PendingToolCall>();

  // register records a pending tool_call id for owner and returns a
  // promise that resolves with the correlated tool_result.
  register(id: string, owner: ToolCallOwner): Promise<InboundToolResult> {
    return new Promise<InboundToolResult>((resolve, reject) => {
      this.pending.set(id, { owner, resolve, reject });
    });
  }

  // deliver routes an inbound tool_result to the call that emitted the
  // matching id. A call matches only when its id equals the frame's id and
  // the session that issued it is the one the frame's sessionId names, so
  // a result addressed to another session never completes the call and
  // leaves it pending for its own result. It reports whether a pending
  // call was found.
  //
  // spec: §28.5.3 (CH-MSGSOCK, Inbound: tool_result).
  deliver(result: InboundToolResult): boolean {
    const entry = this.pending.get(result.id);
    if (!entry || entry.owner.id !== (result.sessionId ?? "")) {
      return false;
    }
    this.pending.delete(result.id);
    entry.resolve(result);
    return true;
  }

  // cancel drops a pending tool_call registration and rejects its
  // promise.
  cancel(id: string, err: Error): void {
    const entry = this.pending.get(id);
    if (entry) {
      this.pending.delete(id);
      entry.reject(err);
    }
  }

  // cancelOwner fails every pending call owner issued. The frame loop
  // calls it when it reads the owner's session_end, so each waiting call
  // rejects at once.
  cancelOwner(owner: ToolCallOwner): void {
    for (const [id, entry] of this.pending) {
      if (entry.owner === owner) {
        this.pending.delete(id);
        entry.reject(
          new SessionEndedError(`session ${owner.id} ended; tool_call abandoned`),
        );
      }
    }
  }

  // rejectAll fails every pending call. The §28.5.3 frame loop calls it
  // when the inbound stream closes so no caller waits forever.
  rejectAll(err: Error): void {
    for (const [id, entry] of this.pending) {
      this.pending.delete(id);
      entry.reject(err);
    }
  }
}

// newCallId generates a unique §28.5.3 tool_call id with the
// recommended tc_ prefix.
function newCallId(): string {
  return "tc_" + randomBytes(8).toString("hex");
}

// firstInline returns the inline content of the first result part, or
// throws when the tool reported a failure.
function firstInline(tr: ToolResult): string {
  toolError(tr, "tool");
  if (tr.content.length === 0) {
    return "";
  }
  return tr.content[0].inline ?? "";
}

// toolError throws when an error-flagged tool result is present. The
// adapter sets content[0].inline to the failure string (for example
// path_outside_workspace).
function toolError(tr: ToolResult, tool: string): void {
  if (!tr.isError) {
    return;
  }
  let msg = "tool reported an error";
  if (tr.content.length > 0 && tr.content[0].inline) {
    msg = tr.content[0].inline as string;
  }
  throw new Error(`${tool}: ${msg}`);
}

// AdapterToolset is the §28.5.3 adapter-local tool surface available at
// every integration level. It emits a stdout tool_call frame and
// resolves once the matching tool_result frame arrives on stdin.
export class AdapterToolset implements AdapterTools {
  constructor(
    private readonly writer: FrameWriter,
    private readonly registry: ToolCallRegistry,
    private readonly timeoutMs: number,
    private readonly owner: ToolCallOwner,
  ) {}

  // toolCall emits a §28.5.3 tool_call frame for the named
  // adapter-local tool and resolves once the correlated tool_result
  // arrives. The id is generated and unique within the process.
  async toolCall(
    name: string,
    args: Record<string, unknown>,
  ): Promise<ToolResult> {
    const id = newCallId();
    const wait = this.registry.register(id, this.owner);
    // §28.5.3: a session-scoped frame carries the session it addresses on
    // every pod, and no frame for a session is written after its
    // session_end, so the write is dropped once the session ended.
    if (this.owner.ended) {
      const err = new SessionEndedError(
        `session ${this.owner.id} ended; tool_call "${name}" dropped`,
      );
      this.registry.cancel(id, err);
      wait.catch(() => undefined);
      throw err;
    }
    try {
      await this.writer.write({
        type: "tool_call",
        id,
        name,
        arguments: args,
        sessionId: this.owner.id,
      });
    } catch (err) {
      wait.catch(() => undefined);
      this.registry.cancel(id, err as Error);
      throw new Error(`write tool_call "${name}": ${(err as Error).message}`);
    }

    const timeout = this.timeoutMs > 0 ? this.timeoutMs : 30_000;
    let timer: NodeJS.Timeout | undefined;
    const guard = new Promise<never>((_, reject) => {
      timer = setTimeout(() => {
        const err = new Error(
          `tool_call "${name}" timed out after ${timeout}ms`,
        );
        this.registry.cancel(id, err);
        reject(err);
      }, timeout);
    });
    try {
      const result = await Promise.race([wait, guard]);
      return {
        content: result.content ?? [],
        isError: result.isError ?? false,
      };
    } finally {
      if (timer) {
        clearTimeout(timer);
      }
    }
  }

  // readFile invokes the §28.5.3 read_file adapter-local tool. The path
  // is confined to the pod workspace by the adapter; a path resolving
  // outside /workspace returns an error result.
  async readFile(path: string): Promise<string> {
    const tr = await this.toolCall("read_file", { path });
    return firstInline(tr);
  }

  // writeFile invokes the §28.5.3 write_file adapter-local tool,
  // creating or overwriting a workspace file with UTF-8 content.
  async writeFile(path: string, content: string): Promise<void> {
    const tr = await this.toolCall("write_file", { path, content });
    toolError(tr, "write_file");
  }

  // listDir invokes the §28.5.3 list_dir adapter-local tool and returns
  // the directory entries the adapter reports.
  async listDir(path: string): Promise<MessagePart[]> {
    const tr = await this.toolCall("list_dir", { path });
    toolError(tr, "list_dir");
    return tr.content;
  }

  // deleteFile invokes the §28.5.3 delete_file adapter-local tool.
  async deleteFile(path: string): Promise<void> {
    const tr = await this.toolCall("delete_file", { path });
    toolError(tr, "delete_file");
  }
}
