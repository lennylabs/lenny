#!/bin/bash
# setup.sh: IAM role and instance profile, security group with no ingress, and the run's bucket
set -euo pipefail
. "$(dirname "$0")/lib.sh"
log "=== setup"
ACCT=$(aws sts get-caller-identity --query Account --output text); save ACCT $ACCT
B=lenny-spike-aq-$ACCT-$(date +%s)
aws s3api create-bucket --bucket $B --create-bucket-configuration LocationConstraint=us-west-2 >/dev/null
aws s3api put-bucket-tagging --bucket $B --tagging "$TAGSET"
aws s3api put-public-access-block --bucket $B --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
save BUCKET $B; log "bucket $B"
cat > $A/trust.json <<J
{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}
J
cat > $A/s3policy.json <<J
{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject","s3:PutObject","s3:DeleteObject","s3:ListBucket"],"Resource":["arn:aws:s3:::$B","arn:aws:s3:::$B/*"]}]}
J
aws iam create-role --role-name $ROLE --assume-role-policy-document file://$A/trust.json --tags Key=project,Value=lenny-spike Key=run,Value=attrib-quota-2026-11-13 >/dev/null
aws iam attach-role-policy --role-name $ROLE --policy-arn arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore
aws iam put-role-policy --role-name $ROLE --policy-name aq-bucket --policy-document file://$A/s3policy.json
aws iam create-instance-profile --instance-profile-name $PROFILE_NAME --tags Key=project,Value=lenny-spike Key=run,Value=attrib-quota-2026-11-13 >/dev/null
aws iam add-role-to-instance-profile --instance-profile-name $PROFILE_NAME --role-name $ROLE
log "role $ROLE, profile $PROFILE_NAME"
VPC=$(aws ec2 describe-vpcs --filters Name=is-default,Values=true --query 'Vpcs[0].VpcId' --output text)
SUBNET=$(aws ec2 describe-subnets --filters Name=vpc-id,Values=$VPC Name=default-for-az,Values=true Name=availability-zone,Values=us-west-2a --query 'Subnets[0].SubnetId' --output text)
SG=$(aws ec2 create-security-group --group-name $SG_NAME --description "lenny spike attrib-quota, no ingress" --vpc-id $VPC \
  --tag-specifications "ResourceType=security-group,Tags=[$TAGS,{Key=Name,Value=$SG_NAME}]" --query GroupId --output text)
save SG_ID $SG; save SUBNET_ID $SUBNET; save VPC_ID $VPC
AMI=$(aws ssm get-parameter --name /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-6.12-x86_64 --query Parameter.Value --output text); save AMI $AMI
log "sg $SG vpc $VPC subnet $SUBNET ami $AMI"
