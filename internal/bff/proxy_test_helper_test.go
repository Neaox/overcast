package bff

import (
	"net/http"
	"net/http/httptest"
)

// stubEmulatorForProxyTests creates an httptest.Server that acts as the
// emulator backend, wires the BFF's HTTP client to dial it directly, and
// points defaultAPIURL at it so resolveEndpoint returns the test server
// URL without hitting normalizeEndpoint.
//
// Callers must call restore() in a defer to undo the globals.
func stubEmulatorForProxyTests(tb testingTB, handler http.Handler) (emulator *httptest.Server, restore func()) {
	tb.Helper()
	emulator = httptest.NewServer(handler)

	origAPIURL := defaultAPIURL
	origClient := bffHTTPClient
	origStreaming := bffStreamingClient

	defaultAPIURL = emulator.URL
	bffHTTPClient = emulator.Client()
	bffStreamingClient = emulator.Client()

	return emulator, func() {
		emulator.Close()
		defaultAPIURL = origAPIURL
		bffHTTPClient = origClient
		bffStreamingClient = origStreaming
	}
}

// testingTB matches both *testing.T and *testing.B.
type testingTB interface {
	Helper()
}
