#!/bin/sh
# t3.sh <config>: remote forms: a remote set on a repository, a clone the agent makes, and a remote added later.
. /t/env.sh; . /t/lib.sh
CFG=$1; export GIT_CONFIG_GLOBAL=/t/cfg/$CFG.gitconfig; [ "$CAINFO" = design ] && export GIT_SSL_CAINFO=/s/bundle.pem
W=$(mktemp -d); cd $W; TL=3
form(){ # label url host kind
 l=$1; u=$2; h=$3; k=$4
 case $k in
  gh) G="env GH_HOST=$h GH_ENTERPRISE_TOKEN=lenny-injected timeout 15 gh pr list";;
  com) G="env GH_TOKEN=lenny-injected timeout 15 gh pr list";;
  gl) G="env GITLAB_HOST=$h GITLAB_TOKEN=lenny-injected timeout 15 glab mr list";;
 esac
 rm -rf r c; git init -q -b main r; cd r; git remote add origin "$u"; git commit -q --allow-empty -m x; git checkout -q -b b
 step "$l-remote-v" "git remote -v | head -1"
 step "$l-git" "timeout 15 git ls-remote origin 2>&1 | tail -2"
 step "$l-tool" "$G 2>&1"
 cd ..
 step "$l-self-clone" "timeout 20 git clone -q '$u' c 2>&1; git -C c remote -v 2>/dev/null | head -1; git -C c config --get remote.origin.url 2>/dev/null"
 if [ -d c/.git ]; then cd c; step "$l-self-clone-tool" "$G 2>&1"; cd ..; fi
 case $k in gl) b=https://gitlab.example.test/acme/widgets.git;; com) b=https://github.com/acme/widgets.git;; *) b=https://ghes.example.test/acme/widgets.git;; esac
 rm -rf a; git clone -q $b a 2>/dev/null; if [ -d a/.git ]; then cd a; git remote add later "$u"; step "$l-added-later" "git remote -v | grep later | head -1; timeout 15 git fetch later 2>&1 | tail -1"; step "$l-added-later-tool" "$G 2>&1"; cd ..; fi
}
form ghes-ssh ssh://git@ghes.example.test/acme/widgets.git ghes.example.test gh
form ghes-scp git@ghes.example.test:acme/widgets.git ghes.example.test gh
form com-ssh ssh://git@github.com/acme/widgets.git github.com com
form com-scp git@github.com:acme/widgets.git github.com com
form gl-ssh ssh://git@gitlab.example.test/acme/widgets.git gitlab.example.test gl
form gl-scp git@gitlab.example.test:acme/widgets.git gitlab.example.test gl
form nossh-scp git@nossh.example.test:acme/widgets.git nossh.example.test gh
form arb-ssh ssh://git@other.example.test/pub/lib.git other.example.test gh
form arb-scp git@other.example.test:pub/lib.git other.example.test gh
form otheruser-scp alice@ghes.example.test:acme/widgets.git ghes.example.test gh
cd /; rm -rf $W
