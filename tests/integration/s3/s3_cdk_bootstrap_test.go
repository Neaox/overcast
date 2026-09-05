// CDK bootstrap bucket coverage (#175).
//
// `cdk bootstrap` deploys the CDKToolkit stack, whose staging bucket is
// configured through a fixed run of S3 calls: CreateBucket, then versioning,
// encryption, public-access block, a deny-insecure-transport bucket policy, a
// noncurrent-version lifecycle rule, and tags. TestCDKBootstrapBucket_flow
// walks that same run over the S3 API and reads every piece back, so a
// regression in any one of them is a named failure rather than a CDK
// deployment that dies somewhere in the middle.
//
// The rest of the file covers the edge cases around those operations that had
// no test: a missing bucket, an empty or oversized tag set, and duplicate tag
// keys. Tag limits come from the S3 User Guide's cost-allocation tag page —
// "A tag set can contain as many as 50 tags, or it can be empty. Keys must be
// unique within a tag set", a key of "1 to 128 Unicode characters" and a
// value of "0 to 256" — with the error codes taken from Versity's AWS
// conformance suite.
package s3_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// bootstrapBucketPolicy is the deny-insecure-transport statement `cdk
// bootstrap` puts on its staging bucket.
const bootstrapBucketPolicy = `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "denyInsecureConnections",
      "Effect": "Deny",
      "Principal": {"AWS": "*"},
      "Action": "s3:*",
      "Resource": [
        "arn:aws:s3:::cdk-hnb659fds-assets-000000000000-us-east-1",
        "arn:aws:s3:::cdk-hnb659fds-assets-000000000000-us-east-1/*"
      ],
      "Condition": {"Bool": {"aws:SecureTransport": "false"}}
    }
  ]
}`

// putBucketSubresource issues PUT /{bucket}?{subresource} with an XML or JSON
// body and returns the response for the caller to assert on and close.
func putBucketSubresource(t *testing.T, srv *helpers.TestServer, bucket, subresource, body string) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(put(srv, "/"+bucket+"?"+subresource, []byte(body), nil))
	if err != nil {
		t.Fatalf("PUT /%s?%s: %v", bucket, subresource, err)
	}
	return resp
}

// getBucketSubresource issues GET /{bucket}?{subresource}.
func getBucketSubresource(t *testing.T, srv *helpers.TestServer, bucket, subresource string) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(get(srv, "/"+bucket+"?"+subresource))
	if err != nil {
		t.Fatalf("GET /%s?%s: %v", bucket, subresource, err)
	}
	return resp
}

// taggingXML renders a Tagging document from ordered key/value pairs, so a
// test can send duplicate keys — which a map could not express.
func taggingXML(pairs ...[2]string) string {
	var b strings.Builder
	b.WriteString(`<Tagging xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><TagSet>`)
	for _, p := range pairs {
		fmt.Fprintf(&b, "<Tag><Key>%s</Key><Value>%s</Value></Tag>", p[0], p[1])
	}
	b.WriteString(`</TagSet></Tagging>`)
	return b.String()
}

// ---- The bootstrap flow ----------------------------------------------------

