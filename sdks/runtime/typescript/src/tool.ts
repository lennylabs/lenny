// SPDX-License-Identifier: MIT

// This module implements the §15.4.1 adapter-local tool surface. An
// adapter-local tool call emits a stdout tool_call frame and resolves
// when the matching tool_result frame arrives on stdin, correlating by
// id. Unlike the platform MCP tools (which require Standard level),
// adapter-local tools (read_file, write_file, list_dir, delete_file)
// are resolved inside the adapter process with no MCP server.

import { randomBytes } from "node:crypto";
import type { FrameWriter } from "./transport.js";
import type { AdapterTools, MessagePart, ToolResult } from "./types.js";

// InboundToolResult is the decoded §15.4.1 tool_result frame.
export interface InboundToolResult {
  type: string;
  id: string;
  content?: MessagePart[];
  isError?: boolean;
  slotId?: string;
}

// PendingToolCall is the in-flight bookkeeping for one tool_call: the
// callbacks that resolve when the correlated tool_result arrives.
interface PendingToolCall {
  resolve(result: InboundToolResult): void;
  reject(err: Error): void;
}

// ToolCallRegistry correlates outbound §15.4.1 tool_call frames with
// inbound tool_result frames. The §15.4.1 frame loop delivers a
// tool_result here; an AdapterTools call registers and awaits one.
export class ToolCallRegistry {
  private readonly pending = new Map<string, PendingToolCall>();

  // register records a pending tool_call id and returns a promise that
  // resolves with the correlated tool_result.
  register(id: string): Promise<InboundToolResult> {
    return new Promise<InboundToolResult>((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
    });
  }

  // deliver routes an inbound tool_result to the call that emitted the
  // matching id. It reports whether a pending call was found.
  deliver(result: InboundToolResult): boolean {
    const entry = this.pending.get(result.id);
    if (!entry) {
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

  // rejectAll fails every pending call. The §15.4.1 frame loop calls it
  // when the inbound stream closes so no caller waits forever.
  rejectAll(err: Error): void {
    for (const [id, entry] of this.pending) {
      this.pending.delete(id);
      entry.reject(err);
    }
  }
}

// newCallId generates a unique §15.4.1 tool_call id with the
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

// AdapterToolset is the §15.4.1 adapter-local tool surface available at
// every integration level. It emits a stdout tool_call frame and
// resolves once the matching tool_result frame arrives on stdin.
export class AdapterToolset implements AdapterTools {
  constructor(
    private readonly writer: FrameWriter,
    private readonly registry: ToolCallRegistry,
    private readonly timeoutMs: number,
    private readonly slotId: string | undefined,
  ) {}

  // toolCall emits a §15.4.1 tool_call frame for the named
  // adapter-local tool and resolves once the correlated tool_result
  // arrives. The id is generated and unique within the process.
  async toolCall(
    name: string,
    args: Record<string, unknown>,
  ): Promise<ToolResult> {
    const id = newCallId();
    const wait = this.registry.register(id);
    const frame: Record<string, unknown> = {
      type: "tool_call",
      id,
      name,
      arguments: args,
    };
    if (this.slotId) {
      frame.slotId = this.slotId;
    }
    try {
      await this.writer.write(frame);
    } catch (err) {
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

  // readFile invokes the §15.4.1 read_file adapter-local tool. The path
  // is confined to the pod workspace by the adapter; a path resolving
  // outside /workspace returns an error result.
  async readFile(path: string): Promise<string> {
    const tr = await this.toolCall("read_file", { path });
    return firstInline(tr);
  }

  // writeFile invokes the §15.4.1 write_file adapter-local tool,
  // creating or overwriting a workspace file with UTF-8 content.
  async writeFile(path: string, content: string): Promise<void> {
    const tr = await this.toolCall("write_file", { path, content });
    toolError(tr, "write_file");
  }

  // listDir invokes the §15.4.1 list_dir adapter-local tool and returns
  // the directory entries the adapter reports.
  async listDir(path: string): Promise<MessagePart[]> {
    const tr = await this.toolCall("list_dir", { path });
    toolError(tr, "list_dir");
    return tr.content;
  }

  // deleteFile invokes the §15.4.1 delete_file adapter-local tool.
  async deleteFile(path: string): Promise<void> {
    const tr = await this.toolCall("delete_file", { path });
    toolError(tr, "delete_file");
  }
}
