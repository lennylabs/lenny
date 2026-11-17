#!/bin/bash
docker rm -f $(docker ps -aq --filter name=^r4-) 2>/dev/null >/dev/null
docker network rm sr4-pub 2>/dev/null >/dev/null
echo "remaining r4 containers:"; docker ps -a --filter name=^r4- --format '{{.Names}}'; echo "remaining sr4 networks:"; docker network ls --filter name=sr4- --format '{{.Name}}'
