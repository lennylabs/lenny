#!/bin/sh
# seed6.sh: LFS repositories with a pointer on main, so a clone smudges.
export HOME=/tmp/s6; mkdir -p $HOME; git config --global user.email a@b; git config --global user.name a; git config --global init.defaultBranch main; git config --global safe.directory '*'
O=ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb
for r in lfsgl lfsother lfsothera lfsvar lfsexp lfsevil lfsfail lfsfail401; do d=/repos/acme/$r.git; rm -rf $d; git init -q --bare $d; w=$(mktemp -d); cd $w; git init -q; echo s > s; echo '*.bin filter=lfs diff=lfs merge=lfs -text' > .gitattributes; printf 'version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize 5\n' $O > o.bin; git add .; git commit -qm c; git push -q $d main; cd /; rm -rf $w; git -C $d config http.receivepack true; done
