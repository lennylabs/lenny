---
code: MSGAREA
type: index
area: messaging
summary: >-
  Lists the capabilities that carry messages into a session and output out of it: delivery, inboxes, inter-session messages, the event stream, input requests, and client tools.
---

# Messaging

Lists the capabilities that carry messages into a session and output out of it: delivery, inboxes, inter-session messages, the event stream, input requests, and client tools.

## Pages

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Client tools](client-tools.md) | `CTOOL` | Lets a client supply tools a runtime calls, with admission, response validation, pending calls, and continuation. |
| [Event stream](event-stream.md) | `STREAM` | States the session event stream: event kinds and their order, activity, output and reasoning streaming, turn and usage events, resume after a gap, and back-pressure. |
| [Inbox and custody](inbox-and-custody.md) | `INBOX` | States the session inbox, its states and custody, durability, expiry, retraction, dead-lettering, and delivery receipts. |
| [Input requests](input-requests.md) | `INPUT` | States how a runtime asks the client for input, the elicitation chain through delegation, harness approvals, and the session's input policy. |
| [Inter-session messaging](inter-session-messaging.md) | `IMSG` | States how one session messages another: scope, the configuration hierarchy, rate limits, content policy, and threading. |
| [Message delivery](message-delivery.md) | `MSG` | States the delivery intents, the delivery paths, turn accounting, and the executor that delivers one message at a time to the runtime. |
