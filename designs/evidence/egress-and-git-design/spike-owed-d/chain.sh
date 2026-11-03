#!/bin/bash
O=spike-owed-d; cd "$O"
until [ -f raw/i1b-done ]; do /bin/sleep 5; done
./i1c.sh > raw/i1c-driver.log 2>&1 </dev/null
./i3e.sh > raw/i3e-driver.log 2>&1 </dev/null
. ./lib-b.sh; b_up > raw/b-up.log 2>&1 </dev/null; docker exec od-pg psql -q -tA -U postgres -c "select version()" >> raw/b-up.log
./i4a.sh > raw/i4a-driver.log 2>&1 </dev/null
./i4b.sh > raw/i4b-driver.log 2>&1 </dev/null
./i5.sh > raw/i5-driver.log 2>&1 </dev/null
echo done > raw/chain-done
