#!/bin/sh
# t1.sh <config> <tag>: git operations through one candidate. Runs inside a client container as the agent UID.
. /t/env.sh; . /t/lib.sh
CFG=$1; TAG=$2; H=${3:-ghes.example.test}
export GIT_CONFIG_GLOBAL=/t/cfg/$CFG.gitconfig
[ "$CAINFO" = design ] && export GIT_SSL_CAINFO=/s/bundle.pem
W=$(mktemp -d); cd $W; B=lenny/$TAG-$(date +%s)
git --version; ldd $(git --exec-path)/git-remote-https | grep -E 'gnutls|ssl\.so|libcurl' | cut -c1-70
U=https://$H/acme
step ls-remote "git ls-remote $U/widgets.git | head -2"
step clone "git clone -q $U/widgets.git c && git -C c log --oneline | wc -l"
step remote-v "git -C c remote -v"
cd c; git checkout -q -b $B; echo $TAG > t; git add t; git commit -qm t; cd ..
step push "git -C c push -q origin HEAD 2>&1"
step fetch "git -C c fetch origin 2>&1 && git -C c rev-parse --abbrev-ref origin/$B"
step shallow "git clone -q --depth 1 $U/widgets.git s && git -C s log --oneline | wc -l"
step partial "git clone -q --filter=blob:none --no-checkout $U/widgets.git p && git -C p cat-file --batch-check --batch-all-objects 2>/dev/null | grep -c blob; git -C p checkout -q main && test -s p/blob && echo checked-out"
step push-main-refused "git -C c push -q origin HEAD:refs/heads/main 2>&1"
printf '#!/bin/sh\necho "askpass: $1" >> /tmp/askpass.log\necho AGENT-OWN-TOKEN\n' > /tmp/askpass.sh; chmod +x /tmp/askpass.sh; rm -f /tmp/askpass.log
step normal-no-prompt "GIT_ASKPASS=/tmp/askpass.sh git ls-remote $U/widgets.git | wc -l; printf 'askpass calls: '; cat /tmp/askpass.log 2>/dev/null | wc -l"
rm -f /tmp/askpass.log
step prompt-401-covered "GIT_ASKPASS=/tmp/askpass.sh git ls-remote $U/needauth.git 2>&1 | tail -1; printf 'askpass calls: '; cat /tmp/askpass.log 2>/dev/null | wc -l"
rm -f /tmp/askpass.log
step prompt-401-unbound "GIT_ASKPASS=/tmp/askpass.sh git ls-remote https://other.example.test/pub/needauth.git 2>&1 | tail -1; printf 'askpass calls: '; cat /tmp/askpass.log 2>/dev/null | wc -l"
step redirect-in-scope "git ls-remote $U/moved.git 2>&1 | wc -l; git ls-remote $U/moved.git >/dev/null 2>&1"
step redirect-out-of-scope "git ls-remote $U/evilredir.git 2>&1"
step clone-outside "git clone -q $U/secret.git x 2>&1"
git clone -q --single-branch -b main $U/big.git b 2>/dev/null; cd b; git config user.email a@b; git config user.name a; git checkout -q -b $B-big 2>/dev/null; head -c 67108864 /dev/urandom > n; git add n; git commit -qm n; cd ..
step push-64mib-default "git -C b push -q origin HEAD 2>&1"
cd b; git checkout -q -b $B-big11; head -c 67108864 /dev/urandom > n; git add n; git commit -qm n2; cd ..
step push-64mib-chunked-h1 "git -c http.version=HTTP/1.1 -C b push -q origin HEAD 2>&1"
step lfs-push "git clone -q $U/lfsrepo.git l && cd l && git lfs install --local >/dev/null && git lfs track '*.bin' >/dev/null && head -c 3000000 /dev/urandom > o.bin && git add .gitattributes o.bin && git commit -qm lfs && git checkout -q -b $B-lfs && git push -q origin HEAD 2>&1"
step lfs-fetch "git clone -q -b $B-lfs $U/lfsrepo.git l2 2>&1 && ls -la l2/o.bin | cut -c20-60 && git -C l2 lfs ls-files | cut -c1-60; test \"\$(head -c 7 l2/o.bin)\" != version"
step submodules "git clone -q --recurse-submodules $U/super.git u 2>&1; r=\$?; for s in abs rel other; do printf '%s ' \$s; git -C u/\$s rev-parse --show-toplevel 2>/dev/null | grep -c /\$s\$; done; exit \$r"
cd /; rm -rf $W
