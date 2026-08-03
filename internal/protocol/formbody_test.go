package protocol_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/protocol"
)

func formRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestParseFormPreservingBody_fieldsParsed(t *testing.T) {
	// Given: a form-encoded POST
	r := formRequest(t, http.MethodPost, "/", "Action=ListTopics&Version=2010-03-31")

	// When: it is parsed
	if err := protocol.ParseFormPreservingBody(r); err != nil {
		t.Fatalf("ParseFormPreservingBody: %v", err)
	}

	// Then: the fields are on r.Form, exactly as ParseForm would leave them
	if got := r.Form.Get("Action"); got != "ListTopics" {
		t.Errorf("Action = %q, want ListTopics", got)
	}
	if got := r.PostForm.Get("Version"); got != "2010-03-31" {
		t.Errorf("Version = %q, want 2010-03-31", got)
	}
}

func TestParseFormPreservingBody_bodyStillReadable(t *testing.T) {
	// Given: a form-encoded POST
	body := "Action=ListTopics&Version=2010-03-31"
	r := formRequest(t, http.MethodPost, "/", body)

	// When: it is parsed and the handler then reads the body
	if err := protocol.ParseFormPreservingBody(r); err != nil {
		t.Fatalf("ParseFormPreservingBody: %v", err)
	}
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}

	// Then: it sees the original bytes — this is the whole point of the helper
	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func TestParseFormPreservingBody_nonFormBodyIsIntact(t *testing.T) {
	// Given: a PUT whose body is not form data at all, mislabelled as form data
	body := "hello-body-check"
	r := formRequest(t, http.MethodPut, "/bucket/key", body)

	// When: it is parsed
	if err := protocol.ParseFormPreservingBody(r); err != nil {
		t.Fatalf("ParseFormPreservingBody: %v", err)
	}

	// Then: the payload survives byte for byte
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func TestParseFormPreservingBody_idempotent(t *testing.T) {
	// Given: a request already parsed once
	body := "Action=ListTopics"
	r := formRequest(t, http.MethodPost, "/", body)
	if err := protocol.ParseFormPreservingBody(r); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// When: it is parsed again
	if err := protocol.ParseFormPreservingBody(r); err != nil {
		t.Fatalf("second call: %v", err)
	}

	// Then: the second call neither re-reads nor discards the body
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
	if a := r.Form.Get("Action"); a != "ListTopics" {
		t.Errorf("Action = %q, want ListTopics", a)
	}
}

func TestParseFormPreservingBody_afterPlainParseForm(t *testing.T) {
	// Given: a request whose body some other caller already drained via the
	// standard library — the helper must not pretend it can hand it back
	r := formRequest(t, http.MethodPost, "/", "Action=ListTopics")
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	// When: the helper runs afterwards
	if err := protocol.ParseFormPreservingBody(r); err != nil {
		t.Fatalf("ParseFormPreservingBody: %v", err)
	}

	// Then: it is a no-op that keeps the already-cached fields
	if a := r.Form.Get("Action"); a != "ListTopics" {
		t.Errorf("Action = %q, want ListTopics", a)
	}
}

func TestParseFormPreservingBody_oversizedBodyIsNotParsedOrLost(t *testing.T) {
	// Given: a body past the parse limit — not a Query request, and far too
	// big to hold in memory just to look for an Action field
	body := strings.Repeat("a", protocol.MaxQueryFormBody+1)
	r := formRequest(t, http.MethodPut, "/bucket/big", body)

	// When: it is parsed
	err := protocol.ParseFormPreservingBody(r)

	// Then: the caller is told the fields are unavailable...
	if err != protocol.ErrFormBodyTooLarge {
		t.Fatalf("err = %v, want ErrFormBodyTooLarge", err)
	}
	// ...and the body is still complete
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(body) {
		t.Errorf("body length = %d, want %d", len(got), len(body))
	}
}

func TestParseFormPreservingBody_atTheLimitIsStillParsed(t *testing.T) {
	// Given: a body exactly at the limit
	value := strings.Repeat("a", protocol.MaxQueryFormBody-len("Action="))
	r := formRequest(t, http.MethodPost, "/", "Action="+value)

	// When: it is parsed
	if err := protocol.ParseFormPreservingBody(r); err != nil {
		t.Fatalf("ParseFormPreservingBody: %v", err)
	}

	// Then: the limit is inclusive
	if got := r.Form.Get("Action"); got != value {
		t.Errorf("Action length = %d, want %d", len(got), len(value))
	}
}

func TestParseFormPreservingBody_noBody(t *testing.T) {
	// Given: a GET with no body but the form content type
	r := httptest.NewRequest(http.MethodGet, "/?Action=ListTopics", nil)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// When: it is parsed
	if err := protocol.ParseFormPreservingBody(r); err != nil {
		t.Fatalf("ParseFormPreservingBody: %v", err)
	}

	// Then: the URL query still lands on r.Form, and nothing panics
	if got := r.Form.Get("Action"); got != "ListTopics" {
		t.Errorf("Action = %q, want ListTopics", got)
	}
}

func TestParseFormPreservingBody_closeReachesTheOriginalBody(t *testing.T) {
	// Given: a request whose body records its Close
	r := formRequest(t, http.MethodPost, "/", "Action=ListTopics")
	tracker := &closeTracker{ReadCloser: r.Body}
	r.Body = tracker

	// When: the helper runs and the server closes the request body
	if err := protocol.ParseFormPreservingBody(r); err != nil {
		t.Fatalf("ParseFormPreservingBody: %v", err)
	}
	if err := r.Body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Then: the close reached the real body, not just the replay buffer
	if !tracker.closed {
		t.Error("original body was never closed")
	}
}

type closeTracker struct {
	io.ReadCloser
	closed bool
}

func (c *closeTracker) Close() error {
	c.closed = true
	return c.ReadCloser.Close()
}
