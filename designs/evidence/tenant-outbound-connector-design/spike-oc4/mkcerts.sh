#!/bin/sh
# Egress CA, a foreign CA, and three outbound gateway leaves.
set -e
apk add -q openssl >/dev/null
cd /certs
SAN=lenny-outbound-gateway.lenny-outbound.svc
mkca() { openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout $1.key -out $1.pem -days 2 -subj "/CN=$1" -addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign 2>/dev/null; }
mkleaf() { # name ca san
  openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout $1.key -out $1.csr -subj "/CN=$3" 2>/dev/null
  printf "subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth\nkeyUsage=critical,digitalSignature\n" "$3" > $1.ext
  openssl x509 -req -in $1.csr -CA $2.pem -CAkey $2.key -CAcreateserial -out $1.pem -days 2 -extfile $1.ext 2>/dev/null
  cat $1.pem $1.key > $1.both; }
mkca egress-ca; mkca foreign-ca
mkleaf gw egress-ca $SAN
mkleaf gw-wrongsan egress-ca other.lenny-outbound.svc
mkleaf gw-foreign foreign-ca $SAN
ls
