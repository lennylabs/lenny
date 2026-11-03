FROM debian:trixie-slim
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends procps util-linux >/dev/null
COPY probe.sh /probe.sh
