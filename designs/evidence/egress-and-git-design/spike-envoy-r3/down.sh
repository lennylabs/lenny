#!/bin/bash
docker rm -f $(docker ps -aq --filter name=^r3-) 2>/dev/null >/dev/null
docker network rm sr3-pub sr3-priv sr3-v6only 2>/dev/null >/dev/null
docker ps -a --filter name=^r3- --format '{{.Names}}'; docker network ls --filter name=sr3- --format '{{.Name}}'
