#!/bin/sh
. /i/clients/urls.sh
apk add -q curl git openssl >/dev/null 2>&1
curl -V | head -1 >&2; git --version >&2; openssl version >&2
for u in $URLS; do cli curl-alpine-openssl $u curl -sS --max-time 8 --cacert /c/bundle.pem -o /dev/null $u; done
for u in $URLS; do cli git-alpine-openssl $u env GIT_SSL_CAINFO=/c/bundle.pem GIT_TERMINAL_PROMPT=0 git ls-remote ${u}x.git; done
for u in $URLS; do h=${u#https://}; h=${h%/}; case $h in *:*) p=${h#*:}; h=${h%:*};; *) p=443;; esac; case $h in [0-9]*) vf="-verify_ip $h";; *) vf="-verify_hostname $h";; esac
  r=$(echo | openssl s_client -connect $h:$p -CAfile /c/bundle.pem $vf -verify_return_error 2>&1); if echo "$r" | grep -q 'Verify return code: 0'; then echo "openssl-s_client $u OK"; else echo "openssl-s_client $u REFUSED $(echo "$r" | grep -E 'Verify return code|verify error' | head -1 | cut -c1-140)"; fi; done
