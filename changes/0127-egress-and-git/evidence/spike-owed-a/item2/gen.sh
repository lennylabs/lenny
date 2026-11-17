#!/bin/bash
D=$(cd "$(dirname "$0")" && pwd); S=$D/../..
G=$S/spike-lsrefs/gen5.py; T=$S/spike-tls/tlsify.py
cd $D
python3 $G envoy/final.json authz=198.19.50.11:9000 && python3 $T envoy/final.json envoy/final-tls.json
python3 $G envoy/sit10.json authz=198.19.50.11:9000 sit=10s
python3 $G envoy/msd5.json authz=198.19.50.11:9000 msd=5s
