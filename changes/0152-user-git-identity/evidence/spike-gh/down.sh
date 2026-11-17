#!/bin/bash
# down.sh: removes every gh- container, network, volume, and image, and nothing else.
for c in $(docker ps -aq --filter name=^gh-); do docker rm -f $c >/dev/null; done
for n in $(docker network ls -q --filter name=^gh-); do docker network rm $n >/dev/null; done
for v in $(docker volume ls -q --filter name=^gh-); do docker volume rm $v >/dev/null; done
for i in $(docker images --format '{{.Repository}}:{{.Tag}}' | grep '^gh-'); do docker rmi -f $i >/dev/null; done
echo "containers: $(docker ps -a --format '{{.Names}}' | grep -c '^gh-')  networks: $(docker network ls --format '{{.Name}}' | grep -c '^gh-')  volumes: $(docker volume ls --format '{{.Name}}' | grep -c '^gh-')  images: $(docker images --format '{{.Repository}}' | grep -c '^gh-')"
