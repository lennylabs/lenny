"""Stateless protocol adapter in front of the mock Lenny REST API.

Serves a subset of MCP 2026-07-28 (stateless Streamable HTTP, Tasks extension) at
POST /mcp and a subset of the A2A 1.0 HTTP+JSON binding at POST /a2a/message:stream.

The adapter keeps no per-request or per-session state between HTTP requests:
  - an MCP task ID is "<sessionId>.<messageId>", resolved through REST on every poll;
  - an A2A task ID is the same pair, and the A2A contextId is the Lenny session ID;
  - the caller's bearer is forwarded unchanged, so Lenny enforces ownership;
  - streams attach to the REST SSE stream at the sequence number REST returned.
Any replica can serve any request.
"""
import json, sys, http.client
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse

LENNY = ("127.0.0.1", 18080)
VERSION = "2026-07-28"
TASKS_EXT = "io.modelcontextprotocol/tasks"

TOOLS = [
    {"name": "lenny_create_session", "description": "Create a session.",
     "inputSchema": {"type": "object", "properties": {"runtime": {"type": "string"}}}},
    {"name": "lenny_send_message", "description": "Send a message; returns a task under the Tasks extension.",
     "inputSchema": {"type": "object", "properties": {"sessionId": {"type": "string"}, "text": {"type": "string"},
                                                       "idempotencyKey": {"type": "string"}}, "required": ["sessionId", "text"]}},
    {"name": "lenny_attach_session", "description": "Stream session events from a sequence number.",
     "inputSchema": {"type": "object", "properties": {"sessionId": {"type": "string"}, "resumeFromSeq": {"type": "integer"},
                                                       "maxEvents": {"type": "integer"}}, "required": ["sessionId"]}},
]


def rest(method, path, bearer, body=None, headers=None):
    c = http.client.HTTPConnection(*LENNY, timeout=10)
    h = {"Authorization": bearer, "Content-Type": "application/json", **(headers or {})}
    c.request(method, path, body=json.dumps(body) if body is not None else None, headers=h)
    r = c.getresponse()
    data = json.loads(r.read() or b"{}")
    c.close()
    return r.status, data


def rest_sse(path, bearer, last_event_id):
    """Yield (seq, kind, data) from the REST SSE stream, starting after last_event_id."""
    c = http.client.HTTPConnection(*LENNY, timeout=30)
    h = {"Authorization": bearer, "Accept": "text/event-stream"}
    if last_event_id:
        h["Last-Event-ID"] = str(last_event_id)
    c.request("GET", path, headers=h)
    r = c.getresponse()
    seq, kind, data = None, None, None
    try:
        for raw in r:
            line = raw.decode().rstrip("\n")
            if line.startswith(":"):
                continue
            if line == "":
                if kind:
                    yield seq, kind, json.loads(data)
                seq, kind, data = None, None, None
            elif line.startswith("id: "):
                seq = int(line[4:])
            elif line.startswith("event: "):
                kind = line[7:]
            elif line.startswith("data: "):
                data = line[6:]
    finally:
        c.close()


