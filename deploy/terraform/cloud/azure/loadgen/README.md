# Azure loadgen module

Provisions the tier-12 load-runner pool on Azure:

- Virtual Machine Scale Set of `Standard_F8s_v2` instances in the AKS VNet, on private subnets.
- Service Bus queue the runners pull jobs from.
- Managed identity granting the runners Service Bus receive on the queue plus Blob Storage create on the load-reports container.
- A metrics-collector instance polling Azure Monitor and exposing Prometheus-format metrics.
- Azure Private Endpoint to the AKS cluster's gateway service.

Wave 5 cut: input/output shape and resource scaffolding. Wave 6 wires the real autoscaling configuration, the runner image bake step, and the Private Endpoint.

## Inputs

| Name | Type | Description |
|:--|:--|:--|
| `release` | string | Lenny release name; used as the resource prefix. |
| `resource_group_name` | string | Resource group the resources land in. |
| `location` | string | Azure region. |
| `subnet_id` | string | Subnet ID the VMSS places instances in. |
| `instance_type` | string | VM size. Default `Standard_F8s_v2`. |
| `capacity` | number | Initial VMSS instance count. Default 2. |
| `runner_image_id` | string | Compute Gallery image ID of the `lenny-loadrunner` image. |
| `reports_container` | string | Blob Storage container the runners write per-runner k6 JSON to. |
| `storage_account_name` | string | Blob Storage account hosting `reports_container`. |

## Outputs

| Name | Description |
|:--|:--|
| `servicebus_queue_name` | Service Bus queue name the runners pull from. |
| `servicebus_namespace_name` | Service Bus namespace; used by the loadctl module. |
| `runner_identity_id` | Managed identity ID assigned to the VMSS. |
| `vmss_name` | VMSS name; used by `down-loadgen.sh` to scale to zero. |
