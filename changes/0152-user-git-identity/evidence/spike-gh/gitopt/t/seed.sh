#!/bin/sh
# seed.sh: bare repositories in /repos, built in the origin container.
export HOME=/tmp/s; mkdir -p $HOME; git config --global user.email a@b; git config --global user.name a; git config --global init.defaultBranch main; git config --global protocol.file.allow always
mk(){ # path, then commits
 d=/repos/$1.git; mkdir -p $(dirname $d); rm -rf $d; git init -q --bare $d
 w=$(mktemp -d); git -C $w init -q; cd $w; for i in 1 2 3 4 5; do echo "$1 $i" > f; git add f; git commit -qm "c$i"; done; head -c 200000 /dev/urandom > blob; git add blob; git commit -qm blob; git push -q $d main; cd /; rm -rf $w
}
for r in acme/widgets acme/secret acme/lib acme/lib2 alice/widgets pub/lib acme/lfsrepo agent/own; do mk $r; done
git -C /repos/alice/widgets.git update-ref refs/heads/cross-branch main
git -C /repos/acme/widgets.git update-ref refs/pull/13/head main
git -C /repos/acme/widgets.git update-ref refs/pull/7/head main
git -C /repos/acme/widgets.git update-ref refs/heads/feature main
mk pub/smallpublic
# a hostile repository: submodules outside the set and on a denied host, and an LFS endpoint elsewhere
d=/repos/acme/hostile.git; rm -rf $d; git init -q --bare $d; w=$(mktemp -d); cd $w; git init -q; echo s > s; git add s
cat > .gitmodules <<'M'
[submodule "out"]
	path = out
	url = ../secret.git
[submodule "evil"]
	path = evil
	url = https://attacker.example.test/x.git
M
printf '[lfs]\n\turl = https://other.example.test/lfs-exfil\n' > .lfsconfig
for s in out evil; do h=$(git -C /repos/acme/lib.git rev-parse main); git update-index --add --cacheinfo 160000,$h,$s; done
git add .gitmodules .lfsconfig; git commit -qm hostile; git push -q $d main; cd /; rm -rf $w
# a 64 MiB incompressible repository
d=/repos/acme/big.git; rm -rf $d; git init -q --bare $d; w=$(mktemp -d); cd $w; git init -q; head -c 67108864 /dev/urandom > big; git add big; git commit -qm big; git push -q $d main; cd /; rm -rf $w
rm -rf /repos/pub/big.git; cp -r /repos/acme/big.git /repos/pub/big.git
# submodules: absolute on a bound host, relative, and on a host with no binding
d=/repos/acme/super.git; rm -rf $d; git init -q --bare $d; w=$(mktemp -d); cd $w; git init -q; echo s > s; git add s
cat > .gitmodules <<'M'
[submodule "abs"]
	path = abs
	url = https://ghes.example.test/acme/lib.git
[submodule "rel"]
	path = rel
	url = ../lib2.git
[submodule "other"]
	path = other
	url = https://other.example.test/pub/lib.git
M
for s in abs rel other; do case $s in abs) t=acme/lib;; rel) t=acme/lib2;; other) t=pub/lib;; esac; h=$(git -C /repos/$t.git rev-parse main); git update-index --add --cacheinfo 160000,$h,$s; done
git add .gitmodules; git commit -qm subs; git push -q $d main; cd /; rm -rf $w
chmod -R a+rwX /repos /lfs; find /repos -maxdepth 2 | wc -l
