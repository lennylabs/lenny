#!/bin/sh
# run a curl through squid from a client container. usage: t.sh TOKEN URL [extra curl args]
tok=$1; url=$2; shift 2
docker run --rm --network spike-px-net spike-squid7:local curl -s -o /dev/null -w "%{http_connect} %{http_code} %{errormsg}\n" --max-time 15 -x http://spike-squid:3128 --proxy-user "agent:$tok" "$@" "$url"
