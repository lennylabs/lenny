# lib.sh: step <label> <shell command>. Prints output and appends the time window for the log join. Every step has a hard timeout.
step(){
 _l=$1; shift
 _s=$(date -u +%FT%T.%NZ)
 _o=$(timeout -k 3 ${STEPT:-90} sh -c "$*" 2>&1 </dev/null); _rc=$?
 _e=$(date -u +%FT%T.%NZ)
 printf '### %s rc=%s\n$ %s\n%s\n' "$_l" "$_rc" "$*" "$(printf '%s' "$_o" | tail -n ${TL:-6} | cut -c1-300)"
 printf '{"label":"%s","start":"%s","end":"%s","rc":%s}\n' "$_l" "$_s" "$_e" "$_rc" >> ${STEPS:-/out/steps.jsonl}
 STEPRC=$_rc
}
