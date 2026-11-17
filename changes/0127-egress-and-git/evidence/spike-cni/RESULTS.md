# CNI NetworkPolicy spike: `proxied` egress profile on Calico and Cilium

Date: 2026-11-02 (UTC timestamps below). Raw outputs are in `out/` next to this file. Nothing in `../../..` was read or modified for this spike beyond the design context given in the task.

## Question

The previous kind spike (`../spike-k8s/RESULTS.md`, Test 6) showed that kindnet enforces the design's `proxied` egress NetworkPolicy for pod, Service, DNS, and internet destinations but does not block node-local destinations (the kubelet on the node's own IP and the API server, which in kind is a hostNetwork static pod reached through `10.96.0.1`). This spike repeats the test on production CNIs: Calico v3.33.0 in iptables mode, Calico v3.33.0 in eBPF mode, and Cilium 1.20.4.

## Environment

- Host: Docker Desktop 29.4.3, arm64, 12 CPUs, 8 GB for the Docker VM; a concurrent Envoy spike shared the host. kind v0.31.0, kubectl v1.34.1, helm v4.1.4, Go 1.26.3.
- Cluster `spike-cni`: two nodes (`spike-cni-control-plane` 172.18.0.2, `spike-cni-worker` 172.18.0.3), Kubernetes v1.35.0, containerd 2.2.0, kernel 6.12.76-linuxkit, `disableDefaultCNI: true`, podSubnet 10.244.0.0/16, serviceSubnet 10.96.0.0/16. One cluster existed at a time; the Calico cluster was deleted before the Cilium cluster was created.
- Images built on the host and loaded with `kind load docker-image`: `spike-cni-net:v1` (`FROM scratch`, Go multi-call binary `img/main.go`: `proxy` is an HTTP CONNECT and absolute-URI forward proxy on :3128, `echo ADDR` is an HTTP server) and `spike-cni-client:v1` (`alpine:3.22` + `curl` 8.x + `bind-tools`).

## Files

| File | Content |
|:--|:--|
| `kind-nocni.yaml`, `kind-nocni-nokubeproxy.yaml` | kind configs (the second adds `kubeProxyMode: none`, used for Cilium) |
| `dl/calico-v3.33.0.yaml` | the official Calico manifest, applied unmodified |
| `calico-ebpf-endpoint.yaml` | the `kubernetes-services-endpoint` ConfigMap for the eBPF switch |
| `workload.yaml` | namespaces, stand-in pods, Services (fixed ClusterIPs) |
| `netpol.yaml` | default-deny egress plus the `proxied` allow policy |
| `probe.sh`, `run-probe.sh` | probe run inside the client pod; the wrapper fills in node and pod IPs |
| `out/probe-<cni>-{baseline,policy}.txt` | probe output without and with the policies |
| `out/calico-*.txt`, `out/cilium-*.txt` | CNI version, configuration, and dataplane evidence |

## Stand-ins and policy (`workload.yaml`, `netpol.yaml`)

| Stand-in | Namespace | Labels | Node | Service |
|:--|:--|:--|:--|:--|
| CONNECT proxy | `lenny-system` | `app=lenny-egress` | control-plane | `10.96.31.28:3128` |
| CoreDNS v1.13.1 (cluster.local only, NXDOMAIN for everything else) | `lenny-dns` | `app=lenny-dns-proxied` | control-plane | `10.96.53.53:53` UDP and TCP |
| echo on :8443 (gateway stand-in) | `lenny-system` | `app=lenny-gateway` | worker (same node as the client) | `10.96.84.43:8443` |
| echo on :8080 | `other` | `app=other-echo` | worker | `10.96.99.99:8080` |
| hostNetwork echo on :9000 | `other` | none | one per node | none |
| client (`sleep infinity`, `dnsPolicy: None`, nameserver 10.96.53.53) | `lenny-agents` | `lenny.dev/egress-profile=proxied` | worker | none |

