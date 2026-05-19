//go:build dev

package pipes

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Pipes
		capabilities.Capability{Service: "pipes", Operation: "CreatePipe", Category: "Pipes",
			Status: capabilities.StatusSupported, Notes: "Creates with async state machine (CREATING→RUNNING)"},
		capabilities.Capability{Service: "pipes", Operation: "DescribePipe", Category: "Pipes",
			Status: capabilities.StatusSupported, Notes: "Returns pipe details and current state"},
		capabilities.Capability{Service: "pipes", Operation: "UpdatePipe", Category: "Pipes",
			Status: capabilities.StatusSupported, Notes: "Updates DesiredState and Description"},
		capabilities.Capability{Service: "pipes", Operation: "DeletePipe", Category: "Pipes",
			Status: capabilities.StatusSupported, Notes: "Async deletion (DELETING→removed)"},
		capabilities.Capability{Service: "pipes", Operation: "ListPipes", Category: "Pipes",
			Status: capabilities.StatusSupported, Notes: "Lists all pipes"},
	)
}
