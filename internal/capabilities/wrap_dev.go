//go:build dev

package capabilities

import "net/http"

// Wrap returns fn after asserting that (service, op) is declared in Default.
// It panics at startup if an operation is registered in the handler map but has
// no corresponding Capability declaration, making drift impossible to overlook.
//
// Usage:
//
//	h.ops["SendMessage"] = capabilities.Wrap("sqs", "SendMessage", h.SendMessage)
func Wrap(service, op string, fn http.HandlerFunc) http.HandlerFunc {
	if _, ok := Default.Lookup(service, op); !ok {
		panic("capabilities: " + service + "/" + op + " has no Capability entry — add it to internal/services/" + service + "/capabilities_dev.go")
	}
	return fn
}
