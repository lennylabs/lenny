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

         Gateway ←──mTLS──→ Pods (gRPC control protocol)

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

