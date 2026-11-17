"""Drives the mock Lenny and two adapter replicas (A on 18081, B on 18082).

Run: python3 -I demo.py   (from this directory; it starts and stops the servers)
Every step alternates replicas to show that no step depends on adapter state.
"""
import json, os, subprocess, sys, time, http.client

HERE = os.path.dirname(os.path.abspath(__file__))
A, B = 18081, 18082
ALICE, BOB = "Bearer tok-alice", "Bearer tok-bob"
CAPS = {"extensions": {"io.modelcontextprotocol/tasks": {}}}
rid = 0


def mcp(port, method, params, bearer=ALICE, caps=CAPS, stream_limit=None):
    global rid
    rid += 1
    params = {**params, "_meta": {"io.modelcontextprotocol/protocolVersion": "2026-07-28",
                                  "io.modelcontextprotocol/clientCapabilities": caps}}
    c = http.client.HTTPConnection("127.0.0.1", port, timeout=20)
    c.request("POST", "/mcp", body=json.dumps({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}),
              headers={"Authorization": bearer, "Content-Type": "application/json", "Accept": "application/json, text/event-stream",
                       "MCP-Protocol-Version": "2026-07-28", "Mcp-Method": method})
    r = c.getresponse()
    if r.getheader("Content-Type", "").startswith("text/event-stream"):
        out = []
        for raw in r:
            line = raw.decode().strip()
            if line.startswith("data: "):
                out.append(json.loads(line[6:]))
                if stream_limit and len(out) >= stream_limit:
                    break  # simulate a dropped stream
        c.close()
        return out
    body = json.loads(r.read())
    c.close()
    return body


def a2a_stream(port, message, bearer=ALICE):
    c = http.client.HTTPConnection("127.0.0.1", port, timeout=20)
    c.request("POST", "/a2a/message:stream", body=json.dumps({"message": message}),
              headers={"Authorization": bearer, "Content-Type": "application/json", "A2A-Version": "1.0"})
    r = c.getresponse()
    out = [json.loads(l.decode()[6:]) for l in r if l.decode().startswith("data: ")]
    c.close()
    return out


def show(title, obj):
    print(f"\n== {title}\n{json.dumps(obj, indent=1)[:1200]}")


def main():
    procs = [subprocess.Popen([sys.executable, "-I", os.path.join(HERE, "mock_lenny.py"), "18080"]),
             subprocess.Popen([sys.executable, "-I", os.path.join(HERE, "adapter.py"), str(A)]),
             subprocess.Popen([sys.executable, "-I", os.path.join(HERE, "adapter.py"), str(B)])]
    time.sleep(0.8)
    try:
        r = mcp(A, "tools/call", {"name": "lenny_create_session", "arguments": {}})
        sid = r["result"]["structuredContent"]["sessionId"]
        show("1. A: create session", r["result"]["structuredContent"])

        r = mcp(A, "tools/call", {"name": "lenny_send_message", "arguments": {"sessionId": sid, "text": "please confirm deploy",
                                                                             "idempotencyKey": "k-1"}})
        task = r["result"]["task"]["taskId"]
        show("2. A: send_message under the Tasks extension", r["result"])
        r2 = mcp(B, "tools/call", {"name": "lenny_send_message", "arguments": {"sessionId": sid, "text": "please confirm deploy",
                                                                              "idempotencyKey": "k-1"}})
        print("   retry with the same idempotency key through B returns the same task:", r2["result"]["task"]["taskId"] == task)

        for _ in range(40):
            g = mcp(B, "tasks/get", {"taskId": task})["result"]
            if g["status"] == "input_required":
                break
            time.sleep(0.1)
        show("3. B: tasks/get reaches input_required with the pending elicitation", g)

        show("4. B as bob: tasks/get on alice's task", mcp(B, "tasks/get", {"taskId": task}, bearer=BOB))

        eid = next(iter(g["inputRequests"]))
        mcp(A, "tasks/update", {"taskId": task, "inputResponses": {eid: {"action": "accept", "content": {"confirm": True}}}})
        for _ in range(40):
            g = mcp(A, "tasks/get", {"taskId": task})["result"]
            if g["status"] == "completed":
                break
            time.sleep(0.1)
        show("5. A: tasks/update answered it, then tasks/get is completed", g)

        part1 = mcp(A, "tools/call", {"name": "lenny_attach_session", "arguments": {"sessionId": sid, "resumeFromSeq": 0}}, stream_limit=4)
        last = [m["params"]["seq"] for m in part1 if m.get("method") == "notifications/lenny/event"][-1]
        part2 = mcp(B, "tools/call", {"name": "lenny_attach_session", "arguments": {"sessionId": sid, "resumeFromSeq": last, "maxEvents": 3}})
        seqs1 = [m["params"]["seq"] for m in part1 if m.get("method") == "notifications/lenny/event"]
        seqs2 = [m["params"]["seq"] for m in part2 if m.get("method") == "notifications/lenny/event"]
        gaps = [m["params"] for m in part1 + part2 if m.get("method") == "notifications/lenny/gap"]
        show("6. attach through A, drop, resume through B from the last seq", {"A": seqs1, "B": seqs2, "gapFrames": gaps})

        evs = a2a_stream(B, {"messageId": "m-42", "contextId": sid, "role": "ROLE_USER", "parts": [{"text": "status report"}]})
        kinds = [next(iter(e)) for e in evs]
        text = "".join(p["text"] for e in evs if "artifactUpdate" in e for p in e["artifactUpdate"]["artifact"]["parts"])
        show("7. B: A2A message:stream on the same context", {"events": kinds, "artifactText": text,
                                                             "final": evs[-1]["statusUpdate"]["status"]["state"]})
        old = mcp(A, "tools/call", {"name": "lenny_attach_session", "arguments": {"sessionId": sid, "resumeFromSeq": 1, "maxEvents": 2}})
        show("7b. A: resume from seq 1 after the ring evicted it", [m for m in old if m.get("method") != "notifications/lenny/event"])
        evs2 = a2a_stream(A, {"messageId": "m-43", "role": "ROLE_USER", "parts": [{"text": "please confirm restart"}]})
        show("8. A: A2A message:stream on a new context stops at INPUT_REQUIRED",
             {"events": [next(iter(e)) for e in evs2], "last": evs2[-1]})
    finally:
        for p in procs:
            p.terminate()


if __name__ == "__main__":
    main()
