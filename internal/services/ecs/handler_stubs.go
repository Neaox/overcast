package ecs

// handler_stubs.go contains every ECS handler that is not yet implemented.
// Each entry returns HTTP 501 Not Implemented with x-emulator-unsupported: true.
//
// Convention: when an operation is implemented, move its method body out of this
// file and into handler.go. handler.go is the authoritative inventory of what
// actually works.

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// initOps registers every known ECS operation to its handler.
// Implemented operations point to their handler method; stubs point to notImplemented.
func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		// ---- Implemented ----
		"CreateCluster":               h.CreateCluster,
		"DescribeClusters":            h.DescribeClusters,
		"ListClusters":                h.ListClusters,
		"DeleteCluster":               h.DeleteCluster,
		"UpdateCluster":               h.UpdateCluster,
		"UpdateClusterSettings":       h.UpdateClusterSettings,
		"RegisterTaskDefinition":      h.RegisterTaskDefinition,
		"DescribeTaskDefinition":      h.DescribeTaskDefinition,
		"ListTaskDefinitions":         h.ListTaskDefinitions,
		"DeregisterTaskDefinition":    h.DeregisterTaskDefinition,
		"ListTaskDefinitionFamilies":  h.ListTaskDefinitionFamilies,
		"RunTask":                     h.RunTask,
		"StopTask":                    h.StopTask,
		"DescribeTasks":               h.DescribeTasks,
		"ListTasks":                   h.ListTasks,
		"CreateService":               h.CreateService,
		"UpdateService":               h.UpdateService,
		"DeleteService":               h.DeleteService,
		"DescribeServices":            h.DescribeServices,
		"ListServices":                h.ListServices,
		"TagResource":                 h.TagResource,
		"UntagResource":               h.UntagResource,
		"ListTagsForResource":         h.ListTagsForResource,
		"CreateCapacityProvider":      h.CreateCapacityProvider,
		"DescribeCapacityProviders":   h.DescribeCapacityProviders,
		"UpdateCapacityProvider":      h.UpdateCapacityProvider,
		"PutClusterCapacityProviders": h.PutClusterCapacityProviders,
		"CreateTaskSet":               h.CreateTaskSet,
		"UpdateTaskSet":               h.UpdateTaskSet,
		"DeleteTaskSet":               h.DeleteTaskSet,
		"DescribeTaskSets":            h.DescribeTaskSets,
		"UpdateServicePrimaryTaskSet": h.UpdateServicePrimaryTaskSet,

		// ---- Stubs (501) ----
		// Task operations
		"StartTask": h.notImplemented,

		// Container instance operations
		"RegisterContainerInstance":     h.RegisterContainerInstance,
		"DeregisterContainerInstance":   h.DeregisterContainerInstance,
		"DescribeContainerInstances":    h.DescribeContainerInstances,
		"ListContainerInstances":        h.ListContainerInstances,
		"UpdateContainerAgent":          h.notImplemented,
		"UpdateContainerInstancesState": h.notImplemented,

		// Attribute operations
		"PutAttributes":    h.notImplemented,
		"ListAttributes":   h.notImplemented,
		"DeleteAttributes": h.notImplemented,

		// Account settings
		"ListAccountSettings":      h.ListAccountSettings,
		"PutAccountSetting":        h.PutAccountSetting,
		"PutAccountSettingDefault": h.PutAccountSettingDefault,
		"DeleteAccountSetting":     h.DeleteAccountSetting,

		// Discovery
		"DiscoverPollEndpoint": h.notImplemented,

		// Execute command
		"ExecuteCommand": h.notImplemented,

		// Task definition families
		// (ListTaskDefinitionFamilies is now implemented in handler.go)
	}
	h.typedOp = h.typedOps()
}

func (h *Handler) notImplemented(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}
