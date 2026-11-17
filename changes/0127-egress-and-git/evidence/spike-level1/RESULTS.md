# Level 1 on stock Squid 7.7 (round 6, 2026-11-02)
render.py turns a policy document into htpasswd, per-identity dstdomain ACLs, and http_access rules. Raw output: t.out, t2.out.
- Per-identity lists hold in both directions; no credential or wrong password -> 407; unlisted host -> 403.
- Go ls-refs client works through it, including a real host.
- The same credential works from any container that can reach the proxy.
- Without an address rule, a listed name resolving to 172.30.0.20 was reached, and listed literal 127.0.0.1 was attempted (503). With the rule all -> 403.
- Fronting: CONNECT to a listed host with TLS and Host naming an unlisted one passes.
- Edit: reconfigure took 0.21 s for new CONNECTs; a tunnel opened before the edit stayed open.
- Rotation: two verifiers on one username do not work (basic_ncsa_auth keeps one; old password 407). A username per credential generation gives overlap, and a retired generation is refused at once even with credentialsttl 7200 s, because the ACL names the username.
- Prior round: client_lifetime does not end active tunnels.
