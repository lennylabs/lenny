# 17. Deployment Topology

## 17.4 Object storage

The policy document below is written to a local file and passed to the cloud
command as a file argument.

    az storage account management-policy create --account-name acme --policy @lenny-lifecycle.json