Policies in `lenny-agents`: `default-deny-egress` (`podSelector: {}`, `policyTypes: [Egress]`, no rules) and `egress-profile-proxied`, which selects `lenny.dev/egress-profile=proxied` and allows egress to `namespaceSelector(kubernetes.io/metadata.name=lenny-system) + podSelector(app=lenny-egress)` on TCP 3128, `namespaceSelector(...=lenny-dns) + podSelector(app=lenny-dns-proxied)` on UDP 53 and TCP 53, and `namespaceSelector(...=lenny-system) + podSelector(app=lenny-gateway)` on TCP 8443. No `ipBlock` peers.

## Probe

`run-probe.sh <label>` reads the node and pod IPs with kubectl and pipes `probe.sh` into `kubectl exec -i client -- sh -s`. Each line is `[label] rc=<exit> <elapsed> :: <output>`. curl runs with `-m 5 --noproxy '*'` (or `-m 8 -x <proxy>` for the CONNECT cases); dig runs with `+time=2 +tries=1`. The probe is run once without policies (baseline, proves reachability) and once with both policies applied.

| ID | Destination | Expected with policy |
|:--|:--|:--|
| A1 | egress Service ClusterIP 10.96.31.28:3128, CONNECT to example.com | allowed (200) |
| A2 | egress pod IP :3128, CONNECT to example.com | allowed (200) |
| A3 | proxied DNS ClusterIP 10.96.53.53 UDP 53 | allowed |
| A4 | proxied DNS ClusterIP 10.96.53.53 TCP 53 | allowed |
| A5 | `getent hosts lenny-egress.lenny-system` via resolv.conf (10.96.53.53) | allowed |
| A6 | resolve `lenny-egress.lenny-system.svc.cluster.local` via the proxied resolver, then CONNECT to example.com | allowed (200) |
| A7 | gateway Service ClusterIP 10.96.84.43:8443, backend on the same node | allowed (200) |
| A8 | gateway pod IP :8443, same node | allowed (200) |
| D1 | API server ClusterIP 10.96.0.1:443 | denied |
| D2 | control-plane node IP 172.18.0.2:6443 (API server, hostNetwork) | denied |
| D3 | worker (own node) IP 172.18.0.3:10250 (kubelet) | denied |
| D4 | control-plane node IP 172.18.0.2:10250 (kubelet) | denied |
| D5 | hostNetwork pod on the same node, 172.18.0.3:9000 | denied |
| D6 | hostNetwork pod on the control-plane, 172.18.0.2:9000 | denied |
| D7 | pod IP in namespace `other`, same node, :8080 | denied |
| D8 | Service ClusterIP 10.96.99.99:8080 for that pod (post-DNAT check, backend on the same node) | denied |
| D9 | 1.1.1.1:443 | denied |
| D10 | default kube-dns ClusterIP 10.96.0.10 UDP 53 | denied |
| D11 | default kube-dns ClusterIP 10.96.0.10 TCP 53 | denied |
| D12 | egress pod IP on port 3129 (selected peer, wrong port) | denied |
| D13 | gateway pod IP on port 8080 (selected peer, wrong port) | denied |

Baseline (no policy) on every CNI: A1 to A8 returned 200 or resolved; D1, D2 returned 200; D3, D4 returned 401 (kubelet without a token); D5 to D8 returned 200; D9 returned 301; D10, D11 resolved; D12, D13 returned `curl: (7) ... Could not connect` immediately (nothing listens, RST). The baselines are in `out/probe-*-baseline.txt`.

---

## 1. Calico v3.33.0, iptables dataplane

Install: `kubectl apply -f dl/calico-v3.33.0.yaml` (the manifest from `https://raw.githubusercontent.com/projectcalico/calico/v3.33.0/manifests/calico.yaml`, unmodified; `CALICO_IPV4POOL_CIDR` left commented, so Calico picked up 10.244.0.0/16 from kubeadm). Rollout of `ds/calico-node` and `deploy/calico-kube-controllers` completed about 30 s after apply; both nodes were Ready 54 s after cluster creation.

Facts (`out/calico-iptables-facts.txt`):

- image `quay.io/calico/node:v3.33.0`; Felix log `Using internal (linux) dataplane driver.` with `DataplaneDriver:"calico-iptables-plugin"`, `BPFEnabled:false`, `NFTablesMode:"Auto"`, `IptablesBackend:"auto"`.
- The node's `iptables -V` is `iptables v1.8.9 (nf_tables)`; 257 `cali-` rules in the filter table on the worker.
- IP pool `default-ipv4-ippool 10.244.0.0/16 ipip=Always vxlan=Never`.
- kube-proxy present, `mode: iptables`.

