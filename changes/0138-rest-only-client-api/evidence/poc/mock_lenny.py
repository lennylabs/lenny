"""Mock of the Lenny REST surface, reduced to what the spike needs.

Existing routes (spec 15.1): POST /v1/sessions, POST /v1/sessions/{id}/messages,
GET /v1/sessions/{id}, DELETE /v1/sessions/{id}, GET /v1/sessions/{id}/events (SSE,
Last-Event-ID, gap_detected), POST /v1/sessions/{id}/elicitations/{eid}/respond.

Proposed additions exercised here (marked NEW):
  - receipt.seq on POST /messages, and lastSeq on GET /v1/sessions/{id}
  - Idempotency-Key honoured on POST /messages
  - GET /v1/sessions/{id}/messages/{messageId}      (message state and turn)
  - GET /v1/sessions/{id}/turns/{n}                 (turn status and end-of-turn message)
  - GET /v1/sessions/{id}/elicitations?state=pending (pending input requests)

The "runtime" echoes the message in chunks. A message containing "confirm" raises a
form elicitation and waits for the answer before ending the turn.
"""
import json, sys, threading, time, uuid
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse, parse_qs

TOKENS = {"tok-alice": "alice", "tok-bob": "bob"}
RING_DEPTH = 12  # small on purpose so the gap path is reachable

lock = threading.Condition()
sessions = {}
idem = {}


class Session:
    def __init__(self, owner, runtime, labels):
        self.id = "sess_" + uuid.uuid4().hex[:10]
        self.owner, self.runtime, self.labels = owner, runtime, labels
        self.state = "idle"
        self.events = []          # ring buffer of (seq, kind, payload)
        self.last_seq = 0
        self.messages = {}        # id -> {state, turn}
        self.turns = {}           # n -> {status, parts}
        self.turn_count = 0
        self.elicitations = {}    # id -> {state, request, response}

    def emit(self, kind, payload):  # caller holds lock
        self.last_seq += 1
        self.events.append((self.last_seq, kind, payload))
        if len(self.events) > RING_DEPTH:
            self.events.pop(0)
        lock.notify_all()


