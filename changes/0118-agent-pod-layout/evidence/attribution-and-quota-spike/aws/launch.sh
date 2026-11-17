#!/bin/bash
# launch.sh NAME TYPE ROOTGB DATAGB MINUTES [nested]: one self-terminating node
set -euo pipefail
. "$(dirname "$0")/lib.sh"
NAME=$1 TYPE=$2 ROOT=$3 DATA=$4 MIN=$5 NESTED=${6:-nested}
SPENT=$(python3 $A/ledger.py --total)
python3 -c "import sys; sys.exit(0 if float('$SPENT') < 12 else 1)" || { log "REFUSING launch: spent \$$SPENT >= \$12"; exit 1; }
cat > $A/ud-$NAME.sh <<U
#!/bin/bash
shutdown -h +$MIN "aq self-termination"
echo "\$(date -u) scheduled shutdown in $MIN min" > /var/tmp/aq-ud
U
CPU=()
[ "$NESTED" = nested ] && CPU=(--cpu-options NestedVirtualization=enabled)
BDM="[{\"DeviceName\":\"/dev/xvda\",\"Ebs\":{\"VolumeSize\":$ROOT,\"VolumeType\":\"gp3\",\"DeleteOnTermination\":true}},{\"DeviceName\":\"/dev/sdf\",\"Ebs\":{\"VolumeSize\":$DATA,\"VolumeType\":\"gp3\",\"Iops\":3000,\"Throughput\":125,\"DeleteOnTermination\":true}}]"
IID=$(aws ec2 run-instances --image-id $AMI --instance-type $TYPE ${CPU[@]+"${CPU[@]}"} \
  --iam-instance-profile Name=$PROFILE_NAME --security-group-ids $SG_ID --subnet-id $SUBNET_ID --associate-public-ip-address \
  --instance-initiated-shutdown-behavior terminate --block-device-mappings "$BDM" \
  --metadata-options HttpTokens=required,HttpEndpoint=enabled,HttpPutResponseHopLimit=3 \
  --user-data file://$A/ud-$NAME.sh \
  --tag-specifications "ResourceType=instance,Tags=[$TAGS,{Key=Name,Value=aq-$NAME}]" "ResourceType=volume,Tags=[$TAGS,{Key=Name,Value=aq-$NAME}]" "ResourceType=network-interface,Tags=[$TAGS]" \
  --query 'Instances[0].InstanceId' --output text)
[ "$IID" = "$FORBIDDEN" ] && { log "launched the forbidden id?!"; exit 1; }
printf "%s\t%s\t%s\t%s\t%s\t%s\n" "$IID" "$NAME" "$TYPE" "$(date -u +%FT%TZ)" "$((ROOT+DATA))" "$MIN" >> $A/ledger.tsv
save IID_$NAME $IID
log "launched $NAME $IID $TYPE root ${ROOT}G data ${DATA}G, self-terminates in ${MIN} min"
echo $IID