Probe with policy (`out/probe-calico-iptables-policy.txt`, 08:42:08Z):

| ID | Result | Verdict |
|:--|:--|:--|
| A1 | `rc=0 0.09s :: 200` | PASS |
| A2 | `rc=0 0.09s :: 200` | PASS |
| A3 | `rc=0 0.01s :: 10.96.31.28` | PASS |
| A4 | `rc=0 0.00s :: 10.96.0.1` | PASS |
| A5 | `rc=0 0.00s :: 10.96.31.28 lenny-egress.lenny-system.svc.cluster.local ...` | PASS |
| A6 | `rc=0 0.09s :: 200` | PASS |
| A7 | `rc=0 0.01s :: 200` | PASS |
| A8 | `rc=0 0.00s :: 200` | PASS |
| D1 | `rc=28 5.02s :: curl: (28) Connection timed out` | PASS (blocked) |
| D2 | `rc=28 5.01s` timeout | PASS (blocked) |
| D3 | `rc=28 5.01s` timeout | PASS (blocked) |
| D4 | `rc=28 5.01s` timeout | PASS (blocked) |
| D5 | `rc=28 5.00s` timeout | PASS (blocked) |
| D6 | `rc=28 5.01s` timeout | PASS (blocked) |
| D7 | `rc=28 5.01s` timeout | PASS (blocked) |
| D8 | `rc=28 5.02s` timeout | PASS (blocked) |
| D9 | `rc=28 5.01s` timeout | PASS (blocked) |
| D10 | `rc=9 2.01s :: ;; communications error to 10.96.0.10#53: timed out` | PASS (blocked) |
| D11 | `rc=9 2.03s :: ;; Connection to 10.96.0.10#53 ... failed: timed out` | PASS (blocked) |
| D12 | `rc=28 5.01s` timeout (baseline was an immediate RST) | PASS (blocked) |
| D13 | `rc=28 5.01s` timeout (baseline was an immediate RST) | PASS (blocked) |

How the node-local destinations get blocked (`out/calico-iptables-rules.txt`): Calico inserts `-A INPUT -j cali-INPUT` and `-A FORWARD -j cali-FORWARD`. `cali-INPUT` has `-A cali-INPUT -i cali+ -g cali-wl-to-host`, which dispatches to the per-workload `cali-fw-<iface>` chain, so a packet from a pod to the node's own IP (kubelet, hostNetwork pod, or the API server behind the `10.96.0.1` DNAT to 172.18.0.2:6443) is evaluated against the workload's egress policy in the INPUT path and dropped. kindnet hooked `postrouting`, which locally delivered packets do not traverse; that is the difference observed in the previous spike. D8 shows that the Service-to-same-node-backend case is evaluated on the post-DNAT pod IP, so a podSelector peer behind a ClusterIP works (A7) and a non-selected backend behind a ClusterIP is dropped (D8).

Verdict: PASS on every destination.

## 2. Calico v3.33.0, eBPF dataplane

Switch, on the same cluster (`out/calico-ebpf-switch.txt`, 08:43:50Z):

```
kubectl apply -f <kubernetes-services-endpoint ConfigMap: KUBERNETES_SERVICE_HOST=172.18.0.2, KUBERNETES_SERVICE_PORT=6443, namespace kube-system>
kubectl -n kube-system patch ds kube-proxy -p '{"spec":{"template":{"spec":{"nodeSelector":{"non-calico":"true"}}}}}'
kubectl -n kube-system rollout restart ds/calico-node deploy/calico-kube-controllers
kubectl patch felixconfigurations.crd.projectcalico.org default --type merge -p '{"spec":{"bpfEnabled":true}}'
```

