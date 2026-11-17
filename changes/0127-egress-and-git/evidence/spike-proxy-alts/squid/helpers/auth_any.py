#!/usr/bin/env python3
# Basic auth helper: accepts any user/password (the lease is validated by the external ACL).
import sys
for line in sys.stdin:
    sys.stdout.write("OK\n"); sys.stdout.flush()
