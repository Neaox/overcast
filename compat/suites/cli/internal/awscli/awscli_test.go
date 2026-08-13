package awscli

import (
	"strings"
	"testing"
)

// RunStatus is the only way a CLI-driven test can see an HTTP status: the AWS
// CLI prints a modeled error's code and stops, so `describe-domain` on a
// missing OpenSearch domain says "ResourceNotFoundException" and never that it
// arrived as a 409 rather than the 404 an author would assume. The status comes
// out of the urllib3 line botocore logs under --debug, which makes that log
// line load-bearing and makes a CLI upgrade that reshapes it a silent way to
// turn every status assertion into a pass. Pin the shape here so it fails in
// the unit-test job instead.
func TestLastHTTPStatus_readsTheBotocoreRequestLog(t *testing.T) {
	for _, tc := range []struct {
		name, stderr string
		want         int
	}{
		{
			name:   "success",
			stderr: `2026-08-13 16:48:57,870 - MainThread - urllib3.connectionpool - DEBUG - http://127.0.0.1:4566 "GET /2021-01-01/domain HTTP/1.1" 200 51`,
			want:   200,
		},
		{
			name:   "a modeled error carries its status here and nowhere else",
			stderr: `2026-08-13 16:48:57,870 - MainThread - urllib3.connectionpool - DEBUG - http://127.0.0.1:4566 "GET /2021-01-01/opensearch/domain/absent HTTP/1.1" 409 82`,
			want:   409,
		},
		{
			name:   "a query string in the URI does not hide the status",
			stderr: `… urllib3.connectionpool - DEBUG - http://127.0.0.1:4566 "GET /2021-01-01/tags/?arn=arn%3Aaws%3Aes%3A... HTTP/1.1" 200 44`,
			want:   200,
		},
		{
			name: "the last attempt wins — botocore retries in process",
			stderr: `… urllib3.connectionpool - DEBUG - http://h "GET /x HTTP/1.1" 500 10
… botocore.retryhandler - DEBUG - Retry needed
… urllib3.connectionpool - DEBUG - http://h "GET /x HTTP/1.1" 200 10`,
			want: 200,
		},
		{
			name:   "nothing reached the wire",
			stderr: `2026-08-13 - MainThread - botocore.hooks - DEBUG - Event calling-handler`,
			want:   0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastHTTPStatus(tc.stderr); got != tc.want {
				t.Fatalf("lastHTTPStatus = %d, want %d — the CLI's wire log shape may have changed", got, tc.want)
			}
		})
	}
}

// --debug output runs to megabytes and the runner parses stdout as NDJSON, so
// a failing RunStatus call must carry the CLI's own message and nothing else.
func TestStripDebugLog_keepsOnlyTheCLIsOwnOutput(t *testing.T) {
	stderr := `2026-08-13 16:48:57,870 - MainThread - botocore.hooks - DEBUG - Event calling-handler
2026-08-13 16:48:57,871 - MainThread - botocore.parsers - DEBUG - Response headers: {…}

aws: [ERROR]: An error occurred (ResourceNotFoundException) when calling the DescribeDomain operation: Domain not found: absent`

	got := stripDebugLog(stderr)
	want := "aws: [ERROR]: An error occurred (ResourceNotFoundException) when calling the DescribeDomain operation: Domain not found: absent"
	if got != want {
		t.Fatalf("stripDebugLog = %q, want %q", got, want)
	}
	if strings.Contains(got, "botocore") {
		t.Fatalf("stripDebugLog left trace lines in: %q", got)
	}
}
