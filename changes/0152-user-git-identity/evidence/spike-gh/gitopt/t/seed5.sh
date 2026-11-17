#!/bin/sh
# seed5.sh: repositories for the LFS forms.
export HOME=/tmp/s5; mkdir -p $HOME; git config --global user.email a@b; git config --global user.name a; git config --global init.defaultBranch main; git config --global safe.directory '*'
for r in lfsgl lfsother lfsothera lfsvar lfsexp lfsevil; do d=/repos/acme/$r.git; rm -rf $d; git init -q --bare $d; w=$(mktemp -d); cd $w; git init -q; echo s > s; git add s; git commit -qm c; git push -q $d main; cd /; rm -rf $w; git -C $d config http.receivepack true; done
