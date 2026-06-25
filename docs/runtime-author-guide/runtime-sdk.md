---
layout: default
title: "Go Runtime SDK"
parent: "Runtime Author Guide"
nav_order: 10
---

# Go Runtime SDK

The Go runtime SDK at `sdks/runtime/go` wraps the adapter binary protocol so a runtime author writes message handlers instead of the JSON Lines frame loop, the lifecycle state machine, heartbeats, and the shutdown handshake. The SDK drives all of that and calls the author's code on each session event. This page covers the Go SDK; the TypeScript and Python SDKs expose the same surface and are described at the end.

The hand-written protocol is documented in the [Adapter Contract](../reference/adapter-contract.md) reference and in [Integration Levels](integration-levels.md). The SDK is the recommended path for a new `type: agent` runtime, and the protocol pages remain the reference for the wire format it implements.

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

`Run` reads the inbound frames from stdin, dispatches each message to `OnMessage` in coordinator-local FIFO order on a worker goroutine, answers heartbeats, and honors the shutdown deadline by calling `OnTerminate`. A handler returns a `Reply` built with `runtime.TextReply` or from `runtime.MessagePart` values; `runtime.Text` constructs a text part.

## Integration levels

A bare `Run(h)` clears the Basic level. Options raise the level:

- `runtime.WithStandardLevel()` dials the platform and connector MCP servers named in the adapter manifest, performing the manifest-nonce handshake. A handler reaches the platform tools through `runtime.ToolsFrom(ctx)`.
- `runtime.WithFullLevel()` and `runtime.WithLifecycleHandlers(...)` open the lifecycle channel. `runtime.OnCheckpoint`, `runtime.OnInterrupt`, `runtime.OnCredentialsRotated`, and `runtime.OnDeadline` register the lifecycle callbacks.

A binary built with the higher-level options degrades to Basic when no manifest advertises the channel, so one binary runs in both a Basic-only and a higher-level environment.

## Platform tools

At the Standard level and above, `runtime.ToolsFrom(ctx)` returns a `Tools` handle for the platform MCP tools. It exposes `DelegateTask`, `AwaitChildren`, `CancelChild`, `DiscoverAgents`, `Output`, `RequestInput`, `RequestElicitation`, `SendMessage`, `MemoryWrite`, `MemoryQuery`, `GetTaskTree`, and `SetTracingContext`, plus a generic `Call` for any other registered tool. The [Platform MCP Tools](platform-tools.md) page documents what each tool does. `runtime.LifecycleFrom(ctx)` and `runtime.CredentialsFrom(ctx)` return the lifecycle channel and the current credential bundle.

## Examples and conformance

`sdks/runtime/go/example/` carries an `echo`, a `delegate`, and a `lifecycle` runtime built on the SDK, covering the Basic, Standard, and Full levels. Run the conformance suite against a runtime with `lenny runtime validate`, which reads the declared `integrationLevel` from `runtime.yaml` and exercises the runtime against the test categories for that level. See [Testing](testing.md) for the conformance workflow.

## TypeScript and Python runtime SDKs

The runtime-author SDK is also published for TypeScript and Python with the same surface. `sdks/runtime/typescript/` is the npm package `@lennylabs/runtime-sdk`, and `sdks/runtime/python/` is the PyPI package `lenny-runtime`. Both wrap the adapter binary protocol the same way the Go SDK does: a handler the author implements, a `run` entry point that drives the JSON Lines frame loop and the lifecycle state machine, the Standard-level MCP client with the platform tools, and the Full-level lifecycle channel. Each ships `echo`, `delegate`, and `lifecycle` example runtimes covering the Basic, Standard, and Full levels. Scaffold a new runtime in any of the languages with `lenny runtime init <name> --language {go|python|typescript}`.