Evidence that the dataplane changed (`out/calico-ebpf-facts.txt`): the worker Felix logged `Felix is shutting down reason="config changed"`, then `BPF enabled, configuring iptables/nftables layer to clean up kube-proxy's rules.`, `BPF enabled, starting BPF endpoint manager and map manager.`, `kube-proxy v4 node info updated, hostname="spike-cni-worker"`, `Default BPF policy programs loaded.`, `Applied program to host interface id="eth0"` (also `tunl0`, `bpfout.cali`, `lo`), and `Attached programs to the WEP hook=egress iface="cali..." type="workload"` for each pod interface. `/sys/fs/bpf` on the worker gained `calico`, `ctlb`, `tc`, `tcx` with `cali<iface>_egress` and `cali<iface>_ingress` pins; `cali-` filter rules dropped from 257 to 12; the kube-proxy `KUBE-SERVICES` nat chain had 0 rules; no kube-proxy pods remained. `tc filter show` lists nothing because v3.33 attaches with tcx (`BPFAttachType:"Netkit"` in the config dump with fallback to tcx; the pins are under `/sys/fs/bpf/tcx`). The `felixconfiguration` reported `bpfEnabled: true`. Services (ClusterIPs) kept working with kube-proxy gone, as the baseline shows.

Probe with policy (`out/probe-calico-ebpf-policy.txt`, 08:46:02Z): identical outcome to the iptables run.

| ID | Result | Verdict |
|:--|:--|:--|
| A1 | `rc=0 0.09s :: 200` | PASS |
| A2 | `rc=0 0.09s :: 200` | PASS |
| A3 | `rc=0 0.01s :: 10.96.31.28` | PASS |
| A4 | `rc=0 0.00s :: 10.96.0.1` | PASS |
| A5 | `rc=0 0.01s :: 10.96.31.28 ...` | PASS |
| A6 | `rc=0 0.09s :: 200` | PASS |
| A7 | `rc=0 0.00s :: 200` | PASS |
| A8 | `rc=0 0.00s :: 200` | PASS |
| D1 to D9, D12, D13 | `rc=28 5.0xs :: curl: (28) Connection timed out` | PASS (blocked) |
| D10 | `rc=9 2.01s :: communications error to 10.96.0.10#53: timed out` | PASS (blocked) |
| D11 | `rc=9 2.03s :: Connection to 10.96.0.10#53 ... failed: timed out` | PASS (blocked) |

Verdict: PASS on every destination. Caveat: the switch was done in place on a cluster where kube-proxy had run first; a fresh install with `kubeProxyMode: none` was not tested for Calico.

## 3. Cilium 1.20.4, kube-proxy replacement

Fresh cluster from `kind-nocni-nokubeproxy.yaml` (`disableDefaultCNI: true`, `kubeProxyMode: none`; in this cluster the control-plane got 172.18.0.3 and the worker 172.18.0.2). No `kube-proxy` DaemonSet existed (`daemonsets.apps "kube-proxy" not found`). Install (`out/cilium-helm-install.txt`):

```
helm install cilium cilium --repo https://helm.cilium.io/ --version 1.20.4 --namespace kube-system \
  --set kubeProxyReplacement=true --set k8sServiceHost=172.18.0.3 --set k8sServicePort=6443 \
  --set ipam.mode=kubernetes --set image.pullPolicy=IfNotPresent --set operator.replicas=1
```

Facts (`out/cilium-facts.txt`, from `cilium-dbg status --verbose` in the worker agent and `cm/cilium-config`):

- image `quay.io/cilium/cilium:v1.20.4`; `Cilium: Ok 1.20.4 (v1.20.4-beb276d2)`.
- `KubeProxyReplacement: True [eth0 172.18.0.2 ... (Direct Routing)]`, `Socket LB: Enabled`, `Socket LB Coverage: Full`, `Mode: SNAT`, `Session Affinity: Enabled`.
- `Routing: Network: Tunnel [vxlan] Host: Legacy`, `Attach Mode: TCX`, `Device Mode: veth`, `Masquerading: IPTables [IPv4: Enabled]`, `Host firewall: Disabled`, `enable-policy: default`, `enable-l7-proxy: true` (no L7 rules were used).
- ClusterIP Services worked with no kube-proxy in the cluster (baseline A1, A3, A4, A7, D1, D8, D10 all reached their backends through ClusterIPs).

Probe with policy (`out/probe-cilium-policy.txt`, 08:50:06Z):

