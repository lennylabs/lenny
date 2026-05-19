// SPDX-License-Identifier: MIT

import type { Authenticator } from './auth.js';
import { decodeApiError } from './errors.js';

/** The SDK identity sent in the User-Agent request header. */
const mcpUserAgent = 'lenny-client-sdk-ts';

/**
 * The MCP protocol revision this client requests in the §15.2
 * initialize handshake. The gateway negotiates the highest version it
 * and the client both support; the negotiated value is reported by
 * {@link MCPClient.initialize} on InitializeResult.protocolVersion.
 */
const mcpProtocolVersion = '2025-06-18';

/** The client identity sent in the §15.2 initialize clientInfo. */
const mcpClientName = 'lenny-client-sdk-ts';

/**
 * MCPTool is one entry in the §15.2 MCP tool catalog returned by
 * {@link MCPClient.listTools}.
 */
export interface MCPTool {
  /** The tool identifier, for example lenny/create_session. */
  name: string;

  /** The human-readable tool description. */
  description: string;

  /** The JSON Schema for the tool's arguments object. */
  inputSchema: unknown;
}

/** MCPContent is one content block in an MCP tool result. */
export interface MCPContent {
  /** The content block type. The gateway tools emit text. */
  type: string;

  /** The inline text when type is text. */
  text?: string;
}

/**
 * MCPToolResult is the §15.2 tools/call result. A tool that reports a
 * failure returns isError true with the failure text in content; the
 * JSON-RPC transport itself succeeded.
 */
export interface MCPToolResult {
  /** The list of result content blocks. */
  content: MCPContent[];

  /**
   * Whether the tool reported a failure. The MCP spec surfaces a
   * tool-level failure as a result with this flag set rather than as a
   * transport error.
   */
  isError: boolean;
}

/** MCPServerInfo identifies an MCP server in the initialize response. */
export interface MCPServerInfo {
  name: string;
  version: string;
}

/** InitializeResult is the §15.2 initialize handshake response. */
export interface InitializeResult {
  /**
   * The MCP spec version the gateway negotiated. The connection is
   * pinned to this version for its lifetime.
   */
  protocolVersion: string;

  /** The gateway's advertised MCP capability set. */
  capabilities: Record<string, unknown>;

  /** The identity of the gateway MCP server. */
  serverInfo: MCPServerInfo;
}

/**
 * MCPError is the typed form of a §15.2 JSON-RPC error object. A
 * tools/call that fails at the transport level (unknown tool, invalid
 * params) rejects with this error; a tool that runs and reports a
 * failure resolves to an {@link MCPToolResult} with isError set
 * instead. MCPError extends the built-in Error.
 */
export class MCPError extends Error {
  /**
   * The JSON-RPC error code. The MCP and JSON-RPC 2.0 reserved codes
   * are negative (for example -32601 method not found, -32602 invalid
   * params).
   */
  readonly code: number;

  /** Error-specific context, when the gateway supplies it. */
  readonly data?: unknown;

  constructor(code: number, message: string, data?: unknown) {
    super(`lenny: MCP error ${code}: ${message}`);
    this.name = 'MCPError';
    this.code = code;
    this.data = data;
    // Restore the prototype chain so `instanceof MCPError` holds after
    // transpilation to ES5-class semantics.
    Object.setPrototypeOf(this, MCPError.prototype);
  }
}

/**
 * asMCPError narrows err to an {@link MCPError}, returning the error
 * when err is an MCPError and undefined otherwise.
 */
export function asMCPError(err: unknown): MCPError | undefined {
  return err instanceof MCPError ? err : undefined;
}

/** The decoded result of the lenny/create_session MCP tool. */
export interface MCPCreateSessionResult {
  /** The identifier of the created session. */
  sessionId: string;

  /** The session state the gateway reports for the new session. */
  state: string;
}

/**
 * The transport surface an {@link MCPClient} needs. It is the subset of
 * the {@link Client} internals the MCP client reads, passed in so the
 * MCP module does not reach into the Client's private fields.
 */
export interface MCPTransport {
  baseUrl: string;
  fetchImpl: typeof fetch;
  auth?: Authenticator;
  tenantId?: string;
}

/** textOf returns the concatenation of every text content block. */
function textOf(result: MCPToolResult): string {
  return result.content
    .filter((c) => c.type === 'text' && typeof c.text === 'string')
    .map((c) => c.text)
    .join('');
}

/**
 * MCPClient drives the §15.2 gateway MCP API. It speaks JSON-RPC 2.0
 * over HTTP POST to the gateway's /mcp endpoint: the same connection
 * carries the initialize handshake, tool discovery, and tool calls.
 *
 * Construct an MCPClient with {@link Client.mcp} so it inherits the
 * REST client's base URL, fetch implementation, authentication
 * credential, and tenant header.
 */
export class MCPClient {
  private readonly endpoint: string;
  private readonly transport: MCPTransport;
  private idSeq = 0;
  private initialized = false;

  constructor(transport: MCPTransport) {
    this.endpoint = `${transport.baseUrl}/mcp`;
    this.transport = transport;
  }

