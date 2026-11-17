#!/bin/bash
. /i/clients/urls.sh
pip --version >&2; git --version >&2; ldd $(git --exec-path)/git-remote-https | grep -E 'curl|gnutls|ssl' >&2; gnutls-cli --version | head -1 >&2; wget --version | head -1 >&2; curl -V | head -1 >&2
python3 -I /i/clients/py.py $URLS
for u in $URLS; do cli git-debian-gnutls $u env GIT_SSL_CAINFO=/c/bundle.pem GIT_TERMINAL_PROMPT=0 git ls-remote ${u}x.git; done
for u in $URLS; do cli curl-debian-openssl $u curl -sS --max-time 8 --cacert /c/bundle.pem -o /dev/null $u; done
for u in $URLS; do cli wget-debian-gnutls $u wget -q --timeout 8 --tries 1 --ca-certificate=/c/bundle.pem -O /dev/null $u; done
for u in $URLS; do h=${u#https://}; h=${h%/}; case $h in *:*) p=${h#*:}; h=${h%:*};; *) p=443;; esac; cli gnutls-cli $u sh -c "echo | gnutls-cli --logfile=/dev/null --x509cafile /c/bundle.pem --port $p $h"; done
for u in $URLS; do rm -rf /tmp/pd; cli pip-default $u pip download --isolated --no-deps --disable-pip-version-check --retries 0 --timeout 6 -d /tmp/pd --index-url ${u}simple/ --cert /c/bundle.pem oapkg; done
for u in $URLS; do rm -rf /tmp/pd; cli pip-PIP_CERT $u env PIP_CERT=/c/bundle.pem pip download --no-deps --disable-pip-version-check --retries 0 --timeout 6 -d /tmp/pd --index-url ${u}simple/ oapkg; done
for u in $URLS; do rm -rf /tmp/pd; cli pip-legacy-certs $u pip download --isolated --no-deps --use-deprecated=legacy-certs --disable-pip-version-check --retries 0 --timeout 6 -d /tmp/pd --index-url ${u}simple/ --cert /c/bundle.pem oapkg; done
# NSS: a database holding the CAs as trust anchors, checked by tstclnt.
mkdir -p /tmp/nss; certutil -N --empty-password -d sql:/tmp/nss
for n in nc ncx ctl; do certutil -A -n $n -t "C,," -d sql:/tmp/nss -i /c/$n.pem; done
dpkg -l libnss3 | tail -1 | awk '{print "nss",$3}' >&2
for u in $URLS; do h=${u#https://}; h=${h%/}; case $h in *:*) p=${h#*:}; h=${h%:*};; *) p=443;; esac
  cli nss-tstclnt $u sh -c "printf 'GET / HTTP/1.0\r\n\r\n' | timeout 8 tstclnt -h $h -p $p -d sql:/tmp/nss 2>&1 | tee /tmp/ts; grep -q '^HTTP/1' /tmp/ts"
done
