# Owed validation spikes for the egress design (2026-11-02 to 03)

Environment: kind v0.31.0 on Docker Desktop (arm64, 8 GB), Kubernetes 1.35.0, containerd 2.2.0, runc 1.2.9, kernel 6.12.76-linuxkit, cert-manager v1.21.3, Envoy 1.40.2, Calico 3.33.0, Cilium 1.20.4, Go 1.26.3, git 2.47.3. All clusters were `ob-` prefixed and deleted. Timestamps for latency come from one kernel, because every kind container shares the Docker VM's clock. Raw output is in `out/`; scripts are beside this file.

## 1. Egress PKI with cert-manager

Setup: self-signed bootstrap, an `egress` CA, a namespaced Issuer, and 24-hour leaves with `renewBefore: 23h58m`, so that a real renewal happens about every 2 minutes. cert-manager refuses a shorter leaf: `certificate duration must be greater than 1h0m0s` (`out/s1-pki.out`). Envoy ran as UID 65532 with the Secret at 0440 through `fsGroup`, with SDS on watched directories.

- **Reload with tunnels open** (`out/s1-renew-analysis.txt`): 200 tunnels were held for 480 s across the renewals; none closed, and 95,780 ticks arrived. Three client pods made 7,197 handshakes with no failure. Secret write to file in the pod took 0.7 to 74.8 s. The served serial changed within 0.1 s of the file changing.
- **Reload moved no `update_success`:** `sds.server.update_success` stayed at 1 across the renewals, and `key_rotation_failed` stayed at 0.
- **Rejected reload moved a counter** (`out/s1-ready.out`): a running pod whose Secret turned invalid kept its serial and stayed Ready, and `sds.server.key_rotation_failed` went to 1. The design says "A rejected reload moved no SDS counter", which is wrong for this form on Envoy 1.40.2.
- **Kubelet `httpGet` `scheme: HTTPS`** (`out/s1-ready.out`):

| State | Result | Heal: Secret write to Ready |
|:--|:--|:--|
| Valid | Ready | |
| Secret absent, non-optional | Container not started, `FailedMount` | 16.3 s |
| Secret absent, optional (empty mount) | Admin `/ready` LIVE, kubelet probe fails with `EOF`, not Ready | 37.9 s, no restart |
| Missing keys | The same | 23.8 s, no restart |
| Invalid material | Envoy exits at start, back-off restarts, not Ready | 33.4 s, after a restart |
| Key that does not match the certificate | The same | 27.9 s, after a restart |
| Expired certificate | Ready, because the probe skips verification | |

- **Invalid material at start depends on the configuration** (`out/s1-ready2.out`): with downstream SDS only, Envoy stays LIVE and not Ready, as round 7 found. With the I3 client certificate through SDS on the static cluster, Envoy exits with `Failed to load certificate chain`. The design's "Envoy reports `LIVE` on `/ready` ... invalid material" and "with invalid material it recovers when valid material arrives" hold only for the first form. With the design's I3 form, recovery goes through a restart under back-off.
- **I3 client certificate through SDS** (`out/s1-i3.out`): with a 15 s `max_connection_duration` on the cluster, the decision service saw each renewed serial 1.1, 4.5, and 8.4 s after the file changed. With no connection cap (`out/s1-renew-analysis.txt`), Envoy held 2 connections for 8 minutes and the decision service saw only the serial from the start. A new certificate reaches the peer only on a new connection.
- **TLS resumption on I3:** 41 of 44 I3 handshakes were resumed. Go's `VerifyPeerCertificate`, which the stand-in uses for the SAN allowlist, did not run on those; only 3 calls were logged in that run. The decision service needs `VerifyConnection` for its allowlist. This is a statement about Go's API and the stand-in, measured here.

## 2. CA bundle delivery and rotation

Stand-in controller: `ob controller`, which reads `ca.crt` from its own leaf mount and a value file, and PUTs `lenny-egress-ca` to the other namespaces.

