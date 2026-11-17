set -u
apk add --no-cache git >/dev/null 2>&1 || true
export HOME=/tmp/h GH_TOKEN=lenny-injected GIT_TERMINAL_PROMPT=0 NO_COLOR=1 GLAB_CHECK_UPDATE=false GLAB_SEND_TELEMETRY=false
mkdir -p $HOME; cd /tmp; git init -q r; cd r
git config --global user.email a@acme.test; git config --global user.name a
git commit -q --allow-empty -m x
git checkout -q -b feature
git remote add origin https://github.com/acme/web.git
run(){ echo "--- $*"; timeout 8 "$@" 2>&1 | head -3 | cut -c1-140; }
echo "=== plain remote"; git remote -v | head -1
run gh pr create --head feature --title t --body b
cat >> $HOME/.gitconfig <<'G'
[url "http://127.0.0.1:3129/https/"]
    insteadOf = https://
G
echo "=== rewritten"; git remote -v | head -1
run gh pr create --head feature --title t --body b
GH_REPO=acme/web run gh pr create --head feature --title t --body b
run gh pr create --repo acme/web --head feature --title t --body b
run gh pr view 1
echo "=== ghes"; git remote set-url origin https://ghes.acme.test/acme/web.git; export GH_HOST=ghes.acme.test GH_ENTERPRISE_TOKEN=lenny-injected
run gh pr create --head feature --title t --body b
echo "=== glab"; git remote set-url origin https://gitlab.acme.test/acme/web.git; export GITLAB_HOST=gitlab.acme.test GITLAB_TOKEN=lenny-injected
run glab mr create --source-branch feature --target-branch main --title t --description d --yes
run glab mr create -R acme/web --source-branch feature --target-branch main --title t --description d --yes
