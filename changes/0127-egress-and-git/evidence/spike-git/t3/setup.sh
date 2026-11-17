#!/bin/sh
# Builds the upstream bare repository: small history plus 4 x 25 MB random blobs.
set -e
export HOME=/tmp/hs GIT_CONFIG_NOSYSTEM=1; rm -rf $HOME /srv/git /tmp/seed; mkdir -p $HOME /srv/git/org
git config --global user.name alice; git config --global user.email alice@acme.com
git init -q -b main /tmp/seed; cd /tmp/seed
for i in $(seq 1 30); do echo "line $i" >> f.txt; git add f.txt; git commit -qm "c$i"; done
for i in 1 2 3 4; do head -c 26214400 /dev/urandom > big$i.bin; done
git add big*.bin; git commit -qm big
git clone -q --bare /tmp/seed /srv/git/org/repo.git
cd /srv/git/org/repo.git
git config http.receivepack true; git config uploadpack.allowFilter true; git config uploadpack.allowAnySHA1InWant true
git config receive.denyNonFastForwards false
du -sh /srv/git/org/repo.git
