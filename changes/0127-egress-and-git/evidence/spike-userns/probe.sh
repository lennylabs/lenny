#!/bin/sh
# Runs as the mirror parent, under the design's start sequence. Each line is one observation.
caps(){ grep -E '^(CapInh|CapPrm|CapEff|CapBnd|CapAmb|NoNewPrivs)' /proc/self/status | tr -s '\t ' ' ' | tr '\n' ' '; echo; }
try(){ L=$1; shift; if "$@" >/tmp/o 2>&1; then echo "  $L: ALLOWED"; else echo "  $L: refused ($(head -1 /tmp/o | cut -c1-60))"; fi; }
echo "== parent"; id; echo "uid_map: $(cat /proc/self/uid_map | tr -s ' ')"; caps
mkdir -p /data/a /data/b; chmod 2770 /data/a /data/b
try "parent chown store a to owner 65533 group $GA" chown 65533:$GA /data/a
try "parent chown store b to owner 65533 group $GB" chown 65533:$GB /data/b
chmod 2770 /data/a /data/b 2>/dev/null; ls -ld /data/a /data/b | awk '{print "  "$1,$3,$4,$9}'
echo "  secret: $(ls -lL /secret/key | awk '{print $1,$3,$4}')"; try "parent reads the 0440 Secret" cat /secret/key
echo "== fetch child under UID $FU, group $GA, all capabilities cleared"
cat > /tmp/child.sh <<'IN'
caps(){ grep -E '^(CapInh|CapPrm|CapEff|CapBnd|CapAmb|NoNewPrivs)' /proc/self/status | tr -s '\t ' ' ' | tr '\n' ' '; echo; }
try(){ L=$1; shift; if "$@" >/tmp/oc 2>&1; then echo "  $L: ALLOWED"; else echo "  $L: refused ($(head -1 /tmp/oc | cut -c1-60))"; fi; }
id; caps
try "write its own store" sh -c 'echo pack > /data/a/pack1'
try "write another store" sh -c 'echo x > /data/b/evil'
try "list another store" ls /data/b
try "read the mirror's Secret" cat /secret/key
try "write the root filesystem" sh -c 'echo x > /evil'
try "signal the parent" kill -0 $1
try "become root" setpriv --reuid=0 true
try "change a file's owner" chown $FU /data/a/pack1
try "switch to a sibling UID" setpriv --reuid=$((FU+1)) true
ln -s /secret/key /data/a/link 2>/dev/null; echo "  planted a symlink to the Secret"
sleep 300 & 
IN
setpriv --reuid=$FU --regid=$GA --clear-groups --inh-caps=-all --ambient-caps=-all sh /tmp/child.sh $$
echo "== after the child: the sweep (a helper under the fetch UID signals its own UID)"
echo "  under $FU before: $(ps -eo uid=,stat=,args= | awk -v u=$FU '$1==u' | cut -c1-50 | tr '\n' ';')"
setpriv --reuid=$FU --regid=$GA --clear-groups --inh-caps=-all --ambient-caps=-all kill -9 -1 2>/dev/null; sleep 0.5
wait 2>/dev/null; echo "  under $FU after: [$(ps -eo uid=,stat=,args= | awk -v u=$FU '$1==u' | cut -c1-50 | tr '\n' ';')]"; echo "  parent alive: yes"
echo "== re-chown, under the store's group, no symlink following"
try "parent re-chowns the store" setpriv --regid=$GA --groups=$GA --inh-caps=+setuid,+setgid,+chown --ambient-caps=+setuid,+setgid,+chown chown -hR 65533 /data/a
setpriv --regid=$GA --groups=$GA --inh-caps=+setuid,+setgid,+chown --ambient-caps=+setuid,+setgid,+chown ls -ln /data/a | awk 'NR>1{print "  "$1,$3,$4,$9}'; echo "  secret still owned: $(ls -lL /secret/key | awk '{print $3,$4}')"
echo "== what a compromised parent can do"
try "become UID 0" setpriv --reuid=0 true
setpriv --reuid=0 --regid=0 --keep-groups sh -c 'id; grep -E "^(CapEff|CapBnd)" /proc/self/status | tr -s "\t " " " | tr "\n" " "; echo; for t in "read /proc/1/environ" ; do cat /proc/1/environ >/dev/null 2>&1 && echo "  read pid 1 environ: ALLOWED" || echo "  read pid 1 environ: refused"; done; mount -t tmpfs t /tmp 2>/dev/null && echo "  mount: ALLOWED" || echo "  mount: refused"; echo x > /proc/sys/kernel/hostname 2>/dev/null && echo "  sysctl write: ALLOWED" || echo "  sysctl write: refused"; touch /data/rootfile 2>/dev/null && echo "  write volume as root: ALLOWED ($(ls -ln /data/rootfile | cut -d" " -f3))" || echo "  write volume as root: refused"'
try "switch to the runtime's UID 65533" setpriv --reuid=65533 true
echo "== marker for the node view"; setpriv --reuid=$FU --regid=$GA --clear-groups --inh-caps=-all --ambient-caps=-all sleep 601 &
sleep 600
