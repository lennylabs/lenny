import type { FrameWriter } from "./transport.js";
import type { AdapterTools, OutputPart, ToolResult } from "./types.js";
export interface InboundToolResult {
    type: string;
    id: string;
    content?: OutputPart[];
    isError?: boolean;
    slotId?: string;
}
export declare class ToolCallRegistry {
    private readonly pending;
    register(id: string): Promise<InboundToolResult>;
    deliver(result: InboundToolResult): boolean;
    cancel(id: string, err: Error): void;
    rejectAll(err: Error): void;
}
export declare class AdapterToolset implements AdapterTools {
    private readonly writer;
    private readonly registry;
    private readonly timeoutMs;
    private readonly slotId;
    constructor(writer: FrameWriter, registry: ToolCallRegistry, timeoutMs: number, slotId: string | undefined);
    toolCall(name: string, args: Record<string, unknown>): Promise<ToolResult>;
    readFile(path: string): Promise<string>;
    writeFile(path: string, content: string): Promise<void>;
    listDir(path: string): Promise<OutputPart[]>;
    deleteFile(path: string): Promise<void>;
}
//# sourceMappingURL=tool.d.ts.map