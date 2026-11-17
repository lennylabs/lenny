show(){ ps -eo uid=,stat=,args= | awk -v u=100001 '$1==u' | cut -c1-44 | tr '\n' ';'; echo; }
D="setpriv --reuid=100001 --regid=70001 --clear-groups --inh-caps=-all --ambient-caps=-all"
echo "which kill: $(command -v kill) $(ls -l /usr/bin/kill /bin/kill 2>/dev/null | head -1 | cut -c40-)"
$D sh -c 'sleep 300 & sleep 301 & (sleep 302 &) ; exit 0'
echo "before: $(show)"
echo "-- procps or util-linux kill through setpriv (what my probe ran), errors shown"
$D /usr/bin/kill -9 -1; echo "rc=$?"; sleep 0.3; echo "after tool: $(show)"
echo "-- direct kill(-1) system call through setpriv"
$D /b/sweep; sleep 0.3; echo "after syscall: $(show)"
echo "-- a process forked from a helper that changes UID again cannot escape: try setsid and a double fork"
$D sh -c 'setsid sleep 310 & (setsid sh -c "sleep 311 &") ; exit 0'; sleep 0.2; echo "before: $(show)"
$D /b/sweep; sleep 0.3; echo "after: $(show)"
echo "-- parent untouched: $(ps -o uid=,args= -p $$ | cut -c1-30)"
