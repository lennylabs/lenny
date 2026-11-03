import boto3, botocore, logging, os
from common import swap, sleep
D = "/var/run/netagent/aws"
swap(D, 1)
s = boto3.Session()
print("region:", s.region_name, "profile:", s.profile_name)
c = s.client("dynamodb")
print("endpoint:", c.meta.endpoint_url)
print("call1 ->", c.list_tables()["TableNames"])
swap(D, 2); sleep()
print("call2 ->", c.list_tables()["TableNames"])
