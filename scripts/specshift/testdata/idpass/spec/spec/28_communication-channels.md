# 28. Communication Channels

## 28.1 Naming law

The naming table below is the one this fixture states. The rules themselves are
stated in the specification; this fixture carries the table alone.

## 28.3 Registers

| Identifier | Boundary | Transport |
|:--|:--|:--|
| `CH-RUNTIMEOPS` | intra-pod | JSON Lines |
| `CH-ADAPTEREVENTS` | pod to gateway | gRPC stream |

### The naming table

| Channel | Carrier | Retired spelling | Canonical spelling |
|:--|:--|:--|:--|
| `CH-RUNTIMEOPS` | go-symbol | `LifecycleChannel` | `RuntimeOpsChannel` |
| `CH-ADAPTEREVENTS` | go-symbol | `LifecycleChannel` | `AdapterEventsChannel` |
| `CH-ADAPTEREVENTS` | proto-rpc | `LifecycleChannel` | `AdapterEvents` |
| `CH-ADAPTEREVENTS` | metric | `LifecycleChannel` | `adapter_events` |
| `CH-RUNTIMEOPS` | socket | `@lenny-lifecycle` | `@lenny-runtime-ops` |
| `CH-RUNTIMEOPS` | path | `lifecyclechannel` | `runtimeops` |
| `CH-RUNTIMEOPS` | path | `lifecycle-events` | `runtime-ops-events` |
