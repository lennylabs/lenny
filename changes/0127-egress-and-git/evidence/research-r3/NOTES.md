# Research r3 notes

## Q7 ls-remote results
See lsremote1.txt, lsremote2.txt. Summary:
- github.com/git/git OK; ssh.github.com:443 over HTTPS -> TLS fails (port 443 speaks SSH).
- gitlab.com top-level and subgroup OK; altssh.gitlab.com https fails (TLS alert).
- bitbucket.org OK; codeberg, gitea.com, git.sr.ht (~user) OK; launchpad OK; invent.kde.org OK.
- gitolite.kernel.org https fails; git.kernel.org same path OK.
- review.opendev.org/openstack/nova OK (anon); /a/ prefix requires auth.
- gerrit.wikimedia.org/mediawiki/core NOT FOUND; /r/mediawiki/core OK (Gerrit HTTP under /r/).
- android-review.googlesource.com & android.googlesource.com both serve.
- GNOME ssh.gitlab.gnome.org:443 no connect; freedesktop ssh.gitlab.freedesktop.org cert mismatch.
- Azure: https://dev.azure.com/dnceng-public/public/_git/dotnet-arcade asked for creds (repo name unverified); v3-form paths 404.
- CodeCommit https endpoint asks creds (SigV4/ git creds needed).

## Q2 GitHub
- REST rate limits: secondary list includes "No more than 2,000 OAuth access token requests per hour are allowed for GitHub Apps and OAuth apps." Per-endpoint 900 points/min, POST=5 pts. Subject to change; undisclosed limits.
- OAuth apps page ties 2000 to "generation of these user access tokens".
- No doc says explicitly whether POST /app/installations/{id}/access_tokens counts. No changelog found.
- GHES: "Rate limits are disabled by default for GitHub Enterprise Server"; admin can set secondary limits.

## Q4 Squid
- squid-cache.org/Versions: stable v7.8 (2026-10-30); dev only via git (no tarballs).
- v7 RELEASENOTES: external_acl_type kept; removed %IDENT format code (Ident removed); ext_time_quota_acl -l removed.
- master doc/release-notes/release-8.sgml.in: removed ext_lm_group_acl helper; ext_ldap_group_acl now requires -B and -F; src_as/dst_as removed, notes recommend external ACLs for ASN. "No removed directives". external_acl_type still in master src/cf.data.pre.

## Q3 Calico/Cilium
- Calico bpf-gpl tc.c (master): calico_nat_lookup_tc() (~L510) sets state->post_nat_ip_dst/dport (~L544), route lookup on post-NAT dst, then do_policy: CALI_JUMP_TO_POLICY (~L803-829). NAT rewrite of packet happens after policy in accepted program (~L1009).
- polprog/pol_prog_builder.go: matchLeg dest -> stateOffPostNATIPDst by default; legDestPreNAT only for HostPreDnatTiers / HostNormalTiers (host endpoint policies).
- Tigera docs: "network policy is enforced based on the pods, not the service Cluster IP"; "Egress network policy is enforced for the client pod after the DNAT has changed the connection's destination IP".
- Cilium bpf_lxc.c main: per-packet LB (__per_packet_lb_svc_xlate_4) runs before CT tailcall; handle_ipv4_from_lxc then policy_can_egress4 using identity of ip4->daddr (backend). With full socket-LB, translation at connect() so packets carry backend IP. Self via ClusterIP (hairpin) skips policy.

