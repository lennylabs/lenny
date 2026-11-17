#!/bin/sh
chromium --version 2>/dev/null >&2
mkdir -p $HOME/.pki/nssdb; certutil -N --empty-password -d sql:$HOME/.pki/nssdb
for n in nc ncx ctl; do certutil -A -n $n -t "C,," -d sql:$HOME/.pki/nssdb -i /c/$n.pem; done
for u in https://allowed.oa.test/ https://denied.oa.test/ https://sub.allowed.oa.test/ https://viaint.oa.test/ https://198.19.50.20/ https://ctl.oa.test/ https://unk.oa.test/ https://xallowed.oa.test/ https://xdenied.oa.test/ https://198.19.50.20:8443/; do
  out=$(timeout 40 chromium --headless=new --no-sandbox --disable-gpu --disable-dev-shm-usage --disable-background-networking --user-data-dir=/tmp/prof --enable-logging=stderr --v=0 --virtual-time-budget=4000 --dump-dom "$u" 2>&1); rc=$?
  if echo "$out" | grep -q 'ok HTTP/'; then s=OK; else s=REFUSED; fi
  echo "chromium $u $s rc=$rc $(echo "$out" | grep -oE 'net_error -?[0-9]+|ERR_[A-Z_]+|net::[A-Z_]+|CERT_[A-Z_]+|ssl_client_socket[^ ]*.*' | sort -u | head -3 | tr '\n' ' ' | cut -c1-160)"
done
