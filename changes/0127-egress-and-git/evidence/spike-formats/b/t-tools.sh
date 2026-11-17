. /b/prelude.sh
echo "user=$(id -un) HOME=$HOME"; curl --version | head -1; wget --version | head -1; busybox | head -1; git --version
for v in $VARIANTS; do setup $v
  r curl-noflag-$v;   curl -s -o /dev/null -w '%{http_code}\n' $U/curl-noflag-$v/plain
  r curl-netrc-$v;    curl -s --netrc -o /dev/null -w '%{http_code}\n' $U/curl-netrc-$v/plain
  r curl-netrcopt-$v; curl -s --netrc-optional -o /dev/null -w '%{http_code}\n' $U/curl-netrcopt-$v/plain
  r curl-netrcfile-$v; curl -s --netrc-file /cfg/netrc -o /dev/null -w '%{http_code}\n' $U/curl-netrcfile-$v/plain
  r bbwget-$v;  busybox wget -q -O /dev/null $U/bbwget-$v/plain; echo "exit $?"
  r gnuwget-$v; wget -q -O /dev/null $U/gnuwget-$v/plain; echo "exit $?"
  r git-$v; GIT_TERMINAL_PROMPT=0 git ls-remote $U/git-$v/repo.git 2>&1; echo "exit $?"
done
