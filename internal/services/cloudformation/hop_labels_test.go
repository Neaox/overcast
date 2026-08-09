package cloudformation

// hop_labels_test.go — a recorded hop has to say which service it reached.
// S3's REST paths are bare "/{bucket}[/{key}]", which serviceFromPath cannot
// tell apart from any other service's REST path, so every S3 hop rendered with
// a blank Service in the trace UI. The dispatcher states the service instead
// of the classifier guessing at it.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/trace"
)

// bucketResource is a stack resource whose teardown dispatches an internal
// S3 DELETE against a bare bucket path — the shape serviceFromPath cannot
// classify.
func bucketResource(logicalID, bucket, status string) StackResource {
	return StackResource{
		LogicalID:  logicalID,
		PhysicalID: bucket,
		Type:       "AWS::S3::Bucket",
		Status:     status,
	}
}

func hopFor(hops []trace.Hop, targetSubstring string) (trace.Hop, bool) {
	for _, h := range hops {
		if strings.Contains(h.TargetURI, targetSubstring) {
			return h, true
		}
	}
	return trace.Hop{}, false
}

// ---- explicit service beats path inference ---------------------------------

func TestInternalCallHopLabels_explicitServiceWinsOverPathInference(t *testing.T) {
	// Given: a REST dispatch to a bare bucket path, with the service stated
	call := internalCall{service: "s3", method: http.MethodDelete, path: "/my-bucket"}

	// When: the hop labels are rendered
	service, operation, targetURI := call.hopLabels()

	// Then: the stated service is used rather than the path being guessed at
	if service != "s3" {
		t.Errorf("service = %q, want s3", service)
	}
	if operation != http.MethodDelete {
		t.Errorf("operation = %q, want DELETE", operation)
	}
	if targetURI != "DELETE /my-bucket" {
		t.Errorf("targetURI = %q, want %q", targetURI, "DELETE /my-bucket")
	}
}

func TestInternalCallHopLabels_unstatedServiceStillInfersFromKnownPrefix(t *testing.T) {
	// Given: a REST dispatch on a prefix serviceFromPath does recognise, with
	// no service stated
	call := internalCall{method: http.MethodPost, path: "/restapis"}

	// When: the hop labels are rendered
	service, _, _ := call.hopLabels()

	// Then: inference still applies, so the 90-odd existing call sites are
	// unaffected by the new field
	if service != "apigateway" {
		t.Errorf("service = %q, want apigateway", service)
	}
}

// This is the guard on the whole approach. Making S3 the fallback in
// serviceFromPath would have labelled every unrecognised REST path "s3" —
// including the path of the next service added to the provisioner, whose
// prefix is not yet in the switch. A blank label is a gap; a confidently wrong
// one is a lie the reader has no reason to doubt.
func TestInternalCallHopLabels_unknownRestPathIsBlankRatherThanGuessedAsS3(t *testing.T) {
	// Given: a REST dispatch on a path no rule recognises, with no service stated
	call := internalCall{method: http.MethodPut, path: "/some-future-service/thing"}

	// When: the hop labels are rendered
	service, _, _ := call.hopLabels()

	// Then: the service is left blank rather than being attributed to S3
	if service != "" {
		t.Errorf("service = %q, want an empty string — an unknown REST path must not be attributed to any service", service)
	}
}

// ---- end to end through the provisioner ------------------------------------

func TestDeleteStack_s3BucketHopNamesTheService(t *testing.T) {
	// Given: a stack owning an S3 bucket, and a DeleteStack request being traced
	router := &recordingRouter{}
	h, st := newTracedTestHandler(t, router)
	seedStack(t, st, "with-bucket", StatusCreateComplete,
		bucketResource("Bucket", "my-demo-bucket", ResourceCreateComplete),
	)
	rec := newTestRecorder("delete-bucket-req")

	// When: the stack is deleted
	w := httptest.NewRecorder()
	h.dispatch(w, tracedRequest(cfnPost("DeleteStack", map[string]string{"StackName": "with-bucket"}), rec))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// Then: the bucket teardown is recorded as a hop attributed to S3
	hop, ok := hopFor(rec.Entry().Hops, "/my-demo-bucket")
	if !ok {
		t.Fatalf("no hop for the bucket delete; got %v", hopTargets(rec.Entry().Hops))
	}
	if hop.Service != "s3" {
		t.Errorf("hop.Service = %q, want s3", hop.Service)
	}
}

func TestCreateStack_s3BucketHopNamesTheService(t *testing.T) {
	// Given: a template creating an S3 bucket, and a traced CreateStack request
	router := &recordingRouter{}
	h, _ := newTracedTestHandler(t, router)
	rec := newTestRecorder("create-bucket-req")
	tmpl := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"made-by-cfn"}}}}`

	// When: the stack is created
	w := httptest.NewRecorder()
	h.dispatch(w, tracedRequest(cfnPost("CreateStack", map[string]string{
		"StackName": "making-a-bucket", "TemplateBody": tmpl,
	}), rec))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// Then: the CreateBucket dispatch is attributed to S3
	hop, ok := hopFor(rec.Entry().Hops, "/made-by-cfn")
	if !ok {
		t.Fatalf("no hop for the bucket create; got %v", hopTargets(rec.Entry().Hops))
	}
	if hop.Service != "s3" {
		t.Errorf("hop.Service = %q, want s3", hop.Service)
	}
}

// ---- template fetch --------------------------------------------------------

// resolveTemplateBody dispatches an internal S3 GET for a TemplateURL, and it
// deliberately does not descend from the request context — chi's route context
// would leak into the dispatch. It must still carry the trace recorder, or the
// fetch is invisible and the child trace has no parent.
func TestResolveTemplateBody_templateURLFetchIsRecordedOnTheTrace(t *testing.T) {
	// Given: a router serving a template body for the URL's path
	const templateBody = `{"Resources":{}}`
	router := &recordingRouter{respond: func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(templateBody))
	}}
	h, _ := newTracedTestHandler(t, router)
	rec := newTestRecorder("template-fetch-req")

	// When: a template is resolved from a TemplateURL on a traced request
	req := tracedRequest(cfnPost("CreateStack", map[string]string{
		"StackName":   "from-url",
		"TemplateURL": "http://localhost:4566/templates/stack.json",
	}), rec)
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	body, err := h.resolveTemplateBody(req)
	if err != nil {
		t.Fatalf("resolveTemplateBody: %v", err)
	}
	if body != templateBody {
		t.Fatalf("body = %q, want %q", body, templateBody)
	}

	// Then: the fetch is recorded as a hop, attributed to S3 and linked to the
	// child request it triggered
	hop, ok := hopFor(rec.Entry().Hops, "/templates/stack.json")
	if !ok {
		t.Fatalf("template fetch recorded no hop; got %v", hopTargets(rec.Entry().Hops))
	}
	if hop.Service != "s3" {
		t.Errorf("hop.Service = %q, want s3", hop.Service)
	}
	if hop.RequestID == "" {
		t.Error("hop.RequestID is empty — the fetch is not linked to its child trace")
	}
}
