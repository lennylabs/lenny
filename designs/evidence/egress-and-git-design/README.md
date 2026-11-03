# Evidence for the egress and git design

This folder holds the test evidence behind `designs/egress-and-git-design.md`, under "Validation record". `designs/forensics-evidence-preservation-design.md` and `designs/tenant-outbound-connector-design.md` cite it for one result each. The git path under candidate T is recorded in `../user-git-identity-design/spike-gh/`.

## Date and environment

The runs took place from 2026-11-01 to 2026-11-03 on a laptop with 12 CPUs, using Docker Desktop on macOS with Linux arm64 containers and kind clusters. Other spikes shared the laptop during several runs, so the numbers show relative cost and do not size production. Each `RESULTS.md` states the versions and the stand-ins it used.

Most runs used stand-ins for `netagent`, the mirror, and the decision service, because those components do not exist yet. Fake endpoints stood in for cloud providers. A result against a stand-in or a fake endpoint shows the behaviour of the client or the proxy under test, and it does not show a provider's behaviour. `spike-lsrefs` and `research-r3` also read public git hosts anonymously, and no test used a credential on a public host.

## Map from rounds to directories

The round numbers are those of "Validation record".

| Round | Directories | Coverage |
| :-- | :-- | :-- |
| First spike | None | Research only, with no files. |
| Second spike | `spike-envoy`, `spike-proxy-alts`, and `spike-git` | Envoy, Squid, a Smokescreen-based proxy, and git. These directories have no `RESULTS.md`. |
| Third | `spike-envoy-r3`, `spike-k8s`, `spike-tools`, `spike-inject`, `spike-formats`, and `research-r3` | Envoy, Kubernetes mechanisms, client tools, credential injection, and credential formats. |
| Fourth | `spike-federation` | Federation and metadata formats. |
| Fifth | `spike-envoy-r4` and `spike-cni` | Envoy resilience, soak, and drain, and production CNIs. The `RESULTS.md` in `spike-envoy-r4` titles itself round 4. |
| Sixth | `spike-lsrefs` and `spike-level1` | `ls-refs` through Envoy, and level 1 on Squid. |
| Seventh | `spike-tls` | Server TLS. |
| Eighth | `spike-userns` | The mirror's start sequence and `hostUsers`. |
| Ninth | `spike-evidence` | Evidence streams. |
| Tenth | `spike-owed-a`, `spike-owed-b`, and Part A of `spike-owed-c` | Owed items, and the Go `netagent` prototype. |
| Eleventh | Part B of `spike-owed-c` | Evidence as a separate service. |
| Twelfth | `spike-owed-d` | The items a laptop could run against revision 42. |

## How the design documents cite this folder

`designs/egress-and-git-design.md` cites results by round and by mechanism, so the map above locates each one. `RESULTS.md` files cite their own files relative to their own directory.

`spike-evidence/t1.out` contains the invalid first load section that the ninth round describes, and `spike-evidence/RESULTS.md` marks it.

## Changes to copied files

`RESULTS.md` files and raw output are otherwise byte-for-byte copies, and no measured value, row, or number was changed.

- Absolute paths were rewritten to paths relative to this folder. The scratchpad root became `.`, and a path into the repository became `../../..`. Rewritten output includes `spike-cni/RESULTS.md`, `spike-federation/logs/a-java.out`, `spike-federation/logs/a-java-svc.out`, and `spike-owed-a/item5/raw/t5b.out`. The other rewrites are in scripts and in `spike-owed-b/gorepo/go.mod.txt`, and those paths no longer run as written.
- Go sources and `go.mod` files carry an added `.txt` suffix, so a citation of `go/cmd/netagent` refers to `go/cmd/netagent/main.go.txt`. Without the suffix, the repository's `go build ./...`, `go vet ./...`, `gofumpt -l .`, and `goimports -l .` treat these throwaway programs as repository source.
- File modes were reset to `0644`, because these programs are records and are not meant to be run from here.

## What was left out

A citation in `RESULTS.md` of one of these paths does not resolve in this repository. `RESULTS.md` states the result.

