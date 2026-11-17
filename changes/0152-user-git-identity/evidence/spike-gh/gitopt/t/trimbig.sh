#!/bin/sh
# trimbig.sh: drops pushed branches from the 64 MiB repository, so later clones and the origin's memory stay flat.
cd /repos/acme/big.git && git for-each-ref --format='%(refname)' refs/heads/lenny | while read r; do git update-ref -d $r; done; git -c gc.pruneExpire=now gc -q --prune=now; du -sh . | cut -f1
