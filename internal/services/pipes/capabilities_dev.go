//go:build dev

package pipes

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Pipes
		capabilities.Capability{Service: "pipes", Operation: "CreatePipe", Category: "Pipes",
			Status: capabilities.StatusSupported, Notes: "Validates source/enrichment/target wiring up front; async state machine (CREATING→RUNNING)"},
		capabilities.Capability{Service: "pipes", Operation: "DescribePipe", Category: "Pipes",
			Status: capabilities.StatusSupported, Notes: "Returns the full pipe configuration and current state"},
		capabilities.Capability{Service: "pipes", Operation: "UpdatePipe", Category: "Pipes",
			Status: capabilities.StatusSupported, Notes: "Updates DesiredState, description, role and parameter blocks (UPDATING→previous state)"},
		capabilities.Capability{Service: "pipes", Operation: "DeletePipe", Category: "Pipes",
			Status: capabilities.StatusSupported, Notes: "Async deletion (DELETING→removed)"},
		capabilities.Capability{Service: "pipes", Operation: "ListPipes", Category: "Pipes",
			Status: capabilities.StatusSupported, Notes: "Lists all pipes as PipeSummary"},
	)
}