- **ConfigMap to running pod, no `subPath`** (`out/s2-propagation.txt`): writes at least 100 s apart, 5 pods, 60 samples: min 0.6 s, median 37.9 s, p90 67.2 s, max 80.8 s. An earlier run with 25 to 55 s gaps was censored, because a later write overtook slow deliveries, and is not used. The `subPath` control never changed.
- **Rotation, with 3 clients at 15 connections per second in total; each connection does a TLS handshake to Envoy and a CONNECT that passes mTLS I3 through a connection capped at 15 s** (`out/s2-analysis.txt`, `out/s2-connections.txt`):

| Phase | OK | Failed |
|:--|:--|:--|
| A. Routine CA renewal, `rotationPolicy: Never`, key hash unchanged | 3,150 | 0 |
| B. Design's order, waiting for the kubelet at each step including delivery of the re-issued leaves | 11,700 | 0 |
| C. Design's order, with the value cleared as soon as every leaf Secret had re-issued | 5,483 | 817 |
| D. Control: Issuer switched with no bundle step | 1,931 | 1,219 |

- **Phase C:** every failure was a 503 from I3, from 5.8 s to 61.2 s after the clear, a window of 55.4 s. The window began 0.5 s after Envoy's new leaf arrived and ended with the first accepted I3 handshake under the new CA. Envoy's `cluster.authz.ssl.fail_verify_error` counted 1,061 across phases C and D, which is consistent with Envoy refusing the decision service's old leaf once its bundle had dropped the old CA. The decision service's own delivery time was not logged. The design's "every leaf re-issues within its renewal window; and only then is the value cleared" needs "and the kubelet has delivered every re-issued leaf".
- **Issuer switch re-issues nothing by itself** (`out/s2-long.out`): a leaf with `renewBefore: 8h` was still under the old CA 150 s after the switch, with `renewalTime` 16 hours away. The value therefore stays until that window passes, unless `cmctl renew` runs, which re-issued it at once.
- **New CA:** the order needs a second CA Certificate and Secret, because the Issuer's `secretName` is what switches. The design does not say where the new CA comes from.
- Phase B's interval from value to Issuer switch is inflated by a harness bug in the wait helper, which was fixed and resumed. The bundle reached every holder within about 88 s in phase C, and a clear reached every holder within 76 s (B) and 79 s (C), measured by a poll loop.

## 3. Egress group labels on Calico and Cilium

The policy is an ingress NetworkPolicy on the group's pods that admits only `lenny.dev/egress-group: group-a` from the agent namespace, on top of a default-deny egress and the `proxied` rule. The probe opens a new socket every 20 ms (100 ms on the long Cilium runs) with a 100 ms timeout. The stamp call and the probe log nanoseconds from one kernel. Every sample was dropped before the stamp, and no sample regressed after opening. An unset pod and a `group-b` pod stayed dropped; an unset pod to a group with no policy was open.

- **Calico, iptables dataplane** (`out/s3-calico.out`): 32 samples, worst 28 ms after the call returned, median 9 to 18 ms by group.
- **Cilium, kube-proxy replacement, CRD identities** (`out/s3-cil-default.out`, `out/s3-cil-grace0.out`, `out/s3-iso-grace0.out`, `out/s3-cilium-10s-window.out`):

| Run | Samples | Open after the call returned |
|:--|:--|:--|
| Default, 10 s window | 32 | 3 opened, at 6.4 to 7.0 s; 29 had not opened within the window |
| Default, 50 s or longer window, stamps every 6 s | 24 | 6.85 to 32.89 s, medians 14.25 and 20.10 s |
| `identity-change-grace-period: 0s`, the same | 24 | 0.25 to 28.84 s, medians 15.21 and 14.95 s |
| Grace 0, one stamp at a time on a quiet cluster | 6 | 0.18, 0.88, 3.23, 18.83, 21.67, and 26.32 s |

The worst case is 32.89 s, and it is an observation rather than a bound. The agent log places the delay between `Resolving identity labels (non-blocking)` (18 ms after the stamp) and `Identity of endpoint changed` (26 s after it, `out/s3-cil-breakdown.out`), with the BPF reload 40 ms later. Node CPU was at 11% and 17% when sampled. The delay is in the mechanism on this setup, and the isolated run rules out contention from the test's own stamps. The root cause inside the identity allocator was not established, and a production cluster may differ.

