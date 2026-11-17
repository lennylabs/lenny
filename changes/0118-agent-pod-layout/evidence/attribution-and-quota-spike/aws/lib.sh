# shared settings for the SI Kata fsGroup and pstat run on AWS
export AWS_PROFILE=lenny-tier6 AWS_REGION=us-west-2 AWS_DEFAULT_REGION=us-west-2 AWS_PAGER=""
W=/private/tmp/claude-501/-Users-joan-projects-lenny/c1165b35-7597-45a5-b0fc-caf23493501b/scratchpad/aws-attrib-quota
A=$W/aws
LOG=$W/logs/aws.log
STATE=$A/state.env
TAGS='{Key=project,Value=lenny-spike},{Key=run,Value=attrib-quota-2026-11-13}'
TAGSET='TagSet=[{Key=project,Value=lenny-spike},{Key=run,Value=attrib-quota-2026-11-13}]'
ROLE=aq-node-role
PROFILE_NAME=aq-node-profile
SG_NAME=aq-sg
FORBIDDEN=i-080c5e8e1e25aec21
log() { echo "$(date -u +%FT%TZ) $*" | tee -a "$LOG"; }
[ -f "$STATE" ] && . "$STATE"
save() { echo "$1=$2" >> "$STATE"; export "$1=$2"; }

# ssm <instance> <script-file> [timeout]: run a script as root and print status, stdout, stderr
ssm() {
  local iid=$1 f=$2 to=${3:-540}
  [ "$iid" = "$FORBIDDEN" ] && { echo "REFUSING forbidden instance"; return 1; }
  local params cid st
  params=$(python3 -c 'import json,sys; print(json.dumps({"commands":[open(sys.argv[1]).read()],"executionTimeout":[sys.argv[2]]}))' "$f" "$to")
  cid=$(aws ssm send-command --instance-ids "$iid" --document-name AWS-RunShellScript --parameters "$params" \
        --timeout-seconds 600 --query Command.CommandId --output text) || return 1
  for i in $(seq 1 $((to/3+20))); do
    sleep 3
    st=$(aws ssm get-command-invocation --command-id "$cid" --instance-id "$iid" --query Status --output text 2>/dev/null)
    case "$st" in Success|Failed|TimedOut|Cancelled) break;; esac
  done
  aws ssm get-command-invocation --command-id "$cid" --instance-id "$iid" --query '[Status,StandardOutputContent,StandardErrorContent]' --output text
}
# ssmc <instance> <command string>
ssmc() { local t; t=$(mktemp); printf "%s\n" "$2" > $t; ssm "$1" $t ${3:-540}; rm -f $t; }

wait_ssm() {
  for i in $(seq 1 60); do
    s=$(aws ssm describe-instance-information --filters Key=InstanceIds,Values=$1 --query 'InstanceInformationList[0].PingStatus' --output text 2>/dev/null)
    [ "$s" = Online ] && return 0
    sleep 10
  done
  return 1
}
