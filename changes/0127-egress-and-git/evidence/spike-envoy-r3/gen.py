#!/usr/bin/env python3
"""gen.py OUT [k=v ...]  render an r3 Envoy bootstrap as JSON.

authz=grpc|http      ext_authz transport (default grpc)
timeout=2s           ext_authz timeout
filter=on|off        resolved_address_filter
family=ALL|V4_ONLY|V6_ONLY|AUTO|V4_PREFERRED
dns=system|dnsd      resolver (dnsd = c-ares pointed at 198.18.0.53)
refresh=60s minrefresh=5s ttl=on|off hostttl=300s
peerhdr=1            (http) add x-lenny-peer: %DOWNSTREAM_REMOTE_ADDRESS% to the check request
hygiene=0|1|2        chained-route response hygiene (1 = remove headers, 2 = also local_reply_mapper)
cb=N                 authz cluster circuit breaker max_requests / max_pending
sni=1                add the SNI gate (internal listener) route + listener
msd=3600s            route max_stream_duration
"""
import json, sys

kv = dict(a.split("=", 1) for a in sys.argv[2:])
G = lambda k, d: kv.get(k, d)
AUTHZ = G("authz", "grpc")
NS = "envoy.filters.http.ext_authz"
BLOCK = [("0.0.0.0", 8), ("10.0.0.0", 8), ("100.64.0.0", 10), ("127.0.0.0", 8), ("169.254.0.0", 16),
         ("172.16.0.0", 12), ("192.0.0.0", 24), ("192.168.0.0", 16), ("224.0.0.0", 4), ("240.0.0.0", 4),
         ("::", 128), ("::1", 128), ("::ffff:0:0", 96), ("64:ff9b::", 96), ("fc00::", 7), ("fe80::", 10), ("ff00::", 8)]

dns_cache = {"name": "egress_dns", "dns_lookup_family": G("family", "ALL"),
             "dns_refresh_rate": G("refresh", "60s"), "dns_min_refresh_rate": G("minrefresh", "5s"),
             "host_ttl": G("hostttl", "300s")}
if G("ttl", "on") == "off":
    pass
if G("filter", "on") == "on":
    dns_cache["resolved_address_filter"] = {"ranges": [{"address_prefix": a, "prefix_len": p} for a, p in BLOCK]}
if G("dns", "system") == "dnsd":
    dns_cache["typed_dns_resolver_config"] = {"name": "envoy.network.dns_resolver.cares", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.network.dns_resolver.cares.v3.CaresDnsResolverConfig",
        "resolvers": [{"socket_address": {"address": "198.18.0.53", "port_value": 53}}],
        "use_resolvers_as_fallback": False}}

TO = G("timeout", "2s")
if AUTHZ == "grpc":
    authz = {"transport_api_version": "V3", "grpc_service": {"envoy_grpc": {"cluster_name": "authz"}, "timeout": TO}}
    md_path = [{"key": "upstream"}]
else:
    req = {}
    if G("peerhdr", "1") == "1":
        req["headers_to_add"] = [{"key": "x-lenny-peer", "value": "%DOWNSTREAM_REMOTE_ADDRESS%"}]
    authz = {"http_service": {
        "server_uri": {"uri": "http://198.18.0.11:9001", "cluster": "authz_http", "timeout": TO},
        "path_prefix": "/check",
        "authorization_request": req,
        "authorization_response": {
            "allowed_upstream_headers": {"patterns": [{"exact": "x-lenny-route"}, {"exact": "proxy-authorization"}]},
            "allowed_client_headers": {"patterns": [{"exact": "x-lenny-deny-reason"}, {"exact": "proxy-authenticate"}]},
            "dynamic_metadata_from_headers": {"patterns": [{"exact": "x-lenny-upstream"}, {"exact": "x-lenny-tenant"}]}}},
        "allowed_headers": {"patterns": [{"exact": "proxy-authorization"}]}}
    md_path = [{"key": "x-lenny-upstream"}]
authz.update({"failure_mode_allow": False, "clear_route_cache": True, "status_on_error": {"code": "ServiceUnavailable"}})

