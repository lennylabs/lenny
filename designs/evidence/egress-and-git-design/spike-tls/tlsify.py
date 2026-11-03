#!/usr/bin/env python3
"""tlsify.py IN OUT: add a downstream TLS context to the external listener, with the certificate through SDS on a watched directory (the Kubernetes Secret mount form)."""
import json,sys
b=json.load(open(sys.argv[1]))
for l in b["static_resources"]["listeners"]:
    if l.get("name")=="egress":
        for fc in l["filter_chains"]:
            fc["transport_socket"]={"name":"envoy.transport_sockets.tls","typed_config":{
              "@type":"type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.DownstreamTlsContext",
              "common_tls_context":{"alpn_protocols":["http/1.1"],
                "tls_params":{"tls_minimum_protocol_version":"TLSv1_2"},
                "tls_certificate_sds_secret_configs":[{"name":"server","sds_config":{"resource_api_version":"V3",
                  "path_config_source":{"path":"/sds/sds.yaml","watched_directory":{"path":"/sds"}}}}]}}}
if "i3" in sys.argv or "i3m" in sys.argv:
    for c in b["static_resources"]["clusters"]:
        if c["name"]=="authz":
            c["transport_socket"]={"name":"envoy.transport_sockets.tls","typed_config":{
              "@type":"type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
              "sni":"lenny-egress-authz.lenny-system.svc",
              "common_tls_context":{"alpn_protocols":["h2"],"tls_certificates":[{"certificate_chain":{"filename":"/certs/live/tls.crt"},"private_key":{"filename":"/certs/live/tls.key"}}] if "i3m" in sys.argv else [],"validation_context":{"trusted_ca":{"filename":"/certs/ca1.pem"},
                "match_typed_subject_alt_names":[{"san_type":"DNS","matcher":{"exact":"lenny-egress-authz.lenny-system.svc"}}]}}}}
if "ready" in sys.argv:
    ctx=[l for l in b["static_resources"]["listeners"] if l.get("name")=="egress"][0]["filter_chains"][0]["transport_socket"]
    b["static_resources"]["listeners"].append({"name":"tls_ready","address":{"socket_address":{"address":"0.0.0.0","port_value":3443}},
      "filter_chains":[{"transport_socket":ctx,"filters":[{"name":"envoy.filters.network.http_connection_manager","typed_config":{
        "@type":"type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager","stat_prefix":"tls_ready",
        "http_filters":[{"name":"envoy.filters.http.router","typed_config":{"@type":"type.googleapis.com/envoy.extensions.filters.http.router.v3.Router"}}],
        "route_config":{"name":"r","virtual_hosts":[{"name":"h","domains":["*"],"routes":[{"match":{"path":"/healthz"},"direct_response":{"status":200,"body":{"inline_string":"ok\n"}}}]}]}}}]}]})
b["node"]={"id":"lenny-egress","cluster":"lenny-egress"}
json.dump(b,open(sys.argv[2],"w"),indent=1)
