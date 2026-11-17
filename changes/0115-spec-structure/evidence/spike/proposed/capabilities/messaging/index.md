---
code: MSGAREA
type: index
area: messaging
summary: >-
  Lists the capabilities that carry messages into a session and output out of it: delivery, inboxes, inter-session messages, the event stream, input requests, and client tools.
---

# Messaging

The capabilities in this area carry messages into a session and output out of it. A client or another session sends a message, the gateway holds it in the session inbox and delivers it to the runtime one turn at a time, and the runtime's output, activity, and requests for input stream back to the client.

## Reading paths

- To change when a message reaches the runtime, read [Message delivery](message-delivery.md), then [Inbox and custody](inbox-and-custody.md).
- To change what a client sees while a turn runs, read [Event stream](event-stream.md), then [Event stream](../../interfaces/client-api/event-stream.md).
- To change how a runtime asks for input or calls client tools, read [Input requests](input-requests.md), then [Client tools](client-tools.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Message delivery](message-delivery.md) | MSG | States the delivery intents, the delivery paths, turn accounting, and the executor that delivers one message at a time to the runtime. |
| [Inbox and custody](inbox-and-custody.md) | INBOX | States the session inbox, its states and custody, durability, expiry, retraction, dead-lettering, and delivery receipts. |
| [Inter session messaging](inter-session-messaging.md) | IMSG | States how one session messages another: scope, the configuration hierarchy, rate limits, content policy, and threading. |
| [Event stream](event-stream.md) | STREAM | States the session event stream: event kinds and their order, activity, output and reasoning streaming, turn and usage events, resume after a gap, and back-pressure. |
| [Input requests](input-requests.md) | INPUT | States how a runtime asks the client for input, the elicitation chain through delegation, harness approvals, and the session's input policy. |
| [Client tools](client-tools.md) | CTOOL | Lets a client supply tools a runtime calls, with admission, response validation, pending calls, and continuation. |

## Components that carry out this area

- [Gateway](../../components/gateway/index.md): The gateway as a unit: responsibilities, the subsystem list, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics. The slice wrote a stub that lists subsystems.
- [Session coordination](../../components/gateway/session-coordination.md): How gateway replicas coordinate a session: the coordination lease, handoff, hold state, stale replicas, and behavior when both stores fail.
- [Adapter](../../components/agent-pod/adapter.md): The adapter container: startup sequence, the slot registry, gateway and runtime connections, health checks, process settings, and metrics.

## Interfaces this area exposes

- [Messages](../../interfaces/client-api/messages.md): Message operations: send, retract, list, respond to input requests, and client tool results.
- [Event stream](../../interfaces/client-api/event-stream.md): The session event stream endpoint, the event kind registry, resume parameters, and keepalive.
- [Messages](../../interfaces/runtime-protocol/messages.md): The message socket frames, the MessagePart format, tool and frame names, exit codes, and leniency rules.
- [Message parts](../../interfaces/runtime-protocol/message-parts.md): The MessagePart format, the message envelope fields, binary input and output, and the mapping from MCP content blocks.
