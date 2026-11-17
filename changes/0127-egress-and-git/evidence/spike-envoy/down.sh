#!/bin/bash
docker rm -f se-envoy se-authz se-proxya se-proxyb se-proxya-auth se-target se-internal se-client 2>/dev/null
docker network rm spike-envoy-pub spike-envoy-priv 2>/dev/null
docker ps -a --filter name=se- --format '{{.Names}}'; docker network ls --filter name=spike-envoy --format '{{.Name}}'