def run_turn(s, mid, text):
    with lock:
        s.turn_count += 1
        n = s.turn_count
        s.state = "running"
        s.messages[mid].update(state="consumed", turn=n)
        s.turns[n] = {"turn": n, "status": "running", "parts": []}
        s.emit("message_state", {"messageId": mid, "state": "consumed", "turn": n})
        s.emit("state_change", {"from": "idle", "to": "running"})
        s.emit("turn_start", {"turn": n, "causedBy": mid})
    out = "Echo: " + text
    for i in range(0, len(out), 8):
        time.sleep(0.05)
        with lock:
            s.emit("output", {"turn": n, "parts": [{"type": "text", "op": "append", "inline": out[i:i + 8]}]})
    if "confirm" in text:
        eid = "elic_" + uuid.uuid4().hex[:6]
        req = {"mode": "form", "message": "Confirm the action?",
               "requestedSchema": {"type": "object", "properties": {"confirm": {"type": "boolean"}}, "required": ["confirm"]}}
        with lock:
            s.elicitations[eid] = {"id": eid, "state": "pending", "request": req, "turn": n}
            s.emit("elicitation", {"elicitationId": eid, **req})
            while s.elicitations[eid]["state"] == "pending" and s.state not in ("cancelled",):
                lock.wait(0.5)
            answer = s.elicitations[eid].get("response")
            out += " | answer=" + json.dumps(answer)
            s.emit("output", {"turn": n, "parts": [{"type": "text", "op": "append", "inline": " | answer=" + json.dumps(answer)}]})
    with lock:
        if s.state == "cancelled":
            return
        s.turns[n].update(status="completed", parts=[{"type": "text", "inline": out}])
        s.emit("turn_end", {"turn": n, "status": "completed", "consumed": [mid]})
        s.state = "idle"
        s.emit("state_change", {"from": "running", "to": "idle"})


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _json(self, code, body, headers=None):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(data)

    def _err(self, code, ecode, cat, msg, retryable=False):
        self._json(code, {"error": {"code": ecode, "category": cat, "message": msg, "retryable": retryable}})

    def _principal(self):
        auth = self.headers.get("Authorization", "")
        return TOKENS.get(auth.removeprefix("Bearer ").strip())

    def _body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return json.loads(self.rfile.read(n) or b"{}")

    def _session(self, sid, who):
        s = sessions.get(sid)
        if not s or s.owner != who:  # spec 10.2 Session ownership rule 7: hide existence
            self._err(404, "RESOURCE_NOT_FOUND", "PERMANENT", "session not found")
            return None
        return s

    def do_POST(self):
        who = self._principal()
        if not who:
            return self._err(401, "UNAUTHORIZED", "POLICY", "bearer required")
        p = urlparse(self.path).path.strip("/").split("/")
        body = self._body()
        if p == ["v1", "sessions"]:
            s = Session(who, body.get("runtime", "echo"), body.get("labels", {}))
            with lock:
                sessions[s.id] = s
                s.emit("state_change", {"from": "created", "to": "idle"})
            return self._json(201, {"id": s.id, "state": s.state, "lastSeq": s.last_seq})
        if len(p) == 4 and p[3] == "messages":
            s = self._session(p[2], who)
            if not s:
                return
            key = self.headers.get("Idempotency-Key")
            if key and (who, key) in idem:  # spec 11.5 extended to message send (NEW)
                return self._json(200, idem[(who, key)])
            if s.state in ("cancelled", "completed"):
                return self._err(409, "INVALID_STATE_TRANSITION", "PERMANENT", "terminal")
            mid = "msg_" + uuid.uuid4().hex[:8]
            text = " ".join(x.get("inline", "") for x in body.get("input", []))
            with lock:
                s.messages[mid] = {"messageId": mid, "state": "delivered", "turn": None}
                seq = s.last_seq  # NEW: the SeqNum at write time, so a reader can attach race-free
            receipt = {"deliveryReceipts": [{"messageId": mid, "status": "delivered"}], "seq": seq}
            if key:
                idem[(who, key)] = receipt
            threading.Thread(target=run_turn, args=(s, mid, text), daemon=True).start()
            return self._json(202, receipt)
        if len(p) == 6 and p[3] == "elicitations" and p[5] == "respond":
            s = self._session(p[2], who)
            if not s:
                return
            with lock:
                e = s.elicitations.get(p[4])
                if not e or e["state"] != "pending":
                    return self._err(404, "RESOURCE_NOT_FOUND", "PERMANENT", "no pending elicitation")
                e.update(state="answered", response=body.get("response"))
                s.emit("elicitation_resolved", {"elicitationId": p[4]})
            return self._json(200, {"elicitationId": p[4], "state": "answered"})
        self._err(404, "RESOURCE_NOT_FOUND", "PERMANENT", "no route")

    def do_DELETE(self):
        who = self._principal()
        p = urlparse(self.path).path.strip("/").split("/")
        s = self._session(p[2], who) if len(p) == 3 else None
        if not s:
            return
        with lock:
            if s.state in ("cancelled", "completed"):
                return self._err(409, "INVALID_STATE_TRANSITION", "PERMANENT", "terminal")
            prev, s.state = s.state, "cancelled"
            s.emit("state_change", {"from": prev, "to": "cancelled"})
            s.emit("terminated", {"code": "cancelled"})
        self._json(200, {"id": s.id, "state": "cancelled"})

    def do_GET(self):
        who = self._principal()
        if not who:
            return self._err(401, "UNAUTHORIZED", "POLICY", "bearer required")
        u = urlparse(self.path)
        p = u.path.strip("/").split("/")
        if len(p) < 3 or p[:2] != ["v1", "sessions"]:
            return self._err(404, "RESOURCE_NOT_FOUND", "PERMANENT", "no route")
        s = self._session(p[2], who)
        if not s:
            return
        with lock:
            if len(p) == 3:
                return self._json(200, {"id": s.id, "state": s.state, "lastSeq": s.last_seq, "labels": s.labels})
            if p[3] == "messages" and len(p) == 5:
                m = s.messages.get(p[4])
                return self._json(200, m) if m else self._err(404, "RESOURCE_NOT_FOUND", "PERMANENT", "no message")
            if p[3] == "turns" and len(p) == 5:
                t = s.turns.get(int(p[4]))
                return self._json(200, t) if t else self._err(404, "RESOURCE_NOT_FOUND", "PERMANENT", "no turn")
            if p[3] == "elicitations" and len(p) == 4:
                want = parse_qs(u.query).get("state", ["pending"])[0]
                items = [e for e in s.elicitations.values() if e["state"] == want]
                return self._json(200, {"items": items})
        if p[3] == "events":
            return self._sse(s)
        self._err(404, "RESOURCE_NOT_FOUND", "PERMANENT", "no route")

    def _sse(self, s):
        after = int(self.headers.get("Last-Event-ID") or 0)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()
        try:
            with lock:
                oldest = s.events[0][0] if s.events else s.last_seq + 1
                if after and after + 1 < oldest:  # spec 10.4 Event replay buffer: gap_detected
                    self.wfile.write(f"event: gap_detected\ndata: {json.dumps({'lastSeenSeq': after, 'nextSeq': oldest})}\n\n".encode())
            while True:
                with lock:
                    pending = [e for e in s.events if e[0] > after]
                    if not pending:
                        lock.wait(1.0)
                        pending = [e for e in s.events if e[0] > after]
                for seq, kind, payload in pending:
                    self.wfile.write(f"id: {seq}\nevent: {kind}\ndata: {json.dumps(payload)}\n\n".encode())
                    after = seq
                if not pending:
                    self.wfile.write(b":keepalive\n\n")
                self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            return


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18080
    ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
