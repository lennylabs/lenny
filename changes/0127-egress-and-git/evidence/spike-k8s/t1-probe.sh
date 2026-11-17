echo "-- whoami: uid=$(id -u) gid=$(id -g) groups=$(id -G) CapEff=$(grep CapEff /proc/self/status | awk '{print $2}')"
echo "-- ls /var/run/secrets:"; ls -la /var/run/secrets 2>&1
echo "-- token-ish files: $(find / -xdev \( -name token -o -name 'ca.crt' \) 2>/dev/null | grep -v '^/proc' | tr '\n' ' ')"
echo "-- mounts mentioning secrets:"; grep -E 'secrets|lenny' /proc/self/mounts || echo "(none)"
echo "-- processes visible:"; ps -o pid,user,args | head -20
NP=$(ps -o pid,args | grep 'lenny-egress' | grep -v grep | awk '{print $1}' | head -1)
echo "-- netagent pid in this ns: ${NP:-<not visible>}"
if [ -n "$NP" ]; then
  echo "-- cat /proc/$NP/root/var/run/secrets/lenny-egress/token:"; cat /proc/$NP/root/var/run/secrets/lenny-egress/token 2>&1 | cut -c1-40
  echo "-- ls /proc/$NP/root/:"; ls /proc/$NP/root/ 2>&1 | head -3
  echo "-- cat /proc/$NP/environ:"; tr '\0' '\n' < /proc/$NP/environ 2>&1 | grep -E 'SECRET|denied' ; cat /proc/$NP/environ >/dev/null 2>&1 || echo "environ: $(cat /proc/$NP/environ 2>&1 | head -c 80)"
  echo "-- cat /proc/$NP/cmdline:"; tr '\0' ' ' < /proc/$NP/cmdline 2>&1 | cut -c1-60; echo
  echo "-- ls /proc/$NP/fd:"; ls /proc/$NP/fd 2>&1 | head -3
  echo "-- /proc/$NP/mountinfo lines with lenny:"; grep lenny /proc/$NP/mountinfo 2>&1 | head -2
fi
