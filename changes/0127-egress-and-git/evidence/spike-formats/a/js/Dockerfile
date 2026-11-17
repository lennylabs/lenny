FROM node:24-slim
WORKDIR /app
RUN npm init -y >/dev/null && npm install @aws-sdk/client-s3 >/dev/null
COPY loop.mjs /app/loop.mjs
