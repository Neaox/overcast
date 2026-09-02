package s3_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// Credentials are not a partition key — S3. The DynamoDB copy of this test
// (tests/integration/dynamodb/console_visibility_test.go) says why it exists.
// S3 has no region half to the story: a bucket name is global on AWS and
// Overcast keys buckets the same way, so a bucket created in any region is
// listed from every region.

func s3SignedAs(accessKey, region string) string {
	return "AWS4-HMAC-SHA256 Credential=" + accessKey + "/20250101/" + region +
		"/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=fake"
}

func listBucketsWith(t *testing.T, srv *helpers.TestServer, headers map[string]string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	if err != nil {
		t.Fatalf("build ListBuckets request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	return helpers.ReadBody(t, resp)
}

// Given: a bucket created by a signed CLI-style client in ap-southeast-2.
// When: the console lists buckets, signed as itself or unsigned, from us-east-1.
// Then: the bucket is there either way.
func TestListBuckets_consoleSeesBucketsCreatedBySignedClients(t *testing.T) {
	srv := helpers.NewTestServer(t)
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/from-cli",
		strings.NewReader(`<CreateBucketConfiguration><LocationConstraint>ap-southeast-2</LocationConstraint></CreateBucketConfiguration>`))
	if err != nil {
		t.Fatalf("build CreateBucket request: %v", err)
	}
	req.Header.Set("Authorization", s3SignedAs("AKIAIOSFODNN7EXAMPLE", "ap-southeast-2"))
	req.Header.Set("X-Amz-Date", "20250101T000000Z")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	consoleShapes := map[string]map[string]string{
		"browser SDK, signed as overcast": {
			"Authorization": s3SignedAs("overcast", "us-east-1"),
			"X-Amz-Date":    "20250101T000000Z",
		},
		"unsigned, X-Overcast-Region us-east-1": {"X-Overcast-Region": "us-east-1"},
		"unsigned, no region evidence at all":   {},
	}
	for name, headers := range consoleShapes {
		t.Run(name, func(t *testing.T) {
			if body := listBucketsWith(t, srv, headers); !strings.Contains(body, "<Name>from-cli</Name>") {
				t.Fatalf("expected the CLI's bucket in ListBuckets, got: %s", body)
			}
		})
	}
}
