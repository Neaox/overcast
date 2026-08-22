package eventbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func newTestEventPatternService() *Service {
	return New(&config.Config{Region: "us-east-1", AccountID: "000000000000"}, state.NewMemoryStore(), zap.NewNop(), clock.NewMock())
}

const testEventPatternEvent = `{"id":"evt-1","detail-type":"order.created","source":"com.example.orders",` +
	`"account":"000000000000","time":"2026-01-01T00:00:00Z","region":"us-east-1","resources":[],"detail":{"orderId":"1"}}`

func TestTestEventPatternTyped_matchAndNoMatch(t *testing.T) {
	// Given: a service and an event carrying the standard envelope fields.
	s := newTestEventPatternService()
	ctx := context.Background()

	cases := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"source and detail-type match", `{"source":["com.example.orders"],"detail-type":["order.created"]}`, true},
		{"nested detail matches", `{"detail":{"orderId":["1"]}}`, true},
		{"source differs", `{"source":["com.example.other"]}`, false},
		{"field absent from event", `{"missing":["x"]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the pattern is tested against the event.
			resp, aerr := s.testEventPatternTyped(ctx, &testEventPatternRequest{
				EventPattern: tc.pattern,
				Event:        testEventPatternEvent,
			})
			// Then: Result reflects the same matcher rule delivery uses.
			if aerr != nil {
				t.Fatalf("TestEventPattern returned %s: %s", aerr.Code, aerr.Message)
			}
			if resp.Result != tc.want {
				t.Fatalf("Result = %v, want %v", resp.Result, tc.want)
			}
		})
	}
}

func TestTestEventPatternTyped_invalidPatternIsInvalidEventPatternException(t *testing.T) {
	// Given: a pattern that is not JSON.
	s := newTestEventPatternService()

	// When: it is tested.
	_, aerr := s.testEventPatternTyped(context.Background(), &testEventPatternRequest{
		EventPattern: `{"source":[`,
		Event:        testEventPatternEvent,
	})

	// Then: the documented 400 InvalidEventPatternException comes back, not a
	// silent Result=false — a caller probing a pattern must learn it is broken.
	if aerr == nil {
		t.Fatal("expected an error for an unparseable pattern")
	}
	if aerr.Code != "InvalidEventPatternException" || aerr.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("error = %s/%d, want InvalidEventPatternException/400", aerr.Code, aerr.HTTPStatus)
	}
}

func TestTestEventPatternTyped_eventMustBeJSONObject(t *testing.T) {
	s := newTestEventPatternService()
	for _, event := range []string{``, `not json`, `[1,2]`, `"str"`} {
		_, aerr := s.testEventPatternTyped(context.Background(), &testEventPatternRequest{
			EventPattern: `{"source":["x"]}`,
			Event:        event,
		})
		if aerr == nil || aerr.Code != "ValidationException" || aerr.HTTPStatus != http.StatusBadRequest {
			t.Fatalf("event %q: error = %v, want ValidationException/400", event, aerr)
		}
	}
}

func TestTestEventPatternTyped_patternTooLong(t *testing.T) {
	s := newTestEventPatternService()
	long := `{"source":["` + strings.Repeat("a", 4096) + `"]}`
	_, aerr := s.testEventPatternTyped(context.Background(), &testEventPatternRequest{
		EventPattern: long,
		Event:        testEventPatternEvent,
	})
	if aerr == nil || aerr.Code != "ValidationException" {
		t.Fatalf("error = %v, want ValidationException for a >4096-byte pattern", aerr)
	}
}

func TestDispatch_TestEventPattern_legacyJSON(t *testing.T) {
	// Given: the AWS JSON 1.1 wire path, which EventBridge serves from the
	// legacy switch rather than the typed table.
	s := newTestEventPatternService()
	body, _ := json.Marshal(map[string]string{
		"EventPattern": `{"source":["com.example.orders"]}`,
		"Event":        testEventPatternEvent,
	})
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSEvents.TestEventPattern")
	rec := httptest.NewRecorder()

	// When: the request is dispatched.
	s.Dispatch(rec, req)

	// Then: it is served (not 501) and the documented {"Result": true} shape comes back.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Result *bool `json:"Result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	if out.Result == nil || !*out.Result {
		t.Fatalf("body = %s, want {\"Result\":true}", rec.Body.String())
	}
}
