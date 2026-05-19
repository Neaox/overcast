package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/protocol/codec"
)

func TestProtocol_NoIdentifiersIsPassthrough(t *testing.T) {
	handler := Protocol(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, op := codec.FromContext(r.Context())
		if c != nil || op != "" {
			t.Errorf("expected empty context, got (%v, %q)", c, op)
		}
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestProtocol_IdentifiesJSON10(t *testing.T) {
	var seenCodec codec.Codec
	var seenOp string
	handler := Protocol(codec.DefaultIdentifiers())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCodec, seenOp = codec.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	r.Header.Set("X-Amz-Target", "Service.SendMessage")
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if seenCodec != codec.JSON10 || seenOp != "SendMessage" {
		t.Errorf("got (%v, %q), want (JSON10, SendMessage)", seenCodec, seenOp)
	}
}

func TestProtocol_NoMatchPassesThroughEmpty(t *testing.T) {
	called := false
	handler := Protocol(codec.DefaultIdentifiers())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		c, op := codec.FromContext(r.Context())
		if c != nil || op != "" {
			t.Errorf("legacy request should not have codec set, got (%v, %q)", c, op)
		}
	}))
	// Request with no AWS-protocol headers (e.g. an S3 GET).
	r := httptest.NewRequest(http.MethodGet, "/some-bucket", nil)
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Fatal("downstream handler not invoked")
	}
}

func TestProtocol_DoesNotConsumeBody(t *testing.T) {
	body := `{"hello":"world"}`
	handler := Protocol(codec.DefaultIdentifiers())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		if string(buf[:n]) != body {
			t.Errorf("body = %q, want %q (middleware consumed it)", buf[:n], body)
		}
	}))
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	r.Header.Set("X-Amz-Target", "X.Op")
	handler.ServeHTTP(httptest.NewRecorder(), r)
}
