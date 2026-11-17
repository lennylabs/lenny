#!/usr/bin/env python3
"""envoycfg.py IN OUT [mcd=30s]: on the gen5 bootstrap, add downstream TLS through SDS (tlsify), the readiness listener, and I3 mTLS with the client certificate and the CA both through SDS on watched directories."""
import json,sys
kv=dict(a.split("=",1) for a in sys.argv[3:])
b=json.load(open(sys.argv[1]))
sds=lambda name,path,d:{"name":name,"sds_config":{"resource_api_version":"V3","path_config_source":{"path":path,"watched_directory":{"path":d}}}}
ctx={"name":"envoy.transport_sockets.tls","typed_config":{
  "@type":"type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext",
  "common_tls_context":{"alpn_protocols":["http/1.1"],"tls_params":{"tls_minimum_protocol_version":"TLSv1_2"},
    "tls_certificate_sds_secret_configs":[sds("server","/sds/sds.yaml","/sds")]}}}
for l in b["static_resources"]["listeners"]:
    if l["name"]=="egress":
        for fc in l["filter_chains"]: fc["transport_socket"]=ctx
b["static_resources"]["listeners"].append({"name":"tls_ready","address":{"socket_address":{"address":"0.0.0.0","port_value":3443}},
  "filter_chains":[{"transport_socket":ctx,"filters":[{"name":"envoy.filters.network.http_connection_manager","typed_config":{
    "@type":"type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager","stat_prefix":"tls_ready",
    "http_filters":[{"name":"envoy.filters.http.router","typed_config":{"@type":"type.googleapis.com/envoy.extensions.filters.http.router.v3.Router"}}],
    "route_config":{"name":"r","virtual_hosts":[{"name":"h","domains":["*"],"routes":[{"match":{"path":"/healthz"},"direct_response":{"status":200,"body":{"inline_string":"ok\n"}}}]}]}}}]}]})
for c in b["static_resources"]["clusters"]:
    if c["name"]=="authz":
        c["transport_socket"]={"name":"envoy.transport_sockets.tls","typed_config":{
          "@type":"type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
          "sni":"lenny-egress-authz.lenny-system.svc",
          "common_tls_context":{"alpn_protocols":["h2"],
            "tls_certificate_sds_secret_configs":[sds("server","/sds/sds.yaml","/sds")],
            "combined_validation_context":{
              "default_validation_context":{"match_typed_subject_alt_names":[{"san_type":"DNS","matcher":{"exact":"lenny-egress-authz.lenny-system.svc"}}]},
              "validation_context_sds_secret_config":sds("egress-ca","/sds/sds-ca.yaml","/sds")}}}}
        if "mcd" in kv:
            c["typed_extension_protocol_options"]["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]["common_http_protocol_options"]={"max_connection_duration":kv["mcd"]}
b["node"]={"id":"lenny-egress","cluster":"lenny-egress"}
json.dump(b,open(sys.argv[2],"w"),indent=1)
