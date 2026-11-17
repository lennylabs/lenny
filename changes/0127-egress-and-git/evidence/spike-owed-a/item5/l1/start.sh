#!/bin/bash
# Squid's first render, then the sync job in the background.
V=$1; S=$2; D=/run/l1; mkdir -p $D
until python3 -c "import urllib.request,json;json.dump(json.load(urllib.request.urlopen('http://198.19.50.51:8080/doc',timeout=2)),open('$D/p.json','w'))"; do sleep 0.5; done
python3 /l1/render5.py $D/p.json $D $V || exit 1
chmod -R a+rwX $D
touch /tmp/access.log; chmod a+rw /tmp/access.log
python3 /l1/sync.py http://198.19.50.51:8080/doc $D $V $S > /tmp/sync.log 2>&1 &
exec squid -N -f $D/squid.conf
