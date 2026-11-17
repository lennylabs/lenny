#!/bin/bash
# st.sh ADMINPORT [grep-regex] : selected Envoy stats.
AP=$1; RE=${2:-'cluster.authz.(upstream_rq_(total|active|pending_overflow|pending_failure_eject|timeout|5xx|2xx|retry|retry_success)|upstream_cx_(total|active|destroy|connect_fail|connect_timeout|destroy_remote|destroy_local)|outlier_detection.ejections_(active|enforced_total|enforced_consecutive_local_origin_failure|enforced_consecutive_5xx|enforced_consecutive_gateway_failure)|membership_healthy|health_check)|http.egress.ext_authz|http.egress.downstream_rq_(total|active)|http.egress.downstream_cx_active|cluster.dfp.upstream_cx_active|http2.keepalive_timeout|http2.(streams|pending_send_bytes)'}
curl -s "http://127.0.0.1:$AP/stats" | grep -E "$RE" | grep -v -E ': 0$' | sed 's/^/  /'
