#!/bin/bash
O=spike-owed-d; cd "$O"
./i8.sh > raw/i8-driver.log 2>&1 </dev/null
docker rm -f od-envoy od-dec1 od-dec2 od-ev1 od-ev2 od-mc od-client od-target od-pgb od-pg od-minio >/dev/null 2>&1; docker network rm od-ev >/dev/null 2>&1; docker volume rm od-pgdata od-miniodata >/dev/null 2>&1
./i1d.sh > raw/i1d-driver.log 2>&1 </dev/null
echo done > raw/chain2-done
