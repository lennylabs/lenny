#!/usr/bin/env python3
"""gen4.py OUT [k=v ...]  render the r4 Envoy bootstrap (gRPC ext_authz, CONNECT termination via DFP) as JSON.

authz=ip:port,ip:port   authz STATIC endpoints (default 198.19.0.11:9000,198.19.0.12:9000)
ka=INTERVAL,TIMEOUT     HTTP/2 connection_keepalive on the authz cluster (default off)
od=0|1                  outlier detection on the authz cluster (default 0)
odn=N                   consecutive failures to eject (default 3)   odbase=10s  odint=1s  odmax=100
timeout=0.5s           ext_authz timeout           cto=0.25s  authz connect_timeout
retry=N                 envoy_grpc retry_policy num_retries on the authz call (default none)
cbauthz=N  cbdfp=N      circuit breaker thresholds
sit=300s                HCM stream_idle_timeout     idle=3600s  common_http_protocol_options.idle_timeout
msd=7200s               route max_stream_duration   dct=0.1s    delayed_close_timeout
flush=60s               access_log_options.access_log_flush_interval (default none)
alog=1                  access log to stdout (default 1)
"""
import json, sys

kv = dict(a.split("=", 1) for a in sys.argv[2:])
G = lambda k, d: kv.get(k, d)
NS = "envoy.filters.http.ext_authz"

dns_cache = {"name": "egress_dns", "dns_lookup_family": "V4_ONLY", "dns_refresh_rate": "60s", "dns_min_refresh_rate": "5s", "host_ttl": "300s"}

grpc = {"envoy_grpc": {"cluster_name": "authz"}, "timeout": G("timeout", "0.5s")}
if "retry" in kv:
    # GrpcService.retry_policy is the one honoured by data-plane gRPC clients (ext_authz); EnvoyGrpc.retry_policy is xDS-only.
    # NOTE: Envoy sets the per-try timeout of this policy to retry_back_off.max_interval (Http::Utility::convertCoreToRouteRetryPolicy),
    # so max_interval must be larger than the slowest acceptable decision, not a small backoff cap.
    grpc["retry_policy"] = {"retry_back_off": {"base_interval": G("rbase", "0.02s"), "max_interval": G("rmax", "0.4s")}, "num_retries": int(kv["retry"]),
                            "retry_on": G("retry_on", "connect-failure,reset,refused-stream,unavailable,cancelled,deadline-exceeded"),
                            "retry_host_predicate": [{"name": "envoy.retry_host_predicates.previous_hosts", "typed_config": {
                                "@type": "type.googleapis.com/envoy.extensions.retry.host.previous_hosts.v3.PreviousHostsPredicate"}}],
                            "host_selection_retry_max_attempts": 3}
authz = {"transport_api_version": "V3", "grpc_service": grpc, "failure_mode_allow": False, "clear_route_cache": True,
         "status_on_error": {"code": "ServiceUnavailable"}}

route_direct = {"match": {"connect_matcher": {}, "headers": [{"name": "x-lenny-route", "string_match": {"exact": "direct"}}]},
                "route": {"cluster": "dfp", "upgrade_configs": [{"upgrade_type": "CONNECT", "connect_config": {}}],
                          "max_stream_duration": {"max_stream_duration": G("msd", "7200s")}}}
route_private = {"match": {"connect_matcher": {}, "headers": [{"name": "x-lenny-route", "string_match": {"exact": "private"}}]},
                "route": {"cluster": "origin_md", "upgrade_configs": [{"upgrade_type": "CONNECT", "connect_config": {}}],
                          "max_stream_duration": {"max_stream_duration": G("msd", "7200s")}}}
routes = [route_direct, route_private,
          {"match": {"connect_matcher": {}}, "direct_response": {"status": 403, "body": {"inline_string": "no route decision\n"}}},
          {"match": {"prefix": "/"}, "direct_response": {"status": 405, "body": {"inline_string": "CONNECT only\n"}}}]

fmt = {"t": "%START_TIME%", "type": "%ACCESS_LOG_TYPE%", "authority": "%REQ(:AUTHORITY)%", "code": "%RESPONSE_CODE%",
       "flags": "%RESPONSE_FLAGS%", "details": "%RESPONSE_CODE_DETAILS%", "dur_ms": "%DURATION%", "peer": "%DOWNSTREAM_REMOTE_ADDRESS%",
       "up_host": "%UPSTREAM_HOST%", "rx": "%BYTES_RECEIVED%", "tx": "%BYTES_SENT%",
       "authz": "%DYNAMIC_METADATA(" + NS + ":instance)%", "conn_term": "%CONNECTION_TERMINATION_DETAILS%"}

