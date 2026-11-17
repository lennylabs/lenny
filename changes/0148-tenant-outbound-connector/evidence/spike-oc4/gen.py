# Renders the Envoy bootstrap for the OC 4 spike. Usage: gen.py OUT MSD_SECONDS
import json, sys
out, msd = sys.argv[1], sys.argv[2]
mode = sys.argv[3] if len(sys.argv) > 3 else 'http'
SAN = 'lenny-outbound-gateway.lenny-outbound.svc'
def tls_socket():
    return {"name": "envoy.transport_sockets.tls", "typed_config": {
        "@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
        "sni": SAN,
        "common_tls_context": {"alpn_protocols": ["http/1.1"],
            "tls_params": {"tls_minimum_protocol_version": "TLSv1_2"},
            "validation_context": {"trusted_ca": {"filename": "/certs/egress-ca.pem"},
                "match_typed_subject_alt_names": [{"san_type": "DNS", "matcher": {"exact": SAN}}]}}}}
def od_cluster(name, ts=None, tsm=None):
    c = {"name": name, "type": "ORIGINAL_DST", "connect_timeout": "3s", "lb_policy": "CLUSTER_PROVIDED",
         "original_dst_lb_config": {"metadata_key": {"key": "envoy.filters.http.ext_authz", "path": [{"key": "x-lenny-upstream"}]}}}
    if ts: c["transport_socket"] = ts
    if tsm: c["transport_socket_matches"] = tsm
    return c
def lrp(name, code, body, up):
    return {"predicate": {"single_predicate": {"input": {"name": "status_code", "typed_config": {"@type": "type.googleapis.com/envoy.type.matcher.v3.HttpResponseStatusCodeMatchInput"}}, "value_match": {"exact": up}}},
            "on_match": {"action": {"name": name, "typed_config": {"@type": "type.googleapis.com/envoy.extensions.http.custom_response.local_response_policy.v3.LocalResponsePolicy",
                "status_code": code, "body": {"inline_string": body}}}}}
CR = {"envoy.filters.http.custom_response": {"@type": "type.googleapis.com/envoy.extensions.filters.http.custom_response.v3.CustomResponse",
      "custom_response_matcher": {"matcher_list": {"matchers": [lrp("gw_407", 502, "outbound gateway refused the grant\n", "407"), lrp("gw_403", 403, "outbound connector denied the destination\n", "403")]}}}}
def route(match_value, cluster):
    return {"match": {"connect_matcher": {}, "headers": [{"name": "x-lenny-route", "string_match": {"exact": match_value}}]},
            "route": {"cluster": cluster, "upgrade_configs": [{"upgrade_type": "CONNECT"}], "max_stream_duration": {"max_stream_duration": f"{msd}s"}},
            "request_headers_to_remove": ["x-request-id", "x-forwarded-proto", "x-lenny-route", "x-lenny-upstream", "user-agent"],
            "typed_per_filter_config": CR}
cfg = {"admin": {"address": {"socket_address": {"address": "0.0.0.0", "port_value": 9901}}},
 "static_resources": {
  "listeners": [{"name": "egress", "address": {"socket_address": {"address": "0.0.0.0", "port_value": 3128}},
   "filter_chains": [{"filters": [{"name": "envoy.filters.network.http_connection_manager", "typed_config": {
     "@type": "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
     "stat_prefix": "egress", "codec_type": "HTTP1", "upgrade_configs": [{"upgrade_type": "CONNECT"}],
     "stream_idle_timeout": "300s", "delayed_close_timeout": "0.1s",
     "access_log": [{"name": "envoy.access_loggers.file", "typed_config": {"@type": "type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog", "path": "/dev/stdout",
        "log_format": {"json_format": {"authority": "%REQ(:AUTHORITY)%", "code": "%RESPONSE_CODE%", "flags": "%RESPONSE_FLAGS%", "details": "%RESPONSE_CODE_DETAILS%", "upstream": "%UPSTREAM_HOST%", "tls_failure": "%UPSTREAM_TRANSPORT_FAILURE_REASON%", "duration": "%DURATION%"}}}}],
     "http_filters": [
      {"name": "envoy.filters.http.custom_response", "typed_config": {"@type": "type.googleapis.com/envoy.extensions.filters.http.custom_response.v3.CustomResponse"}},
      {"name": "envoy.filters.http.ext_authz", "typed_config": {"@type": "type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz",
        **({"grpc_service": {"envoy_grpc": {"cluster_name": "authz_grpc"}, "timeout": "2s"}, "transport_api_version": "V3"} if mode == 'grpc' else {}),
        **({} if mode == 'grpc' else {"http_service": {"server_uri": {"uri": "http://198.20.0.11:9001", "cluster": "authz_http", "timeout": "2s"}, "path_prefix": "/check",
          "authorization_response": {"allowed_upstream_headers": {"patterns": [{"exact": "x-lenny-route"}, {"exact": "proxy-authorization"}]},
            "allowed_client_headers": {"patterns": [{"exact": "x-lenny-deny-reason"}]},
            "dynamic_metadata_from_headers": {"patterns": [{"exact": "x-lenny-upstream"}]}}}}),
        "allowed_headers": {"patterns": [{"exact": "proxy-authorization"}]},
        "failure_mode_allow": False, "clear_route_cache": True, "status_on_error": {"code": "ServiceUnavailable"}}},
      {"name": "envoy.filters.http.router", "typed_config": {"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router", "suppress_envoy_headers": True}}],
     "route_config": {"name": "egress", "virtual_hosts": [{"name": "all", "domains": ["*"], "routes": [
        route("outbound", "outbound_tls"), route("outbound-tsm", "outbound_tsm"), route("outbound-plain", "outbound_plain"),
        {"match": {"connect_matcher": {}}, "direct_response": {"status": 403, "body": {"inline_string": "no route decision\n"}}},
        {"match": {"prefix": "/"}, "direct_response": {"status": 405, "body": {"inline_string": "CONNECT only\n"}}}]}]}}}]}]}],
  "clusters": [
   {"name": "authz_http", "type": "STATIC", "connect_timeout": "1s", "typed_extension_protocol_options": {"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": {"@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions", "explicit_http_config": {"http2_protocol_options": {}}}}, "load_assignment": {"cluster_name": "authz_http", "endpoints": [{"lb_endpoints": [{"endpoint": {"address": {"socket_address": {"address": "198.20.0.11", "port_value": 9001}}}}]}]}},
   {"name": "authz_grpc", "type": "STATIC", "connect_timeout": "1s", "typed_extension_protocol_options": {"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": {"@type": "type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions", "explicit_http_config": {"http2_protocol_options": {}}}}, "load_assignment": {"cluster_name": "authz_grpc", "endpoints": [{"lb_endpoints": [{"endpoint": {"address": {"socket_address": {"address": "198.20.0.12", "port_value": 9000}}}}]}]}},
   od_cluster("outbound_tls", ts=tls_socket()),
   od_cluster("outbound_tsm", tsm=[{"name": "tls", "match": {"tls": "true"}, "transport_socket": tls_socket()}]),
   od_cluster("outbound_plain")]}}
json.dump(cfg, open(out, 'w'), indent=1)
