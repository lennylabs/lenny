---
layout: default
title: "Python Runtime SDK"
parent: "Runtime SDK Examples"
grand_parent: "Runtime Author Guide"
nav_order: 2
---

# Python Runtime SDK

This page presents a complete file summarizer runtime in Python. It implements the Minimum tier, reads workspace files via adapter-local tools, and produces summaries. The full source is ~180 lines including comments.

---

## Complete Source Code

```python
#!/usr/bin/env python3
"""
file-summarizer: A Lenny runtime that reads workspace files and produces summaries.

Integration tier: Minimum
  - Reads JSON Lines from stdin
  - Handles "message" by reading workspace files and summarizing them
  - Uses adapter-local tools (read_file, list_dir) via tool_call/tool_result
  - Handles "heartbeat" by responding with "heartbeat_ack"
  - Handles "shutdown" by exiting cleanly
  - Ignores unknown message types for forward compatibility

Run:  python -u main.py
      make run LENNY_AGENT_BINARY="python -u examples/runtimes/file-summarizer-python/main.py"

IMPORTANT: The -u flag disables stdout buffering. Without it, the adapter
never receives output and the session hangs.
"""

import json
import sys
import os

# ---- Globals ----

# Counter for generating unique tool call IDs.
tool_call_counter = 0

# Current processing phase:
#   0 = waiting for message
#   1 = listing directory
#   2 = reading files
#   3 = producing summary
phase = 0

# Pending tool call ID (for correlating results).
pending_tool_call_id = None

# List of files discovered via list_dir.
file_list = []

# Accumulated file contents (list of "=== filename ===\ncontent" strings).
file_contents = []

# Index of the next file to read.
current_file_index = 0


# ---- Output helpers ----

def write_json(obj):
    """Write a JSON object as a single line to stdout, followed by a flush.

    Python buffers stdout by default when connected to a pipe. We MUST
    flush after every write, or the adapter never receives our messages.
    The -u flag also helps, but explicit flush is the safest approach.
    """
    line = json.dumps(obj, ensure_ascii=False)
    sys.stdout.write(line + "\n")
    sys.stdout.flush()


def write_response(text):
    """Send a response message signaling task completion."""
    global phase
    write_json({
        "type": "response",
        "output": [
            {"type": "text", "inline": text}
        ]
    })
    phase = 0


def next_tool_call_id():
    """Generate a unique tool call ID."""
    global tool_call_counter
    tool_call_counter += 1
    return f"tc_{tool_call_counter:03d}"


def truncate(s, n=500):
    """Return the first n characters of s, adding '...' if truncated."""
    if len(s) <= n:
        return s
    return s[:n] + "..."


# ---- Tool call helpers ----

def list_dir(path):
    """Send a list_dir tool call."""
    global pending_tool_call_id
    tc_id = next_tool_call_id()
    pending_tool_call_id = tc_id
    write_json({
        "type": "tool_call",
        "id": tc_id,
        "name": "list_dir",
        "arguments": {"path": path}
    })


def read_file(path):
    """Send a read_file tool call."""
    global pending_tool_call_id
    tc_id = next_tool_call_id()
    pending_tool_call_id = tc_id
    write_json({
        "type": "tool_call",
        "id": tc_id,
        "name": "read_file",
        "arguments": {"path": path}
    })


# ---- Message handlers ----

def handle_message(msg):
    """Process a new task message."""
    global phase, file_list, file_contents, current_file_index

    # Reset state for this task.
    file_list = []
    file_contents = []
    current_file_index = 0
    phase = 1

    # Extract the user's request text.
    request_text = ""
    if msg.get("input") and len(msg["input"]) > 0:
        request_text = msg["input"][0].get("inline", "")

    print(f"file-summarizer: received request: {request_text}", file=sys.stderr)

    # Step 1: List files in the workspace.
    list_dir("/workspace/current")


def handle_tool_result(msg):
    """Process the result of a tool call."""
    global phase, file_list, file_contents, current_file_index

    # Verify this result matches our pending tool call.
    if msg.get("id") != pending_tool_call_id:
        print(
            f"file-summarizer: unexpected tool_result id={msg.get('id')}",
            file=sys.stderr
        )
        return

    # Check for errors.
    if msg.get("isError"):
        error_text = "unknown error"
        if msg.get("content") and len(msg["content"]) > 0:
            error_text = msg["content"][0].get("inline", error_text)
        print(f"file-summarizer: tool error: {error_text}", file=sys.stderr)
        write_response(f"Error reading workspace: {error_text}")
        return

    if phase == 1:
        # Phase 1: We received the directory listing.
        if msg.get("content") and len(msg["content"]) > 0:
            listing = msg["content"][0].get("inline", "")
            for line in listing.split("\n"):
                line = line.strip()
                if line and not line.startswith("."):
                    file_list.append(line)

        if not file_list:
            write_response("No files found in the workspace.")
            return

        # Step 2: Start reading files one by one.
        phase = 2
        current_file_index = 0
        read_next_file()

    elif phase == 2:
        # Phase 2: We received a file's contents.
        if msg.get("content") and len(msg["content"]) > 0:
            file_name = file_list[current_file_index]
            content = msg["content"][0].get("inline", "")
            file_contents.append(f"=== {file_name} ===\n{truncate(content)}")

        current_file_index += 1
        if current_file_index < len(file_list) and current_file_index < 10:
            # Read the next file (cap at 10 files).
            read_next_file()
        else:
            # All files read. Produce the summary.
            phase = 3
            produce_summary()


def read_next_file():
    """Send a read_file tool call for the next file in the list."""
    if current_file_index >= len(file_list):
        return
    file_path = f"/workspace/current/{file_list[current_file_index]}"
    read_file(file_path)


def produce_summary():
    """Generate the final summary response."""
    lines = [f"Workspace Summary ({len(file_contents)} files)\n"]
    for fc in file_contents:
        lines.append(fc)
        lines.append("")
    lines.append(f"Total files examined: {len(file_contents)}")
    write_response("\n".join(lines))


# ---- Main loop ----

def main():
    """Read JSON Lines from stdin and dispatch by message type."""
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue

        try:
            msg = json.loads(line)
        except json.JSONDecodeError as e:
            print(f"file-summarizer: parse error: {e}", file=sys.stderr)
            continue

        msg_type = msg.get("type", "")

        if msg_type == "message":
            handle_message(msg)

        elif msg_type == "tool_result":
            handle_tool_result(msg)

        elif msg_type == "heartbeat":
            # Respond immediately. Failure to ack within 10 seconds causes SIGTERM.
            write_json({"type": "heartbeat_ack"})

        elif msg_type == "shutdown":
            reason = msg.get("reason", "unknown")
            print(f"file-summarizer: shutdown (reason={reason})", file=sys.stderr)
            sys.exit(0)

        else:
            # Ignore unknown message types for forward compatibility.
            print(f"file-summarizer: ignoring unknown type: {msg_type}", file=sys.stderr)


if __name__ == "__main__":
    main()
```