MSD = {"max_stream_duration": {"max_stream_duration": G("msd", "3600s")}}
STRIP = ["x-request-id", "x-forwarded-proto", "x-lenny-route", "x-lenny-upstream", "user-agent"]
H = G("hygiene", "0")
def cr_rule(code, status, body, extra_hdr):
    return {"predicate": {"single_predicate": {
                "input": {"name": "status_code", "typed_config": {"@type": "type.googleapis.com/envoy.type.matcher.v3.HttpResponseStatusCodeMatchInput"}},
                "value_match": {"exact": str(code)}}},
            "on_match": {"action": {"name": "lenny_upstream_%d" % code, "typed_config": {
                "@type": "type.googleapis.com/envoy.extensions.http.custom_response.local_response_policy.v3.LocalResponsePolicy",
                "status_code": status, "body": {"inline_string": body},
                "response_headers_to_add": [{"header": {"key": "x-lenny-upstream-status", "value": extra_hdr}, "append_action": "OVERWRITE_IF_EXISTS_OR_ADD"}]}}}}
CR_ROUTE = {"@type": "type.googleapis.com/envoy.extensions.filters.http.custom_response.v3.CustomResponse",
            "custom_response_matcher": {"matcher_list": {"matchers": [
                cr_rule(407, 502, "upstream proxy rejected the egress credential\n", "407"),
                cr_rule(403, 403, "upstream proxy denied the destination\n", "403")]}}}
def rt(name, cluster, terminate, extra=None):
    r = {"match": {"connect_matcher": {}, "headers": [{"name": "x-lenny-route", "string_match": {"exact": name}}]},
         "route": dict(cluster=cluster, upgrade_configs=[{"upgrade_type": "CONNECT", **({"connect_config": {}} if terminate else {})}], **MSD)}
    if not terminate:
        r["request_headers_to_remove"] = STRIP
        if H in ("1", "2"):
            r["response_headers_to_remove"] = ["proxy-authenticate", "proxy-connection", "x-corp-policy", "x-corp-hint", "via"]
        if H == "3":
            r["typed_per_filter_config"] = {"envoy.filters.http.custom_response": CR_ROUTE}
    if extra:
        r.update(extra)
    return r

routes = [rt("direct", "dfp", True), rt("upstream-md", "upstream_dynamic_md", False)]
if G("sni", "0") == "1":
    routes.append(rt("sni-gate", "sni_gate", True))
routes += [{"match": {"connect_matcher": {}}, "direct_response": {"status": 403, "body": {"inline_string": "no route decision\n"}}},
           {"match": {"prefix": "/"}, "direct_response": {"status": 405, "body": {"inline_string": "CONNECT only\n"}}}]

fmt = {"t": "%START_TIME%", "method": "%REQ(:METHOD)%", "authority": "%REQ(:AUTHORITY)%", "code": "%RESPONSE_CODE%",
       "flags": "%RESPONSE_FLAGS%", "details": "%RESPONSE_CODE_DETAILS%", "dur_ms": "%DURATION%", "peer": "%DOWNSTREAM_REMOTE_ADDRESS%",
       "up_cluster": "%UPSTREAM_CLUSTER%", "up_host": "%UPSTREAM_HOST%", "up_fail": "%UPSTREAM_TRANSPORT_FAILURE_REASON%",
       "rx": "%BYTES_RECEIVED%", "tx": "%BYTES_SENT%", "route_hdr": "%REQ(x-lenny-route)%",
       "tenant": "%DYNAMIC_METADATA(" + NS + ":tenant)%", "md_upstream": "%DYNAMIC_METADATA(" + NS + ":" + md_path[0]["key"] + ")%",
       "md_tenant_h": "%DYNAMIC_METADATA(" + NS + ":x-lenny-tenant)%", "resp_pa": "%RESP(proxy-authenticate)%",
       "conn_term": "%CONNECTION_TERMINATION_DETAILS%", "sni_expect": "%FILTER_STATE(lenny.authority:PLAIN)%"}

http_filters = [{"name": NS, "typed_config": {"@type": "type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz", **authz}}]
if G("sni", "0") == "1":
    http_filters.append({"name": "envoy.filters.http.set_filter_state", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.filters.http.set_filter_state.v3.Config",
        "on_request_headers": [{"object_key": "lenny.authority", "factory_key": "envoy.string",
                                "format_string": {"text_format_source": {"inline_string": "%REQ(:AUTHORITY)%"}},
                                "shared_with_upstream": "ONCE"},
                               {"object_key": "envoy.upstream.dynamic_host", "format_string": {"text_format_source": {"inline_string": "%REQ(:AUTHORITY)%"}}, "shared_with_upstream": "ONCE"}]}})
