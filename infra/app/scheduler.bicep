param name string
param location string = resourceGroup().location
param tags object = {}
param taskHubName string
param managedIdentityPrincipalId string
param userIdentityPrincipalId string = ''

resource scheduler 'Microsoft.DurableTask/schedulers@2025-11-01' = {
  name: name
  location: location
  tags: tags
  properties: {
    ipAllowlist: []
    sku: {
      name: 'Consumption'
    }
  }
}

resource taskHub 'Microsoft.DurableTask/schedulers/taskHubs@2025-11-01' = {
  parent: scheduler
  name: taskHubName
  properties: {}
}

var contributorRoleId = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '0ad04412-c4d5-4796-b79c-f76d14c8d402')

resource appRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(taskHub.id, managedIdentityPrincipalId, contributorRoleId)
  scope: taskHub
  properties: {
    principalId: managedIdentityPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: contributorRoleId
  }
}

resource developerRole 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!empty(userIdentityPrincipalId)) {
  name: guid(taskHub.id, userIdentityPrincipalId, contributorRoleId)
  scope: taskHub
  properties: {
    principalId: userIdentityPrincipalId
    roleDefinitionId: contributorRoleId
  }
}

output connectionString string = 'Endpoint=${scheduler.properties.endpoint};Authentication=DefaultAzure;TaskHub=${taskHubName}'
output dashboardUrl string = taskHub.properties.dashboardUrl
