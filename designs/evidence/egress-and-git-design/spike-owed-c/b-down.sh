#!/bin/bash
docker ps -a --format '{{.Names}}' | grep '^oc-' | xargs docker rm -f >/dev/null 2>&1; docker network ls --format '{{.Name}}' | grep '^oc-' | xargs docker network rm >/dev/null 2>&1; docker volume ls --format '{{.Name}}' | grep '^oc-' | xargs docker volume rm >/dev/null 2>&1; echo down
