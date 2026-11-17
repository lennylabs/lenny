FROM python:3.13-slim
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends git wget curl gnutls-bin libnss3-tools ca-certificates openssl >/dev/null
RUN pip install -q requests 'httpx[http2]' aiohttp
