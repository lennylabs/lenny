#!/bin/bash
P=http://198.19.0.41:3128; L="docker exec ls-client /b/lsrefs -mode one -insecure -proxy $P"
echo "initech ls-refs : $($L -tenant initech -repo https://gitlab.initech.internal/fast.git)"
echo "acme ls-refs    : $($L -tenant acme -repo https://gitlab.initech.internal/fast.git)"
echo "initech big     : $($L -tenant initech -repo https://gitlab.initech.internal/big.git)"
echo "--- decision service down"
docker stop -t0 ls-authz1 ls-authz2 >/dev/null; /bin/sleep 2
echo "initech         : $($L -tenant initech -repo https://gitlab.initech.internal/fast.git)"
