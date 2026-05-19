package ecs

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateCluster": op.NewTyped[createClusterRequest, createClusterResponse](
			"CreateCluster", h.createClusterTyped,
		),
		"DescribeClusters": op.NewTyped[describeClustersRequest, describeClustersResponse](
			"DescribeClusters", h.describeClustersTyped,
		),
		"ListClusters": op.NewTyped[struct{}, listClustersResponse](
			"ListClusters", h.listClustersTyped,
		),
		"DeleteCluster": op.NewTyped[deleteClusterRequest, deleteClusterResponse](
			"DeleteCluster", h.deleteClusterTyped,
		),
		"UpdateCluster": op.NewTyped[updateClusterRequest, updateClusterResponse](
			"UpdateCluster", h.updateClusterTyped,
		),
		"UpdateClusterSettings": op.NewTyped[updateClusterSettingsRequest, updateClusterSettingsResponse](
			"UpdateClusterSettings", h.updateClusterSettingsTyped,
		),
		"RegisterTaskDefinition": op.NewTyped[registerTaskDefinitionRequest, registerTaskDefinitionResponse](
			"RegisterTaskDefinition", h.registerTaskDefinitionTyped,
		),
		"DescribeTaskDefinition": op.NewTyped[taskDefinitionRefRequest, describeTaskDefinitionResponse](
			"DescribeTaskDefinition", h.describeTaskDefinitionTyped,
		),
		"ListTaskDefinitions": op.NewTyped[listTaskDefinitionsRequest, listTaskDefinitionsResponse](
			"ListTaskDefinitions", h.listTaskDefinitionsTyped,
		),
		"DeregisterTaskDefinition": op.NewTyped[taskDefinitionRefRequest, deregisterTaskDefinitionResponse](
			"DeregisterTaskDefinition", h.deregisterTaskDefinitionTyped,
		),
		"ListTaskDefinitionFamilies": op.NewTyped[listTaskDefinitionFamiliesRequest, listTaskDefinitionFamiliesResponse](
			"ListTaskDefinitionFamilies", h.listTaskDefinitionFamiliesTyped,
		),
		"RunTask": op.NewTyped[runTaskRequest, runTaskResponse](
			"RunTask", h.runTaskTyped,
		),
		"StopTask": op.NewTyped[stopTaskRequest, stopTaskResponse](
			"StopTask", h.stopTaskTyped,
		),
		"DescribeTasks": op.NewTyped[describeTasksRequest, describeTasksResponse](
			"DescribeTasks", h.describeTasksTyped,
		),
		"ListTasks": op.NewTyped[listTasksRequest, listTasksResponse](
			"ListTasks", h.listTasksTyped,
		),
		"CreateService": op.NewTyped[createServiceRequest, createServiceResponse](
			"CreateService", h.createServiceTyped,
		),
		"UpdateService": op.NewTyped[updateServiceRequest, updateServiceResponse](
			"UpdateService", h.updateServiceTyped,
		),
		"DeleteService": op.NewTyped[deleteServiceRequest, deleteServiceResponse](
			"DeleteService", h.deleteServiceTyped,
		),
		"DescribeServices": op.NewTyped[describeServicesRequest, describeServicesResponse](
			"DescribeServices", h.describeServicesTyped,
		),
		"ListServices": op.NewTyped[listServicesRequest, listServicesResponse](
			"ListServices", h.listServicesTyped,
		),
		"TagResource": op.NewTyped[tagResourceRequest, tagResourceResponse](
			"TagResource", h.tagResourceTyped,
		),
		"UntagResource": op.NewTyped[untagResourceRequest, untagResourceResponse](
			"UntagResource", h.untagResourceTyped,
		),
		"ListTagsForResource": op.NewTyped[listTagsForResourceRequest, listTagsForResourceResponse](
			"ListTagsForResource", h.listTagsForResourceTyped,
		),
		"CreateCapacityProvider": op.NewTyped[createCapacityProviderRequest, createCapacityProviderResponse](
			"CreateCapacityProvider", h.createCapacityProviderTyped,
		),
		"DescribeCapacityProviders": op.NewTyped[describeCapacityProvidersRequest, describeCapacityProvidersResponse](
			"DescribeCapacityProviders", h.describeCapacityProvidersTyped,
		),
		"UpdateCapacityProvider": op.NewTyped[updateCapacityProviderRequest, updateCapacityProviderResponse](
			"UpdateCapacityProvider", h.updateCapacityProviderTyped,
		),
		"PutClusterCapacityProviders": op.NewTyped[putClusterCapacityProvidersRequest, putClusterCapacityProvidersResponse](
			"PutClusterCapacityProviders", h.putClusterCapacityProvidersTyped,
		),
		"CreateTaskSet": op.NewTyped[createTaskSetRequest, createTaskSetResponse](
			"CreateTaskSet", h.createTaskSetTyped,
		),
		"UpdateTaskSet": op.NewTyped[updateTaskSetRequest, updateTaskSetResponse](
			"UpdateTaskSet", h.updateTaskSetTyped,
		),
		"DeleteTaskSet": op.NewTyped[deleteTaskSetRequest, deleteTaskSetResponse](
			"DeleteTaskSet", h.deleteTaskSetTyped,
		),
		"DescribeTaskSets": op.NewTyped[describeTaskSetsRequest, describeTaskSetsResponse](
			"DescribeTaskSets", h.describeTaskSetsTyped,
		),
		"UpdateServicePrimaryTaskSet": op.NewTyped[updateServicePrimaryTaskSetRequest, updateServicePrimaryTaskSetResponse](
			"UpdateServicePrimaryTaskSet", h.updateServicePrimaryTaskSetTyped,
		),
		"RegisterContainerInstance": op.NewTyped[registerContainerInstanceRequest, registerContainerInstanceResponse](
			"RegisterContainerInstance", h.registerContainerInstanceTyped,
		),
		"DeregisterContainerInstance": op.NewTyped[deregisterContainerInstanceRequest, deregisterContainerInstanceResponse](
			"DeregisterContainerInstance", h.deregisterContainerInstanceTyped,
		),
		"DescribeContainerInstances": op.NewTyped[describeContainerInstancesRequest, describeContainerInstancesResponse](
			"DescribeContainerInstances", h.describeContainerInstancesTyped,
		),
		"ListContainerInstances": op.NewTyped[listContainerInstancesRequest, listContainerInstancesResponse](
			"ListContainerInstances", h.listContainerInstancesTyped,
		),
		"ListAccountSettings": op.NewTyped[listAccountSettingsRequest, listAccountSettingsResponse](
			"ListAccountSettings", h.listAccountSettingsTyped,
		),
		"PutAccountSetting": op.NewTyped[putAccountSettingRequest, putAccountSettingResponse](
			"PutAccountSetting", h.putAccountSettingTyped,
		),
		"PutAccountSettingDefault": op.NewTyped[putAccountSettingRequest, putAccountSettingResponse](
			"PutAccountSettingDefault", h.putAccountSettingDefaultTyped,
		),
		"DeleteAccountSetting": op.NewTyped[deleteAccountSettingRequest, deleteAccountSettingResponse](
			"DeleteAccountSetting", h.deleteAccountSettingTyped,
		),
		"StartTask": op.NewTyped[struct{}, struct{}](
			"StartTask", h.notImplementedTyped,
		),
		"UpdateContainerAgent": op.NewTyped[struct{}, struct{}](
			"UpdateContainerAgent", h.notImplementedTyped,
		),
		"UpdateContainerInstancesState": op.NewTyped[struct{}, struct{}](
			"UpdateContainerInstancesState", h.notImplementedTyped,
		),
		"PutAttributes": op.NewTyped[struct{}, struct{}](
			"PutAttributes", h.notImplementedTyped,
		),
		"ListAttributes": op.NewTyped[struct{}, struct{}](
			"ListAttributes", h.notImplementedTyped,
		),
		"DeleteAttributes": op.NewTyped[struct{}, struct{}](
			"DeleteAttributes", h.notImplementedTyped,
		),
		"DiscoverPollEndpoint": op.NewTyped[struct{}, struct{}](
			"DiscoverPollEndpoint", h.notImplementedTyped,
		),
		"ExecuteCommand": op.NewTyped[struct{}, struct{}](
			"ExecuteCommand", h.notImplementedTyped,
		),
	}
}

// Operations implements router.ProtocolService.
func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

// SupportedProtocols implements router.ProtocolService.
func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