| ID | Result | Verdict |
|:--|:--|:--|
| A1 | `rc=0 0.09s :: 200` | PASS |
| A2 | `rc=0 0.10s :: 200` | PASS |
| A3 | `rc=0 0.01s :: 10.96.31.28` | PASS |
| A4 | `rc=0 0.00s :: 10.96.0.1` | PASS |
| A5 | `rc=0 0.00s :: 10.96.31.28 ...` | PASS |
| A6 | `rc=0 0.09s :: 200` | PASS |
| A7 | `rc=0 0.00s :: 200` | PASS |
| A8 | `rc=0 0.01s :: 200` | PASS |
| D1 | `rc=28 5.00s :: curl: (28) Connection timed out` | PASS (blocked) |
| D2 | `rc=28 5.00s` timeout | PASS (blocked) |
| D3 | `rc=28 5.01s` timeout | PASS (blocked) |
| D4 | `rc=28 5.01s` timeout | PASS (blocked) |
| D5 | `rc=28 5.00s` timeout | PASS (blocked) |
| D6 | `rc=28 5.01s` timeout | PASS (blocked) |
| D7 | `rc=28 5.01s` timeout | PASS (blocked) |
| D8 | `rc=28 5.01s` timeout | PASS (blocked) |
| D9 | `rc=28 5.02s` timeout | PASS (blocked) |
| D10 | `rc=9 2.02s :: communications error to 10.96.0.10#53: timed out` | PASS (blocked) |
| D11 | `rc=9 2.03s :: Connection to 10.96.0.10#53 ... failed: timed out` | PASS (blocked) |
| D12 | `rc=28 5.01s` timeout (baseline was an immediate RST) | PASS (blocked) |
| D13 | `rc=28 5.02s` timeout (baseline was an immediate RST) | PASS (blocked) |

With socket-level load balancing, the ClusterIP is translated to the backend pod IP at `connect()` in the client's network namespace, before the packet reaches the endpoint's egress BPF program, so the policy is evaluated against the backend pod identity. A7 (allowed backend behind a ClusterIP on the same node) and D8 (non-selected backend behind a ClusterIP on the same node) show the two sides of that. Cilium's own view (`out/cilium-policy-evidence.txt`; the `endpoint get` and `bpf policy get` calls in that file failed on argument parsing and printed usage text, the `endpoint list` and Hubble output are the evidence):

- `cilium-dbg endpoint list` on the worker: endpoint 1633, IPv4 10.244.1.183 (the client), `POLICY (ingress) Disabled`, `POLICY (egress) Enabled`, identity 63610.
- `hubble observe --from-pod lenny-agents/client --type policy-verdict`: `-> lenny-system/lenny-gateway-...:8443 (ID:13286) policy-verdict:L3-L4 EGRESS ALLOWED (TCP Flags: SYN)` for A7/A8; `<> 172.18.0.3:6443 (kube-apiserver) policy-verdict:none EGRESS DENIED` for D1 and D2 (the ClusterIP 10.96.0.1 is already translated to the node IP when the verdict is recorded); `<> 172.18.0.2:10250 (host) policy-verdict:none EGRESS DENIED` for D3; `<> 172.18.0.3:10250 (kube-apiserver) ... DENIED` for D4 (Cilium labels the control-plane node IP with the `kube-apiserver` identity); `<> other/other-echo:8080 (ID:4181) ... DENIED` for D7/D8; `<> 1.1.1.1:443 (world) ... DENIED` for D9; `<> kube-system/coredns-...:53 (ID:7703) ... DENIED (UDP)` and `(TCP Flags: SYN)` for D10/D11, which shows the kube-dns ClusterIP had been translated to a CoreDNS pod before the policy verdict.
- Every drop is also logged as `Policy denied DROPPED`, one per SYN retransmit, which matches the silent timeouts the client observed.

Verdict: PASS on every destination.

---

## Summary

