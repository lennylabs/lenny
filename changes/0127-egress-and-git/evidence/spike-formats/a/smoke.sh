#!/bin/bash
cd "$(dirname "$0")"; . ./env.sh
echo "== CLI v2"; docker run --rm --label spike-formats=1 --network container:spike-cred-cli2 -v $PWD/tok-cli2:/tok "${AENV[@]}" spike-a-cli2 sh -c 'aws --version; aws s3api list-buckets --query "Buckets[].Name" --output text; printf tok2-cli2 > /tok/token; aws s3api list-buckets --query "Buckets[].Name" --output text'
echo "== CLI v1"; docker run --rm --label spike-formats=1 --network container:spike-cred-py -v $PWD/tok-py:/tok "${AENV[@]}" spike-a-py sh -c 'aws --version; aws s3api list-buckets --query "Buckets[].Name" --output text'