- **What `netagent` needs:** a dropped SYN looks like a timeout rather than a refusal. A socket begun before the path opened recovers only on its SYN retransmit: 1 s on Calico, and up to 16.4 s behind the path on Cilium (this kernel retransmits linearly at first, then backs off). `netagent` therefore needs fresh sockets with an attempt timeout of 1 s or less, retried across a budget above the worst case on the slowest CNI: at least 60 s to cover Cilium here. The design's "it forwards the first request only after the stamp succeeds" gives no path guarantee, because on Cilium the stamp's return precedes the path by up to 33 s.

## 4. Label decider and drain on mismatch

Tree read: `pkg/admission/label_immutability/label_immutability.go`, `pkg/admission/webhook/label_immutability.go`, and `charts/lenny/templates/admission-policies/label-immutability-webhook.yaml`. The prototype is a webhook compiled with the repository's two deciders unchanged, plus a VAP run on the same matrix. Results: `out/s4.out`, `out/s4b.out`, `out/s4c.out`. An earlier run with reused pod names is `out/s4-invalid-harness-name-reuse.out` and is invalid.

- **Identities:** the gateway and the controller may set the label from unset through merge patch, `kubectl label`, JSON patch, server-side apply, or CREATE. The pool scaler, an agent service account, and cluster-admin are refused. Value to value and value to unset are refused for everyone. The same value, annotation-only updates, and other labels pass. Two gateways racing different values gave exactly one winner in 20 of 20 rounds. The webhook and the VAP agreed on every row.
- **No conflict for the stamp:** `DecideImmutableLabels` does not list the label, and `DecideTenantTransition` looks only at `lenny.dev/tenant-id`, so a gateway patch with both labels passes.
- **Conflict the design already names:** `DecideTenantTransition` has `if r.UserInfoUsername != GatewayServiceAccount` on unset to value, and the chart's rule includes CREATE, so a controller CREATE with `tenant-id` is refused: `tenant_label_immutable: only system:serviceaccount:lenny-system:lenny-gateway may set`. The design stages a new rule for this in proposal 2. A controller CREATE with the group label alone passes.
- **String comparison:** `DecideImmutableLabels` compares `oldV != newV`, so unset and empty are equal, and an agent service account's `unset -> ""` on `lenny.dev/egress-profile` was allowed. A decider copied from this pattern would admit an empty group label. The prototype checks map presence.
- **`pods/status` carries labels:** the chart's rule is `resources: ["pods"]`. Through `pods/status`, the controller (which holds `patch` on it in `controller-rbac.yaml`) set both `lenny.dev/egress-group` and `lenny.dev/tenant-id: evil`, with no webhook consulted. An agent service account with that RBAC, which only this test granted, did the same. The kubelet is refused by the API server: `node "..." cannot update labels through pod status`. Adding `pods/status` to the rule closes it in both prototypes. The design's "only those two set it, and only from unset" needs that rule, and the existing tenant rule has the same gap.
- **Drain:** with the real CRDs, `podclaim.StampDrainRequest` as the gateway and `warmpool.PodReconciler.Reconcile` as the controller drove idle, claimed, and reserved Sandboxes to `draining`. `state.ValidTransitions` has no `reserved -> draining` edge. `claimer.go` says of the same case: "No drain request is stamped here, because a stamp written before that projection lands would drive `reserved → draining`." `DrainRefusedPod` also writes nothing while a claim holds the pod. The design's "on a mismatch they refuse the pod and stamp `lenny.dev/drain-request`" is therefore wrong for a reserved pod at rebind: the hold has to end first, as `endReservedHolds` does. The fixture had no SandboxClaim objects, and the consumer does not read them. The gateway's claim path itself did not run, because no gateway code for the group mismatch exists.

## 5. The mirror's Go parent

`go/mirror/main.go` ran as PID 1 under the design's `setpriv` start, with `hostUsers` unset and false, on kind's local-path volume (ext4; idmapped under `hostUsers: false`). The two runs matched apart from the UID map and mount flags (`out/s5/m-unset.log`, `out/s5/m-userns.log`, `out/s5/m-stripped.log`, `out/s5/v-*.log`).

