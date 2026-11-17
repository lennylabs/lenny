#!/bin/sh
# Pushes 3 MB (> http.postBuffer) straight to the net/http/cgi upstream, no proxy.
export HOME=/tmp/h3 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/tmp/h3/.gitconfig
/spike/t3/bin/upstream -mode cgi 2>/dev/null & sleep 0.5
cd /tmp/w3/full && head -c 3000000 /dev/urandom > m.bin && git add m.bin && git commit -qm m
git push http://alice:s3cret@localhost:8080/org/repo.git HEAD:main 2>&1 | grep -E 'error|fatal|->'
git -c http.postBuffer=10000000 push http://alice:s3cret@localhost:8080/org/repo.git HEAD:main 2>&1 | grep -E 'error|fatal|->'
pkill -f bin/upstream
