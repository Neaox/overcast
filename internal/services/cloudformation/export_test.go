package cloudformation

// export_test.go bridges unexported fidelity.go internals out to
// fidelity_tier_sync_test.go, which lives in the external cloudformation_test
// package so it can import internal/router without an import cycle (router.go
// registers this package, so a same-package test file cannot import router —
// see the package comment on fidelity.go).
//
// Compiled only for tests: nothing here exists in a production build.

// ResourceServiceTiersForTest exposes fidelity.go's copy of the services that
// back a real (non-stub) resource handler at inert or stub tier.
var ResourceServiceTiersForTest = resourceServiceTiers

// CFNResourceServiceForTest exposes cfnResourceService.
func CFNResourceServiceForTest(resType string) (string, bool) {
	return cfnResourceService(resType)
}

// ResourceHandlerTypesForTest returns every CloudFormation type registered in
// resourceHandlers, mapped to whether its handler is the deliberate
// stubResourceHandler.
func ResourceHandlerTypesForTest() map[string]bool {
	out := make(map[string]bool, len(resourceHandlers))
	for resType, h := range resourceHandlers {
		_, isStub := h.(*stubResourceHandler)
		out[resType] = isStub
	}
	return out
}
