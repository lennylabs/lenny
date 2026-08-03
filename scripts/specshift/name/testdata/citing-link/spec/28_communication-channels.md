# 28. Communication Channels

This specimen tree carries a link register and a channel register whose Link cell names a connection the
link register does not declare. It exists to pin what the declaration index does with such a cell.

## 28.3 Registers

### Link register

| Identifier | Participants | Dial direction | Transport | Endpoint | Lifetime | Provenance |
|:--|:--|:--|:--|:--|:--|:--|
| `LNK-POD-GRPC` | Gateway replica and pod adapter | Gateway | gRPC | Pod IP, TCP 50051 | One connection per gateway replica per pod | C1 |

### Channel register

| Identifier | Link | Boundary | Plane | Dial direction | Authority direction | Transport | Message vocabulary | Provenance |
|:--|:--|:--|:--|:--|:--|:--|:--|:--|
| `CH-ATTACH` | `LNK-GHOST` | `gateway-to-pod` | Content | Gateway | Both | gRPC | Message delivery and agent output | C2 |