http_filters = [
    {"name": NS, "typed_config": {"@type": "type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz", **authz}},
    {"name": "envoy.filters.http.dynamic_forward_proxy", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.filters.http.dynamic_forward_proxy.v3.FilterConfig", "dns_cache_config": dns_cache}},
    {"name": "envoy.filters.http.router", "typed_config": {"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router", "suppress_envoy_headers": True}}]

alo = {"flush_log_on_tunnel_successfully_established": True}
if "flush" in kv:
    alo["access_log_flush_interval"] = kv["flush"]
hcm = {"@type": "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
       "stat_prefix": "egress", "codec_type": "HTTP1", "upgrade_configs": [{"upgrade_type": "CONNECT"}],
       "stream_idle_timeout": G("sit", "300s"), "delayed_close_timeout": G("dct", "0.1s"),
       "common_http_protocol_options": {"idle_timeout": G("idle", "3600s")},
       "access_log_options": alo,
       "http_filters": http_filters,
       "route_config": {"name": "egress", "virtual_hosts": [{"name": "all", "domains": ["*"], "routes": routes}]}}
if "dto" in kv:
    hcm["drain_timeout"] = kv["dto"]   # time between the start of a connection's drain and its forced close (default 5 s)
if G("alog", "1") == "1":
    hcm["access_log"] = [{"name": "envoy.access_loggers.stdout", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.access_loggers.stream.v3.StdoutAccessLog", "log_format": {"json_format": fmt}}}]

def cb(k):
    if k not in kv:
        return {}
    n = int(kv[k])
    return {"circuit_breakers": {"thresholds": [{"max_connections": n, "max_pending_requests": n, "max_requests": n, "max_retries": n}]}}

h2 = {}
if "ka" in kv and kv["ka"] != "off":
    i, t = kv["ka"].split(",")
    h2["connection_keepalive"] = {"interval": i, "timeout": t}
authz_cluster = {"name": "authz", "type": "STATIC", "connect_timeout": G("cto", "0.25s"), **cb("cbauthz"),
                 "typed_extension_protocol_options": {"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": {
                     "@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions",
                     "explicit_http_config": {"http2_protocol_options": h2}}},
                 "load_assignment": {"cluster_name": "authz", "endpoints": [{"lb_endpoints": [
                     {"endpoint": {"address": {"socket_address": {"address": e.split(":")[0], "port_value": int(e.split(":")[1])}}}}
                     for e in G("authz", "198.19.0.11:9000,198.19.0.12:9000").split(",")]}]}}
if G("od", "0") == "1":
    n = int(G("odn", "3"))
    authz_cluster["outlier_detection"] = {
        "interval": G("odint", "1s"), "base_ejection_time": G("odbase", "10s"), "max_ejection_percent": int(G("odmax", "100")),
        "consecutive_5xx": n, "enforcing_consecutive_5xx": 100,
        "consecutive_gateway_failure": n, "enforcing_consecutive_gateway_failure": 100,
        "split_external_local_origin_errors": True,
        "consecutive_local_origin_failure": n, "enforcing_consecutive_local_origin_failure": 100,
        "enforcing_success_rate": 0, "enforcing_local_origin_success_rate": 0}
    if "odmaxt" in kv:
        authz_cluster["outlier_detection"]["max_ejection_time"] = kv["odmaxt"]
if G("hc", "0") == "1":
    # Active gRPC health check (grpc.health.v1); with successful_active_health_check_uneject_host an ejected host
    # only returns when the health check passes, so an uneject never probes with live CONNECTs.
    authz_cluster["health_checks"] = [{"timeout": G("hcto", "0.5s"), "interval": G("hcint", "1s"), "no_traffic_interval": G("hcint", "1s"),
                                       "unhealthy_threshold": 1, "healthy_threshold": 1, "grpc_health_check": {},
                                       "event_log_path": "/dev/stdout"}]
    if "outlier_detection" in authz_cluster:
        authz_cluster["outlier_detection"]["successful_active_health_check_uneject_host"] = True

clusters = [authz_cluster,
            {"name": "origin_md", "type": "ORIGINAL_DST", "connect_timeout": "5s", "lb_policy": "CLUSTER_PROVIDED",
             "original_dst_lb_config": {"metadata_key": {"key": NS, "path": [{"key": "private_addr"}]}}},
            {"name": "dfp", "connect_timeout": "5s", "lb_policy": "CLUSTER_PROVIDED", **cb("cbdfp"),
             "cluster_type": {"name": "envoy.clusters.dynamic_forward_proxy", "typed_config": {
                 "@type": "type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig", "dns_cache_config": dns_cache}}}]

boot = {"admin": {"address": {"socket_address": {"address": "0.0.0.0", "port_value": 9901}}},
        "cluster_manager": {"outlier_detection": {"event_log_path": "/dev/stdout"}},
        "layered_runtime": {"layers": [{"name": "static", "static_layer": {"overload": {"global_downstream_max_connections": 50000}}}]},
        "static_resources": {"listeners": [{"name": "egress", "address": {"socket_address": {"address": "0.0.0.0", "port_value": 3128}},
                                            "filter_chains": [{"filters": [{"name": "envoy.filters.network.http_connection_manager", "typed_config": hcm}]}]}],
                             "clusters": clusters}}
json.dump(boot, open(sys.argv[1], "w"), indent=1)
