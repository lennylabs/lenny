#!/bin/sh
apk add -q git >/dev/null 2>&1
export HOME=/tmp/h; mkdir -p $HOME; cat > /tmp/gitcfg <<'C'
[url "http://127.0.0.1:3129/https/"]
    insteadOf = https://
[url "http://127.0.0.1:3129/http/"]
    insteadOf = http://
[url "http://127.0.0.1:3129/ssh/"]
    insteadOf = ssh://
[url "http://127.0.0.1:3129/scp/"]
    insteadOf = git@
[url "http://127.0.0.1:3129/"]
    insteadOf = http://127.0.0.1:3129/
[user]
    name = a
    email = a@b
C
export GIT_CONFIG_GLOBAL=/tmp/gitcfg HTTPS_PROXY=http://127.0.0.1:9 HTTP_PROXY=http://127.0.0.1:9 GH_PROMPT_DISABLED=1 GLAB_CHECK_UPDATE=false GLAB_SEND_TELEMETRY=false
/g/gh --version | head -1; /g/glab --version | head -1
mkdir /tmp/r /tmp/rl /tmp/re
cd /tmp/r; git init -q -b main; git commit -q --allow-empty -m x; git remote add origin https://github.com/acme/web.git; git checkout -q -b lenny/alice/fix
echo "== gh, github.com, rewrite"; GH_TOKEN=lenny-injected /g/gh pr create --title t --body b --head lenny/alice/fix 2>&1 | head -2
echo "== gh, github.com, no rewrite"; GIT_CONFIG_GLOBAL=/dev/null GH_TOKEN=lenny-injected /g/gh pr create --title t --body b --head lenny/alice/fix 2>&1 | head -2
echo "== gh, GH_REPO, rewrite"; GH_REPO=acme/web GH_TOKEN=lenny-injected /g/gh pr create --title t --body b --head lenny/alice/fix 2>&1 | head -2
cd /tmp/re; git init -q -b main; git commit -q --allow-empty -m x; git remote add origin https://ghes.acme.internal/acme/web.git; git checkout -q -b lenny/alice/fix
echo "== gh, GHES, rewrite"; GH_HOST=ghes.acme.internal GH_ENTERPRISE_TOKEN=lenny-injected /g/gh pr create --title t --body b --head lenny/alice/fix 2>&1 | head -2
echo "== gh, GHES, no rewrite"; GIT_CONFIG_GLOBAL=/dev/null GH_HOST=ghes.acme.internal GH_ENTERPRISE_TOKEN=lenny-injected /g/gh pr create --title t --body b --head lenny/alice/fix 2>&1 | head -2
cd /tmp/rl; git init -q -b main; git commit -q --allow-empty -m x; git remote add origin https://gitlab.initech.internal/initech/web.git; git checkout -q -b lenny/erin/fix
echo "== glab, rewrite"; GITLAB_HOST=gitlab.initech.internal GITLAB_TOKEN=lenny-injected /g/glab mr create --title t --description d --source-branch lenny/erin/fix --target-branch main --yes 2>&1 | head -3
echo "== glab, no rewrite"; GIT_CONFIG_GLOBAL=/dev/null GITLAB_HOST=gitlab.initech.internal GITLAB_TOKEN=lenny-injected /g/glab mr create --title t --description d --source-branch lenny/erin/fix --target-branch main --yes 2>&1 | head -3