func TestCDKBootstrapBucket_flow(t *testing.T) {
	// Given: the staging bucket `cdk bootstrap` creates
	srv := helpers.NewTestServer(t)
	const bucket = "cdk-hnb659fds-assets-000000000000-us-east-1"
	createBucket(t, srv, bucket)

	// When: the bootstrap stack applies each of its bucket properties
	steps := []struct {
		name        string
		subresource string
		body        string
		wantStatus  int
	}{
		{
			name:        "VersioningConfiguration",
			subresource: "versioning",
			body: `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
				`<Status>Enabled</Status></VersioningConfiguration>`,
			wantStatus: http.StatusOK,
		},
		{
			name:        "BucketEncryption",
			subresource: "encryption",
			body: `<ServerSideEncryptionConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
				`<Rule><ApplyServerSideEncryptionByDefault><SSEAlgorithm>AES256</SSEAlgorithm>` +
				`</ApplyServerSideEncryptionByDefault><BucketKeyEnabled>true</BucketKeyEnabled></Rule>` +
				`</ServerSideEncryptionConfiguration>`,
			wantStatus: http.StatusOK,
		},
		{
			name:        "LifecycleConfiguration",
			subresource: "lifecycle",
			body: `<LifecycleConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
				`<Rule><ID>CleanupOldVersions</ID><Status>Enabled</Status><Filter><Prefix></Prefix></Filter>` +
				`<NoncurrentVersionExpiration><NoncurrentDays>30</NoncurrentDays>` +
				`</NoncurrentVersionExpiration></Rule></LifecycleConfiguration>`,
			wantStatus: http.StatusOK,
		},
		{
			name:        "BucketPolicy",
			subresource: "policy",
			body:        bootstrapBucketPolicy,
			wantStatus:  http.StatusOK,
		},
		{
			name:        "Tags",
			subresource: "tagging",
			body: taggingXML(
				[2]string{"aws-cdk:bootstrap-role", "file-publishing"},
				[2]string{"Application", "CDKToolkit"},
			),
			wantStatus: http.StatusNoContent,
		},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			resp := putBucketSubresource(t, srv, bucket, step.subresource, step.body)
			defer resp.Body.Close()
			helpers.AssertStatus(t, resp, step.wantStatus)
		})
	}

	// Then: every configuration reads back, which is what the CDK's own
	// post-deploy drift detection and `cdk deploy` re-runs depend on.
	t.Run("GetBucketVersioning", func(t *testing.T) {
		resp := getBucketSubresource(t, srv, bucket, "versioning")
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var got struct {
			Status string `xml:"Status"`
		}
		helpers.DecodeXML(t, resp, &got)
		if got.Status != "Enabled" {
			t.Errorf("Status = %q, want Enabled", got.Status)
		}
	})

	t.Run("GetBucketEncryption", func(t *testing.T) {
		resp := getBucketSubresource(t, srv, bucket, "encryption")
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var got struct {
			Rules []struct {
				SSEAlgorithm string `xml:"ApplyServerSideEncryptionByDefault>SSEAlgorithm"`
			} `xml:"Rule"`
		}
		helpers.DecodeXML(t, resp, &got)
		if len(got.Rules) != 1 || got.Rules[0].SSEAlgorithm != "AES256" {
			t.Errorf("encryption rules = %+v, want one AES256 rule", got.Rules)
		}
	})

	t.Run("GetBucketLifecycleConfiguration", func(t *testing.T) {
		resp := getBucketSubresource(t, srv, bucket, "lifecycle")
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var got struct {
			Rules []struct {
				ID            string `xml:"ID"`
				Status        string `xml:"Status"`
				NoncurrentDay int    `xml:"NoncurrentVersionExpiration>NoncurrentDays"`
			} `xml:"Rule"`
		}
		helpers.DecodeXML(t, resp, &got)
		if len(got.Rules) != 1 {
			t.Fatalf("lifecycle rules = %d, want 1", len(got.Rules))
		}
		if got.Rules[0].ID != "CleanupOldVersions" || got.Rules[0].NoncurrentDay != 30 {
			t.Errorf("rule = %+v, want CleanupOldVersions / 30 noncurrent days", got.Rules[0])
		}
	})

	t.Run("GetBucketPolicy", func(t *testing.T) {
		resp := getBucketSubresource(t, srv, bucket, "policy")
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if body := helpers.ReadBody(t, resp); !strings.Contains(body, "denyInsecureConnections") {
			t.Errorf("policy body = %q, want the stored bootstrap policy", body)
		}
	})

	t.Run("GetBucketTagging", func(t *testing.T) {
		resp := getBucketSubresource(t, srv, bucket, "tagging")
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var got struct {
			Tags []struct {
				Key   string `xml:"Key"`
				Value string `xml:"Value"`
			} `xml:"TagSet>Tag"`
		}
		helpers.DecodeXML(t, resp, &got)
		found := map[string]string{}
		for _, tag := range got.Tags {
			found[tag.Key] = tag.Value
		}
		if found["aws-cdk:bootstrap-role"] != "file-publishing" {
			t.Errorf("tags = %+v, want aws-cdk:bootstrap-role=file-publishing", found)
		}
		if found["Application"] != "CDKToolkit" {
			t.Errorf("tags = %+v, want Application=CDKToolkit", found)
		}
	})

	// And: an asset upload into the versioned, encrypted bucket works, which
	// is the only thing the bootstrap bucket exists to do.
	t.Run("PutAsset", func(t *testing.T) {
		putObject(t, srv, bucket, "assets/abc123.zip", []byte("asset"), "application/zip")
		resp, err := http.DefaultClient.Do(get(srv, "/"+bucket+"/assets/abc123.zip"))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		if got := resp.Header.Get("x-amz-version-id"); got == "" {
			t.Error("x-amz-version-id absent, want a version id from the versioned bucket")
		}
	})
}

// ---- Missing-bucket edge cases ---------------------------------------------

// Every bucket sub-resource write on a bucket that does not exist is a
// NoSuchBucket, not a silently created configuration — the failure mode a CDK
// deployment against the wrong account or region has to produce.
func TestBucketSubresources_noSuchBucket(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cases := []struct {
		name        string
		method      string
		subresource string
		body        string
	}{
		{"PutBucketVersioning", http.MethodPut, "versioning",
			`<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`},
		{"PutBucketTagging", http.MethodPut, "tagging", taggingXML([2]string{"k", "v"})},
		{"GetBucketTagging", http.MethodGet, "tagging", ""},
		{"DeleteBucketTagging", http.MethodDelete, "tagging", ""},
		{"GetBucketVersioning", http.MethodGet, "versioning", ""},
		{"DeleteBucketPolicy", http.MethodDelete, "policy", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: no bucket of that name (fresh server, nothing created)
			// When: the sub-resource is addressed
			var req *http.Request
			path := "/ghost-bucket?" + tc.subresource
			switch tc.method {
			case http.MethodPut:
				req = put(srv, path, []byte(tc.body), nil)
			case http.MethodGet:
				req = get(srv, path)
			default:
				req = del(srv, path)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			// Then: 404 NoSuchBucket
			helpers.AssertStatus(t, resp, http.StatusNotFound)
			helpers.AssertXMLError(t, resp, "NoSuchBucket")
		})
	}
}

// ---- Bucket policy edge cases ----------------------------------------------

// PutBucketPolicy takes a JSON policy document; a body that is not JSON is a
// MalformedPolicy, the error the API reference lists for the operation.
func TestPutBucketPolicy_notJSON_returnsMalformedPolicy(t *testing.T) {
	// Given: a bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "policy-bucket")

	// When: the policy body is not JSON
	resp := putBucketSubresource(t, srv, "policy-bucket", "policy", "not a policy at all")
	defer resp.Body.Close()

	// Then: 400 MalformedPolicy
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "MalformedPolicy")
}

// A second PutBucketPolicy replaces the first outright — CDK re-bootstrapping
// an account depends on the policy converging rather than accumulating.
func TestPutBucketPolicy_replacesPreviousPolicy(t *testing.T) {
	// Given: a bucket carrying the bootstrap policy
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "policy-bucket")
	first := putBucketSubresource(t, srv, "policy-bucket", "policy", bootstrapBucketPolicy)
	first.Body.Close()

	// When: a different policy is put over it
	replacement := `{"Version":"2012-10-17","Statement":[{"Sid":"replacement","Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::policy-bucket/*"}]}`
	second := putBucketSubresource(t, srv, "policy-bucket", "policy", replacement)
	defer second.Body.Close()
	helpers.AssertStatus(t, second, http.StatusOK)

	// Then: only the replacement is stored
	resp := getBucketSubresource(t, srv, "policy-bucket", "policy")
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if strings.Contains(body, "denyInsecureConnections") {
		t.Errorf("policy body still contains the first statement: %q", body)
	}
	if !strings.Contains(body, "replacement") {
		t.Errorf("policy body = %q, want the replacement statement", body)
	}
}

// ---- Bucket tagging edge cases ---------------------------------------------

// "A tag set can contain as many as 50 tags, or it can be empty." An empty
// tag set is accepted and leaves the bucket with no tag set at all, so
// GetBucketTagging answers NoSuchTagSet rather than an empty TagSet — the
// same state a bucket is in before it is ever tagged.
func TestPutBucketTagging_emptyTagSet_clearsTagSet(t *testing.T) {
	// Given: a bucket that already carries tags
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "tag-bucket")
	resp := putBucketSubresource(t, srv, "tag-bucket", "tagging", taggingXML([2]string{"Application", "CDKToolkit"}))
	resp.Body.Close()

	// When: an empty tag set is put over them
	cleared := putBucketSubresource(t, srv, "tag-bucket", "tagging", taggingXML())
	defer cleared.Body.Close()
	helpers.AssertStatus(t, cleared, http.StatusNoContent)

	// Then: the bucket has no tag set
	got := getBucketSubresource(t, srv, "tag-bucket", "tagging")
	defer got.Body.Close()
	helpers.AssertStatus(t, got, http.StatusNotFound)
	helpers.AssertXMLError(t, got, "NoSuchTagSet")
}

// "Keys must be unique within a tag set." Two Tag elements with the same Key
// are rejected rather than silently collapsed to whichever came last.
func TestPutBucketTagging_duplicateKeys_returnsInvalidTag(t *testing.T) {
	// Given: a bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "tag-bucket")

	// When: the tag set repeats a key
	resp := putBucketSubresource(t, srv, "tag-bucket", "tagging",
		taggingXML([2]string{"Application", "CDKToolkit"}, [2]string{"Application", "Other"}))
	defer resp.Body.Close()

	// Then: 400 InvalidTag
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidTag")
}

// The tag key is "a case-sensitive string that can contain 1 to 128 Unicode
// characters" — so both an empty key and an over-long one are invalid.
func TestPutBucketTagging_tagKeyOutOfRange_returnsInvalidTag(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "tag-bucket")

	cases := map[string]string{
		"empty":   "",
		"tooLong": strings.Repeat("k", 129),
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			// When: the tag key is outside 1..128 characters
			resp := putBucketSubresource(t, srv, "tag-bucket", "tagging", taggingXML([2]string{key, "v"}))
			defer resp.Body.Close()

			// Then: 400 InvalidTag
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertXMLError(t, resp, "InvalidTag")
		})
	}
}

// The tag value "can contain from 0 to 256 Unicode characters": empty is
// legal, 257 is not.
func TestPutBucketTagging_tagValueLength(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "tag-bucket")

	t.Run("emptyIsAccepted", func(t *testing.T) {
		// When: the value is empty
		resp := putBucketSubresource(t, srv, "tag-bucket", "tagging", taggingXML([2]string{"k", ""}))
		defer resp.Body.Close()
		// Then: accepted
		helpers.AssertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("tooLongIsRejected", func(t *testing.T) {
		// When: the value is 257 characters
		resp := putBucketSubresource(t, srv, "tag-bucket", "tagging",
			taggingXML([2]string{"k", strings.Repeat("v", 257)}))
		defer resp.Body.Close()
		// Then: 400 InvalidTag
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertXMLError(t, resp, "InvalidTag")
	})
}

// "A tag set can contain as many as 50 tags." The 50th is accepted and the
// 51st is not.
func TestPutBucketTagging_tagCountLimit(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "tag-bucket")

	tags := func(n int) []([2]string) {
		out := make([][2]string, 0, n)
		for i := range n {
			out = append(out, [2]string{fmt.Sprintf("key%d", i), "v"})
		}
		return out
	}

	t.Run("fiftyIsAccepted", func(t *testing.T) {
		resp := putBucketSubresource(t, srv, "tag-bucket", "tagging", taggingXML(tags(50)...))
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusNoContent)
	})

	t.Run("fiftyOneIsRejected", func(t *testing.T) {
		resp := putBucketSubresource(t, srv, "tag-bucket", "tagging", taggingXML(tags(51)...))
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertXMLError(t, resp, "BadRequest")
	})
}

// Object tagging carries the tighter limit of 10 tags per object, and the
// same key/value rules.
func TestPutObjectTagging_limits(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "tag-bucket")
	putObject(t, srv, "tag-bucket", "asset.zip", []byte("x"), "application/zip")

	tags := func(n int) []([2]string) {
		out := make([][2]string, 0, n)
		for i := range n {
			out = append(out, [2]string{fmt.Sprintf("key%d", i), "v"})
		}
		return out
	}

	t.Run("tenIsAccepted", func(t *testing.T) {
		resp, err := http.DefaultClient.Do(put(srv, "/tag-bucket/asset.zip?tagging", []byte(taggingXML(tags(10)...)), nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
	})

	t.Run("elevenIsRejected", func(t *testing.T) {
		resp, err := http.DefaultClient.Do(put(srv, "/tag-bucket/asset.zip?tagging", []byte(taggingXML(tags(11)...)), nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertXMLError(t, resp, "BadRequest")
	})

	t.Run("duplicateKeyIsRejected", func(t *testing.T) {
		resp, err := http.DefaultClient.Do(put(srv, "/tag-bucket/asset.zip?tagging",
			[]byte(taggingXML([2]string{"k", "a"}, [2]string{"k", "b"})), nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertXMLError(t, resp, "InvalidTag")
	})
}
