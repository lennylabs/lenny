// SPDX-License-Identifier: MIT
import { LineReader, dialUnixSocket } from "./transport.js";
import { SCHEMA_VERSION } from "./types.js";
// MCP_PROTOCOL_VERSION is the §15.4.3 intra-pod MCP spec version the
// adapter's local MCP servers speak.
const MCP_PROTOCOL_VERSION = "2025-03-26";
// NONCE_PARAM_KEY is the §15.4.3 canonical injection key for the
// intra-pod MCP nonce: the top-level field of the MCP initialize
// request's params object. The adapter validates and strips it before
// forwarding the request to its MCP server implementation.
const NONCE_PARAM_KEY = "_lennyNonce";
// stampParts sets schemaVersion on every part that left it unset,
// honoring the §15.4.1 producer obligation.
function stampParts(parts) {
    return parts.map((p) => p.schemaVersion === undefined
        ? { ...p, schemaVersion: SCHEMA_VERSION }
        : p);
}
// McpClient is a minimal §15.4.3 intra-pod MCP client over a single
// Unix socket. The SDK issues calls sequentially; the connection is
// serialized by a promise chain.
export class McpClient {
    conn;
    reader;
    nextId = 0;
    pending = Promise.resolve();
    constructor(conn, reader) {
        this.conn = conn;
        this.reader = reader;
    }
    // connect dials the intra-pod MCP socket, completes the
    // nonce-authenticated initialize handshake (§15.4.3), and discovers
    // the tool set via tools/list. The nonce is presented as the
    // top-level params._lennyNonce field of the initialize request.
    static async connect(socket, nonce, clientName, timeoutMs) {
        const conn = await dialUnixSocket(socket, timeoutMs);
        const reader = new LineReader(conn);
        const client = new McpClient(conn, reader);
        try {
            await client.call("initialize", {
                [NONCE_PARAM_KEY]: nonce,
                protocolVersion: MCP_PROTOCOL_VERSION,
                clientInfo: { name: clientName, version: "1.0.0" },
            });
            await client.call("tools/list", {});
        }
        catch (err) {
            conn.destroy();
            throw new Error(`connect ${socket}: ${err.message}`);
        }
        return client;
    }
    // call sends one JSON-RPC request and reads the matching response.
    async call(method, params) {
        const run = this.pending.then(() => this.callLocked(method, params));
        this.pending = run.then(() => undefined, () => undefined);
        return run;
    }
    async callLocked(method, params) {
        const id = ++this.nextId;
        const request = { jsonrpc: "2.0", id, method, params };
        await new Promise((resolve, reject) => {
            this.conn.write(JSON.stringify(request) + "\n", (err) => err ? reject(err) : resolve());
        });
        const line = await this.reader.next();
        if (line === null) {
            throw new Error(`${method}: MCP server closed the connection`);
        }
        let resp;
        try {
            resp = JSON.parse(line);
        }
        catch (err) {
            throw new Error(`${method}: response not JSON: ${err.message}`);
        }
        if (resp.error) {
            throw new Error(`${method}: rpc error ${resp.error.code}: ${resp.error.message}`);
        }
        return resp.result;
    }
    // callTool invokes one MCP tool via tools/call and returns the raw
    // result.
    callTool(name, args) {
        return this.call("tools/call", { name, arguments: args });
    }
    // close releases the MCP connection.
    close() {
        this.conn.destroy();
    }
}
// decodeTaskResults decodes a lenny/await_children result, accepting
// either a list (mode all/settled) or a single object (mode any).
function decodeTaskResults(raw) {
    if (Array.isArray(raw)) {
        return raw;
    }
    return [raw];
}
// decodeParts decodes a result that is either a bare OutputPart array
// or an object with a parts field.
function decodeParts(raw) {
    if (Array.isArray(raw)) {
        return raw;
    }
    const wrapped = raw;
    return wrapped.parts ?? [];
}
// Tools is the §15.7 platform MCP tool surface a Standard-level or
// Full-level runtime uses. It wraps the §15.4.3 intra-pod MCP client to
// the adapter's platform MCP server and exposes typed helpers for the
// §8.5 / §4.7 platform tool set.
export class Tools {
    platform;
    connectors;
    constructor(platform, connectors) {
        this.platform = platform;
        this.connectors = connectors;
    }
    // dial dials the §15.4.3 platform MCP server and every connector MCP
    // server advertised in the manifest, completing the manifest-nonce
    // handshake on each.
    static async dial(manifest, timeoutMs) {
        if (!manifest.platformMcpServer?.socket) {
            throw new Error("adapter manifest has no platform MCP server socket");
        }
        const nonce = manifest.mcpNonce ?? "";
        const platform = await McpClient.connect(manifest.platformMcpServer.socket, nonce, "lenny-runtime-sdk-ts", timeoutMs);
        const connectors = new Map();
        for (const conn of manifest.connectorServers ?? []) {
            try {
                const cc = await McpClient.connect(conn.socket, nonce, "lenny-runtime-sdk-ts", timeoutMs);
                connectors.set(conn.id, cc);
            }
            catch (err) {
                platform.close();
                for (const c of connectors.values()) {
                    c.close();
                }
                throw new Error(`connect connector MCP server "${conn.id}": ${err.message}`);
            }
        }
        return new Tools(platform, connectors);
    }
    // close releases the platform and connector MCP connections.
    close() {
        this.platform.close();
        for (const c of this.connectors.values()) {
            c.close();
        }
    }
    // connector returns the MCP client for the named §4.7 connector MCP
    // server, or undefined when no connector with that id was advertised.
    connector(id) {
        return this.connectors.get(id);
    }
    // delegateTask invokes the §8.2 lenny/delegate_task platform tool. It
    // spawns a child sub-task whose input is parts and returns the child
    // TaskHandle. budget, when set, is forwarded as the delegation budget
    // metadata the §8.3 policy validates.
    async delegateTask(target, parts, budget) {
        const task = { input: stampParts(parts) };
        if (budget) {
            task.budget = budget;
        }
        const raw = (await this.platform.callTool("lenny/delegate_task", {
            target,
            task,
        }));
        if (!raw || !raw.taskId) {
            throw new Error("lenny/delegate_task returned an empty taskId");
        }
        return { taskId: raw.taskId };
    }
    // awaitChildren invokes the §8.5 lenny/await_children platform tool.
    // It blocks until the named children settle per mode (all, any,
    // settled) and returns their TaskResult values.
    async awaitChildren(childIds, mode = "all") {
        const raw = await this.platform.callTool("lenny/await_children", {
            child_ids: childIds,
            mode,
        });
        return decodeTaskResults(raw);
    }
    // cancelChild invokes the §8.5 lenny/cancel_child platform tool.
    async cancelChild(childId) {
        await this.platform.callTool("lenny/cancel_child", { child_id: childId });
    }
    // discoverAgents invokes the §4.7 lenny/discover_agents platform tool
    // and returns the raw result for the runtime to decode.
    discoverAgents(query) {
        return this.platform.callTool("lenny/discover_agents", query);
    }
    // output invokes the §4.7 lenny/output platform tool, emitting output
    // parts incrementally to the parent or client. The stdout response
    // frame is still required to signal turn completion (§15.4.1).
    async output(parts) {
        await this.platform.callTool("lenny/output", {
            output: stampParts(parts),
        });
    }
    // requestInput invokes the §4.7 lenny/request_input platform tool. It
    // blocks until an answer arrives and returns the answer parts.
    async requestInput(prompt) {
        const raw = await this.platform.callTool("lenny/request_input", {
            parts: stampParts(prompt),
        });
        return decodeParts(raw);
    }
    // requestElicitation invokes the §4.7 lenny/request_elicitation
    // platform tool and returns the raw result.
    requestElicitation(args) {
        return this.platform.callTool("lenny/request_elicitation", args);
    }
    // sendMessage invokes the §4.7 lenny/send_message platform tool and
    // returns the raw delivery_receipt for the runtime to decode.
    sendMessage(args) {
        return this.platform.callTool("lenny/send_message", args);
    }
    // memoryWrite invokes the §4.7 lenny/memory_write platform tool.
    async memoryWrite(args) {
        await this.platform.callTool("lenny/memory_write", args);
    }
    // memoryQuery invokes the §4.7 lenny/memory_query platform tool and
    // returns the raw result.
    memoryQuery(args) {
        return this.platform.callTool("lenny/memory_query", args);
    }
    // getTaskTree invokes the §4.7 lenny/get_task_tree platform tool and
    // returns the raw result.
    getTaskTree(args) {
        return this.platform.callTool("lenny/get_task_tree", args);
    }
    // setTracingContext invokes the §4.7 lenny/set_tracing_context
    // platform tool, registering tracing identifiers that propagate
    // through delegation (§16.3).
    async setTracingContext(ctx) {
        await this.platform.callTool("lenny/set_tracing_context", ctx);
    }
    // call invokes an arbitrary tool on the platform MCP server by name.
    // It is the escape hatch for tools the typed helpers do not cover.
    call(name, args) {
        return this.platform.callTool(name, args);
    }
}
//# sourceMappingURL=mcp.js.map