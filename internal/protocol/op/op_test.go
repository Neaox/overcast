package op

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
)

type fakeIn struct {
	Name string `json:"Name"`
}

type fakeOut struct {
	Greeting string `json:"Greeting"`
}

func TestTyped_HappyPath(t *testing.T) {
	hello := NewTyped("Hello", func(_ context.Context, in *fakeIn) (*fakeOut, *protocol.AWSError) {
		return &fakeOut{Greeting: "hi " + in.Name}, nil
	})
	if hello.Name() != "Hello" {
		t.Fatalf("name = %q, want Hello", hello.Name())
	}

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"Name":"world"}`))
	w := httptest.NewRecorder()
	hello.Invoke(w, r, codec.JSON10)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"Greeting":"hi world"`) {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestTyped_DecodeError(t *testing.T) {
	op := NewTyped("X", func(_ context.Context, _ *fakeIn) (*fakeOut, *protocol.AWSError) {
		t.Fatal("function should not be called on decode error")
		return nil, nil
	})
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{bad"))
	w := httptest.NewRecorder()
	op.Invoke(w, r, codec.JSON10)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestTyped_BusinessError(t *testing.T) {
	op := NewTyped("X", func(_ context.Context, _ *fakeIn) (*fakeOut, *protocol.AWSError) {
		return nil, &protocol.AWSError{Code: "ResourceNotFoundException", Message: "nope", HTTPStatus: 404}
	})
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"Name":"x"}`))
	w := httptest.NewRecorder()
	op.Invoke(w, r, codec.JSON10)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ResourceNotFoundException") {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestTyped_NilOutputIsEmptyResponse(t *testing.T) {
	op := NewTyped("Void", func(_ context.Context, _ *fakeIn) (*fakeOut, *protocol.AWSError) {
		return nil, nil
	})
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"Name":"x"}`))
	w := httptest.NewRecorder()
	op.Invoke(w, r, codec.JSON10)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "{}" {
		t.Errorf("nil out should render {}, got %q", w.Body.String())
	}
}

func TestTypedAny_HappyPath(t *testing.T) {
	hello := NewTypedAny("HelloAny", func(_ context.Context, in *fakeIn) (any, *protocol.AWSError) {
		if in.Name == "count" {
			return map[string]int{"Count": 1}, nil
		}
		return &fakeOut{Greeting: "hi " + in.Name}, nil
	})
	if hello.Name() != "HelloAny" {
		t.Fatalf("name = %q, want HelloAny", hello.Name())
	}

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"Name":"world"}`))
	w := httptest.NewRecorder()
	hello.Invoke(w, r, codec.JSON10)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"Greeting":"hi world"`) {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestRawOperation_DelegatesHandler(t *testing.T) {
	op := NewRaw("RawThing", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "ok" {
			t.Fatalf("header missing from raw request")
		}
		w.Header().Set("X-Raw", "true")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("raw body"))
	}))
	if op.Name() != "RawThing" {
		t.Fatalf("name = %q, want RawThing", op.Name())
	}

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"Name":"ignored"}`))
	r.Header.Set("X-Test", "ok")
	w := httptest.NewRecorder()
	op.Invoke(w, r, codec.JSON10)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	if w.Header().Get("X-Raw") != "true" {
		t.Fatalf("expected raw handler header")
	}
	if w.Body.String() != "raw body" {
		t.Fatalf("body = %q, want raw body", w.Body.String())
	}
}

// BenchmarkDispatcher_TypedInvoke is a Phase 0 baseline. See
// docs/plans/smithy.md §9.4. The dispatcher itself MUST stay
// allocation-free (the allocations reported here come from the codec's
// JSON decoder and httptest.NewRequest, not the dispatcher).
func BenchmarkDispatcher_TypedInvoke(b *testing.B) {
	b.ReportAllocs()
	op := NewTyped("Bench", func(_ context.Context, in *fakeIn) (*fakeOut, *protocol.AWSError) {
		return &fakeOut{Greeting: in.Name}, nil
	})
	body := []byte(`{"Name":"x"}`)
	for i := 0; i < b.N; i++ {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		op.Invoke(w, r, codec.JSON10)
	}
}
