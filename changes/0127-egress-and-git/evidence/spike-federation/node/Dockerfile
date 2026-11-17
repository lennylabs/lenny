FROM node:24-slim
WORKDIR /deps
RUN npm init -y >/dev/null && npm install --no-audit --no-fund @aws-sdk/client-dynamodb @aws-sdk/credential-providers google-auth-library @azure/identity && npm ls --depth=0 > /versions.txt && npm ls --all 2>/dev/null | grep -E 'nested-clients|client-sts|gcp-metadata|msal-node|credential-provider-(ini|web-identity|node)' | sort -u >> /versions.txt
ENV NODE_PATH=/deps/node_modules
WORKDIR /app
