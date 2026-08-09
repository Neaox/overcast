package middleware

import "net/http"

// DetectServiceForTest exposes detectService to this package's external test
// package, which is where the route-coverage guard lives: that test has to
// build a real router to enumerate what every service registered, and the
// router imports middleware, so it cannot be reached from inside this package.
// Exporting through export_test.go keeps the classifier itself unexported —
// nothing outside middleware may branch on it at runtime.
func DetectServiceForTest(r *http.Request) string { return detectService(r) }