| ID | Destination | kindnet (previous spike) | Calico iptables | Calico eBPF | Cilium KPR |
|:--|:--|:--|:--|:--|:--|
| A1 | egress proxy via ClusterIP | allowed | allowed | allowed | allowed |
| A2 | egress proxy pod IP | not tested | allowed | allowed | allowed |
| A3, A4 | proxied resolver ClusterIP UDP, TCP | allowed (UDP) | allowed | allowed | allowed |
| A5, A6 | resolve egress Service name via proxied resolver, then CONNECT | not tested | allowed | allowed | allowed |
| A7, A8 | gateway via ClusterIP and pod IP, backend on the same node | not tested | allowed | allowed | allowed |
| D1 | API server ClusterIP 10.96.0.1:443 | **not blocked** | blocked | blocked | blocked |
| D2 | control-plane node IP :6443 | not tested | blocked | blocked | blocked |
| D3 | own node IP :10250 | **not blocked** | blocked | blocked | blocked |
| D4 | control-plane node IP :10250 | not tested | blocked | blocked | blocked |
| D5 | hostNetwork pod on the same node | not tested | blocked | blocked | blocked |
| D6 | hostNetwork pod on the other node | not tested | blocked | blocked | blocked |
| D7 | pod IP in another namespace (same node) | blocked | blocked | blocked | blocked |
| D8 | Service ClusterIP whose backend is a non-selected pod on the same node | blocked (single node) | blocked | blocked | blocked |
| D9 | 1.1.1.1:443 | blocked | blocked | blocked | blocked |
| D10, D11 | default kube-dns ClusterIP UDP, TCP | blocked (UDP) | blocked | blocked | blocked |
| D12, D13 | selected peer, non-listed port | not tested | blocked | blocked | blocked |

The two cases kindnet let through (the pod's own node IP, and the API server reached through `10.96.0.1`, which DNATs to a hostNetwork process on a node) are blocked by all three production dataplanes. In Calico iptables mode the reason is visible in the rule set: workload egress policy is applied from both the FORWARD and the INPUT hooks (`cali-INPUT -i cali+ -g cali-wl-to-host`), so locally delivered packets are covered. In Calico eBPF and Cilium, policy is attached to the pod's veth egress, which every packet leaving the pod traverses regardless of its destination.

## Caveats

- Each probe ran once per configuration; no repetition or load. Blocking manifests as a 5 s curl timeout (silent drop) on every CNI; none of them sent an ICMP or RST for a denied destination.
- Calico eBPF was enabled in place on a cluster that had run kube-proxy and the iptables dataplane first; a fresh eBPF install was not tested. The config dump shows `BPFAttachType:"Netkit"` but the pod interfaces are veths (`Device Mode` not applicable to Calico; the pins are under `/sys/fs/bpf/tcx`), so the attach fell back to tcx.
- Cilium ran in VXLAN tunnel mode with legacy host routing and iptables masquerading (the defaults on this kernel in kind); native routing and BPF host routing were not tested. `kubeProxyReplacement=true` was feasible on Docker Desktop's 6.12 linuxkit kernel without extra settings.
- The policy denies the client's own kube-dns, so the client relies entirely on `dnsPolicy: None` pointing at the proxied resolver; any process that hardcodes the cluster resolver fails to resolve rather than being redirected.
- The ClusterIPs in `workload.yaml` are fixed so the client's `dnsConfig.nameservers` can name the resolver before the Service exists; a chart has to solve that ordering (a fixed ClusterIP, or a resolver address injected after the Service is created).
- Kernel: both clusters shared the Docker Desktop VM kernel (6.12.76-linuxkit, arm64). Results on another kernel or on cloud-provider CNIs (AWS VPC CNI with its own policy agent, GKE Dataplane V2, Azure CNI) were not observed.
- A client pod created before the policy exists was not tested on any CNI; the policies were applied while the client was running, and the policy run started 5 to 8 s after apply.

## Cleanup (`out/cleanup.txt`, 08:52:31Z)

The Calico cluster was deleted at 08:46 before the Cilium cluster was created (`out/kind-swap-cilium.log`). `kind delete cluster --name spike-cni` for the Cilium cluster reported `Deleted nodes: ["spike-cni-control-plane" "spike-cni-worker"]`; `kind get clusters` then reported `No kind clusters found.`, no `spike-cni` kube context remained, and `docker rmi` removed `spike-cni-net:v1` and `spike-cni-client:v1` (0 `spike-cni` images left). The containers still running afterwards were the concurrent Envoy spike's `r4-*` set and `podium-postgres` and `podium-minio`; none of them was touched. `helm install --repo` was used instead of `helm repo add`, so no Helm repository was added to the host configuration. Nothing in `../../..` was modified.
