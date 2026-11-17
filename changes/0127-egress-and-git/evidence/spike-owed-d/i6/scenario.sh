#!/bin/bash
# Runs as root holding only CAP_SETUID, CAP_SETGID, and CAP_CHOWN. Stands in for the parent's launch of git, the helper, and the walk.
OWNER=60000; F1=61001; F2=61002; ST=62001; PUB=62002; RG=63001; RU=64001; ALIEN=62009
M=/b/mhelper
P(){ setpriv --reuid=$1 --regid=$2 --clear-groups --inh-caps=-all --ambient-caps=-all "${@:3}"; }
PARENT(){ gid=$1; shift; setpriv --reuid=65532 --regid=$gid --groups=$gid --inh-caps=+chown --ambient-caps=+chown "$@"; }
SWEEP(){ P $1 $2 /b/sweep >/dev/null 2>&1; }
t(){ L=$1; shift; if timeout 6 "$@" >/tmp/o.$$ 2>&1; then echo "    $L: ALLOWED"; else rc=$?; echo "    $L: refused rc=$rc ($(head -1 /tmp/o.$$ | cut -c1-90))"; fi; }
cat /proc/self/status | grep -E '^(CapEff|CapBnd)'; echo "protected_hardlinks=$(cat /proc/sys/fs/protected_hardlinks) protected_fifos=$(cat /proc/sys/fs/protected_fifos) protected_regular=$(cat /proc/sys/fs/protected_regular)"; grep ' /data ' /proc/mounts
mkdir -p /tmp/h; chmod 777 /tmp/h; printf '[safe]\n\tdirectory = *\n[user]\n\tname=a\n\temail=a@b\n' > /tmp/h/.gitconfig; chmod 644 /tmp/h/.gitconfig; export HOME=/tmp/h
SRC=/tmp/src; git init -q $SRC; (cd $SRC; for i in 1 2 3; do head -c 3000 /dev/urandom | base64 > f$i; done; git add .; git commit -q -m one; echo two >> f1; git commit -qam two); chmod -R a+rX $SRC
# mkstore NAME GID DIRMODE: an honest store, filled by a fetch under F2 and walked, with the ACL set before the owner
mkstore(){ S=/data/$1; rm -rf $S; mkdir -p $S; chmod $3 $S; [ "$2" = "$ST" ] && setfacl -m g:$RG:rx,d:g:$RG:rx,d:g::rwx,d:u::rwx,d:m:rwx,d:o::- $S; chown $OWNER:$2 $S
  P $F2 $2 sh -c "git init -q --bare --shared=group $S/repo.git && git -C $S/repo.git fetch -q file://$SRC '+refs/heads/*:refs/heads/*'"; SWEEP $F2 $2; PARENT $2 $M rechown $S $OWNER | sed 's/^/    /' | cut -c1-120; }
