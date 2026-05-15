# MCP message-framing fuzz corpus

Seed inputs for the §9 MCP parser fuzz target. Each file in this directory is one input the fuzz engine replays as a regression seed; the file's content is the raw bytes the parser sees.

Files are content-addressed by what they exercise; the filename itself has no semantic meaning beyond the comment header inside.

The seed set covers the failure modes the parser must handle without panic:

| File | Scenario |
|:--|:--|
| `valid-initialize` | A well-formed `initialize` request envelope |
| `valid-tools-list` | A well-formed `tools/list` request |
| `truncated-frame` | Header announces 200 bytes but only 12 arrive |
| `oversized-payload` | Header declares a payload past the §9 message-size ceiling |
| `unknown-method` | Valid envelope, method name not in the registered set |
| `invalid-json-body` | Valid frame header, body is not parseable JSON |
| `mixed-encoding` | UTF-8 BOM mid-stream, mojibake bytes after a valid header |
| `header-only` | Frame header with zero-byte body declared |
| `null-bytes` | Embedded NUL bytes inside what's otherwise a valid frame |
| `nested-batch` | Batch of three messages with the second truncated |

The corpus stays minimal on purpose: the §19.2 fuzzer extends it as crashes accumulate. Each new file should carry a short comment header naming the issue it covered.
