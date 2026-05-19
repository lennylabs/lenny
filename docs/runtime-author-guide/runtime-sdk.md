---
layout: default
title: "Go Runtime SDK"
parent: "Runtime Author Guide"
nav_order: 10
---

# Go Runtime SDK

The Go runtime SDK at `sdks/runtime/go` wraps the §15.4 adapter binary protocol so a runtime author writes message handlers instead of the JSON Lines frame loop, the RPC lifecycle state machine, heartbeats, and the shutdown handshake. The SDK drives all of that and calls the author's code on each session event.

The hand-written protocol is documented in [adapter-contract.md](adapter-contract.md) and [integration-levels.md](integration-levels.md). The SDK is the recommended path for a new runtime; the protocol pages remain the reference for the wire format the SDK implements.

## The Handler interface

A runtime implements the `Handler` interface and passes it to `Run`:

```go
package main

import (
    "context"

    runtime "github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

type echo struct{}

func (echo) OnCreate(ctx context.Context, req runtime.CreateRequest) error {
    return nil
}

func (echo) OnMessage(ctx context.Context, msg runtime.Message) (runtime.Reply, error) {
    return runtime.TextReply("echo: " + msg.Text()), nil
}

func (echo) OnTerminate(ctx context.Context, reason runtime.TerminationReason) error {
    return nil
}

func main() {
    if err := runtime.Run(echo{}); err != nil {
        panic(err)
    }
}
```

`Run` reads the §15.4.1 frames from stdin, dispatches each message to `OnMessage` in coordinator-local FIFO order on a worker goroutine, answers heartbeats, and honors the shutdown deadline by calling `OnTerminate`. A handler returns a `Reply` built with `runtime.TextReply` or from `runtime.OutputPart` values; `runtime.Text` constructs a text part.

## Integration levels

A bare `Run(h)` clears the Basic level. Options raise the level:

- `runtime.WithStandardLevel()` dials the platform and connector MCP servers named in the adapter manifest, performing the manifest-nonce handshake. A handler reaches the platform tools through `runtime.ToolsFrom(ctx)`.
- `runtime.WithFullLevel()` and `runtime.WithLifecycleHandlers(...)` open the §15.4.3 lifecycle channel. `runtime.OnCheckpoint`, `runtime.OnInterrupt`, `runtime.OnCredentialsRotated`, and `runtime.OnDeadline` register the lifecycle callbacks.

A binary built with the higher-level options degrades to Basic when no manifest advertises the channel, so one binary runs in both a Basic-only and a higher-level environment.

## Platform tools

At the Standard level and above, `runtime.ToolsFrom(ctx)` returns a `Tools` handle for the §8.5 platform tools. It exposes `DelegateTask`, `AwaitChildren`, `CancelChild`, `DiscoverAgents`, `Output`, `RequestInput`, `RequestElicitation`, `SendMessage`, `MemoryWrite`, `MemoryQuery`, `GetTaskTree`, and `SetTracingContext`, and a generic `Call` for any other registered tool. `runtime.LifecycleFrom(ctx)` and `runtime.CredentialsFrom(ctx)` return the lifecycle channel and the current credential bundle.

## Examples and conformance

`sdks/runtime/go/example/` carries an `echo`, a `delegate`, and a `lifecycle` runtime built on the SDK; they pass `lenny-compliance` at the Basic, Standard, and Full levels. Run the conformance battery against a runtime with `lenny-compliance --level basic|standard|full`. See [testing.md](testing.md) for the conformance workflow.
