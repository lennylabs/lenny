#!/bin/sh
# usage: sni_test.sh PROXY:PORT CONNECT_HOST SNI  -> prints handshake outcome
docker run --rm --network spike-px-net spike-squid7:local sh -c "echo | timeout 15 openssl s_client -proxy $1 -proxy_user agent -proxy_pass pass:lease-A -connect $2:443 -servername $3 -brief 2>&1 | grep -E 'CONNECTION ESTABLISHED|Peer certificate|Verification|error|errno|Connection refused|unexpected eof|alert' | head -3"
