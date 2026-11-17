#!/bin/bash
kind create cluster --name lenny-grp --wait 90s 2>&1 | tail -1 | cut -c1-40; K="kubectl --context kind-lenny-grp"; kind load docker-image alpine:3.22 --name lenny-grp >/dev/null 2>&1
$K apply -f /tmp/np.yaml >/dev/null; $K -n lenny-system wait --for=condition=Ready pod/group-a --timeout=60s >/dev/null; IP=$($K -n lenny-system get pod group-a -o jsonpath='{.status.podIP}')
cat > /tmp/probe.sh <<IN
# Probes every 50 ms from when /tmp/stamp appears; prints seconds from the stamp to the first success.
while [ ! -e /tmp/stamp ]; do sleep 0.02; done
t0=\$(cut -d' ' -f1 /proc/uptime)
i=0
while [ \$i -lt 400 ]; do
  if nc -w 1 $IP 8080 </dev/null >/dev/null 2>&1; then t1=\$(cut -d' ' -f1 /proc/uptime); echo "\$t0 \$t1" | awk '{printf "%.2f s\n", \$2-\$1}'; exit 0; fi
  i=\$((i+1)); sleep 0.05
done
echo "never within 400 attempts"
IN
for i in 1 2 3 4 5 6 7 8; do
  $K -n agents run c$i --image=alpine:3.22 --image-pull-policy=IfNotPresent --command -- sleep 600 >/dev/null; $K -n agents wait --for=condition=Ready pod/c$i --timeout=60s >/dev/null; /bin/sleep 2
  if $K -n agents exec c$i -- sh -c "nc -w 1 $IP 8080 </dev/null >/dev/null 2>&1"; then B=open; else B=dropped; fi
  $K -n agents cp /tmp/probe.sh c$i:/tmp/probe.sh >/dev/null 2>&1
  $K -n agents exec c$i -- sh /tmp/probe.sh > /tmp/lat$i 2>&1 &
  /bin/sleep 1; $K -n agents label pod c$i lenny.dev/egress-group=group-a >/dev/null; $K -n agents exec c$i -- touch /tmp/stamp; wait
  echo "   sample $i: before stamp=$B, open after $(cat /tmp/lat$i)"
done
echo "--- a pod with the wrong label stays dropped"
$K -n agents run w --image=alpine:3.22 --image-pull-policy=IfNotPresent --labels=lenny.dev/egress-group=group-b --command -- sleep 600 >/dev/null; $K -n agents wait --for=condition=Ready pod/w --timeout=60s >/dev/null; /bin/sleep 2
if $K -n agents exec w -- sh -c "nc -w 1 $IP 8080 </dev/null >/dev/null 2>&1"; then echo "   wrong label: open"; else echo "   wrong label: dropped"; fi
kind delete cluster --name lenny-grp 2>&1 | tail -1 | cut -c1-40
