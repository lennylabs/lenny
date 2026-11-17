#!/usr/bin/env python3
"""alsify.py IN OUT : add the gRPC access log, flushed at tunnel establishment and at an interval, with an ALS cluster."""
import json,sys
b=json.load(open(sys.argv[1]))
for l in b["static_resources"]["listeners"]:
    if l.get("name")=="egress":
        h=l["filter_chains"][0]["filters"][0]["typed_config"]
        h["access_log"]=[{"name":"envoy.access_loggers.http_grpc","typed_config":{
          "@type":"type.googleapis.com/envoy.extensions.access_loggers.grpc.v3.HttpGrpcAccessLogConfig",
          "common_config":{"log_name":"egress","transport_api_version":"V3","buffer_flush_interval":"1s","buffer_size_bytes":16384,
            "grpc_service":{"envoy_grpc":{"cluster_name":"als"}},"filter_state_objects_to_log":[]}}}]
        h["access_log_options"]={"flush_log_on_tunnel_successfully_established":True,"access_log_flush_interval":"60s"}
b["static_resources"]["clusters"].append({"name":"als","type":"STATIC","connect_timeout":"1s",
  "typed_extension_protocol_options":{"envoy.extensions.upstreams.http.v3.HttpProtocolOptions":{"@type":"type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions","explicit_http_config":{"http2_protocol_options":{}}}},
  "load_assignment":{"cluster_name":"als","endpoints":[{"lb_endpoints":[{"endpoint":{"address":{"socket_address":{"address":"198.19.0.11","port_value":9003}}}}]}]}})
json.dump(b,open(sys.argv[2],"w"),indent=1)