## Q6 Envoy
- api/envoy/config/route/v3/route_components.proto (main): RouteMatch.connect_matcher and RouteAction.UpgradeConfig.connect_config both say "Note that CONNECT support is currently considered alpha in Envoy." + TODO(htuch) to replace with alpha tag.
- docs/root/intro/arch_overview/http/upgrades.rst: CONNECT off by default (403); warning on terminate mode "major security holes ... unsanitized headers"; CONNECT-UDP alpha; raw UDP tunneling alpha; HTTP/3 allow_extended_connect alpha.
- extensions_metadata.yaml: dynamic_forward_proxy filter+cluster stable, robust_to_untrusted_downstream; ext_authz http stable robust_to_untrusted_downstream_and_upstream; tcp_proxy stable.
- Latest tag v1.39.6.
- Production: Istio waypoint "Envoy-based proxy", HBONE = HTTP/2 CONNECT+mTLS port 15008 (ztunnel is separate); Google Cloud SWP "Envoy proxy pool", supports HTTP/HTTPS CONNECT; Lyft egress (blog 403, via search snippet).
- Advisories (github.com/envoyproxy/envoy/security/advisories): CVE-2025-64763 GHSA-rj35-4m94-77jh early CONNECT data (Low); CVE-2025-54588 GHSA-g9vw-6pvx-7gmw DFP DNS cache UAF (High); GHSA-gq3v-vvhj-96j6 ext_authz bypass via proxy protocol invalid UTF-8 (Feb 2024, = CVE-2024-23324); GHSA-vcf8-7238-v74c accepts HTTP 200 for upgrade mode (Jun 2024); GHSA-3mh5-6q8v-25wj auto_sni >255 authority crash (Apr 2024); GHSA-mvh9-767w-x47j ext_authz UAF per-route overrides (Jun 2026); GHSA-87ph-jqwm-pg6r ext_authz :path null deref (Aug 2026); GHSA-q8wp-gf7q-m8cv ext_authz RawHttpClientImpl segfault; GHSA-8vc2-jrm4-835w OAuth2 crash path-less CONNECT (Oct 2026); GHSA-47vj-9r25-wv5j api_key_auth CONNECT crash (Oct 2026); GHSA-3vhp-c83q-jqc2 response poisoning via generic upgrade (Aug 2026); GHSA-pq33-4jxh-hgm3 TCP conn pool crash large requests (Oct 2025).

## Q1 SWG identity
- Netskope https://docs.netskope.com/en/proxy-chaining : "add at least one header X-Forwarded-For or X-Authenticated-User HTTTP header inside the SSL tunnel"; "proxy must perform SSL decryption to add HTTP headers inside the SSL tunnel"; allowlist proxy public IP; trust Netskope CA. -> CONNECT-only headers not documented as sufficient.
- Prisma Access explicit proxy (how-explicit-proxy-identifies-users): XAU only from Trusted Source Address; XAU base64 (Blue Coat guide); XFF not supported for ingestion (logged only); decryption needed for identity; undecrypted -> known-IP (swg-authenticated-ip-user). Proxy chaining Blue Coat guide says on-prem proxy must authenticate+decrypt to insert XAU.
- Zscaler: help pages JS-rendered. Search snippets: XFF used to identify sub-location (location "Use XFF from Client Request"); Surrogate IP for user policy; ZIA *inserts* X-Authenticated-User toward upstream third-party proxies (disabled default, "Unknown" when unauthenticated, base64 option). No doc found that ZIA consumes XAU from downstream.
- Umbrella: XFF from downstream (WSA) used for internal IP -> internal-network identities; egress public IP registered as Network identity; SAML disabled in chained WSA guide. No user header found. CONNECT detail not confirmed.
- Cloudflare Gateway proxy endpoints: source IP endpoints (no user identity) or authorization endpoints (Access login cookie, require TLS inspection); "PAC files do not currently support username/password, mTLS, or Kerberos authorization"; no XFF/XAU ingestion documented.
- Forcepoint Web Security Cloud proxy chaining: reads XFF (client IP) and X-Authenticated-User (domain\user) from downstream ISA/TMG, Blue Coat, Squid; HTTPS requires Blue Coat SSL license (decryption). Forcepoint ONE specifics not confirmed.
- Broadcom Cloud SWG: Proxy Forwarding access method; "no authentication options for proxy-forwarded traffic" in portal, auth configured on forwarding appliance (ProxySG/ISA) which supplies user/group (KB 175990 / tech256300). Header names not confirmed in this pass.
- Skyhigh, iboss: no primary doc found.

