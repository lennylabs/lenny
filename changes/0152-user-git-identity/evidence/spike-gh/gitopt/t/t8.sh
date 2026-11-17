#!/bin/sh
# t8.sh: alternatives that keep the loopback endpoint.
. /t/env.sh; . /t/lib.sh; export GIT_SSL_CAINFO=/s/bundle.pem
W=$(mktemp -d); TL=3; TAG=$(date +%s)
export GIT_CONFIG_GLOBAL=/t/cfg/L.gitconfig GH_HOST=ghes.example.test GH_ENTERPRISE_TOKEN=lenny-injected
cd $W; git clone -q https://ghes.example.test/acme/widgets.git g; cd g; git checkout -q -b feature-alt-$TAG; echo x > n; git add n; git commit -qm n; git push -q -u origin HEAD
step L-remote-v "git remote -v"
step L-pr-create "gh pr create --title t --body b 2>&1"
step L-pr-list "gh pr list 2>&1"
step L-pr-view "gh pr view --json number 2>&1"
step L-pr-status "gh pr status 2>&1"
step L-glab "cd $W; git clone -q https://gitlab.example.test/acme/widgets.git gl; cd gl; env GITLAB_HOST=gitlab.example.test GITLAB_TOKEN=lenny-injected glab mr list 2>&1"
step GHREPO-pr-create-nohead "GH_REPO=ghes.example.test/acme/widgets gh pr create --title t --body b 2>&1"
step GHREPO-pr-create-head "GH_REPO=ghes.example.test/acme/widgets gh pr create --title t --body b --head feature-alt-$TAG 2>&1"
step GHREPO-pr-list "GH_REPO=ghes.example.test/acme/widgets gh pr list 2>&1"
step GHREPO-pr-view-noarg "GH_REPO=ghes.example.test/acme/widgets gh pr view --json number,headRefName 2>&1"
step GHREPO-pr-status "GH_REPO=ghes.example.test/acme/widgets gh pr status 2>&1"
step GHREPO-pr-checkout "GH_REPO=ghes.example.test/acme/widgets gh pr checkout 7 2>&1"
git checkout -q feature-alt-$TAG 2>/dev/null
step set-default "gh repo set-default ghes.example.test/acme/widgets 2>&1"
step gh-resolved-config "git config remote.origin.gh-resolved base; gh pr list 2>&1"
git config --unset remote.origin.gh-resolved
step noconfig-wrapper-view "GIT_CONFIG_GLOBAL=/dev/null gh pr view --json number,headRefName 2>&1"
step noconfig-wrapper-create "git checkout -q -b w-$TAG; git push -q -u origin HEAD; GIT_CONFIG_GLOBAL=/dev/null gh pr create --title t --body b 2>&1"
step noconfig-wrapper-checkout "GIT_CONFIG_GLOBAL=/dev/null gh pr checkout 7 2>&1"
git checkout -q feature-alt-$TAG 2>/dev/null
step glab-R "cd $W/gl; env GITLAB_HOST=gitlab.example.test GITLAB_TOKEN=lenny-injected glab mr list -R gitlab.example.test/acme/widgets 2>&1"
step glab-R-create "cd $W/gl; git checkout -q -b gl-$TAG; git commit -q --allow-empty -m e; git push -q -u origin HEAD; env GITLAB_HOST=gitlab.example.test GITLAB_TOKEN=lenny-injected glab mr create -R gitlab.example.test/acme/widgets --title t --description d --source-branch gl-$TAG --target-branch main --yes 2>&1"
# pushInsteadOf alone: fetch stays https, push is loopback
export GIT_CONFIG_GLOBAL=/t/cfg/PIO.gitconfig
step PIO-remote-v "git remote -v"
step PIO-gh-list "gh pr list 2>&1"
step PIO-fetch "git fetch origin 2>&1"
step PIO-push "git push -q origin HEAD 2>&1"
# gh falls back to the push URL when the fetch URL gives no repository: loopback for fetch, the real URL on the push line
export GIT_CONFIG_GLOBAL=/t/cfg/LPI.gitconfig
step LPI-remote-v "git remote -v"
step LPI-gh-list "gh pr list 2>&1"
step LPI-gh-view "gh pr view --json number,headRefName 2>&1"
step LPI-fetch "git fetch origin 2>&1"
step LPI-push "git push -q origin HEAD 2>&1"
# hostname preserved with a port: only what gh makes of the URL
export GIT_CONFIG_GLOBAL=/dev/null
step port-url-gh "cd $W; git init -q z; cd z; git remote add origin http://ghes.example.test:3129/acme/widgets.git; gh pr list 2>&1"
step loop-short-gh "cd $W/z; git remote set-url origin http://127.0.0.1:3129/acme/widgets.git; gh pr list 2>&1"
step loop-short-gh-host "cd $W/z; GH_HOST=127.0.0.1 GH_ENTERPRISE_TOKEN=x gh pr list 2>&1"
cd /; rm -rf $W