  /**
   * initialize performs the §15.2 MCP initialize handshake. It sends
   * the client's supported protocol version and clientInfo, and
   * returns the gateway's negotiated protocol version, capability set,
   * and serverInfo.
   *
   * Calling initialize is optional before listTools or callTool: those
   * methods perform the handshake on first use when it has not run
   * yet. Call it explicitly to read the negotiated protocol version or
   * the gateway serverInfo.
   */
  async initialize(): Promise<InitializeResult> {
    const result = await this.call('initialize', {
      protocolVersion: mcpProtocolVersion,
      capabilities: {},
      clientInfo: { name: mcpClientName, version: '0.1.0' },
    });
    this.initialized = true;
    return result as InitializeResult;
  }

  /**
   * listTools calls the §15.2 tools/list method and returns the
   * platform MCP tool catalog (lenny/create_session,
   * lenny/send_message, and the others). It runs the initialize
   * handshake first when it has not run yet.
   */
  async listTools(): Promise<MCPTool[]> {
    await this.ensureInitialized();
    const result = (await this.call('tools/list', {})) as { tools?: MCPTool[] };
    return result.tools ?? [];
  }

  /**
   * callTool calls the §15.2 tools/call method, invoking the named tool
   * with args. args is sent as the JSON-RPC arguments object; pass an
   * empty object for a tool that takes no arguments.
   *
   * A transport-level failure (unknown tool, invalid params) rejects
   * with an {@link MCPError}. A tool that runs and reports a failure
   * resolves to an {@link MCPToolResult} with isError set, matching the
   * MCP contract that a tool failure is a result rather than a
   * transport error. callTool runs the initialize handshake first when
   * it has not run yet.
   */
  async callTool(name: string, args: Record<string, unknown> = {}): Promise<MCPToolResult> {
    await this.ensureInitialized();
    if (!name) {
      throw new Error('lenny: MCP tool name is required');
    }
    const result = (await this.call('tools/call', { name, arguments: args })) as {
      content?: MCPContent[];
      isError?: boolean;
    };
    return { content: result.content ?? [], isError: result.isError ?? false };
  }

  /**
   * createSession invokes the §15.2 lenny/create_session MCP tool and
   * returns the created session identifier and state. It is the MCP
   * counterpart of {@link Client.createSession}.
   */
  async createSession(runtimeRef: string, userId?: string): Promise<MCPCreateSessionResult> {
    const args: Record<string, unknown> = { runtimeRef };
    if (userId) {
      args.userId = userId;
    }
    const res = await this.callTool('lenny/create_session', args);
    if (res.isError) {
      throw new Error(`lenny: lenny/create_session reported a failure: ${textOf(res)}`);
    }
    return JSON.parse(textOf(res)) as MCPCreateSessionResult;
  }

  /**
   * sendMessage invokes the §15.2 lenny/send_message MCP tool,
   * delivering content to the session and returning the agent's text
   * reply. It is the MCP counterpart of a §15.1 send-message REST call.
   */
  async sendMessage(sessionId: string, content: string): Promise<string> {
    const res = await this.callTool('lenny/send_message', { sessionId, content });
    if (res.isError) {
      throw new Error(`lenny: lenny/send_message reported a failure: ${textOf(res)}`);
    }
    return textOf(res);
  }

  /** ensureInitialized runs the initialize handshake once. */
  private async ensureInitialized(): Promise<void> {
    if (!this.initialized) {
      await this.initialize();
    }
  }

  /**
   * call executes one JSON-RPC 2.0 method against the gateway MCP
   * endpoint and returns the raw result. A JSON-RPC error object in the
   * response is thrown as an {@link MCPError}; a non-2xx HTTP status is
   * thrown as the typed REST ApiError so a single error-handling
   * strategy covers both surfaces.
   */
  private async call(method: string, params: unknown): Promise<unknown> {
    this.idSeq += 1;
    const headers = new Headers();
    headers.set('Content-Type', 'application/json');
    headers.set('Accept', 'application/json');
    headers.set('User-Agent', mcpUserAgent);
    if (this.transport.tenantId) {
      headers.set('X-Lenny-Tenant-ID', this.transport.tenantId);
    }
    if (this.transport.auth) {
      await this.transport.auth.apply(headers);
    }

    const resp = await this.transport.fetchImpl(this.endpoint, {
      method: 'POST',
      headers,
      body: JSON.stringify({
        jsonrpc: '2.0',
        id: this.idSeq,
        method,
        params,
      }),
    });
    const text = await resp.text();
    // A JSON-RPC transport error still returns HTTP 200; the error is
    // in the body. A non-2xx status is a gateway-level failure (auth
    // rejection, an unmounted endpoint) and is surfaced as the typed
    // REST error.
    if (resp.status < 200 || resp.status >= 300) {
      throw decodeApiError(resp.status, text);
    }
    let envelope: { result?: unknown; error?: { code?: number; message?: string; data?: unknown } };
    try {
      envelope = JSON.parse(text) as typeof envelope;
    } catch (err) {
      throw new Error(
        `lenny: decode MCP ${method} response: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
    if (envelope.error) {
      throw new MCPError(
        envelope.error.code ?? 0,
        envelope.error.message ?? 'unknown MCP error',
        envelope.error.data,
      );
    }
    return envelope.result;
  }
}
