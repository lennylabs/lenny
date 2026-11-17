#!/bin/bash
W=spike-federation
docker rm -f spikefed-srv >/dev/null 2>&1
docker run -d --name spikefed-srv --network spikefed-net \
  --network-alias sts.aws.test --network-alias dynamodb.aws.test --network-alias sts.gcp.test --network-alias iamcredentials.gcp.test \
  --network-alias api.gcp.test --network-alias login.azure.test --network-alias api.azure.test --network-alias msi.azure.test --network-alias metadata.gcp.test \
  --network-alias cloudresourcemanager.googleapis.com \
  -v $W/server:/srv:ro -v $W/certs:/certs:ro -v $W/logs:/logs -e EXP=${EXP:-20} -e CRM_STATUS=${CRM_STATUS:-200} spikefed-py:t python -u /srv/fakesrv.py >/dev/null
sleep 1.5; docker logs spikefed-srv