| Left out | Reason |
| :-- | :-- |
| `bin/`, `spike-owed-b/mirror-img/`, `spike-userns/bin/`, `.gocache/`, `.gopath/`, `rust-target/`, and `__pycache__/` | Binaries, Go module and build caches, and Rust build output. |
| `dl/` | Downloaded releases and packages. The versions are below. |
| `ref/`, `spike-git/t6/src/`, `research-r3/calico/`, `research-r3/cilium/`, `research-r3/envoy/`, `research-r3/squidsrc/`, `research-r3/dl/`, `spike-proxy-alts/squid/squid.conf.documented`, and the Gradle wrapper scripts | Third-party source and documents, read as data. `research-r3/NOTES.md` records what was read. |
| `certs/`, `certs-l1/`, `mitmca/`, `spike-inject/shared/trust.*`, `spike-k8s/tokens/`, `spike-federation/run/issuer-key.pem`, the token files under `spike-federation/run/` and `spike-formats/a/`, `spike-owed-a/item3/tokens.env`, and `run/*/ca.state` | Private keys, certificates, and tokens, each generated at run time. |
| `spike-envoy-r4/logs/*accesslog*.jsonl`, `spike-owed-b/out/s1-i3/`, `s1-renew/`, `s2/`, and `s3-*/`, `spike-owed-d/raw/i1/` and `i1b/`, `spike-owed-d/work/` (35 MB), and driver logs | Bulk logs and per-sample readings. The tables beside them remain, such as `spike-owed-d/raw/i1-table.txt`, `i1-hardlimit.txt`, and `i1b-sustain.txt`, and `spike-owed-b/out/s1-renew-analysis.txt`, `s2-analysis.txt`, and each `s3-*.out`. |
| `raw/invalid/` in `spike-owed-c` and `spike-owed-d`, `spike-federation/logs/c-*-wi-long.contaminated.out`, `spike-owed-b/out/s4-invalid-harness-name-reuse.out`, and the first-run and second-run `.out` files under `spike-owed-a/item3/raw/` and `item5/raw/` other than `t5-first-run-mapped-rule.out` | Passes a harness defect spoiled, or that a later run superseded. Each `RESULTS.md` describes the defect. |
| `patch*.py` | Edits to the harness, which the kept source reflects. |
| `go.sum` and `Cargo.lock` | Module checksums and lock data. Versions are in `go.mod.txt` and `Cargo.toml`. |
| `spike-envoy-r3/logs/t6-cost-agent:good.stats` and `t6-cost-agent:sni.stats` | A file name holding a colon does not check out on every platform. `logs/t6-cost-cpu-v1.40.2.txt` carries the cost result. |
| Design edit scripts, section drafts, and review reports | The design document's own review sections are the record. |

## Third-party inputs

These were downloaded for the runs and not copied.

| Input | Version |
| :-- | :-- |
| [Envoy](https://github.com/envoyproxy/envoy) images and source | v1.40.2 and v1.39.6 |
| [Calico manifest](https://raw.githubusercontent.com/projectcalico/calico/v3.33.0/manifests/calico.yaml) | v3.33.0 |
| [Cilium Helm chart](https://helm.cilium.io/) | 1.20.4 |
| [cert-manager](https://cert-manager.io/) | v1.21.3 |
| [Squid](https://www.squid-cache.org/) | 7.7 |
| [mitmproxy](https://mitmproxy.org/) | 12.3.1 |
| [git](https://github.com/git/git) and [git-lfs](https://github.com/git-lfs/git-lfs) | git 2.49 and git-lfs 3.6 |
| [`gh`](https://github.com/cli/cli) and [`glab`](https://gitlab.com/gitlab-org/cli) | 2.103.3 and 1.127.0 |

## Credentials

No private key, certificate, or kubeconfig is in this folder. Scripts that read a key at run time, such as `spike-owed-b/s4b.sh`, hold no key material. `spike-k8s/t4-sidecar.yaml` and `spike-k8s/RESULTS.md` hold a `BEGIN CERTIFICATE` marker in a fake test string. `spike-k8s/t1-out.txt` holds the first 40 characters of a service account token from a deleted kind cluster, which the probe truncated to part of the token's header. The other token values, such as `lenny-injected`, `GH-REAL-55aa01`, `sk-LLM-REAL-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL`, and `gcp-sts-at-v1-6fcfaf`, are placeholders that the harness or a fake provider wrote.