- **Parent:** every thread holds `c1` in inheritable, permitted, effective, and ambient. The subreaper setting read back on another thread.
- **Child launch:** `exec.Cmd` with `Credential` alone is not an equivalent. The child kept `c1` in all four sets, and `setuid(0)` succeeded. `setpriv`, a re-exec shim on one locked thread, and fork from a locked thread with cleared sets all gave zero in those sets, with the bounding set at `c1` and `no_new_privs` at 1. All boundaries then held.
- **Go quirks:** a thread with cleared capabilities must not return to the scheduler. With one such thread, `syscall.Setgroups` aborts the process: `fatal error: AllThreadsSyscall6 results differ between threads`. A locked goroutine that exits destroys its thread, but one thread stayed alive with cleared sets in one run, which is consistent with Go parking the main thread rather than ending it. `wait4(-1)` takes the status from `exec.Cmd.Wait`, which then returns `waitid: no child processes`, so the reaper must wait by pid for orphans it finds in `/proc`.
- **Sweep:** `kill(-1)` kept succeeding while only zombies remained, 1,000 calls in a row, because the orphans reparent to the parent and wait for it to reap them. Without reaping, 8 zombies stayed under the UID, and the UID stayed out of the pool. With a concurrent by-pid reaper, the helper saw `ESRCH` after 1 call, and `/proc` was clear in 3 to 6 ms, including `setsid`, double-fork, and a fork loop. The parent survived.
- **Re-chown:** the walk through directory descriptors did not follow planted symlinks, needed the store group (the control got `EACCES`), and on a locked thread left other threads' groups unchanged.
- **Setuid and setgid plants:** the walk got `EPERM` on a setuid or group-executable setgid file, because that chown also changes the mode, which needs ownership or `CAP_FOWNER`. The design's "no fetch UID keeps ownership of files once it exits" is false for these files. A helper under the fetch UID that strips the bits before the walk fixed it.
- **Planted modes and ACLs survive:** the owner-only walk leaves them. After a hostile fetch of a public store planted a 0777 directory and a 0666 file, a public reader with no group could write both, and a fetch for another store could write the file. A reader could write a tenant-store file whose ACL a fetch had planted. The design's "a hostile client cannot write a public store" and "a compromised fetch can write one store and no other" hold only until a fetch of that store has been compromised.
- **Git ownership:** after the re-chown, a fetch under another UID fails with `detected dubious ownership`, and so does a reader. The fetch UID and the reader never own the store, so `safe.directory` is needed on every run, and the design does not state it. `-c safe.directory=*` works for a direct `fetch` and `upload-pack` and does not work through `ls-remote` on a local path; a global config worked for the reader's `ls-remote` and clone.
- **ACL:** set the mode and the ACL before the chown, because the parent gets `EPERM` for both afterwards. On ext4, in both modes, the reader could list, read, `ls-remote`, and clone, and could not create, overwrite, delete, rename, chmod, set an ACL, or push.
- **Volume lacking ACLs:** tmpfs on this kernel (`emptyDir` with `medium: Memory`, `CONFIG_TMPFS_POSIX_ACL` unset) returned `operation not supported` from `setxattr`, and the reader then got `EACCES`. The failure is visible to the parent. The design's fallback to the store's group is silent unless the parent reports it.

## Not run, and harness notes

- ext4 mounted with `noacl` on a loop device: the mount failed (`wrong fs type, bad option`), so `out/s5/v-noacl-*.log` tested ordinary ext4 and is not evidence.
- NFS and cloud block volumes, which the design's item names as "the volume types the chart supports", were not available on kind.
- `out/s5/v-localpath-*.log` are empty because the harness deleted those PVCs; `m-unset` and `m-userns` cover local-path.
- The kubelet is covered through its status path only.
- `debian:trixie-slim`, which the mirror image's Dockerfile needed, was pulled and left in Docker, because it carries no `ob-` prefix and another spike shares the daemon. All `ob-` images were removed.
- Harness defects found and fixed before the numbers above: pod names reused by a subshell counter, `select {}` crashing the sleep command, the Envoy pod's sidecar lacking the CA mount, and 32 client failures that came from replacing the only Envoy pod.
