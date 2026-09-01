package sns

// handler_publish_unsubscribeurl_test.go covers the origin the notification
// envelope's UnsubscribeURL is minted on.
//
// Delivery is asynchronous, so the publishing request is long gone by the time
// the envelope is built. The origin therefore has to travel with the fan-out —
// and when there is no HTTP caller behind the publish at all, the fallback has
// to be a sane configured base rather than a panic or an empty string. Both
// halves are pinned here; the end-to-end shape is in
// tests/integration/sns. Issue #797, docs/plans/client-facing-url-minting.md.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/middleware"
)

// publishFrom drives the real Publish handler on a request whose context
// carries origin, as middleware.ClientEndpoint would have stamped it. An empty
// origin stands for a publish with no dialable HTTP caller behind it.
func publishFrom(t *testing.T, svc *Service, origin string, form map[string]string) {
	t.Helper()
	values := url.Values{}
	for k, v := range form {
		values.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("parse form: %v", err)
	}
	if origin != "" {
		req = req.WithContext(middleware.ContextWithClientEndpoint(req.Context(), origin))
	}
	rec := httptest.NewRecorder()
	svc.handler.Publish(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Publish status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	// Stop drains the fan-out WaitGroup, so delivery is complete on return.
	svc.Stop(context.Background())
}

// deliveredUnsubscribeURL returns the UnsubscribeUrl from the single Lambda
// event the fixture's invoker captured.
func deliveredUnsubscribeURL(t *testing.T, inv *fakeInvoker) string {
	t.Helper()
	calls := inv.captured()
	if len(calls) != 1 {
		t.Fatalf("invocations = %d, want 1", len(calls))
	}
	var event struct {
		Records []struct {
			SNS struct {
				// The Lambda event spells it "UnsubscribeUrl"; the SQS/HTTP
				// envelope spells it "UnsubscribeURL". Both are minted from the
				// same field, so pinning one pins both.
				UnsubscribeURL string `json:"UnsubscribeUrl"`
			} `json:"Sns"`
		} `json:"Records"`
	}
	if err := json.Unmarshal(calls[0].payload, &event); err != nil {
		t.Fatalf("decode lambda payload: %v\npayload: %s", err, calls[0].payload)
	}
	if len(event.Records) != 1 {
		t.Fatalf("Records = %d, want 1", len(event.Records))
	}
	return event.Records[0].SNS.UnsubscribeURL
}

func TestPublish_unsubscribeURLUsesPublishersOrigin(t *testing.T) {
	// Given: a topic with a subscriber, on an instance whose listen port is 4566.
	svc, inv, _ := newLambdaFanOutFixture(t, "remapped")
	sub := addSubscription(t, svc, "remapped", "lambda", testFunctionARN, nil)

	// When: the publish arrives from a caller who dialed a remapped port.
	publishFrom(t, svc, "http://localhost:4570", map[string]string{
		"TopicArn": sub.TopicARN,
		"Message":  "hello",
	})

	// Then: the unsubscribe link names the port the caller can actually reach,
	// not the one config knows about.
	got := deliveredUnsubscribeURL(t, inv)
	if want := "http://localhost:4570/?"; !strings.HasPrefix(got, want) {
		t.Errorf("UnsubscribeUrl = %q, want it to start with %q", got, want)
	}
}

func TestPublish_unsubscribeURLFallsBackToConfiguredBase(t *testing.T) {
	// Given: the same topic and subscriber.
	svc, inv, _ := newLambdaFanOutFixture(t, "internal")
	sub := addSubscription(t, svc, "internal", "lambda", testFunctionARN, nil)

	// When: the publish has no originating caller — nothing dialable to echo.
	publishFrom(t, svc, "", map[string]string{
		"TopicArn": sub.TopicARN,
		"Message":  "hello",
	})

	// Then: the link falls back to the configured external base. It is the only
	// answer available, it is what the instance is listening on, and it must not
	// be empty — a subscriber that follows an empty link gets a confusing error
	// instead of a wrong port.
	got := deliveredUnsubscribeURL(t, inv)
	if want := svc.cfg.ExternalBaseURL() + "/?"; !strings.HasPrefix(got, want) {
		t.Errorf("UnsubscribeUrl = %q, want it to start with %q", got, want)
	}
	if !strings.Contains(got, "Action=Unsubscribe") {
		t.Errorf("UnsubscribeUrl = %q, want an Unsubscribe action", got)
	}
}
