package middleware

import "net/http"

// DetectServiceForTest exposes detectService to this package's external test
// package, which is where the route-coverage guard lives: that test has to
// build a real router to enumerate what every service registered, and the
// router imports middleware, so it cannot be reached from inside this package.
// Exporting through export_test.go keeps the classifier itself unexported —
// nothing outside middleware may branch on it at runtime.
func DetectServiceForTest(r *http.Request) string { return detectService(r) }

// ServiceKeyForSigningNameForTest exposes serviceKeyForSigningName to the same
// external test package, which uses it to state the classifier's invariant: an
// answer that still moves when put through this mapping is a signing name, not
// a service key, and every caller of detectService needs the latter.
func ServiceKeyForSigningNameForTest(signingName string) string {
	return serviceKeyForSigningName(signingName)
}
