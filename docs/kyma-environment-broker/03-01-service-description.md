# Service description

Kyma Environment Broker (KEB) is compatible with the [Open Service Broker API](https://www.openservicebrokerapi.org/) (OSBA) specification. It provides a ServiceClass that provisions Kyma Runtime on a cluster.

## Service plans

The supported plans are as follows:

| Plan name | Plan ID | Description |
|-----------|---------|-------------|
| `azure` | `4deee563-e5ec-4731-b9b1-53b42d855f0c` |Installs Kyma Runtime on the Azure cluster. |
| `azure_lite` | `8cb22518-aa26-44c5-91a0-e669ec9bf443` | Installs Kyma Lite on the Azure cluster. |
| `aws` | `361c511f-f939-4621-b228-d0fb79a1fe15` | Installs Kyma Runtime on the AWS cluster. |
| `openstack` | `03b812ac-c991-4528-b5bd-08b303523a63` | Installs Kyma Runtime on the Openstack cluster. |
| `gcp` | `ca6e5357-707f-4565-bbbd-b3ab732597c6` | Installs Kyma Runtime on the GCP cluster. |
| `trial` | `7d55d31d-35ae-4438-bf13-6ffdfa107d9f` | Installs Kyma trial plan on Azure, AWS or GCP. |
| `free` | `b1a5764e-2ea1-4f95-94c0-2b4538b37b55` | Installs Kyma free plan on Azure or AWS. |
| `own_cluster` | `b1a5764e-2ea1-4f95-94c0-2b4538b37b55` | Installs Kyma on custom K8S cluster. |
| `preview` | `5cb3d976-b85c-42ea-a636-79cadda109a9` | Installs Kyma on AWS using Lifecycle Manager. |

## Provisioning parameters

There are two types of configurable provisioning parameters: the ones that are compliant for all providers and provider-specific ones.

### Parameters compliant for all providers

These are the provisioning parameters that you can configure:

| Parameter name | Type | Description | Required | Default value |
|----------------|-------|-------------|:----------:|---------------|
| **name** | string | Specifies the name of the cluster. | Yes | None |
| **components** | array | Defines optional components that are installed in a Kyma Runtime. The possible values are `kiali` and `tracing`. | No | [] |
| **kymaVersion** | string | Provides a Kyma version on demand. | No | None |
| **overridesVersion** | string | Provides an overrides version for a specific Kyma version. | No | None |
| **purpose** | string | Provides a purpose for an SKR. | No | None |
| **targetSecret** | string | Provides the name of the Secret that contains hyperscaler's credentials for an SKR. | No | None |
| **platform_region** | string | Defines the platform region that is sent in the request path. | No | None |
| **platform_provider** | string | Defines the platform provider for an SKR. | No | None |
| **context.tenant_id** | string | Provides a tenant ID for an SKR. | No | None |
| **context.subaccount_id** | string | Provides a subaccount ID for an SKR. | No | None |
| **context.globalaccount_id** | string | Provides a global account ID for an SKR. | No | None |
| **context.sm_platform_credentials.credentials.basic.username** | string | Provides the Service Manager username for an SKR. | No | None |
| **context.sm_platform_credentials.credentials.basic.password** | string | Provides the Service Manager password for an SKR. | No | None |
| **context.sm_platform_credentials.url** | string | Provides the Service Manager URL for an SKR. | No | None |
| **context.user_id** | string | Provides a user ID for an SKR. | No | None |
| **oidc.clientID** | string | Provides an OIDC client ID for an SKR. | No | None |
| **oidc.groupsClaim** | string | Provides an OIDC groups claim for an SKR. | No | `groups` |
| **oidc.issuerURL** | string | Provides an OIDC issuer URL for an SKR. | No | None |
| **oidc.signingAlgs** | string | Provides the OIDC signing algorithms for an SKR. | No | `RS256` |
| **oidc.usernameClaim** | string | Provides an OIDC username claim for an SKR. | No | `email` |
| **oidc.usernamePrefix** | string | Provides an OIDC username prefix for an SKR. | No | None |

### Provider-specific parameters

These are the provisioning parameters for Azure that you can configure:

<div tabs name="azure-plans" group="azure-plans">
  <details>
  <summary label="azure-plan">
  Azure
  </summary>

| Parameter name | Type | Description | Required | Default value                                 |
| ---------------|-------|-------------|:----------:|-----------------------------------------------|
| **machineType** | string | Specifies the provider-specific virtual machine type. | No | `Standard_D8_v3`                              |
| **volumeSizeGb** | int | Specifies the size of the root volume. | No | `50`                                          |
| **region** | string | Defines the cluster region. | No | `eastus` or `switzerlandnorth` for EU Access  |
| **zones** | string | Defines the list of zones in which Runtime Provisioner creates a cluster. | No | `["1"]`                                       |
| **autoScalerMin[<sup>1</sup>](#update)** | int | Specifies the minimum number of virtual machines to create. | No | `2`                                           |
| **autoScalerMax[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines to create, up to `40` allowed. | No | `10`                                          |
| **maxSurge[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines that are created during an update. | No | `4`                                           |
| **maxUnavailable[<sup>1</sup>](#update)** | int | Specifies the maximum number of VMs that can be unavailable during an update. | No | `1`                                           |

  </details>
  <details>
  <summary label="azure-lite-plan">
  Azure Lite
  </summary>

| Parameter name | Type | Description | Required | Default value                                |
| ---------------|-------|-------------|:----------:|----------------------------------------------|
| **machineType** | string | Specifies the provider-specific virtual machine type. | No | `Standard_D4_v3`                             |
| **volumeSizeGb** | int | Specifies the size of the root volume. | No | `50`                                         |
| **region** | string | Defines the cluster region. | No | `eastus` or `switzerlandnorth` for EU Access |
| **zones** | string | Defines the list of zones in which Runtime Provisioner creates a cluster. | No | `["1"]`                                      |
| **autoScalerMin[<sup>1</sup>](#update)** | int | Specifies the minimum number of virtual machines to create. | No | `2`                                          |
| **autoScalerMax[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines to create, up to `40` allowed. | No | `10`                                         |
| **maxSurge[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines that are created during an update. | No | `4`                                          |
| **maxUnavailable[<sup>1</sup>](#update)** | int | Specifies the maximum number of VMs that can be unavailable during an update. | No | `1`                                          |

 </details>
 </div>

These are the provisioning parameters for AWS that you can configure:
<div tabs name="aws-plans" group="aws-plans">
  <details>
  <summary label="aws-plan">
  AWS
  </summary>

| Parameter name | Type | Description | Required | Default value |
| ---------------|-------|-------------|:----------:|---------------|
| **machineType** | string | Specifies the provider-specific virtual machine type. | No | `m5.2xlarge` |
| **volumeSizeGb** | int | Specifies the size of the root volume. | No | `50` |
| **region** | string | Defines the cluster region. | No | `eu-central-1` |
| **zones** | string | Defines the list of zones in which Runtime Provisioner creates a cluster. | No | `["1"]` |
| **autoScalerMin[<sup>1</sup>](#update)** | int | Specifies the minimum number of virtual machines to create. | No | `3` |
| **autoScalerMax[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines to create, up to `40` allowed. | No | `10` |
| **maxSurge[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines that are created during an update. | No | `4` |
| **maxUnavailable[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines that can be unavailable during an update. | No | `1` |

  </details>
 </div>

These are the provisioning parameters for GCP that you can configure:

<div tabs name="gcp-plans" group="gcp-plans">
  <details>
  <summary label="gcp-plan">
  GCP
  </summary>

| Parameter name | Type | Description | Required | Default value |
| ---------------|-------|-------------|:----------:|---------------|
| **machineType** | string | Specifies the provider-specific virtual machine type. | No | `n2-standard-8` |
| **volumeSizeGb** | int | Specifies the size of the root volume. | No | `30` |
| **region** | string | Defines the cluster region. | No | `europe-west3` |
| **zones** | string | Defines the list of zones in which Runtime Provisioner creates a cluster. | No | `["a"]` |
| **autoScalerMin[<sup>1</sup>](#update)** | int | Specifies the minimum number of virtual machines to create. | No | `3` |
| **autoScalerMax[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines to create. | No | `4` |
| **maxSurge[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines that are created during an update. | No | `4` |
| **maxUnavailable[<sup>1</sup>](#update)** | int | Specifies the maximum number of VMs that can be unavailable during an update. | No | `1` |

 </details>
 </div>

These are the provisioning parameters for Openstack that you can configure:

<div tabs name="openstack-plans" group="openstack-plans">
  <details>
  <summary label="openstack-plan">
  Openstack
  </summary>

| Parameter name | Type | Description | Required | Default value |
| ---------------|-------|-------------|:----------:|---------------|
| **machineType** | string | Specifies the provider-specific virtual machine type. | No | `m2.xlarge` |
| **volumeSizeGb** | int | Specifies the size of the root volume. | No | `30` |
| **region** | string | Defines the cluster region. | No | `europe-west4` |
| **zones** | string | Defines the list of zones in which Runtime Provisioner creates a cluster. | No | `["a"]` |
| **autoScalerMin[<sup>1</sup>](#update)** | int | Specifies the minimum number of virtual machines to create. | No | `2` |
| **autoScalerMax[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines to create. | No | `10` |
| **maxSurge[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines that are created during an update. | No | `4` |
| **maxUnavailable[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines that can be unavailable during an update. | No | `1` |

 </details>
 </div>


## Trial plan

Trial plan allows you to install Kyma on Azure, AWS, or GCP. The trial plan assumptions are as follows:
- Kyma is uninstalled after 14 days and the Kyma cluster is deprovisioned after this time.
- It's possible to provision only one Kyma Runtime per global account.

To reduce the costs, the Trial plan skips one of the [provisioning steps](./03-03-runtime-operations.md#provisioning).

- `AVS External Evaluation` 

### Provisioning parameters

These are the provisioning parameters for the Trial plan that you can configure:

<div tabs name="trial-plan" group="trial-plan">
  <details>
  <summary label="trial-plan">
  Trial plan
  </summary>

| Parameter name | Type | Description | Required | Possible values| Default value |
| ---------------|-------|-------------|----------|---------------|---------------|
| **name** | string | Specifies the name of the Kyma Runtime. | Yes | Any string| None |
| **region** | string | Defines the cluster region. | No | `europe`,`us`, `asia` | Calculated from the platform region |
| **provider** | string | Specifies the cloud provider used during provisioning. | No | `Azure`, `AWS`, `GCP` | `Azure` |
| **context.active** | string | Specifies if the SKR should be suspended or unsuspended. | `true`, `false` | None |

The **region** parameter is optional. If not specified, the region is calculated from platform region specified in this path:
```shell
/oauth/{platform-region}/v2/service_instances/{instance_id}
```
The mapping between the platform region and the provider region (Azure, AWS or GCP) is defined in the configuration file in the **APP_TRIAL_REGION_MAPPING_FILE_PATH** environment variable. If the platform region is not defined, the default value is `europe`.

 </details>
 </div>

## Own cluster plan

These are the provisioning parameters for the `own_cluster` plan that you configure:

<div tabs name="own_cluster-plan" group="own_cluster-plan">
  <details>
  <summary label="own_cluster-plan">
  Own cluster plan
  </summary>

| Parameter name | Type | Description | Required | Default value |
| ---------------|-------|-------------|----------|---------------|
| **kubeconfig** | string | Kubeconfig that points to the cluster where you install Kyma. | Yes | None |
| **shootDomain** | string | Domain of the shoot where you install Kyma. | Yes | None |
| **shootName** | string | Name of the shoot where you install Kyma. | Yes | None |

</details>
</div>

## Preview cluster plan

The preview plan allows to test integration with Lifecycle Manager. The preview plan skips steps which integrate Kyma Environment Broker and Reconciler.

### Provisioning parameters

These are the provisioning parameters for the `preview` plan that you configure:

<div tabs name="preview_cluster-plan" group="preview_cluster-plan">
  <details>
  <summary label="preview_cluster-plan">
  Preview cluster plan
  </summary>

| Parameter name | Type | Description | Required | Default value |
| ---------------|-------|-------------|:----------:|---------------|
| **machineType** | string | Specifies the provider-specific virtual machine type. | No | `m5.2xlarge` |
| **volumeSizeGb** | int | Specifies the size of the root volume. | No | `50` |
| **region** | string | Defines the cluster region. | No | `westeurope` |
| **zones** | string | Defines the list of zones in which Runtime Provisioner creates a cluster. | No | `["1"]` |
| **autoScalerMin[<sup>1</sup>](#update)** | int | Specifies the minimum number of virtual machines to create. | No | `3` |
| **autoScalerMax[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines to create, up to `40` allowed. | No | `10` |
| **maxSurge[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines that are created during an update. | No | `4` |
| **maxUnavailable[<sup>1</sup>](#update)** | int | Specifies the maximum number of virtual machines that can be unavailable during an update. | No | `1` |

</details>
</div>

