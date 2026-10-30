## 3. High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Client / MCP Host                        │
└──────────────────────────────┬──────────────────────────────────┘
                               │ REST / MCP / OpenAI / Open Responses
                               │ (via ExternalAdapterRegistry — see Section 15)
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Gateway Edge Replicas                        │
│  ┌──────────┐ ┌─────────────┐ ┌───────────┐ ┌───────────────┐  │
│  │  Auth /   │ │   Policy    │ │  Session   │ │  MCP Fabric   │  │
│  │  OIDC     │ │   Engine +  │ │  Router    │ │  (tasks,      │  │
│  │           │ │  Intercep-  │ │            │ │  elicitation,  │  │
│  │           │ │  tors       │ │            │ │  delegation)   │  │
│  └──────────┘ └─────────────┘ └───────────┘ └───────────────┘  │
└────────┬──────────┬──────────────┬──────────────┬───────────────┘
         │          │              │              │
    ┌────▼────┐ ┌───▼────┐ ┌──────▼─────┐ ┌─────▼──────┐
    │Session  │ │Token  │ │  Event /   │ │  Artifact  │   ┌──────────────┐
    │Manager  │ │Service│ │ Checkpoint │ │   Store    │   │  External    │
    │(Postgres│ │       │ │   Store    │ │            │   │  Connectors  │
    │+ Redis) │ │       │ │            │ │            │   │ (GitHub,     │
    └─────────┘ └───┬───┘ └────────────┘ └────────────┘   │  Jira, ...)  │
                    │   OAuth tokens (encrypted,           └──────▲───────┘
                    └──  cached in Redis) ────────────────────────┘

    Gateway ──mTLS gRPC──→ Pods    LNK-POD-GRPC, dialled by the gateway
    Gateway ←──mTLS gRPC── Pods    LNK-GWCONTROL, dialled by the adapter
      (channels on each link, and the intra-pod, pod-egress,
       gateway-to-store, and control-plane boundaries: see Section 28)

┌─────────────────────────────────────────────────────────────────┐
│  Warm Pool Controller (pod lifecycle, agent-sandbox CRDs)       │
│  PoolScalingController (scaling intelligence, admin API → CRDs) │
└────────┬───────────────┬────────────────┬───────────────────────┘
         │               │                │
    ┌────▼────┐    ┌─────▼─────┐    ┌─────▼─────┐
    │  Pod A  │    │   Pod B   │    │   Pod C   │
    │┌───────┐│    │┌─────────┐│    │┌─────────┐│
    ││Runtime││    ││ Runtime  ││    ││ Runtime  ││
    ││Adapter││    ││ Adapter  ││    ││ Adapter  ││
    │├───────┤│    │├─────────┤│    │├─────────┤│
    ││Agent  ││    ││  Agent   ││    ││  Agent   ││
    ││Binary ││    ││  Binary  ││    ││  Binary  ││
    │└───────┘│    │└─────────┘│    │└─────────┘│
    └─────────┘    └───────────┘    └───────────┘
```

Each arrow between the gateway and a pod is a transport connection carrying several typed conversations.
[Section 28](28_communication-channels.md#28-communication-channels) is the normative home for those
conversations: [§28.3](28_communication-channels.md#283-registers) registers every link and every channel
with its participants, plane, dial direction, and transport, and
[§28.5](28_communication-channels.md#285-contract-cards) states the contract card for each boundary,
including the intra-pod, pod-egress, gateway-to-store, and control-plane boundaries this diagram does not
draw.

