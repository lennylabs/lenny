#!/bin/bash
R=spike-owed-b; cd $R; . ./kc.sh pki
$K -n agents delete pod --all --wait=false >/dev/null; $K -n lenny-system delete pod --all --wait=false >/dev/null
