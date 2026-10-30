# 7. Session Lifecycle

## 7.2 Attach

The attach handshake is carried on [the adapter protocol](28_communication-channels.md#2851-ch-adapterbinary),
and the compatibility window it runs under is [the versioning rules](15_external-api-surface.md#155-api-versioning-and-stability).

The framing that handshake writes onto the connection is stated in §28.5.1.

The kramdown anchor below addresses this heading explicitly.

## 7.3 Detach
{: #detach-sequence }

Detach drains the session before it closes.
