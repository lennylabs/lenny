## 28. Communication Channels

A fixture standing in for the section the gate reads its identifier set
and its naming table out of.

### 28.2 Taxonomy and axes

| Axis | Values |
|:--|:--|
| Plane | Content, control, and state |

### 28.3 Registers

#### Link register

| Identifier | Participants | Transport |
|:--|:--|:--|
| `LNK-POD-GRPC` | Gateway replica and pod adapter | gRPC |

#### Channel register

| Identifier | Link | Boundary | Transport |
|:--|:--|:--|:--|
| `CH-ADAPTEREVENTS` | `LNK-POD-GRPC` | `pod-to-gateway` | gRPC |

#### Register-entry register

| Identifier | Store | Key or table |
|:--|:--|:--|
| `REG-SLOTCOUNT` | Redis | `lenny:pod:<pod>:active_slots` |

#### Naming table

| channel | carrier | retired spelling | canonical spelling |
|:--|:--|:--|:--|
| `CH-ADAPTEREVENTS` | go-symbol | `LifecycleChannel` | `AdapterEvents` |
| `CH-RUNTIMEOPS` | go-symbol | `LifecycleChannel` | `RuntimeOps` |
| `CH-RUNTIMEOPS` | socket | `@lenny-lifecycle` | `@lenny-runtime-ops` |
| `CH-RUNTIMEOPS` | path | `lifecyclechannel` | `runtimeops` |

### 28.4 Claim register

The fixture states no claim.
