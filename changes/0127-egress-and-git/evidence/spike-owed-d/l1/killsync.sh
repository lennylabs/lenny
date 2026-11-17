#!/bin/sh
for p in /proc/[0-9]*; do if grep -aq 'sync.py' $p/cmdline 2>/dev/null; then echo "killing ${p#/proc/}"; kill ${p#/proc/}; fi; done
