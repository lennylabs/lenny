#!/bin/sh
# Test 3b: upstream redirects, passthrough versus rewrite, including a real GitHub 301.
L=/spike/t3/logs-redir; rm -rf $L; mkdir -p $L
pkill -f bin/upstream; pkill -f bin/proxy; sleep 0.3
/spike/t3/bin/upstream -mode direct 2> $L/upstream.log &
/spike/t3/bin/proxy -addr 127.0.0.1:7074 -redirect passthrough \
  -routes 'moved301.test=http://127.0.0.1:8080/moved301,tohub.test=http://127.0.0.1:8080/tohub,ghhttp.test=http://github.com' \
  -allow 'moved301.test/org/repo,tohub.test/google/protobuf,ghhttp.test/google/protobuf' 2> $L/proxy-pass.log &
/spike/t3/bin/proxy -addr 127.0.0.1:7075 -redirect rewrite \
  -routes 'ghhttp.test=http://github.com,tohub.test=http://127.0.0.1:8080/tohub,moved301.test=http://127.0.0.1:8080/moved301,local.test=http://localhost:8080' \
  -allow 'ghhttp.test/google/protobuf,github.com/google/protobuf,tohub.test/google/protobuf,moved301.test/org/repo,local.test/org/repo' 2> $L/proxy-rewrite.log &
sleep 0.5
export HOME=/tmp/h3r GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 GIT_CONFIG_GLOBAL=/tmp/h3r/gc; rm -rf $HOME; mkdir -p $HOME
r() { echo "## [$1] git ls-remote $2"; GIT_TRACE_CURL=1 GIT_TRACE_CURL_NO_DATA=1 git -c url."http://127.0.0.1:$1/https/".insteadOf=https:// ls-remote "$2" 2>&1 | grep -E 'Connected to|Send header: (GET|POST)|warning|fatal|refs/heads/main$|HEAD$' | sed -E 's/^[0-9:.]+ +[a-z.:0-9]+ +//' | head -8; }
r 7074 https://moved301.test/org/repo
r 7074 https://tohub.test/google/protobuf
r 7074 https://ghhttp.test/google/protobuf
r 7075 https://ghhttp.test/google/protobuf
r 7075 https://tohub.test/google/protobuf
r 7075 https://moved301.test/org/repo
echo "## rewrite proxy, redirect to a host not allowlisted (tohub to github.com/google/protobuf allowed; now try a disallowed one)"
/spike/t3/bin/proxy -addr 127.0.0.1:7076 -redirect rewrite -cred '' -routes 'ghhttp.test=http://github.com' -allow 'ghhttp.test/google/protobuf' 2> $L/proxy-deny.log &
sleep 0.3; r 7076 https://ghhttp.test/google/protobuf
pkill -f bin/upstream; pkill -f bin/proxy
echo "== logs"; grep -h REDIRECT $L/proxy-*.log | cut -c21- | sort | uniq -c
