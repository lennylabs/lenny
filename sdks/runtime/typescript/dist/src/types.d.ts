export declare const SCHEMA_VERSION = 1;
export interface OutputPart {
    schemaVersion?: number;
    id?: string;
    type: string;
    mimeType?: string;
    inline?: string;
    ref?: string;
    annotations?: Record<string, unknown>;
    parts?: OutputPart[];
    status?: string;
}
export declare function text(s: string): OutputPart;
export interface MessageFrom {
    kind: string;
    id: string;
}
export interface MessageEnvelope {
    schemaVersion?: number;
    type: string;
    id: string;
    from?: MessageFrom;
    inReplyTo?: string;
    threadId?: string;
    delivery?: string;
    delegationDepth?: number;
    slotId?: string;
    input?: OutputPart[];
}
export interface ResponseError {
    code: string;
    message?: string;
}
export interface CredentialBundle {
    mode?: string;
    provider?: string;
    leaseId?: string;
    apiKey?: string;
    apiKeyEnv?: string;
    baseUrl?: string;
    expiresAt?: string;
}
export interface MCPServerRef {
    socket: string;
}
export interface ConnectorServerRef {
    id: string;
    socket: string;
}
export interface SocketRef {
    socket: string;
}
export interface AdapterLocalTool {
    name: string;
    description?: string;
    inputSchema?: Record<string, unknown>;
}
export interface AdapterManifest {
    version?: number;
    sessionId?: string;
    taskId?: string;
    mcpNonce?: string;
    platformMcpServer?: MCPServerRef;
    connectorServers?: ConnectorServerRef[];
    lifecycleChannel?: SocketRef;
    adapterLocalTools?: AdapterLocalTool[];
    runtimeOptions?: Record<string, unknown>;
    tracingContext?: Record<string, unknown>;
}
export interface WorkspacePlan {
    schemaVersion?: number;
    sources?: unknown[];
    setupCommands?: string[];
}
export interface TerminationReason {
    reason: string;
    deadlineMs: number;
}
export interface CreateRequest {
    sessionId: string;
    taskId: string;
    runtimeOptions?: Record<string, unknown>;
    workspacePlan?: WorkspacePlan;
    credentials?: CredentialBundle;
    manifestSnapshot?: AdapterManifest;
}
export interface Message {
    envelope: MessageEnvelope;
    sessionId: string;
    taskId: string;
    sequence: number;
}
export interface Reply {
    parts?: OutputPart[];
    error?: ResponseError;
    streaming?: boolean;
    final?: boolean;
}
export declare function textReply(s: string): Reply;
export interface Handler {
    onCreate(req: CreateRequest): Promise<void> | void;
    onMessage(msg: Message, tools: HandlerTools): Promise<Reply> | Reply;
    onTerminate(reason: TerminationReason): Promise<void> | void;
}
export interface HandlerTools {
    adapter: AdapterTools;
    platform?: PlatformTools;
    credentials?: CredentialBundle;
}
export interface AdapterTools {
    toolCall(name: string, args: Record<string, unknown>): Promise<ToolResult>;
    readFile(path: string): Promise<string>;
    writeFile(path: string, content: string): Promise<void>;
    listDir(path: string): Promise<OutputPart[]>;
    deleteFile(path: string): Promise<void>;
}
export interface ToolResult {
    content: OutputPart[];
    isError: boolean;
}
export interface PlatformTools {
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
    connector(id: string): MCPConnection | undefined;
    call(name: string, args: Record<string, unknown>): Promise<unknown>;
}
export interface TaskHandle {
    taskId: string;
}
export interface TaskOutput {
    parts?: OutputPart[];
}
export interface TaskResult2 {
    schemaVersion?: number;
    taskId: string;
    state: string;
    output: TaskOutput;
    error?: ResponseError;
}
export interface MCPConnection {
    callTool(name: string, args: unknown): Promise<unknown>;
}
export declare class ProtocolError extends Error {
    constructor(message: string);
}
export declare function isProtocolError(err: unknown): err is ProtocolError;
//# sourceMappingURL=types.d.ts.map