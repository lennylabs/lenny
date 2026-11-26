# Steps: 0171 Revocation cache rehydration on event-bus reconnect

## Validation owed

None.

## Steps

### 0171.S1 Rehydration on reconnect
- status: Landed
- lanes: [specification, code, documentation]
- depends-on: []
- closes: []

§13.3 **Authoritative durability for revocation.** and **Token rotation and revocation — no grace period.**, and the `TokenRevocationPropagationLag` row of §16.5, as RR-3 in `change.md` states; then the reconnect callback of `pkg/storage/pubsub.Bus.Subscribe` (RR-1), the rehydration callback of `propagator.Run` and the gateway's rehydration signal (RR-2), the alert's description in `pkg/alerting/rules`, and the unit, component, chaos, and security tests; then `docs/alerting/rules.yaml`, regenerated, and `docs/operator-guide/security.md`. No issue tracks the gap, because decision 1 lands it as this change.

### 0171.S2 Rehydration on the first confirmation, and the reconnect spacing
- status: Landed
- lanes: [specification, code, documentation]
- depends-on: [0171.S1]
- closes: []

Decisions 2 and 3. §13.3 **Authoritative durability for revocation.** states that a replica rehydrates when its subscription is confirmed, at start as well as after a reconnect, and no sooner than the reconnect spacing after the previous such rehydration, as RR-3 in `change.md` states. Then `pkg/storage/pubsub.Bus.Subscribe` calls the callback on every subscription confirmation, the first included (RR-1), `revocation.Cache.RunRehydration` holds a signal inside the spacing until it has passed while the periodic tick runs as before (RR-2), the gateway flag sets the spacing with a 5-second default, and no metric is added. The unit, component, and chaos tests of 0171.S1 gain the first-confirmation and spacing cases. Then the bearer-token paragraph of `docs/operator-guide/security.md`, with the flag.

### 0171.S3 A chart value for the reconnect spacing
- status: Landed
- lanes: [code, documentation]
- depends-on: [0171.S2]
- closes: []

Decision 4. The chart value `auth.revocationReconnectSpacing` in `charts/lenny/values.yaml`, default `5s`, with its entry in `charts/lenny/values.schema.json`, rendered as `--revocation-reconnect-spacing` on the gateway in `charts/lenny/templates/gateway-deployment.yaml`, with `pkg/chart/values` and the embedded manifests regenerated, and its chart test in `charts/lenny/tests/gateway-deployment_test.yaml`. Then the operator documentation that lists chart values: `docs/reference/configuration.md` and the bearer-token paragraph of `docs/operator-guide/security.md`, which names the value beside the flag. The step edits no specification text, because §13.3 states that a gateway flag sets the spacing, and the chart value renders that flag.