if H == "3":
    # Global instance has no matcher; the chained route supplies one via typed_per_filter_config.
    http_filters.insert(0, {"name": "envoy.filters.http.custom_response", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.filters.http.custom_response.v3.CustomResponse"}})
http_filters += [
    {"name": "envoy.filters.http.dynamic_forward_proxy", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.filters.http.dynamic_forward_proxy.v3.FilterConfig", "dns_cache_config": dns_cache}},
    {"name": "envoy.filters.http.router", "typed_config": {"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router", "suppress_envoy_headers": True}}]

hcm = {"@type": "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
       "stat_prefix": "egress", "codec_type": "HTTP1", "upgrade_configs": [{"upgrade_type": "CONNECT"}],
       "stream_idle_timeout": "300s", "delayed_close_timeout": "0.1s",
       "access_log_options": {"flush_log_on_tunnel_successfully_established": True},
       "access_log": [{"name": "envoy.access_loggers.stdout", "typed_config": {
           "@type": "type.googleapis.com/envoy.extensions.access_loggers.stream.v3.StdoutAccessLog", "log_format": {"json_format": fmt}}}],
       "http_filters": http_filters,
       "route_config": {"name": "egress", "virtual_hosts": [{"name": "all", "domains": ["*"], "routes": routes}]}}
if H == "2":
    hcm["local_reply_config"] = {"mappers": [{
        "filter": {"status_code_filter": {"comparison": {"op": "EQ", "value": {"default_value": 407, "runtime_key": "x"}}}},
        "status_code": 502, "body": {"inline_string": "upstream proxy authentication failed\n"}}]}

def cb():
    if "cb" not in kv:
        return {}
    n = int(kv["cb"])
    return {"circuit_breakers": {"thresholds": [{"max_connections": n, "max_pending_requests": n, "max_requests": n}]}}

clusters = [
    {"name": "authz", "type": "STATIC", "connect_timeout": "1s", **cb(),
     "typed_extension_protocol_options": {"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": {
         "@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions", "explicit_http_config": {"http2_protocol_options": {}}}},
     "load_assignment": {"cluster_name": "authz", "endpoints": [{"lb_endpoints": [{"endpoint": {"address": {"socket_address": {"address": "198.18.0.11", "port_value": 9000}}}}]}]}},
    {"name": "authz_http", "type": "STATIC", "connect_timeout": "1s", **cb(),
     **({"typed_extension_protocol_options": {"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": {
         "@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions", "explicit_http_config": {"http2_protocol_options": {}}}}} if G("httph2", "0") == "1" else {}),
     "load_assignment": {"cluster_name": "authz_http", "endpoints": [{"lb_endpoints": [{"endpoint": {"address": {"socket_address": {"address": "198.18.0.11", "port_value": 9003 if G("httph2", "0") == "1" else 9001}}}}]}]}},
    {"name": "dfp", "connect_timeout": "5s", "lb_policy": "CLUSTER_PROVIDED", "cluster_type": {"name": "envoy.clusters.dynamic_forward_proxy", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig", "dns_cache_config": dns_cache}}},
    {"name": "upstream_dynamic_md", "type": "ORIGINAL_DST", "connect_timeout": "5s", "lb_policy": "CLUSTER_PROVIDED",
     "original_dst_lb_config": {"metadata_key": {"key": NS, "path": md_path}}},
]
static = {"listeners": [{"name": "egress", "address": {"socket_address": {"address": "::", "port_value": 3128, "ipv4_compat": True}},
                         "filter_chains": [{"filters": [{"name": "envoy.filters.network.http_connection_manager", "typed_config": hcm}]}]}],
          "clusters": clusters}
boot = {"admin": {"address": {"socket_address": {"address": "0.0.0.0", "port_value": 9901}}}, "static_resources": static}

if G("sni", "0") == "1":
    import sni_gate
    sni_gate.add(boot, dns_cache, kv)

if "cbdfp" in kv:
    n = int(kv["cbdfp"])
    for c in clusters:
        if c["name"] in ("dfp", "upstream_dynamic_md", "sni_gate"):
            c["circuit_breakers"] = {"thresholds": [{"max_connections": n, "max_pending_requests": n, "max_requests": n}]}
json.dump(boot, open(sys.argv[1], "w"), indent=1)
