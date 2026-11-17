#!/bin/bash
# cleanup.sh: terminate every run instance, delete the bucket, the IAM role and instance profile, and the security group,
# then list by tag to prove nothing remains
. "$(dirname "$0")/lib.sh"
log "=== cleanup"
IIDS=$(aws ec2 describe-instances --filters Name=tag:run,Values=attrib-quota-2026-11-13 Name=instance-state-name,Values=pending,running,stopping,stopped,shutting-down --query 'Reservations[].Instances[].InstanceId' --output text)
for i in $IIDS; do [ "$i" = "$FORBIDDEN" ] && { log "skipping forbidden $i"; continue; }; aws ec2 terminate-instances --instance-ids $i >/dev/null && log "terminate $i"; done
for i in $IIDS; do [ "$i" = "$FORBIDDEN" ] || aws ec2 wait instance-terminated --instance-ids $i; done
aws s3 rb s3://$BUCKET --force >/dev/null && log "bucket $BUCKET deleted"
aws iam remove-role-from-instance-profile --instance-profile-name $PROFILE_NAME --role-name $ROLE
aws iam delete-instance-profile --instance-profile-name $PROFILE_NAME && log "instance profile deleted"
aws iam detach-role-policy --role-name $ROLE --policy-arn arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore
aws iam delete-role-policy --role-name $ROLE --policy-name sikp-bucket
aws iam delete-role --role-name $ROLE && log "role deleted"
for t in 1 2 3 4 5 6; do aws ec2 delete-security-group --group-id $SG_ID 2>/dev/null && { log "sg $SG_ID deleted"; break; }; sleep 20; done
echo "== verification"
echo "instances (non-terminated) tagged run=attrib-quota-2026-11-13:"; aws ec2 describe-instances --filters Name=tag:run,Values=attrib-quota-2026-11-13 Name=instance-state-name,Values=pending,running,stopping,stopped,shutting-down --query 'Reservations[].Instances[].InstanceId' --output text
echo "volumes tagged:"; aws ec2 describe-volumes --filters Name=tag:run,Values=attrib-quota-2026-11-13 --query 'Volumes[].[VolumeId,State]' --output text
echo "security groups tagged:"; aws ec2 describe-security-groups --filters Name=tag:run,Values=attrib-quota-2026-11-13 --query 'SecurityGroups[].GroupId' --output text
echo "network interfaces tagged:"; aws ec2 describe-network-interfaces --filters Name=tag:run,Values=attrib-quota-2026-11-13 --query 'NetworkInterfaces[].NetworkInterfaceId' --output text
echo "tagging API (all services) run=attrib-quota-2026-11-13:"; aws resourcegroupstaggingapi get-resources --tag-filters Key=run,Values=attrib-quota-2026-11-13 --query 'ResourceTagMappingList[].ResourceARN' --output text
echo "bucket:"; aws s3api head-bucket --bucket $BUCKET 2>&1 | head -1
echo "iam role:"; aws iam get-role --role-name $ROLE 2>&1 | head -1
echo "iam instance profile:"; aws iam get-instance-profile --instance-profile-name $PROFILE_NAME 2>&1 | head -1
echo "forbidden instance state (untouched):"; aws ec2 describe-instances --instance-ids $FORBIDDEN --query 'Reservations[0].Instances[0].State.Name' --output text 2>&1
