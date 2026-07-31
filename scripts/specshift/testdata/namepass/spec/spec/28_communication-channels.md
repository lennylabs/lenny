# 28. Communication Channels

## 28.1 Naming law

The identifier space this fixture declares is the space the name pass holds every
substitution to. The rules themselves are stated in the specification; this fixture
carries the declarations alone.

## 28.3 Registers

| Identifier | Boundary | Transport |
|:--|:--|:--|
| `LNK-GWCONTROL` | pod to gateway | gRPC |
| `LNK-POD-GRPC` | gateway to pod | gRPC |
| `CH-PODLIFECYCLE` | pod to gateway | gRPC stream |
| `CH-LLMPROXY` | pod to gateway | HTTPS |
| `REG-SESSIONSTORE` | gateway to store | Postgres |

## 28.5 Contract cards

### 28.5.2 CH-ADAPTEREVENTS

The card heading names the identifier, which is the second position the
specification declares one in.
