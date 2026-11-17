#!/bin/bash
mkdir -p /tmp/nss; certutil -N --empty-password -d sql:/tmp/nss
for n in nc ncx ctl; do certutil -A -n $n -t "C,," -d sql:/tmp/nss -i /c/$n.pem; done
echo "--- gnutls-cli to the IP leaf under DNS-only nc"
echo | gnutls-cli --x509cafile /c/bundle.pem --port 443 198.19.50.20 2>&1 | grep -E 'Status|error|Error|Name|name|verif|constraint' | cut -c1-200 | head
echo "--- certtool --verify-profile on leaf and chain"
certtool --verify --load-ca-certificate /c/nc.pem --infile /c/ip/tls.crt 2>&1 | tail -4 | cut -c1-200
certtool --verify --load-ca-certificate /c/nc.pem --infile /c/denied/tls.crt 2>&1 | tail -4 | cut -c1-200
certtool --verify --load-ca-certificate /c/nc.pem --infile /c/allowed/tls.crt 2>&1 | tail -3 | cut -c1-200
echo "--- nss vfychain, legacy then libpkix, each leaf"
for n in allowed denied sub ip; do
  for p in "" "-pp"; do printf '%s %s: ' $n "$p"; openssl x509 -in /c/$n/tls.crt -out /tmp/$n.pem; vfychain -d sql:/tmp/nss $p -a -u 1 /tmp/$n.pem 2>&1 | grep -E 'Chain is|ERROR|SEC_ERROR' | head -2 | tr '\n' ' ' | cut -c1-180; echo; done
done
echo "--- tstclnt reason per target"
for h in allowed.oa.test denied.oa.test viaint.oa.test unk.oa.test; do printf '%s: ' $h; printf 'GET / HTTP/1.0\r\n\r\n' | timeout 8 tstclnt -h $h -p 443 -d sql:/tmp/nss 2>&1 | grep -E 'tstclnt:|^HTTP' | head -1 | cut -c1-160; done
which tstclnt; tstclnt -h 2>&1 | grep -iE -- '-[A-Za-z]  .*(pkix|ocsp|verify|mozilla)' | head
