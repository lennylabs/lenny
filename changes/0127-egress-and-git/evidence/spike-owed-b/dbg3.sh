#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh calico
$K -n agents get pods | head -8; $K -n agents describe pod x0 | tail -8 | cut -c1-170; docker exec ob-calico-worker crictl images | grep -E "ob-tools|pause"; $K -n lenny-system get pods | head
