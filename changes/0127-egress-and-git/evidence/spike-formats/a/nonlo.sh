#!/bin/bash
cd "$(dirname "$0")"
URI=${1:-http://cred-nonlo:8080/creds}
E=(-e AWS_CONTAINER_CREDENTIALS_FULL_URI=$URI -e AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE=/tok/token -e AWS_ENDPOINT_URL=http://fakes3:9000 -e AWS_REGION=us-east-1 -e AWS_EC2_METADATA_DISABLED=true -e LOOP_SECONDS=0)
R=(docker run --rm --label spike-formats=1 --network spike-net -v $PWD/tok-nonlo:/tok:ro "${E[@]}")
echo "## FULL_URI=$URI"
echo "-- boto3"; "${R[@]}" spike-a-py python -u /loop.py 2>&1 | tail -1
echo "-- cli v1"; "${R[@]}" spike-a-py aws s3api list-buckets --query 'Buckets[].Name' --output text 2>&1 | tail -2
echo "-- cli v2"; "${R[@]}" spike-a-cli2 aws s3api list-buckets --query 'Buckets[].Name' --output text 2>&1 | tail -2
echo "-- go";  "${R[@]}" spike-a-go /loop 2>&1 | tail -1
echo "-- js";  "${R[@]}" spike-a-js node /app/loop.mjs 2>&1 | tail -1
echo "-- java"; "${R[@]}" spike-a-java java -cp '/app/spike.jar:/app/lib/*' spike.Main 2>&1 | grep -v '^\[' | tail -1
