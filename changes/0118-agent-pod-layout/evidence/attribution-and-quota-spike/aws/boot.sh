#!/bin/bash
# boot.sh BUCKET: node setup and the Kata handlers
B=$1
aws s3 cp s3://$B/scripts/node-setup.sh /root/node-setup.sh
aws s3 cp s3://$B/scripts/kata-config.sh /root/kata-config.sh
bash /root/node-setup.sh $B
bash /root/kata-config.sh >> /var/log/aq-setup.log 2>&1
echo "boot done $(date -u +%FT%TZ)" >> /var/log/aq-setup.log
touch /var/tmp/aq-boot-done