mkdir -p /secret; echo secretkey > /secret/key; chmod 440 /secret/key; chgrp 65534 /secret/key; mkdir -p /data; chmod 755 /data
# plant: what a compromised fetch under F1 writes
plant(){ S=$1; R=$S/repo.git; cat <<PL | P $F1 $ST sh
umask 000
cd $S
echo x > suid; chmod 4755 suid; echo x > sgid; chmod 2755 sgid; echo x > sticky-file; chmod 1755 sticky-file
mkdir sticky-dir; chmod 1777 sticky-dir; echo x > sticky-dir/owned-by-f1; chmod 0644 sticky-dir/owned-by-f1
mkdir ww; chmod 0777 ww; echo x > ww/f; chmod 0666 ww/f
mkdir locked; echo x > locked/f; chmod 0600 locked/f; chmod 0000 locked
ln -s /secret/key link-secret; ln -s /data/pub link-otherstore; mkdir -p d/sub; echo x > d/sub/pack; ln -s /secret d/sub/dirlink; ln -s link-loop link-loop
ln -s /data/pub/repo.git/HEAD $R/refs/heads/link-ref 2>/dev/null
ln /secret/key hl-secret 2>/dev/null; echo "    hard link to the Secret: rc=\$?"
ln /data/pub/repo.git/HEAD hl-public 2>/dev/null; echo "    hard link to a public store's file (mode 0664, not ours): rc=\$?"
ln $R/HEAD hl-samestore 2>/dev/null; echo "    hard link to this store's HEAD (owner's file, group-writable): rc=\$?"
echo x > own-file; ln own-file hl-own; echo "    hard link to a file of its own: rc=\$?"
mknod chardev c 1 3 2>/dev/null; echo "    mknod character device: rc=\$?"; mknod blockdev b 7 0 2>/dev/null; echo "    mknod block device: rc=\$?"
mkfifo fifo; echo "    mkfifo: rc=\$?"
python3 -c 1 2>/dev/null
mv $R/packed-refs $R/packed-refs.moved 2>/dev/null; mkfifo $R/packed-refs; echo "    FIFO at packed-refs: rc=\$?"
touch -- "name with spaces" "-rf" "\$(printf 'new\nline')"
echo x > aclplant; setfacl -m g:$RG:rwx aclplant; mkdir acldir; setfacl -d -m g:$RG:rwx acldir; setfacl -m g:$ALIEN:rwx acldir
echo x > xattr-file; setfattr -n user.evil -v 1 xattr-file; echo "    user xattr: rc=\$?"; setfattr -n trusted.evil -v 1 xattr-file 2>/dev/null; echo "    trusted xattr: rc=\$?"; setfattr -n security.capability -v 0sAQAAAgAgAAAAAAAAAAAAAAAAAAA= suid 2>/dev/null; echo "    file capability on the setuid file: rc=\$?"
chattr +i xattr-file 2>/dev/null; echo "    chattr +i: rc=\$?"
exit 0
PL
}
state(){ S=$1; PARENT $ST sh -c "ls -lnAR $S 2>&1 | grep -vE 'objects/|^total|^$|\.sample' | head -${2:-46}"; }
probes(){ S=$1; R=$S/repo.git; echo "  -- a later reader under its group"; P $RU $RG sh -c "cd /; t(){ L=\$1; shift; if timeout 6 \"\$@\" >/tmp/o.\$\$ 2>&1; then echo \"    \$L: ALLOWED\"; else rc=\$?; echo \"    \$L: refused rc=\$rc (\$(head -1 /tmp/o.\$\$ | cut -c1-90))\"; fi; }
t 'write a planted 0666 file' sh -c 'echo y >> $S/ww/f'
t 'create in a planted 0777 directory' sh -c 'echo y > $S/ww/new'
t 'write the file with a planted ACL' sh -c 'echo y >> $S/aclplant'
t 'create in the directory with a planted ACL' sh -c 'echo y > $S/acldir/new'
t 'read through link-secret' cat $S/link-secret
t 'git ls-remote (a FIFO at packed-refs hangs it)' git ls-remote $R
t 'git clone' git clone -q $R /tmp/rc.\$\$
t 'run the planted setuid file' test -u $S/suid
"; echo "  -- a later fetch under another UID in the same store"; P $F2 $ST sh -c "cd /; t(){ L=\$1; shift; if timeout 8 \"\$@\" >/tmp/o.\$\$ 2>&1; then echo \"    \$L: ALLOWED\"; else rc=\$?; echo \"    \$L: refused rc=\$rc (\$(head -1 /tmp/o.\$\$ | cut -c1-90))\"; fi; }
t 'remove a file in a planted sticky directory' rm -f $S/sticky-dir/owned-by-f1
t 'git fetch (a FIFO at packed-refs hangs it)' git -C $R fetch -q file://$SRC '+refs/heads/*:refs/heads/*'
"; echo "  -- a fetch for another store, with another group"; P $F2 $PUB sh -c "cd /; t(){ L=\$1; shift; if timeout 6 \"\$@\" >/tmp/o.\$\$ 2>&1; then echo \"    \$L: ALLOWED\"; else rc=\$?; echo \"    \$L: refused rc=\$rc (\$(head -1 /tmp/o.\$\$ | cut -c1-90))\"; fi; }
t 'write a planted 0666 file' sh -c 'echo y >> $S/ww/f'
"; }
echo "### public store used as a neighbour"; mkstore pub $PUB 2775
echo "### A. A compromised fetch, no helper: the walk alone"; mkstore a $ST 2770; plant /data/a; SWEEP $F1 $ST
PARENT $ST $M rechown /data/a $OWNER | cut -c1-700; state /data/a; probes /data/a
echo "### B. The same plants, then the sweep, the helper under the fetch UID, and the walk"; mkstore b $ST 2770; plant /data/b; SWEEP $F1 $ST
P $F1 $ST $M normalize /data/b -dir 2770 -file 0660 -reader $RG -hardlink keep | cut -c1-900
PARENT $ST $M rechown /data/b $OWNER | cut -c1-500; state /data/b; probes /data/b
echo "  -- honest work after normalization"; rm -f /tmp/rc2; P $F2 $ST sh -c "git -C /data/b/repo.git fetch -q file://$SRC '+refs/heads/*:refs/heads/*'; echo fetch rc=\$?; git -C /data/b/repo.git fsck 2>&1 | tail -1"; P $RU $RG sh -c "git clone -q /data/b/repo.git /tmp/rc2.\$\$; echo clone rc=\$?; git -C /tmp/rc2.\$\$ log --oneline | wc -l"
PARENT $ST sh -c "getfacl -n /data/b/d 2>/dev/null | grep -v '^$' | tr '\n' ' '; echo; f=\$(find /data/b/repo.git/objects -type f | head -1); ls -ln \$f; getfacl -n \$f 2>/dev/null | grep -v '^$' | tr '\n' ' '; echo"
echo "### C. A helper that removes setuid and setgid only (the earlier spike's find command): what stays"; mkstore c $ST 2770; plant /data/c; SWEEP $F1 $ST
P $F1 $ST sh -c "find /data/c -xdev -uid $F1 ! -type d -perm /6000 -exec chmod ug-s {} + ; echo strip rc=\$?"; PARENT $ST $M rechown /data/c $OWNER | cut -c1-300; probes /data/c | grep -E 'ALLOWED|rc=124' 
echo "### D. Order: a descendant left running replants unless the sweep precedes the helper"; mkstore d $ST 2770
P $F1 $ST sh -c "(setsid sh -c 'while :; do chmod 4755 /data/d/replant 2>/dev/null || { echo x > /data/d/replant; chmod 4755 /data/d/replant; }; sleep 0.05; done' &) ; exit 0"; /bin/sleep 0.4
P $F1 $ST $M normalize /data/d -dir 2770 -file 0660 -reader $RG | cut -c1-160; /bin/sleep 0.4; echo "  helper before the sweep: replant mode now $(PARENT $ST stat -c '%a %u' /data/d/replant)"; SWEEP $F1 $ST; /bin/sleep 0.2; P $F1 $ST $M normalize /data/d -dir 2770 -file 0660 -reader $RG | cut -c1-160; /bin/sleep 0.3; echo "  sweep, then helper: replant mode now $(PARENT $ST stat -c '%a %u' /data/d/replant)"; PARENT $ST $M rechown /data/d $OWNER | cut -c1-200
echo "### E. A planted config and hook: content rather than modes"; mkstore e $ST 2770
P $F1 $ST sh -c "git config --file /data/e/repo.git/config core.fsmonitor 'touch /tmp/fsm-ran-\$(id -u); false'; git config --file /data/e/repo.git/config core.sshCommand 'touch /tmp/ssh-ran-\$(id -u)'; git config --file /data/e/repo.git/config core.hooksPath /data/e/hooks; git config --file /data/e/repo.git/config uploadpack.packObjectsHook 'touch /tmp/pack-hook-ran-\$(id -u); exec git'; mkdir -p /data/e/hooks; printf '#!/bin/sh\ntouch /tmp/hook-ran-\$(id -u)\n' > /data/e/hooks/post-fetch; chmod 755 /data/e/hooks/post-fetch; printf '#!/bin/sh\ntouch /tmp/uphook-ran-\$(id -u)\n' > /data/e/repo.git/hooks/post-update; chmod 755 /data/e/repo.git/hooks/post-update; chmod 777 /data/e/repo.git/config 2>/dev/null; echo planted"; SWEEP $F1 $ST; P $F1 $ST $M normalize /data/e -dir 2770 -file 0660 -reader $RG | cut -c1-140; PARENT $ST $M rechown /data/e $OWNER | cut -c1-120; rm -f /tmp/*-ran-*
P $RU $RG sh -c "git -c safe.directory='*' upload-pack --advertise-refs /data/e/repo.git >/dev/null 2>&1; echo reader upload-pack rc=\$?; git clone -q /data/e/repo.git /tmp/rc3.\$\$ 2>&1 | tail -1"; P $F2 $ST sh -c "git -C /data/e/repo.git -c core.hooksPath=/dev/null fetch -q file://$SRC '+refs/heads/*:refs/heads/*'; echo fetch rc=\$?"; echo "  executed by later runs: $(ls /tmp/*-ran-* 2>/dev/null | tr '\n' ' ')"; PARENT $ST grep -E 'fsmonitor|sshCommand|hooksPath|packObjects' /data/e/repo.git/config | tr '\n' ';'; echo
echo "### F. Helper cost on a large store: 100,000 files and 1,000 directories, all owned by the fetch UID, then all owned by the parent"; mkdir -p /data/big; chmod 2770 /data/big; chown $OWNER:$ST /data/big
P $F1 $ST sh -c 'umask 007; cd /data/big; for d in $(seq 1 1000); do mkdir d$d; done; for d in $(seq 1 1000); do (cd d$d; i=0; while [ $i -lt 100 ]; do : > f$i; i=$((i+1)); done); done'; P $F1 $ST $M normalize /data/big -dir 2770 -file 0660 -reader $RG | cut -c1-260; PARENT $ST $M rechown /data/big $OWNER | cut -c1-160; P $F1 $ST $M normalize /data/big -dir 2770 -file 0660 -reader $RG | cut -c1-260; P $F1 $ST $M normalize /data/big -dir 2770 -file 0660 | cut -c1-260; rm -rf /data/big
echo "### G. A public store: no ACL, mode 2775, files 0664"
pubplant(){ S=$1; cat <<PL | P $F1 $PUB sh
umask 000
cd $S
mkdir ww; chmod 0777 ww; echo x > ww/f; chmod 0666 ww/f; echo x > w0666; chmod 0666 w0666; echo x > suid; chmod 4755 suid; ln -s /data/a link; mkfifo $S/repo.git/packed-refs.fifo; mv $S/repo.git/packed-refs $S/repo.git/packed-refs.old 2>/dev/null; mv $S/repo.git/packed-refs.fifo $S/repo.git/packed-refs
exit 0
PL
}
pubprobe(){ S=$1; P $RU 64999 sh -c "cd /; t(){ L=\$1; shift; if timeout 6 \"\$@\" >/tmp/o.\$\$ 2>&1; then echo \"    \$L: ALLOWED\"; else rc=\$?; echo \"    \$L: refused rc=\$rc (\$(head -1 /tmp/o.\$\$ | cut -c1-80))\"; fi; }; echo '  reader with no group:'; t 'write a planted 0666 file' sh -c 'echo y >> $S/w0666'; t 'create in a planted 0777 directory' sh -c 'echo y > $S/ww/new'; t 'git ls-remote' git ls-remote $S/repo.git"; P $F2 $ST sh -c "cd /; t(){ L=\$1; shift; if timeout 6 \"\$@\" >/tmp/o.\$\$ 2>&1; then echo \"    \$L: ALLOWED\"; else rc=\$?; echo \"    \$L: refused rc=\$rc (\$(head -1 /tmp/o.\$\$ | cut -c1-80))\"; fi; }; echo '  a fetch for another store:'; t 'write a planted 0666 file' sh -c 'echo y >> $S/w0666'; t 'create in a planted 0777 directory' sh -c 'echo y > $S/ww/new'"; }
mkstore pa $PUB 2775; pubplant /data/pa; SWEEP $F1 $PUB; PARENT $PUB $M rechown /data/pa $OWNER | cut -c1-200; echo "--- no helper"; pubprobe /data/pa
mkstore pb $PUB 2775; pubplant /data/pb; SWEEP $F1 $PUB; P $F1 $PUB $M normalize /data/pb -dir 2775 -file 0664 | cut -c1-260; PARENT $PUB $M rechown /data/pb $OWNER | cut -c1-200; echo "--- helper, mode 2775 and 0664, no reader ACL"; pubprobe /data/pb
echo "### H. A hard link to the store's own HEAD: removing any name with more than one link can remove the real one"
for pol in remove keep; do for run in 1 2 3 4; do mkstore h$pol$run $ST 2770; P $F1 $ST sh -c "for i in 1 2 3 4 5 6 7 8; do ln /data/h$pol$run/repo.git/HEAD /data/h$pol$run/repo.git/hl\$i; done"; SWEEP $F1 $ST; x=$(P $F1 $ST $M normalize /data/h$pol$run -dir 2770 -file 0660 -reader $RG -hardlink $pol | grep -o 'hard-links-seen=[0-9]*'); PARENT $ST $M rechown /data/h$pol$run $OWNER >/dev/null; r=$(P $RU $RG sh -c "cd /; timeout 6 git ls-remote /data/h$pol$run/repo.git 2>&1 | head -1 | cut -c1-70"); echo "  policy=$pol run=$run $x HEAD present: $(PARENT $ST sh -c "test -f /data/h$pol$run/repo.git/HEAD && echo yes || echo no") ls-remote: $r"; done; done
echo "### end"