---

## Critical: Stdout Flushing

Python buffers stdout when connected to a pipe (which is how the adapter connects). You **must** either:

1. Run with the `-u` flag: `python -u main.py`
2. Call `sys.stdout.flush()` after every write (as shown in `write_json()` above)
3. Set line buffering at startup: `sys.stdout = io.TextIOWrapper(sys.stdout.buffer, line_buffering=True)`

Without explicit flushing, the adapter never receives your output and the session hangs silently. This is the single most common cause of runtime bugs.

---

## requirements.txt

```
# No external dependencies for Minimum tier.
# Standard tier: add the MCP client library:
# mcp>=1.0.0
```

---

## Dockerfile

```dockerfile
FROM python:3.12-slim AS builder
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir --target=/deps -r requirements.txt
COPY . .

FROM python:3.12-slim
COPY --from=builder /deps /usr/local/lib/python3.12/site-packages
COPY --from=builder /app /app
WORKDIR /app
# -u disables stdout buffering --- critical for the adapter protocol.
ENTRYPOINT ["python", "-u", "main.py"]
```

---

## Build and Run

```bash
# Run locally (Tier 1)
make run LENNY_AGENT_BINARY="python -u examples/runtimes/file-summarizer-python/main.py"

# Run with Docker (Tier 2)
docker build -t file-summarizer-py:dev \
  -f examples/runtimes/file-summarizer-python/Dockerfile .
docker compose up
```

---

## Register the Runtime

```bash
# Tier 2: Register via admin API
curl -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "file-summarizer-py",
    "type": "agent",
    "image": "file-summarizer-py:dev",
    "description": "Python file summarizer"
  }'

# Create a session
curl -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"runtimeName": "file-summarizer-py", "tenantId": "default"}'
```

---

## Upgrading to Standard Tier

### 1. Add MCP Client Dependency

```
# requirements.txt
mcp>=1.0.0
```

### 2. Read the Adapter Manifest

```python
import json

def read_manifest():
    """Read the adapter manifest to discover MCP server sockets."""
    with open("/run/lenny/adapter-manifest.json") as f:
        return json.load(f)
```

### 3. Connect to the Platform MCP Server

```python
from mcp import Client

async def connect_mcp(manifest):
    """Connect to the platform MCP server via abstract Unix socket."""
    socket_path = manifest["platformMcpServer"]["socket"]
    nonce = manifest["mcpNonce"]

    client = Client()
    await client.connect_unix(socket_path)
    await client.initialize(
        nonce=nonce,
        client_name="file-summarizer-py",
        client_version="1.0.0",
        protocol_version="2025-03-26"
    )
    return client
```

### 4. Use Platform Tools

```python
async def emit_output(client, text):
    """Emit incremental output to the parent/client."""
    await client.call_tool("lenny/output", {
        "output": [{"type": "text", "inline": text}]
    })

async def delegate_review(client, code):
    """Delegate a code review subtask."""
    result = await client.call_tool("lenny/delegate_task", {
        "target": "code-reviewer",
        "task": {
            "input": [
                {"type": "text", "inline": f"Review this code:\n{code}"}
            ]
        }
    })
    return result
```

### 5. Async Main Loop

Standard tier with MCP requires an async event loop. Restructure your main loop:

```python
import asyncio

async def async_main():
    manifest = read_manifest()
    mcp_client = await connect_mcp(manifest)

    # Discover available tools
    tools = await mcp_client.list_tools()
    print(f"Available tools: {[t.name for t in tools]}", file=sys.stderr)

    # Continue with stdin/stdout loop (run in executor for blocking reads)
    loop = asyncio.get_event_loop()
    for line in sys.stdin:
        # ... same message handling as before ...
        pass

if __name__ == "__main__":
    asyncio.run(async_main())
```

### 6. macOS Note

Standard tier requires abstract Unix sockets, which are Linux-only. Use `docker compose up` (Tier 2) on macOS.
