---
id: TOKSVC-I1
status: open
kind: test-gap
severity: low
requirements: []
---

# The Token Service secret-probe component test reads a secret before its RBAC grant takes effect

## Specification

None: the defect is in `tests/component/secretprobe/secretprobe_envtest_test.go`, which tests the Token Service secret resolution of §4.9. The issue is filed under `TOKSVC`, the code of the Token Service page.

## Evidence

- `startAPI` creates a `Role` and a `RoleBinding` for the Token Service user in envtest's API server, and `TestTokenServiceSecretReadAgainstAPIServer` probes the secret as that user straight away. The API server's RBAC authorizer reads roles and bindings through informers, so a new binding takes effect a short time after its create returns.
- Gate-main workflow run `45869562906` (selected run on `main` at `ff5c50644`, 2026-11-26, gate run `1ef717d03449b75eb99b3863`): the first two cases, `unkeyed_allowed_returns_no_value` and `keyed_allowed_returns_value`, failed with "got (1, \"\"), want (0, \"ghp_old\")" and "got (1, \"\"), want (0, \"\")", and the later cases of the same test passed. The next run, `45872051820` at `0384658de`, whose code is the same as `ff5c50644` (the commits between them change records only), passed the test.

## Gap

The test fails without a defect in the code when the first probe runs before the binding reaches the authorizer, and each failure costs a run of the gate on `main`.

## Suggested resolution

After creating the binding, wait for the grant to take effect, for example by polling a `SubjectAccessReview` for the Token Service user until it allows `get` on the secret, with a deadline, rather than waiting a fixed time.
