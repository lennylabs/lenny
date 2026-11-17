#!/bin/bash
E=http://198.19.0.41:3128; EB=http://198.19.0.42:3128
L="docker exec ls-client /b/lsrefs"
ONE="$L -mode one -insecure -proxy $E"
G=https://git.spike
ctl(){ docker exec ls-client wget -qO- "http://198.19.0.$1:9002/$2"; }
echo "### GitLab with .git"
echo "  $($L -mode one -proxy $E -repo https://gitlab.com/gitlab-org/gitlab-runner.git -ref main)"
echo "  truth $(perl -e 'alarm 20; exec @ARGV' env GIT_TERMINAL_PROMPT=0 git ls-remote https://gitlab.com/gitlab-org/gitlab-runner.git refs/heads/main </dev/null | cut -c1-40)"
echo "  (GitLab advert size v0) $($L -mode one -proxy $E -v0 -repo https://gitlab.com/gitlab-org/gitlab-runner.git -ref main)"
echo "### hostile hosts"
for r in fast big slow hang redirect malformed oversize busy; do echo "  $r: $($ONE -repo $G/$r.git)"; done
echo "  v0only: $($ONE -repo $G/v0only.git)"
echo "  v0big (match sorts early): $($ONE -repo $G/v0big.git)"
echo "  v0late (match after 20 MiB): $($ONE -repo $G/v0late.git)"
echo "### classification on the egress path"
echo "  decision denies: $($ONE -repo $G/fast.git -tenant denied)"
echo "  bad token: $($ONE -repo $G/fast.git -token bad)"
echo "  name does not resolve: $($ONE -repo https://no-such-host.invalid/x.git)"
echo "### tunnel reuse across tenants (20 calls alternating acme and globex)"
for k in true false; do
  ctl 11 'tenants?reset=1' >/dev/null; ctl 12 'tenants?reset=1' >/dev/null; ctl 11 tenants >/dev/null
  $L -mode tenants -insecure -proxy $E -repo $G/fast.git -n 20 -keepalive=$k
  echo "  keepalive=$k decisions seen: authz1 $(ctl 11 tenants) authz2 $(ctl 12 tenants)"
done
echo "### latency, one tunnel per call, local git host"
echo "  direct:    $($L -mode bench -insecure -proxy '' -repo $G/fast.git -n 500 -c 1 -tenant acme)"
echo "  via Envoy: $($L -mode bench -insecure -proxy $E -repo $G/fast.git -n 500 -c 1)"
echo "### burst, via two Envoys"
echo "  c=50:  $($L -mode bench -insecure -proxy $E,$EB -repo $G/fast.git -n 3000 -c 50)"
echo "  c=200: $($L -mode bench -insecure -proxy $E,$EB -repo $G/fast.git -n 6000 -c 200)"
echo "### real host through Envoy, 10 sequential calls each"
for u in "https://github.com/git/git master" "https://bitbucket.org/atlassian/aui master" "https://gitlab.com/gitlab-org/gitlab-runner.git main"; do set -- $u
  echo "  $1 direct: $($L -mode bench -proxy '' -repo $1 -ref $2 -n 10)"
  echo "  $1 envoy:  $($L -mode bench -proxy $E -repo $1 -ref $2 -n 10)"
done
