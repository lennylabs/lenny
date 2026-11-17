#!/bin/sh
# Find a usable public repository per host: prints host url tipsha.
export HTTPS_PROXY=http://wc-proxy:3128 GIT_SSL_CAINFO=/out/ca.pem GIT_TERMINAL_PROMPT=0
while read name url; do
  tip=$(timeout 30 git ls-remote "$url" HEAD 2>/tmp/err | awk '{print $1}')
  echo "$name $url ${tip:-FAIL:$(tr '\n' ' ' </tmp/err | cut -c1-160)}"
done <<LIST
github https://github.com/golang/example
gitlab https://gitlab.com/gitlab-org/gitlab-shell.git
bitbucket https://bitbucket.org/tildeslash/monit.git
codeberg https://codeberg.org/Codeberg/Documentation.git
gitea https://gitea.com/gitea/tea.git
sourcehut https://git.sr.ht/~sircmpwn/scdoc
kernelorg https://git.kernel.org/pub/scm/utils/dtc/dtc.git
azure1 https://dev.azure.com/dnceng-public/public/_git/dotnet-arcade-validation
azure2 https://dev.azure.com/ms/calculator/_git/calculator
LIST
