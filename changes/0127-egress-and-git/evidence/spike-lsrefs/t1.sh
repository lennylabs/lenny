#!/bin/bash
# For each real public repo: truth from git ls-remote, then the Go client direct and through Envoy, v2 then forced v0.
C="docker exec ls-client /b/lsrefs -mode one -timeout 10s"
while read -r url ref; do
  [ -z "$url" ] && continue
  truth=$(perl -e 'alarm 20; exec @ARGV' env GIT_TERMINAL_PROMPT=0 GCM_INTERACTIVE=never git -c credential.helper= ls-remote "$url" "refs/heads/$ref" "refs/tags/$ref" "refs/tags/$ref^{}" 2>&1 </dev/null | tail -1 | cut -c1-40)
  echo "== $url $ref  truth=$truth"
  echo "  direct   $($C -repo "$url" -ref "$ref" -proxy '' )"
  echo "  envoy    $($C -repo "$url" -ref "$ref" -proxy http://198.19.0.41:3128)"
  echo "  envoy v0 $($C -repo "$url" -ref "$ref" -proxy http://198.19.0.41:3128 -v0)"
done <<LIST
https://github.com/git/git master
https://github.com/git/git v2.49.0
https://github.com/kubernetes/kubernetes master
https://github.com/kubernetes/kubernetes v1.30.0
https://github.com/git/git no-such-ref-xyz
https://github.com/lennylabs-does-not-exist/nope master
https://gitlab.com/gitlab-org/gitlab-runner main
https://codeberg.org/forgejo/forgejo forgejo
https://gitea.com/gitea/tea main
https://bitbucket.org/atlassian/aui master
https://bitbucket.org/snakeyaml/snakeyaml master
https://git.kernel.org/pub/scm/git/git.git master
https://git.sr.ht/~sircmpwn/scdoc master
https://dev.azure.com/dnceng-public/public/_git/public main
LIST
