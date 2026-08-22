package sqs

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestWriteQueryXMLFromJSON_NoOutputActionOmitsResultWrapper asserts that
// AWS's documented shape for no-output Query/XML operations is honored: the
// response body goes straight from the opening <{Action}Response> element to
// <ResponseMetadata>, with no <{Action}Result> wrapper at all — not even an
// empty one.
//
// See https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteQueue.html
func TestWriteQueryXMLFromJSON_NoOutputActionOmitsResultWrapper(t *testing.T) {
	for _, action := range []string{"DeleteQueue", "PurgeQueue", "SetQueueAttributes", "TagQueue", "UntagQueue"} {
		t.Run(action, func(t *testing.T) {
			rec := httptest.NewRecorder()
			rec.Code = http.StatusOK
			rec.Body.WriteString("{}")

			r := httptest.NewRequest(http.MethodPost, "/123456789012/MyQueue/", strings.NewReader(url.Values{}.Encode()))
			w := httptest.NewRecorder()

			writeQueryXMLFromJSON(w, r, action, rec)

			body := w.Body.String()
			if strings.Contains(body, action+"Result") {
				t.Fatalf("%s: expected no <%sResult> wrapper, got body:\n%s", action, action, body)
			}
			if !strings.Contains(body, "<ResponseMetadata>") {
				t.Fatalf("%s: expected <ResponseMetadata> to still be present, got body:\n%s", action, body)
			}
			wantOpen := "<" + action + "Response "
			if !strings.Contains(body, wantOpen) {
				t.Fatalf("%s: expected %q, got body:\n%s", action, wantOpen, body)
			}
		})
	}
}

// TestWriteQueryXMLFromJSON_OutputActionKeepsResultWrapper is the control:
// operations with a real output shape must keep their <{Action}Result>
// wrapper, even scoped down to a case with no members returned.
func TestWriteQueryXMLFromJSON_OutputActionKeepsResultWrapper(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Code = http.StatusOK
	rec.Body.WriteString(`{"QueueUrl":"http://localhost:4566/123456789012/MyQueue"}`)

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{}.Encode()))
	w := httptest.NewRecorder()

	writeQueryXMLFromJSON(w, r, "CreateQueue", rec)

	body := w.Body.String()
	if !strings.Contains(body, "<CreateQueueResult>") {
		t.Fatalf("expected <CreateQueueResult> wrapper to be kept, got body:\n%s", body)
	}
	if !strings.Contains(body, "<QueueUrl>") {
		t.Fatalf("expected QueueUrl member inside result, got body:\n%s", body)
	}
}
