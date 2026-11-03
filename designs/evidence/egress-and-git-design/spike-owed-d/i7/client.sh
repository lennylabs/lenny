#!/bin/bash
# client.sh VERS: POSIX default ACLs through a kernel NFS mount
V=$1; S=198.19.70.10
M=/mnt/n; mkdir -p $M
mount -t nfs -o vers=$V,nolock,soft,timeo=30,retrans=2 $S:$2 $M; rc=$?; echo "mount -t nfs vers=$V rc=$rc"; [ $rc = 0 ] || exit 1
grep ' /mnt/n ' /proc/mounts | cut -c1-200
d=$M/s-v$V; rm -rf $d; mkdir -p $d; chmod 2770 $d
echo "--- what the mirror parent does: setxattr of the access and default ACL, through setfacl"
setfacl -m g:63001:rx,d:g:63001:rx,d:g::rwx,d:u::rwx,d:m:rwx,d:o::- $d; echo "setfacl rc=$?"; getfacl -n $d 2>&1 | grep -v '^$' | tr '\n' ' '; echo
getfattr -d -m - $d 2>&1 | cut -c1-200 | head -5
echo "nfs4_getfacl: $(nfs4_getfacl $d 2>&1 | grep -v '^#' | grep -v '^$' | tr '\n' ' ' | cut -c1-300)"
echo "--- the same grant through NFSv4 ACL tools, inheriting"
nfs4_setfacl -a 'A:g:63001:RXtncy' $d 2>&1 | head -2; echo "allow rc=$?"; nfs4_setfacl -a 'A:fdg:63001:RXtncy' $d 2>&1 | head -2; echo "inherit rc=${PIPESTATUS[0]}"; echo "after: $(nfs4_getfacl $d 2>&1 | grep -v '^#' | grep -v '^$' | tr '\n' ' ' | cut -c1-400)"
chown 60000:62001 $d; chmod 2770 $d
echo "--- a fetch under UID 61002, group 62001, creates a directory and a file, a reader under group 63001 reads"
setpriv --reuid=61002 --regid=62001 --clear-groups sh -c "umask 007; mkdir $d/sub; echo x > $d/sub/f; echo y > $d/g; ls -ln $d $d/sub | grep -v total"
echo "new file: $(nfs4_getfacl $d/g 2>&1 | grep -v '^#' | grep -v '^$' | tr '\n' ' ' | cut -c1-300)"
setpriv --reuid=64001 --regid=63001 --clear-groups sh -c "ls $d >/dev/null 2>&1; echo reader-list rc=\$?; cat $d/g >/dev/null 2>&1; echo reader-read rc=\$?; cat $d/sub/f >/dev/null 2>&1; echo reader-read-sub rc=\$?; echo z > $d/evil 2>/dev/null; echo reader-create rc=\$?; echo z >> $d/g 2>/dev/null; echo reader-overwrite rc=\$?"
setpriv --reuid=61003 --regid=62009 --clear-groups sh -c "ls $d >/dev/null 2>&1; echo other-store-list rc=\$?"
echo "--- the parent's order, as a non-root owner: a store with the mode and ACL set before its chown; a hostile setuid plant, and the chown"
setpriv --reuid=61002 --regid=62001 --clear-groups sh -c "echo x > $d/suid; chmod 4755 $d/suid; mkfifo $d/fifo; ln -s /etc/passwd $d/link; ls -ln $d/suid $d/fifo $d/link"
chown 60000 $d/suid; echo "chown of the setuid file as root with CAP_CHOWN: rc=$? mode now $(stat -c %a $d/suid)"
chown -h 60000 $d/link; echo "lchown of the symlink rc=$? target owner $(stat -c %u /etc/passwd)"