## Q5 prior art
- Fly tokenizer README: sealed secret (NaCl box to proxy pubkey) in Proxy-Tokenizer header; client speaks plain HTTP to proxy, proxy upgrades to HTTPS ("assumes ... secure transport (a VPN)"); no MITM CA; processors inject/hmac/sigv4/jwt/github_app; allowed_hosts; fly-src auth binds secret to machine.
- Deno Sandbox (blog 2026-02-03; docs.deno.com/sandbox/security): placeholder in env; real value only on outbound request to approved host via outbound proxy; allowNet; TLS/where-substituted not documented; HN raised body-insertion concern.
- Vercel Sandbox firewall docs: header injection via transform (overwrites same-named headers) — not placeholder; TLS terminated only for domains with transform/forwardURL; per-sandbox CA added to system certs + standard env vars; SNI-based; domain fronting caveat; no Postgres transforms; IP-literal via subnets bypasses.
- E2B docs.e2b.dev/secrets/inject: private beta; Secret.fill reference in header transform; HTTPS only; fail open (header omitted); TLS detail not documented.
- Daytona docs secrets: placeholder token in env; HTTPS request headers only; no body/query; response scrubbing; host allowlist (no allowlist => all hosts!); transformed placeholders (base64 Basic) fail; TLS detail not documented.
- Cloudflare Sandbox SDK: outbound Workers (outbound/outboundByHost) inject creds; interceptHttps opt-in; ephemeral per-sandbox CA at /etc/cloudflare/certs/cloudflare-containers-ca.crt, best-effort trust + common CA env vars; ports 80/443 only (changelog 2026-04-13).
- Anthropic Claude Code cloud: GitHub proxy (scoped credential swapped; gh sees placeholder "proxy-injected"); network secrets attached by agent proxy for listed hosts; never attached to api.anthropic.com/registries/setup script.
- OpenAI Codex cloud: secrets only for setup scripts, removed before agent phase; HTTP/HTTPS proxy for all egress. No substitution documented.
- GitHub Codespaces: secrets exported as env vars.
- Modal: (see fetch)

## Q7 additional (forge APIs)
- GitLab API ssh_url_to_repo: GNOME git@ssh.gitlab.gnome.org:GNOME/glib.git vs https://gitlab.gnome.org/...; freedesktop git@ssh.gitlab.freedesktop.org:mesa/mesa.git; KDE, salsa, gitlab.com same host.
- Codeberg ssh://git@codeberg.org/forgejo/forgejo.git; gitea.com git@gitea.com:gitea/tea.git.
- Bitbucket Cloud API clone links: git@ssh.bitbucket.org:atlassian/... (SSH host move; bitbucket.org SSH refused after 2026-11-12 per Atlassian community announcement); https://ssh.bitbucket.org hangs (no HTTPS).
- Gerrit /config/server/info: wikimedia anonymous http https://gerrit.wikimedia.org/r/${project}; opendev https://review.opendev.org/${project}.
- Azure docs: git@ssh.dev.azure.com:v3/fabrikam-fiber/FabrikamFiber/FabrikamFiber vs dev.azure.com/{org}/{project}/_git/{repo}; old vs-ssh.visualstudio.com.
- kernel.org korg docs: git@gitolite.kernel.org:pub/scm/... push; git.kernel.org public.

## Q8
- man proc_pid_environ: initial environment at execve; later putenv/unsetenv not reflected; access PTRACE_MODE_READ_FSCREDS.
- Node report.md: diagnostic report includes environmentVariables on uncaught exception/fatal error/signal; --report-exclude-env.
- OWASP Secrets Mgmt CS: env vars "generally accessible to all processes and may be included in logs or system dumps"; "not recommended unless the other methods are not possible".
- CWE-526 Cleartext Storage of Sensitive Information in an Environment Variable.
- K8s: secret volumes in tmpfs; restrict to containers that need it; apps must avoid logging.