def split_task(task_id):
    sid, _, mid = task_id.partition(".")
    return sid, mid


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _send(self, code, obj, headers=None):
        data = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(data)

    def _sse_start(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Connection", "close")
        self.end_headers()

    def _sse(self, obj, event_id=None):
        prefix = f"id: {event_id}\n" if event_id is not None else ""
        self.wfile.write(f"{prefix}data: {json.dumps(obj)}\n\n".encode())
        self.wfile.flush()

    def do_POST(self):
        bearer = self.headers.get("Authorization", "")
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
        path = urlparse(self.path).path
        if path == "/mcp":
            return self.mcp(bearer, body)
        if path == "/a2a/message:stream":
            return self.a2a_stream(bearer, body)
        self._send(404, {"error": "no route"})

    # ---------------- MCP 2026-07-28 ----------------
    def mcp(self, bearer, req):
        rid, method, params = req.get("id"), req.get("method"), req.get("params", {})
        meta = params.get("_meta", {})
        ver = meta.get("io.modelcontextprotocol/protocolVersion")
        if self.headers.get("MCP-Protocol-Version") != ver or self.headers.get("Mcp-Method") != method:
            return self._send(400, {"jsonrpc": "2.0", "id": rid, "error": {"code": -32020, "message": "HeaderMismatch"}})
        if ver != VERSION:
            return self._send(400, {"jsonrpc": "2.0", "id": rid, "error": {"code": -32022, "message": "UnsupportedProtocolVersion",
                                                                             "data": {"supported": [VERSION]}}})
        caps = meta.get("io.modelcontextprotocol/clientCapabilities", {})
        ok = lambda result: self._send(200, {"jsonrpc": "2.0", "id": rid, "result": {"resultType": "complete", **result}})
        rpc_err = lambda code, msg, data=None: self._send(200, {"jsonrpc": "2.0", "id": rid, "error": {"code": code, "message": msg, "data": data}})

        if method == "server/discover":
            return ok({"supportedVersions": [VERSION], "capabilities": {"tools": {}, "extensions": {TASKS_EXT: {}}},
                       "serverInfo": {"name": "lenny-mcp-adapter-poc", "version": "0"}})
        if method == "tools/list":
            return ok({"tools": TOOLS, "ttlMs": 300000, "cacheScope": "public"})
        if method == "tools/call":
            name, args = params.get("name"), params.get("arguments", {})
            if name == "lenny_create_session":
                st, d = rest("POST", "/v1/sessions", bearer, {"runtime": args.get("runtime", "echo")})
                return self.tool_result(rid, st, d, {"sessionId": d.get("id")})
            if name == "lenny_send_message":
                hdr = {"Idempotency-Key": args["idempotencyKey"]} if args.get("idempotencyKey") else {}
                st, d = rest("POST", f"/v1/sessions/{args['sessionId']}/messages", bearer,
                             {"input": [{"type": "text", "inline": args["text"]}]}, hdr)
                if st >= 400:
                    return self.tool_result(rid, st, d, None)
                mid = d["deliveryReceipts"][0]["messageId"]
                if TASKS_EXT in caps.get("extensions", {}):
                    return self._send(200, {"jsonrpc": "2.0", "id": rid, "result": {"resultType": "task", "task": {
                        "taskId": f"{args['sessionId']}.{mid}", "status": "working", "ttlMs": 86400000, "pollIntervalMs": 200}}})
                return self.tool_result(rid, st, d, {"messageId": mid, "seq": d["seq"]})
            if name == "lenny_attach_session":
                return self.mcp_attach(rid, bearer, args)
            return rpc_err(-32602, f"unknown tool {name}")
        if method == "tasks/get":
            sid, mid = split_task(params.get("taskId", ""))
            task = self.task_state(bearer, sid, mid)
            return ok(task) if task else rpc_err(-32602, "task not found")
        if method == "tasks/update":
            sid, _ = split_task(params.get("taskId", ""))
            for eid, resp in params.get("inputResponses", {}).items():
                rest("POST", f"/v1/sessions/{sid}/elicitations/{eid}/respond", bearer,
                     {"response": resp.get("content") if resp.get("action") == "accept" else None})
            return ok({})
        if method == "tasks/cancel":
            sid, _ = split_task(params.get("taskId", ""))
            st, d = rest("DELETE", f"/v1/sessions/{sid}", bearer)
            return ok({}) if st < 400 or st == 409 else rpc_err(-32602, "task not found")
        return self._send(404, {"jsonrpc": "2.0", "id": rid, "error": {"code": -32601, "message": "Method not found"}})

    def tool_result(self, rid, status, data, structured):
        if status >= 400:  # MP-2: tool error with Lenny's envelope in structuredContent
            res = {"resultType": "complete", "isError": True, "content": [{"type": "text", "text": data["error"]["message"]}],
                   "structuredContent": data["error"]}
        else:
            res = {"resultType": "complete", "content": [{"type": "text", "text": json.dumps(structured)}], "structuredContent": structured}
        self._send(200, {"jsonrpc": "2.0", "id": rid, "result": res})

    def task_state(self, bearer, sid, mid):
        """Derive the MCP task state from REST reads only."""
        st, m = rest("GET", f"/v1/sessions/{sid}/messages/{mid}", bearer)
        if st == 404:
            return None
        base = {"taskId": f"{sid}.{mid}", "ttlMs": 86400000, "pollIntervalMs": 200}
        _, s = rest("GET", f"/v1/sessions/{sid}", bearer)
        if s.get("state") == "cancelled":
            return {**base, "status": "cancelled"}
        if not m.get("turn"):
            return {**base, "status": "working", "statusMessage": "queued"}
        _, t = rest("GET", f"/v1/sessions/{sid}/turns/{m['turn']}", bearer)
        if t.get("status") == "completed":
            return {**base, "status": "completed", "result": {"resultType": "complete",
                    "content": [{"type": "text", "text": p["inline"]} for p in t["parts"]]}}
        _, pend = rest("GET", f"/v1/sessions/{sid}/elicitations?state=pending", bearer)
        reqs = {e["id"]: {"method": "elicitation/create", "params": e["request"]} for e in pend["items"] if e["turn"] == m["turn"]}
        if reqs:
            return {**base, "status": "input_required", "inputRequests": reqs}
        return {**base, "status": "working"}

    def mcp_attach(self, rid, bearer, args):
        sid, resume, cap = args["sessionId"], int(args.get("resumeFromSeq") or 0), int(args.get("maxEvents") or 1000)
        st, _ = rest("GET", f"/v1/sessions/{sid}", bearer)
        if st == 404:
            return self.tool_result(rid, 404, {"error": {"code": "RESOURCE_NOT_FOUND", "message": "session not found"}}, None)
        self._sse_start()
        last, n = resume, 0
        try:
            for seq, kind, data in rest_sse(f"/v1/sessions/{sid}/events", bearer, resume):
                if kind == "gap_detected":
                    self._sse({"jsonrpc": "2.0", "method": "notifications/lenny/gap", "params": data})
                    continue
                self._sse({"jsonrpc": "2.0", "method": "notifications/lenny/event", "params": {"seq": seq, "kind": kind, "data": data}})
                last, n = seq, n + 1
                if n >= cap or kind == "terminated":
                    break
            self._sse({"jsonrpc": "2.0", "id": rid, "result": {"resultType": "complete",
                       "content": [{"type": "text", "text": f"lastSeq={last}"}], "structuredContent": {"lastSeq": last}}})
        except (BrokenPipeError, ConnectionResetError):
            pass  # 2026-07-28: closing the response stream cancels the request; nothing to clean up

    # ---------------- A2A 1.0, HTTP+JSON binding ----------------
    def a2a_stream(self, bearer, body):
        msg = body.get("message", {})
        sid = msg.get("contextId")
        if not sid:
            st, d = rest("POST", "/v1/sessions", bearer, {"runtime": "echo", "labels": {"a2a": "1"}})
            if st >= 400:
                return self._send(st, {"type": "about:blank", "title": d["error"]["code"]})
            sid = d["id"]
        text = " ".join(p.get("text", "") for p in msg.get("parts", []))
        st, d = rest("POST", f"/v1/sessions/{sid}/messages", bearer, {"input": [{"type": "text", "inline": text}]},
                     {"Idempotency-Key": "a2a:" + msg.get("messageId", "")})
        if st >= 400:
            code = 404 if st == 404 else 400
            return self._send(code, {"type": "about:blank", "title": "TaskNotFoundError" if st == 404 else d["error"]["code"]})
        mid, seq = d["deliveryReceipts"][0]["messageId"], d["seq"]
        task_id = f"{sid}.{mid}"
        self._sse_start()
        self._sse({"task": {"id": task_id, "contextId": sid, "status": {"state": "TASK_STATE_SUBMITTED"}}})
        turn = None
        try:
            for ev_seq, kind, data in rest_sse(f"/v1/sessions/{sid}/events", bearer, seq):
                if kind == "message_state" and data.get("messageId") == mid:
                    turn = data["turn"]
                    self._sse({"statusUpdate": {"taskId": task_id, "contextId": sid, "status": {"state": "TASK_STATE_WORKING"}}}, ev_seq)
                elif turn and kind == "output" and data.get("turn") == turn:
                    for p in data["parts"]:
                        self._sse({"artifactUpdate": {"taskId": task_id, "contextId": sid, "append": True,
                                   "artifact": {"artifactId": f"{task_id}.out", "parts": [{"text": p["inline"]}]}}}, ev_seq)
                elif turn and kind == "elicitation":
                    self._sse({"statusUpdate": {"taskId": task_id, "contextId": sid, "status": {"state": "TASK_STATE_INPUT_REQUIRED",
                               "message": {"role": "ROLE_AGENT", "parts": [{"data": data}]}}}}, ev_seq)
                    break  # the A2A stream ends at an interrupted state
                elif turn and kind == "turn_end" and data.get("turn") == turn:
                    self._sse({"artifactUpdate": {"taskId": task_id, "contextId": sid, "lastChunk": True,
                               "artifact": {"artifactId": f"{task_id}.out", "parts": []}}}, ev_seq)
                    self._sse({"statusUpdate": {"taskId": task_id, "contextId": sid, "status": {"state": "TASK_STATE_COMPLETED"}}}, ev_seq)
                    break
                elif kind == "terminated":
                    self._sse({"statusUpdate": {"taskId": task_id, "contextId": sid, "status": {"state": "TASK_STATE_CANCELED"}}}, ev_seq)
                    break
        except (BrokenPipeError, ConnectionResetError):
            pass


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18081
    ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
