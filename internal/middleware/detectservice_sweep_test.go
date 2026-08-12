//go:build dev

package middleware_test

// detectservice_sweep_test.go — every operation Overcast declares, classified
// through the two middlewares that actually label it.
//
// The two sibling guards walk the router's *paths*: detectservice_routes_test.go
// classifies each registered route family unsigned, and
// detectservice_signed_test.go signs each one the way the models say it is
// signed. Both vary the path and neither names an operation, so neither can say
// anything about the protocols that put the service and the operation in the
// request *content* — X-Amz-Target and the Query Action/Version pair, which
// between them carry most of what Overcast serves. This file is that third
// axis, and it asserts the operation as well as the service.
//
// It also goes through the real middlewares rather than calling the classifier
// directly, because the defect class here is a *call site* that classifies with
// less than it has. detectService takes the request body variadically, so a
// caller that omits it silently loses the Query step and falls through to the
// s3 fallback — and the compiler cannot say so. A test that called
// detectService itself would pass while both real paths were wrong. So each
// case is run twice:
//
//   - the trace path, where DebugTrace reads the body eagerly and classifies
//     *before* the handler runs, and
//   - the log path, where Logger classifies *after* it, from whatever the
//     handler's own read left in the capture.
//
// Those are different inputs and they can disagree, which is why both are
// asserted rather than one standing in for the other.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/internal/capabilities"
	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/trace"
)

// wireProbe is one request shape for one declared operation, together with the
// service and operation label both middlewares must produce for it.
type wireProbe struct {
	shape   string // "target" or "query" — which signal classification must read
	service string
	op      string
	build   func() *http.Request
}

// TestClassifiesEveryDeclaredOperationOnItsContentWire sweeps every operation
// in the capability registry that AWS models on a content-classified wire, and
// requires both observability paths to name the service and the operation.
//
// The capability registry is the source of truth for what Overcast claims to
// serve; the pinned models are the source of truth for how a client addresses
// it. Neither is a list anybody maintains by hand, so a service added later is
// swept without anyone remembering to extend a table — which is the property
// the whole file exists for.
func TestClassifiesEveryDeclaredOperationOnItsContentWire(t *testing.T) {
	// Given: every declared operation, in each content-classified shape the
	// models say a client can address it with.
	probes := declaredContentWireProbes(t)
	t.Logf("sweeping %d content-wire probes across %d declared capabilities", len(probes), len(capabilities.AllCapabilities))
	// A floor rather than an exact count, which would be churn on every
	// capability added. It is here because every failure mode of the
	// enumeration itself — a build tag that empties the capability registry, a
	// model corpus that stops loading — produces a *passing* test otherwise.
	if len(probes) < 800 {
		t.Fatalf("built only %d probes; the capability registry or the model corpus is not loading", len(probes))
	}

	// When: each is classified by the trace path and by the log path.
	// Then: both name the service that answers it and the operation it asked for.
	var wrong []string
	seen := map[string]bool{}
	for _, probe := range probes {
		for _, path := range []struct {
			name     string
			classify func(*testing.T, *http.Request) (string, string)
		}{
			{"trace", classifyThroughDebugTrace},
			{"log", classifyThroughLogger},
		} {
			gotService, gotOp := path.classify(t, probe.build())
			if gotService == probe.service && gotOp == probe.op {
				continue
			}
			// Collapsed per (shape, path, service, answer): a whole service
			// missing from a registry index reports once with an example
			// rather than once per operation.
			key := fmt.Sprintf("%s/%s: %s -> %s.%s", probe.shape, path.name, probe.service, gotService, gotOp)
			if seen[key] {
				continue
			}
			seen[key] = true
			wrong = append(wrong, fmt.Sprintf("%s via %s: %s.%s classified as %s.%s",
				probe.shape, path.name, probe.service, probe.op, gotService, orNone(gotOp)))
		}
	}
	sort.Strings(wrong)

	if len(wrong) > 0 {
		t.Errorf("declared operations misclassified on their own wire (%d distinct):\n\t%s\n\n"+
			"The service label is the request log's primary filter, the trace list's Service\n"+
			"column, and the input to IAM enforcement — a request labelled s3 here is one\n"+
			"authorised as an s3: action.",
			len(wrong), strings.Join(wrong, "\n\t"))
	}
}

func orNone(op string) string {
	if op == "" {
		return "(no operation)"
	}
	return op
}

// declaredContentWireProbes pairs the capability registry with the model corpus
// and yields one probe per (operation, wire shape).
//
// A declared operation with no modeled binding is skipped rather than failed:
// several are Overcast's own disambiguating names for bindings AWS models under
// a shared name (apigateway's CreateV2Api is the v2 CreateApi), and capgen
// already guards the declaration against the corpus. This file's subject is
// classification, not declaration hygiene.
func declaredContentWireProbes(t *testing.T) []wireProbe {
	t.Helper()
	var probes []wireProbe
	for _, capability := range capabilities.AllCapabilities {
		for _, binding := range awsapi.Operations(capability.Service, capability.Operation) {
			// The capability registry and the classifier key the same service
			// differently in one case by design: capabilities call CloudWatch
			// Logs "cloudwatch-logs", while every consumer of detectService
			// wants the IAM prefix, which is "logs". middlewareServiceKey is
			// the mapping, and asking for its answer here is what keeps this
			// sweep testing classification rather than restating the
			// difference between two namespaces.
			probes = append(probes, bindingProbes(middleware.ServiceKeyForClassificationForTest(capability.Service), binding)...)
		}
	}
	return probes
}

