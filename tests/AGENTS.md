# tests/AGENTS.md

> Test conventions for this project. Read this before writing any test.
> For general project conventions see the root [AGENTS.md](../AGENTS.md).

---

## Test philosophy

- **Every feature has a failing test before implementation.**
- **Every bug has a reproducing test before the fix.**
- Tests are the specification. If the behaviour isn't tested, it isn't guaranteed.
- Tests must be deterministic — no time dependencies, no random ordering, no
  shared mutable state between tests.
- Tests must be fast. Integration tests should complete in under 5 seconds total.

---

## Test structure: Given / When / Then

All tests are written in GWT (Given/When/Then) form. This makes the intent
unambiguous and makes failures easy to diagnose.

```go
func TestGetObject_success(t *testing.T) {
    // Given: a bucket and an object exist
    srv := helpers.NewTestServer(t)
    createBucket(t, srv, "my-bucket")
    putObject(t, srv, "my-bucket", "hello.txt", []byte("hello world"), "text/plain")

    // When: we GET the object
    resp, err := http.DefaultClient.Do(get(srv, "/my-bucket/hello.txt"))
    require.NoError(t, err)
    defer resp.Body.Close()

    // Then: we receive the correct body and headers
    helpers.AssertStatus(t, resp, http.StatusOK)
    helpers.AssertHeader(t, resp, "Content-Type", "text/plain")
    body := helpers.ReadBody(t, resp)
    assert.Equal(t, "hello world", body)
}
```

### Naming convention

Test function names follow this pattern:

```
Test<Subject>_<scenario>
```

Examples:

- `TestCreateBucket_success`
- `TestCreateBucket_nameTooShort`
- `TestReceiveMessage_visibilityTimeout`
- `TestDeleteObject_nonExistentIsIdempotent`

The scenario name should describe the **state or condition**, not the expected
outcome. The outcome is in the assertion, not the name.

---

## Two types of tests

### Unit tests — `internal/*/`

Test a single function or type in isolation. No HTTP, no server startup.
Mock or stub any external dependency.

```go
// internal/state/memory_test.go
func TestMemoryStore_GetSetDelete(t *testing.T) {
    // No server, no HTTP — just the Store interface
    s := state.NewMemoryStore()
    ctx := context.Background()
    ...
}
```

Unit tests live **alongside the code they test** in `internal/`.
Run with: `make test-unit`

### Integration tests — `tests/integration/*/`

Test the full HTTP request/response cycle through the real middleware stack.
Use `helpers.NewTestServer(t)` which spins up a real `httptest.Server`.

```go
// tests/integration/s3/s3_test.go
func TestPutObject_success(t *testing.T) {
    srv := helpers.NewTestServer(t)
    // real HTTP requests through the real router
    ...
}
```

Integration tests live in `tests/integration/<service>/`.
Run with: `make test-integration`

---

## Coverage requirements

| Layer                            | Target coverage                                          |
| -------------------------------- | -------------------------------------------------------- |
| `internal/protocol/`             | 100% — these are the AWS wire format contracts           |
| `internal/state/`                | 100% — both MemoryStore and SQLiteStore, same test suite |
| `internal/config/`               | 100% — all env var parsing paths                         |
| `internal/services/*/store.go`   | 100% — all domain model operations                       |
| `internal/services/*/handler.go` | ≥ 90% — all happy paths + key error paths                |
| `internal/middleware/`           | ≥ 90%                                                    |

Check coverage: `make test-coverage` → opens `coverage.html`

---

## Shared helpers — always extract, never duplicate

If the same setup pattern appears in more than one test, extract it to a helper.
Helpers live in one of two places:

1. **`tests/helpers/`** — shared across all service tests (TestServer, assertions)
2. **Local to the test file** — unexported helpers used only within one `_test.go` file

### When to use `tests/helpers/`

- `helpers.NewTestServer(t, opts...)` — always use this, never construct manually
- `helpers.AssertStatus(t, resp, code)` — always use this, never `t.Error` inline
- `helpers.AssertRequestID(t, resp)` — verify AWS request ID header is present
- `helpers.AssertJSONError(t, resp, code)` — decode and check JSON error code
- `helpers.AssertXMLError(t, resp, code)` — decode and check XML error code
- `helpers.DecodeJSON(t, resp, &v)` — decode response body, fail on error
- `helpers.DecodeXML(t, resp, &v)` — decode XML response body, fail on error
- `helpers.ReadBody(t, resp)` — read response body as string

### Local helpers (file-scoped)

Setup helpers specific to one service's tests are defined in the same file,
unexported, and named after what they create:

```go
// Good — named after what it creates, takes testing.T + server + params
func createBucket(t *testing.T, srv *helpers.TestServer, name string) { ... }
func putObject(t *testing.T, srv *helpers.TestServer, bucket, key string, body []byte, ct string) { ... }
func createQueue(t *testing.T, srv *helpers.TestServer, name string) string { ... }

// Bad — too generic, hides intent
func setup(t *testing.T) { ... }
func doRequest(t *testing.T, ...) { ... }
```

---

## Mocks

Use mocks to test components in isolation when the real dependency is:

