#!/bin/bash
. spike-owed-b/s2-lib.sh
$N logs ctrl | tail -3 | cut -c1-170
while read ns p c f; do echo "$p: $($K -n $ns exec $p -c $c -- sh -c "grep -c BEGIN $f" </dev/null)"; done < <(holders)
ns=agents; p=c1; c=wca; f=/ca/ca.crt
$K -n $ns exec $p -c $c -- sh -c "cd /tmp; awk 'BEGIN{n=0}/BEGIN/{n++}{print > \"/tmp/z.\" n}' $f; ls /tmp; for x in /tmp/z.*; do openssl x509 -in \$x -noout -subject; done; rm -f /tmp/z.*"
