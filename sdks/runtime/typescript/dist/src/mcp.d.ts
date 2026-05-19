import type { AdapterManifest, MCPConnection, OutputPart, PlatformTools, TaskHandle, TaskResult2 } from "./types.js";
export declare class McpClient implements MCPConnection {
    private readonly conn;
    private readonly reader;
    private nextId;
    private pending;
    private constructor();
    static connect(socket: string, nonce: string, clientName: string, timeoutMs: number): Promise<McpClient>;
    call(method: string, params: unknown): Promise<unknown>;
    private callLocked;
    callTool(name: string, args: unknown): Promise<unknown>;
    close(): void;
}
export declare class Tools implements PlatformTools {
    private readonly platform;
    private readonly connectors;
    private constructor();
    static dial(manifest: AdapterManifest, timeoutMs: number): Promise<Tools>;
    close(): void;
    connector(id: string): MCPConnection | undefined;
    delegateTask(target: string, parts: OutputPart[], budget?: Record<string, unknown>): Promise<TaskHandle>;
    awaitChildren(childIds: string[], mode?: string): Promise<TaskResult2[]>;
    cancelChild(childId: string): Promise<void>;
    discoverAgents(query: Record<string, unknown>): Promise<unknown>;
    output(parts: OutputPart[]): Promise<void>;
    requestInput(prompt: OutputPart[]): Promise<OutputPart[]>;
    requestElicitation(args: Record<string, unknown>): Promise<unknown>;
    sendMessage(args: Record<string, unknown>): Promise<unknown>;
    memoryWrite(args: Record<string, unknown>): Promise<void>;
    memoryQuery(args: Record<string, unknown>): Promise<unknown>;
    getTaskTree(args: Record<string, unknown>): Promise<unknown>;
    setTracingContext(ctx: Record<string, unknown>): Promise<void>;
    call(name: string, args: Record<string, unknown>): Promise<unknown>;
}
//# sourceMappingURL=mcp.d.ts.map