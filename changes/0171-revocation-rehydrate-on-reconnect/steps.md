# Steps: 0171 Revocation cache rehydration on event-bus reconnect

## Validation owed

None.

## Steps

### 0171.S1 Rehydration on reconnect
- status: Building
- lanes: [specification, code, documentation]
- depends-on: []
- closes: []

§13.3 **Authoritative durability for revocation.** and **Token rotation and revocation — no grace period.**, and the `TokenRevocationPropagationLag` row of §16.5, as RR-3 in `change.md` states; then the reconnect callback of `pkg/storage/pubsub.Bus.Subscribe` (RR-1), the rehydration callback of `propagator.Run` and the gateway's rehydration signal (RR-2), the alert's description in `pkg/alerting/rules`, and the unit, component, chaos, and security tests; then `docs/alerting/rules.yaml`, regenerated, and `docs/operator-guide/security.md`. No issue tracks the gap, because decision 1 lands it as this change.
