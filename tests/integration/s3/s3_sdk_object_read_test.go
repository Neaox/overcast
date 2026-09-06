package s3_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// The SDK half of #1704 and #1705. Both defects are only fully described from a
// client's side: an empty Metadata map on a HeadObject that returned 200, and a
// transport failure where an SDK should have raised an InvalidRange error.

// s3Client builds an AWS SDK client pointed at the test server. It uses the
// path-style addressing an httptest server's host requires, and placeholder
// credentials, which is what every SDK caller of Overcast does.
func s3Client(t *testing.T, srv *helpers.TestServer) *s3.Client {
	t.Helper()
	return s3.New(s3.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		HTTPClient:   http.DefaultClient,
	})
}

func TestSDKHeadObject_returnsUserMetadata(t *testing.T) {
	// Given: an object written through the SDK with user metadata
	srv := helpers.NewTestServer(t)
	client := s3Client(t, srv)
	ctx := context.Background()

	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("sdk-meta")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:   aws.String("sdk-meta"),
		Key:      aws.String("meta.txt"),
		Body:     bytes.NewReader([]byte("hello")),
		Metadata: map[string]string{"Foo": "bar", "second-key": "second value"},
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// When: we read the object's metadata with each verb
	getOut, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("sdk-meta"),
		Key:    aws.String("meta.txt"),
	})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer getOut.Body.Close()
	headOut, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String("sdk-meta"),
		Key:    aws.String("meta.txt"),
	})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	// Then: HeadObject reports the same Metadata map GetObject does, keyed by
	// the lower-cased names AWS stores
	want := map[string]string{"foo": "bar", "second-key": "second value"}
	assertMetadata(t, "GetObject", getOut.Metadata, want)
	assertMetadata(t, "HeadObject", headOut.Metadata, want)
}

func TestSDKGetObject_unsatisfiableRangeIsAnAPIError(t *testing.T) {
	// Given: a ten-byte object
	srv := helpers.NewTestServer(t)
	client := s3Client(t, srv)
	ctx := context.Background()

	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("sdk-range")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("sdk-range"),
		Key:    aws.String("r.txt"),
		Body:   bytes.NewReader([]byte("0123456789")),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// When: we ask for a range entirely beyond it
	_, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("sdk-range"),
		Key:    aws.String("r.txt"),
		Range:  aws.String("bytes=500-600"),
	})

	// Then: the SDK raises a modelled API error the caller can branch on,
	// rather than a transport failure with no AWS error code
	if err == nil {
		t.Fatal("expected an error for an unsatisfiable range")
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected a modelled API error, got %T: %v", err, err)
	}
	if apiErr.ErrorCode() != "InvalidRange" {
		t.Errorf("expected error code %q, got %q", "InvalidRange", apiErr.ErrorCode())
	}

	// And: the client is unharmed — the next call on the same pool succeeds
	next, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("sdk-range"),
		Key:    aws.String("r.txt"),
	})
	if err != nil {
		t.Fatalf("GetObject after the 416: %v", err)
	}
	defer next.Body.Close()
	body, err := io.ReadAll(next.Body)
	if err != nil {
		t.Fatalf("reading the body after the 416: %v", err)
	}
	if string(body) != "0123456789" {
		t.Errorf("expected the whole object back, got %q", body)
	}
}

func TestSDKGetObject_partiallySatisfiableRangeIsTruncated(t *testing.T) {
	// Given: a ten-byte object
	srv := helpers.NewTestServer(t)
	client := s3Client(t, srv)
	ctx := context.Background()

	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("sdk-partial")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("sdk-partial"),
		Key:    aws.String("r.txt"),
		Body:   bytes.NewReader([]byte("0123456789")),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// When: we ask for a range that starts inside the object and runs past it
	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("sdk-partial"),
		Key:    aws.String("r.txt"),
		Range:  aws.String("bytes=5-100"),
	})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer out.Body.Close()

	// Then: it truncates to the object's end rather than erroring
	body, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "56789" {
		t.Errorf("expected %q, got %q", "56789", body)
	}
	if got := aws.ToString(out.ContentRange); got != "bytes 5-9/10" {
		t.Errorf("expected ContentRange %q, got %q", "bytes 5-9/10", got)
	}
}

// assertMetadata compares one response's Metadata map against the expected one,
// case-insensitively on the keys — AWS lower-cases metadata names, and so does
// every SDK reading them back.
func assertMetadata(t *testing.T, operation string, got, want map[string]string) {
	t.Helper()
	lowered := make(map[string]string, len(got))
	for k, v := range got {
		lowered[strings.ToLower(k)] = v
	}
	if len(lowered) != len(want) {
		t.Errorf("%s Metadata: expected %v, got %v", operation, want, got)
		return
	}
	for k, v := range want {
		if lowered[k] != v {
			t.Errorf("%s Metadata[%q]: expected %q, got %q", operation, k, v, lowered[k])
		}
	}
}