- An external service (not our emulated services — those we test for real)
- Slow to set up
- Non-deterministic

We use the standard Go mock pattern — interfaces with hand-written test doubles.
Do **not** use code-generation mock libraries (mockery, gomock) in this project
to keep the dependency footprint small. Hand-written fakes are simpler and easier
to read.

### Mock pattern example

```go
// In the test file or a _test.go helper:

// mockStore is a fake state.Store for unit tests.
// Only implement the methods your test actually calls.
type mockStore struct {
    data map[string]string
    // Record calls for assertion:
    setCalls []setCall
}

type setCall struct{ namespace, key, value string }

func (m *mockStore) Get(_ context.Context, ns, key string) (string, bool, error) {
    v, ok := m.data[ns+"\x00"+key]
    return v, ok, nil
}

func (m *mockStore) Set(_ context.Context, ns, key, value string) error {
    m.data[ns+"\x00"+key] = value
    m.setCalls = append(m.setCalls, setCall{ns, key, value})
    return nil
}

// Implement remaining interface methods as no-ops if not needed:
func (m *mockStore) Delete(_ context.Context, _, _ string) error  { return nil }
func (m *mockStore) List(_ context.Context, _, _ string) ([]string, error) { return nil, nil }
func (m *mockStore) Close() error                                  { return nil }
```

### When NOT to mock

- Do not mock `state.Store` in integration tests — use `helpers.NewTestServer(t)`
  which provides a real `MemoryStore`. Integration tests must exercise the real stack.
- Do not mock HTTP handlers — test them through the real router.

---

## Table-driven tests

Use table-driven tests when the same logic needs many input/output pairs:

```go
func TestValidateBucketName(t *testing.T) {
    // Given: various bucket name inputs
    cases := []struct {
        name        string
        input       string
        expectError bool
    }{
        // When + Then combined in the table:
        {name: "valid name",    input: "my-bucket",    expectError: false},
        {name: "too short",     input: "ab",           expectError: true},
        {name: "too long",      input: strings.Repeat("a", 64), expectError: true},
        {name: "with numbers",  input: "bucket123",    expectError: false},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            // When: we validate the name
            err := validateBucketName(tc.input)

            // Then: error presence matches expectation
            if tc.expectError && err == nil {
                t.Errorf("expected error for input %q, got nil", tc.input)
            }
            if !tc.expectError && err != nil {
                t.Errorf("expected no error for input %q, got: %v", tc.input, err)
            }
        })
    }
}
```

---

## Test isolation

Every test gets a fresh server with empty state:

```go
srv := helpers.NewTestServer(t)   // fresh MemoryStore, no state
```

`t.Cleanup(srv.Close)` is registered automatically. You do not need to close
the server manually.

**Never share state between tests.** Never use `TestMain` to pre-populate state
for all tests in a package. Each test must arrange its own state in the Given section.

---

## Race condition tests

Run all tests with `-race`: `make test` (not `make test-unit`).

Tests that specifically verify concurrent behaviour use goroutines explicitly:

```go
func TestMemoryStore_concurrentAccess(t *testing.T) {
    // Given: a store
    s := state.NewMemoryStore()
    ctx := context.Background()

    // When: 50 goroutines read and write concurrently
    done := make(chan struct{}, 50)
    for i := 0; i < 50; i++ {
        go func() {
            s.Set(ctx, "ns", "key", "value")
            s.Get(ctx, "ns", "key")
            done <- struct{}{}
        }()
    }
    for i := 0; i < 50; i++ {
        <-done
    }
    // Then: no data race (enforced by -race flag, no explicit assertion needed)
}
```

---

## Test for error responses specifically

Always verify:

1. The HTTP status code
2. The error code in the response body (not just the message — messages can change)
3. The request ID header is present

```go
// Then: we get a well-formed AWS error response
helpers.AssertStatus(t, resp, http.StatusNotFound)
helpers.AssertXMLError(t, resp, "NoSuchBucket")   // checks the Code field
helpers.AssertRequestID(t, resp)                   // checks x-amz-request-id header
```

---

## Adding tests for a new service

1. Create `tests/integration/<service>/<service>_test.go`
2. Package name: `package <service>_test` (black-box — tests via HTTP only)
3. Write P1 tests in GWT form, failing first
4. Extract setup helpers at the bottom of the file (unexported, file-scoped)
5. Run `make test-integration` to confirm they fail before implementing

Template for the first test in a new service file:

```go
package myservice_test

import (
    "net/http"
    "testing"

    "github.com/Neaox/overcast/tests/helpers"
)

// ---- CreateThing -----------------------------------------------------------

func TestCreateThing_success(t *testing.T) {
    // Given: a running server
    srv := helpers.NewTestServer(t)

    // When: we create a thing
    resp := serviceCall(t, srv, "CreateThing", map[string]any{
        "Name": "my-thing",
    })
    defer resp.Body.Close()

    // Then: it succeeds and returns the thing's identifier
    helpers.AssertStatus(t, resp, http.StatusOK)
    helpers.AssertRequestID(t, resp)
    // ... decode and assert response body
}

// ---- Test helpers ----------------------------------------------------------

func serviceCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
    t.Helper()
    // ... build and send the request
}
```
