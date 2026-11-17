#!/bin/bash
shutdown -h +330 "aq self-termination"
echo "$(date -u) scheduled shutdown in 330 min" > /var/tmp/aq-ud
