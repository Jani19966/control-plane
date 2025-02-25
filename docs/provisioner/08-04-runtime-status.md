---
title: Check Runtime Status
type: Tutorials
---

This tutorial shows how to check the Runtime status.

## Steps

> **NOTE:** To access Runtime Provisioner, forward the port on which the GraphQL server is listening.

Make a call to Runtime Provisioner with a **tenant** header to check the Runtime status. Pass the Runtime ID as `id`. 

```graphql
query { runtimeStatus(id: "{RUNTIME_ID}") {
    lastOperationStatus {
      id operation state message runtimeID 
  	} 
    runtimeConnectionStatus { 
      status errors {
        message
      } 
    } 

    runtimeConfiguration {
      clusterConfig {
        name 
        workerCidr
        region 
        diskType 
        maxSurge 
        volumeSizeGB 
        machineType 
        targetSecret 
        autoScalerMin 
        autoScalerMax 
        provider 
        maxUnavailable 
        kubernetesVersion
        euAccess
      }
      kymaConfig {
        version  
        components {
          component
          namespace 
          configuration {
            key
            value
            secret
          }
          sourceURL
        }
        configuration {
          key 
          value 
          secret
        }
      }
    	kubeconfig
    } 
	} 
}
```

An example response for a successful request looks like this:

```json
{
  "data": {
    "runtimeStatus": {
      "lastOperationStatus": {
        "id": "20ed1cfb-7407-4ec5-89af-c550eb0fce49",
        "operation": "Provision",
        "state": "Succeeded",
        "message": "Operation succeeded.",
        "runtimeID": "b70accda-4008-466c-96ec-9b42c2cfd264"
      },
      "runtimeConnectionStatus": {
        "status": "Pending",
        "errors": null
      },
      "runtimeConfiguration": {
        "clusterConfig": {CLUSTER_CONFIG},
        "kymaConfig": {
          "version": "1.12.0",
          "components": [{COMPONENTS_LIST}]
        },
        "kubeconfig": {KUBECONFIG}
      }
    }
  }
}
``` 