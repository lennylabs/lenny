#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh calico
/opt/homebrew/bin/kind load docker-image ob-tools:3 --name ob-calico 2>&1 | tail -1 | cut -c1-100
$K delete ns agents lenny-system --wait=true >/dev/null
./s3-run.sh calico
/opt/homebrew/bin/kind delete cluster --name ob-calico --kubeconfig $R/kc-calico 2>&1 | tail -1 | cut -c1-90
