//go:build dev

package sns_test

// sns_fanout_wiring_dev_test.go is #1306's startup-wiring assertion.
//
// SNS's Subscribe accepts exactly 7 protocols — sqs, sms, email, email-json,
// http, https, lambda (capabilities_dev.go's Subscribe entry; `application`
// and `firehose` are rejected by Subscribe itself with a 400, so they never
// reach a real subscriber and are documented unsupported there instead). Each
// of the 7 has a corresponding delivery dependency router.go wires in
// router.New (service.go's Init*Delivery calls) — but nothing before #1306
// asserted that the wiring was actually complete: a dependency router.go
// forgot to wire would have made fanOut `continue` past every subscriber of
// that protocol silently, forever, with no test failing anywhere.
//
// This test drives the real production wiring (helpers.NewTestServer, which
// calls the same router.New every "overcast serve" does) through all 7
// protocols and asserts fanOut's "delivery dependency not wired" Warn
// (handler_publish.go's warnUnwiredOnce) never fires. It does not assert
// successful delivery to a real endpoint for every protocol — sqs/lambda
// point at resources that were never created, so their deliveries fail for
// an entirely different, expected reason (no such queue/function) — only
// that the *wiring itself* is present, which is the fact this test exists to
// pin. If a future change ever drops one of the five Init*Delivery calls
// (SQS, Lambda, Email, SMS, Outbound), this test turns that into a build
// failure instead of a silently-vanishing subscriber class.
import (
	"net/url"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestFanOut_productionWiring_coversEverySubscribableProtocol(t *testing.T) {
	// Given: a server built the same way "overcast serve" builds one —
	// WithSMTPMock() matches config.Load's own default (SMTPMock=true unless
	// SMTPHost is configured), since the bare test-harness default
	// (SMTPMock=false, see defaultTestConfig) exists only to keep unrelated
	// tests quiet and is not what a real deployment runs with — plus an
	// observed logger so the operator-facing unwired-dependency Warn can be
	// asserted directly.
	core, observed := observer.New(zapcore.WarnLevel)
	srv := helpers.NewTestServer(t, helpers.WithSMTPMock(), helpers.WithLogger(zap.New(core)))

	topicArn := createTopic(t, srv, "wiring-check")

	protocols := []struct{ proto, endpoint string }{
		{"sqs", "arn:aws:sqs:us-east-1:000000000000:wiring-check-queue"},
		{"lambda", "arn:aws:lambda:us-east-1:000000000000:function:wiring-check-fn"},
		{"email", "wiring-check@example.com"},
		{"email-json", "wiring-check-json@example.com"},
		{"http", "http://127.0.0.1:1/hook"},
		{"https", "https://127.0.0.1:1/hook"},
		{"sms", "+15555550100"},
	}
	for _, p := range protocols {
		subscribe(t, srv, topicArn, p.proto, p.endpoint)
	}

	// When: a message fans out to all seven subscriptions.
	resp := snsCall(t, srv, "Publish", url.Values{
		"TopicArn": {topicArn},
		"Message":  {"wiring check"},
	})
	resp.Body.Close()

	// Then: draining every service's in-flight fan-out (Shutdown, which
	// t.Cleanup would otherwise do at the very end of the test) is the
	// deterministic point at which every delivery attempt above is known to
	// have completed — no sleep-and-hope polling required.
	srv.Shutdown()

	entries := observed.FilterMessage(unwiredDependencyWarnMsg).All()
	if len(entries) == 0 {
		return
	}
	faults := make([]string, 0, len(entries))
	for _, e := range entries {
		ctx := e.ContextMap()
		faults = append(faults, ctx["protocol"].(string)+": "+ctx["reason"].(string))
	}
	t.Fatalf("router.New's production wiring left %d subscribable protocol(s) unwired: %v — "+
		"either wire the missing dependency in router.go/service.go, or reject the protocol in "+
		"Subscribe/typed_logic.go and document it unsupported in capabilities_dev.go", len(faults), faults)
}

// unwiredDependencyWarnMsg is warnUnwiredOnce's exact Warn message
// (internal/services/sns/handler_publish.go), pinned here independently of
// internal/services/sns's own copy (handler_publish_unwired_test.go) since
// this is a different package proving the same contract from the outside.
const unwiredDependencyWarnMsg = "SNS fan-out: delivery dependency not wired for this protocol — every delivery to it will fail until this server is configured for it"
