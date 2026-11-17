#!/bin/bash
. /i/clients/urls.sh
node -e 'console.error("node",process.version,"openssl",process.versions.openssl)'; npm --version >&2
NODE_EXTRA_CA_CERTS=/c/bundle.pem node /i/clients/node.js $URLS | sed 's/^NAME/node-fetch-NODE_EXTRA_CA_CERTS/'
SSL_CERT_FILE=/c/bundle.pem node --use-openssl-ca /i/clients/node.js $URLS | sed 's/^NAME/node-fetch-use-openssl-ca/'
for u in $URLS; do cli npm-NODE_EXTRA_CA_CERTS $u env NODE_EXTRA_CA_CERTS=/c/bundle.pem npm view oapkg version --registry $u --fetch-retries 0 --fetch-timeout 6000 --no-update-notifier; done
for u in $URLS; do cli npm-cafile $u npm view oapkg version --registry $u --cafile /c/bundle.pem --fetch-retries 0 --fetch-timeout 6000 --no-update-notifier; done