// bindingProbes builds the content-classified request shapes for one modeled
// binding. REST bindings are deliberately absent: their signal is the path, and
// the two sibling files already walk every registered route both unsigned and
// signed.
func bindingProbes(service string, binding awsapi.Operation) []wireProbe {
	var probes []wireProbe
	if binding.TargetPrefix != "" {
		target := binding.TargetPrefix + binding.Name
		probes = append(probes, wireProbe{
			shape:   "target",
			service: service,
			op:      binding.Name,
			build: func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
				r.Header.Set("X-Amz-Target", target)
				r.Header.Set("Content-Type", "application/x-amz-json-1.0")
				return r
			},
		})
	}
	if binding.Protocols&awsapi.ProtocolsRPCV2CBOR != 0 && binding.ServiceShape != "" {
		// The Smithy RPC v2 URI names the service shape and the operation
		// outright, and the router dispatches on exactly that pair. The header
		// is what makes it RPC v2 rather than an S3 object read of
		// "operation/X" in a bucket called "service" — see smithyRPCClaim.
		uri := "/service/" + binding.ServiceShape + "/operation/" + binding.Name
		probes = append(probes, wireProbe{
			shape:   "rpcv2",
			service: service,
			op:      binding.Name,
			build: func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, uri, strings.NewReader(""))
				r.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
				r.Header.Set("Content-Type", "application/cbor")
				return r
			},
		})
	}
	if queryVersion, ok := overcastQueryVersion(service, binding); ok {
		// Unsigned, which is the case that matters: an SDK sends a credential
		// scope and is classified a step earlier, but the web UI, curl and the
		// AWS CLI's unsigned paths send exactly this, and it is the shape that
		// falls through to the s3 fallback when the registry does not know the
		// action.
		body := "Action=" + binding.Name + "&Version=" + queryVersion
		probes = append(probes, wireProbe{
			shape:   "query",
			service: service,
			op:      binding.Name,
			build: func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return r
			},
		})
	}
	return probes
}

// overcastQueryVersion reports the API version an unsigned Query request to this
// binding carries, and whether Overcast answers that binding on the Query wire
// at all.
//
// The models answer this for most services, and additively: CloudWatch's model
// declares awsQuery alongside awsJson1_0, and a service that carries the trait
// at all carries it for every operation. SQS is the exception the models cannot
// answer — AWS migrated its model to awsJson1_0 only, while Overcast still
// serves the Query wire for it (sqs.Service implements router.QueryDispatcher
// and declares codec.QueryXML) — so it is named here, in the test that has to
// know what Overcast serves rather than what AWS publishes.
func overcastQueryVersion(service string, binding awsapi.Operation) (string, bool) {
	if binding.APIVersion == "" {
		return "", false
	}
	if binding.Protocols&(awsapi.ProtocolsAWSQuery|awsapi.ProtocolsEC2Query) != 0 {
		return binding.APIVersion, true
	}
	return binding.APIVersion, overcastServedQueryServices[service]
}

// overcastServedQueryServices names the services Overcast answers on the AWS
// Query wire that the pinned models do not declare a Query protocol for.
//
// **Adding an entry here is not the fix on its own.** It states that unsigned
// Query traffic reaches this service, which makes the sweep above require the
// generated query index to name it — and that index is generated by
// cmd/awsmodelgen from the models, so a service the models do not describe as
// Query has to be declared to the generator too. See overcastQueryServices
// there.
var overcastServedQueryServices = map[string]bool{
	"sqs": true,
}

// classifyThroughDebugTrace runs the request through the real DebugTrace
// middleware and returns the service and operation it stamped on the trace
// entry — the Service and Op columns of the debug trace list, and the service
// filter beside them.
//
// This is the *before* classification: DebugTrace reads the body eagerly,
// because it pushes a partial entry before the handler runs.
func classifyThroughDebugTrace(t *testing.T, r *http.Request) (string, string) {
	t.Helper()
	buf := trace.NewBuffer(4)
	handler := middleware.DebugTrace(&config.Config{Debug: true, Region: "us-east-1"}, buf, clock.New())(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			drainRequestBody(r)
			w.WriteHeader(http.StatusOK)
		}))
	handler.ServeHTTP(httptest.NewRecorder(), r)

	summaries, _ := buf.ListSummaries(trace.ListFilter{Limit: 1})
	if len(summaries) == 0 {
		t.Fatal("DebugTrace recorded no entry")
	}
	return summaries[0].Service, summaries[0].Operation
}

// classifyThroughLogger runs the request through the real Logger middleware and
// returns the service and operation on the request log line — the `service`
// field the log's primary filter reads.
//
// This is the *after* classification, and its input is whatever the handler's
// own read left in the capture, which is why the handler below reads the body
// exactly as a real Query handler must in order to find its Action.
func classifyThroughLogger(t *testing.T, r *http.Request) (string, string) {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	handler := middleware.Logger(zap.New(core), clock.New())(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			drainRequestBody(r)
			w.WriteHeader(http.StatusOK)
		}))
	handler.ServeHTTP(httptest.NewRecorder(), r)

	entries := logs.All()
	if len(entries) == 0 {
		t.Fatal("Logger wrote no line")
	}
	fields := entries[len(entries)-1].ContextMap()
	service, _ := fields["service"].(string)
	operation, _ := fields["operation"].(string)
	return service, operation
}

// drainRequestBody reads the request body the way a handler that dispatches on
// it does. The log path classifies from what the handler read, so a handler
// that read nothing would make every Query case fall through for a reason the
// production code does not have.
func drainRequestBody(r *http.Request) {
	if r.Body == nil {
		return
	}
	buf := make([]byte, 512)
	for {
		if _, err := r.Body.Read(buf); err != nil {
			return
		}
	}
}
