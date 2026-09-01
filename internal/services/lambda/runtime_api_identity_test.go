package lambda

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
)

// runtime_api_identity_test.go covers how the Runtime API decides which
// execution environment a call came from.
//
// The source address is not that answer on its own. Overcast registers the
// bridge IP InspectContainer reported and used to match /next against
// r.RemoteAddr, which holds only while the container's packets arrive on-link.
// Docker Desktop's userspace host proxy re-originates the connection from the
// host, so a natively-built Overcast on Windows or macOS saw 127.0.0.1 for
// every container, matched none of them, and failed every invocation at INIT.

const identityTestARN = "arn:aws:lambda:us-east-1:000000000000:function:demo"

func newIdentityTestServer(t *testing.T) *RuntimeAPIServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewRuntimeAPIServerFromListener(ln, ln.Addr().String(), zap.NewNop(), clock.New())
	if err != nil {
		_ = ln.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	return srv
}

func TestRuntimeAPI_nextFromReoriginatedSourceAddress(t *testing.T) {
	// Given: an execution environment with a Runtime API endpoint of its own,
	// registered under the Docker bridge address Overcast read from
	// InspectContainer — an address this test will never connect from.
	srv := newIdentityTestServer(t)
	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatalf("AddContainerListener() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	srv.RegisterContainerConfig("172.18.0.5", runtimeContainerConfig{
		FunctionARN:  identityTestARN,
		FunctionName: "demo",
		Handler:      "index.handler",
	})
	listener.Attach("172.18.0.5")
	srv.SubmitInvocation(identityTestARN, []byte(`{"hello":"world"}`), time.Now().Add(30*time.Second))

	// When: the RIC polls /next on the address it was handed, and its packets
	// do not arrive on-link.
	resp := getIdentity(t, listener.Addr(), "/2018-06-01/runtime/invocation/next")
	defer resp.Body.Close()

	// Then: the invocation is served rather than refused, and it is attributed
	// to the environment the listener belongs to.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /next = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Lambda-Runtime-Invoked-Function-Arn"); got != identityTestARN {
		t.Fatalf("invoked function ARN = %q, want %q", got, identityTestARN)
	}
	if _, seen := srv.FirstNextAt("172.18.0.5"); !seen {
		t.Fatal("first /next was not recorded against the registered container address")
	}
}

func TestRuntimeAPI_extensionRegisterFromReoriginatedSourceAddress(t *testing.T) {
	// Given: the same environment, whose extension also dials the endpoint it
	// was handed rather than reaching Overcast on-link.
	srv := newIdentityTestServer(t)
	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatalf("AddContainerListener() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	srv.RegisterContainerConfig("172.18.0.5", runtimeContainerConfig{
		FunctionARN:  identityTestARN,
		FunctionName: "demo",
		Handler:      "index.handler",
	})
	listener.Attach("172.18.0.5")

	// When: an extension registers.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://"+listener.Addr()+"/2020-01-01/extension/register",
		strings.NewReader(`{"events":["INVOKE"]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Name", "collector")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: it is accepted rather than refused as an unknown container.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /extension/register = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("Lambda-Extension-Identifier") == "" {
		t.Fatal("no Lambda-Extension-Identifier returned")
	}
}

func TestRuntimeAPI_nextFromOnLinkSourceAddress(t *testing.T) {
	// Given: a containerised Overcast's arrangement — no per-environment
	// listener attached, the container registered under the address its packets
	// really arrive from.
	srv := newIdentityTestServer(t)
	srv.RegisterContainer("127.0.0.1", identityTestARN)
	srv.SubmitInvocation(identityTestARN, []byte(`{}`), time.Now().Add(30*time.Second))

	// When: the RIC polls /next on the shared address.
	resp := getIdentity(t, srv.Addr(), "/2018-06-01/runtime/invocation/next")
	defer resp.Body.Close()

	// Then: the source-address fallback still identifies it.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /next = %d, want 200", resp.StatusCode)
	}
}

func TestRuntimeAPI_nextAfterTheEnvironmentIsRetired(t *testing.T) {
	// Given: an environment whose listener has been closed, as
	// containerInstance.Close does when the pool retires it.
	srv := newIdentityTestServer(t)
	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatalf("AddContainerListener() error = %v", err)
	}
	srv.RegisterContainerConfig("172.18.0.5", runtimeContainerConfig{FunctionARN: identityTestARN, FunctionName: "demo"})
	listener.Attach("172.18.0.5")
	addr := listener.Addr()
	srv.UnregisterContainer("172.18.0.5")
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	// When/Then: nothing answers on the retired endpoint any more.
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://"+addr+"/2018-06-01/runtime/invocation/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("GET /next on a closed endpoint = %d, want a connection failure", resp.StatusCode)
	}
}

func TestRegistrationWaitFor_expiresBeforeTheInitTimeout(t *testing.T) {
	// Given: the init timeouts an operator can configure, including the default
	// and values small enough that a fixed margin would go negative.
	//
	// The wait used to be a hardcoded 15 s against a 10 s init timeout, so the
	// environment was always torn down before the 403 could be written and the
	// operator saw nothing at all.
	for _, initTimeout := range []time.Duration{
		100 * time.Millisecond,
		time.Second,
		2 * time.Second,
		defaultLambdaInitTimeout,
		time.Minute,
	} {
		// When: the registration wait is derived from it.
		wait := registrationWaitFor(initTimeout)

		// Then: it always expires first, leaving room for the 403 and its log
		// line before the invocation abandons the environment.
		if wait <= 0 {
			t.Errorf("registrationWaitFor(%s) = %s, want a positive wait", initTimeout, wait)
		}
		if wait >= initTimeout {
			t.Errorf("registrationWaitFor(%s) = %s, want less than the init timeout", initTimeout, wait)
		}
	}

	// And: an unset init timeout falls back to the default rather than to zero.
	if got, want := registrationWaitFor(0), registrationWaitFor(defaultLambdaInitTimeout); got != want {
		t.Errorf("registrationWaitFor(0) = %s, want %s", got, want)
	}
}

// ---- Test helpers ----------------------------------------------------------

func getIdentity(t *testing.T, addr, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
