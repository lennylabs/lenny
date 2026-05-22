# Azure loadctl module

Provisions the tier-12 control plane on Azure:

- Container Apps environment hosting the `lenny-loadctl` container image.
- Flexible Server Postgres for run-state persistence.
- Managed certificate for the Container Apps ingress.
- User-assigned managed identity granting Service Bus `Sender` on the loadgen queue and Blob Storage `Contributor` on the reports container.

## Outputs

| Name | Description |
|:--|:--|
| `service_fqdn` | Container Apps ingress hostname. |
| `db_fqdn` | Flexible Server hostname. |
| `identity_id` | User-assigned managed identity. |
