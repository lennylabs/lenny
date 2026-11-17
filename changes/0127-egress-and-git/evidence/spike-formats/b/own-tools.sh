. /b/own-prep.sh
U=http://fake.test/t
run() { su -s /bin/sh u -c "export HOME=/home/u $1; $2"; }
adduser -D -u 1000 -h /home/u u 2>/dev/null
for m in home env; do
  if [ $m = env ]; then mv /home/u/.netrc /home/u/.netrc.off; X=" NETRC=/cfg/netrc"; else X=""; fi
  echo "--- curl-own$m";    run "$X" "id -u; curl -s --netrc-optional -o /dev/null -w '%{http_code}\n' $U/curl-own$m/plain"
  echo "--- gnuwget-own$m"; run "$X" "wget -q -O /dev/null $U/gnuwget-own$m/plain; echo exit \$?"
  echo "--- git-own$m";     run "$X" "GIT_TERMINAL_PROMPT=0 git ls-remote $U/git-own$m/repo.git 2>&1; echo exit \$?"
done
