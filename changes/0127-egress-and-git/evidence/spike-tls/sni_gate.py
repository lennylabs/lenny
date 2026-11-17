"""SNI gate: the outer HCM terminates CONNECT on route `sni-gate` and sends the payload to the
internal listener `sni_inspect` through an internal-address cluster. The CONNECT authority and
the decision service's host/port travel as filter state shared with the upstream (internal)
connection. The internal listener runs tls_inspector, an RBAC CEL condition comparing
connection.requested_server_name with the expected host, then sni_dynamic_forward_proxy +
tcp_proxy to the real destination (same DNS cache, so the SSRF filter still applies).

kv: sniplain=deny|allow   payload without TLS ClientHello
    snitimeout=15s        listener_filters_timeout of the internal listener
"""


def cel_condition():
    # connection.requested_server_name == string(filter_state['lenny.sni_expect'])
    return {"call_expr": {"function": "_==_", "args": [
        {"select_expr": {"operand": {"ident_expr": {"name": "connection"}}, "field": "requested_server_name"}},
        {"call_expr": {"function": "string", "args": [
            {"call_expr": {"function": "_[_]", "args": [
                {"ident_expr": {"name": "filter_state"}},
                {"const_expr": {"string_value": "lenny.sni_expect"}}]}}]}}]}}


def add(boot, dns_cache, kv):
    boot["bootstrap_extensions"] = [{"name": "envoy.bootstrap.internal_listener", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.bootstrap.internal_listener.v3.InternalListener"}}]
    hcm = boot["static_resources"]["listeners"][0]["filter_chains"][0]["filters"][0]["typed_config"]
    # Replace the placeholder set_filter_state entries with the decision-service-provided values.
    for f in hcm["http_filters"]:
        if f["name"] == "envoy.filters.http.set_filter_state":
            fs = lambda key, fmt, factory=None: dict(
                object_key=key, **({"factory_key": factory} if factory else {}),
                format_string={"text_format_source": {"inline_string": fmt}, "omit_empty_values": True}, shared_with_upstream="ONCE", skip_if_empty=True)
            f["typed_config"]["on_request_headers"] = [
                fs("lenny.authority", "%REQ(:AUTHORITY)%", "envoy.string"),
                fs("lenny.sni_expect", "%REQ(x-lenny-host)%", "envoy.string"),
                fs("envoy.upstream.dynamic_host", "%REQ(x-lenny-host)%"),
                fs("envoy.upstream.dynamic_port", "%REQ(x-lenny-port)%"),
            ]
    rbac = {"name": "envoy.filters.network.rbac", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.filters.network.rbac.v3.RBAC",
        "stat_prefix": "sni_gate",
        "rules": {"action": "ALLOW", "policies": {"sni-matches-authority": {
            "permissions": [{"any": True}], "principals": [{"any": True}],
            "condition": cel_condition(),
            "cel_config": {"enable_string_conversion": True}}}}}}
    deny_all = {"name": "envoy.filters.network.rbac", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.filters.network.rbac.v3.RBAC",
        "stat_prefix": "sni_gate_plain", "rules": {"action": "ALLOW", "policies": {}}}}
    sni_dfp = {"name": "envoy.filters.network.sni_dynamic_forward_proxy", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.filters.network.sni_dynamic_forward_proxy.v3.FilterConfig",
        "dns_cache_config": dns_cache, "port_value": 443}}
    tcp = {"name": "envoy.filters.network.tcp_proxy", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy",
        "stat_prefix": "sni_gate_tcp", "cluster": "dfp",
        "access_log": [{"name": "envoy.access_loggers.stdout", "typed_config": {
            "@type": "type.googleapis.com/envoy.extensions.access_loggers.stream.v3.StdoutAccessLog",
            "log_format": {"json_format": {"inner": "sni_inspect", "sni": "%REQUESTED_SERVER_NAME%",
                                           "expect": "%FILTER_STATE(lenny.sni_expect:PLAIN)%",
                                           "authority": "%FILTER_STATE(lenny.authority:PLAIN)%",
                                           "up_host": "%UPSTREAM_HOST%", "flags": "%RESPONSE_FLAGS%",
                                           "conn_term": "%CONNECTION_TERMINATION_DETAILS%", "dur_ms": "%DURATION%"}}}}]}}
    plain_chain = [] if kv.get("sniplain", "deny") == "deny" else [{"name": "plain", "filters": [sni_dfp, tcp]}]
    boot["static_resources"]["listeners"].append({
        "name": "sni_inspect", "internal_listener": {},
        "listener_filters": [{"name": "envoy.filters.listener.tls_inspector", "typed_config": {
            "@type": "type.googleapis.com/envoy.extensions.filters.listener.tls_inspector.v3.TlsInspector"}}],
        "listener_filters_timeout": kv.get("snitimeout", "15s"),
        "continue_on_listener_filters_timeout": True,
        "filter_chains": [
            {"name": "tls", "filter_chain_match": {"transport_protocol": "tls"}, "filters": [rbac, sni_dfp, tcp]}] + plain_chain})
    boot["static_resources"]["clusters"].append({
        "name": "sni_gate", "connect_timeout": "1s",
        "load_assignment": {"cluster_name": "sni_gate", "endpoints": [{"lb_endpoints": [{"endpoint": {
            "address": {"envoy_internal_address": {"server_listener_name": "sni_inspect"}}}}]}]},
        "transport_socket": {"name": "envoy.transport_sockets.internal_upstream", "typed_config": {
            "@type": "type.googleapis.com/envoy.extensions.transport_sockets.internal_upstream.v3.InternalUpstreamTransport",
            "transport_socket": {"name": "envoy.transport_sockets.raw_buffer", "typed_config": {
                "@type": "type.googleapis.com/envoy.extensions.transport_sockets.raw_buffer.v3.RawBuffer"}}}}})
