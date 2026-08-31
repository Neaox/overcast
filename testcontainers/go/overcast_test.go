package overcast_test

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"

	overcast "github.com/Neaox/overcast/testcontainers/go"
)

// testImage is the image under test. CI points it at the image built from the
// pull request (overcast-slim:ci); locally it defaults to the published alpha.
func testImage() string {
	if img := os.Getenv("OVERCAST_TESTCONTAINERS_IMAGE"); img != "" {
		return img
	}
	return "ghcr.io/neaox/overcast-slim:alpha"
}

func TestRun(t *testing.T) {
	ctx := context.Background()

	ctr, err := overcast.Run(ctx, testImage())
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	endpoint, err := ctr.APIEndpoint(ctx)
	require.NoError(t, err)

	resp, err := http.Get(endpoint + "/_overcast/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Equal(t, "us-east-1", ctr.Region())
	require.Equal(t, "000000000000", ctr.AccountID())

	client := s3.New(s3.Options{
		BaseEndpoint: aws.String(endpoint),
		Region:       ctr.Region(),
		Credentials:  credentials.NewStaticCredentialsProvider(ctr.AccessKey(), ctr.SecretKey(), ""),
		UsePathStyle: true,
	})

	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("testcontainers-module")})
	require.NoError(t, err)

	buckets, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	require.NoError(t, err)
	require.Len(t, buckets.Buckets, 1)
	require.Equal(t, "testcontainers-module", aws.ToString(buckets.Buckets[0].Name))
}

func TestRunReportsEffectiveRegion(t *testing.T) {
	ctx := context.Background()

	// Region() must reflect the emulator's effective config, not a hardcoded
	// default. Uses the native variable rather than the LocalStack alias
	// DEFAULT_REGION: the image bakes OVERCAST_DEFAULT_REGION as ENV, and an
	// alias disagreeing with an explicitly set native variable fails startup.
	ctr, err := overcast.Run(ctx, testImage(),
		testcontainers.WithEnv(map[string]string{"OVERCAST_DEFAULT_REGION": "eu-west-1"}))
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	require.Equal(t, "eu-west-1", ctr.Region())
}

func TestWithConsoleExposesPort(t *testing.T) {
	req := testcontainers.GenericContainerRequest{}
	require.NoError(t, overcast.WithConsole().Customize(&req))
	require.Contains(t, req.ExposedPorts, "4567/tcp")
}

func TestWithDockerSocketBindsSocket(t *testing.T) {
	req := testcontainers.GenericContainerRequest{}
	require.NoError(t, overcast.WithDockerSocket().Customize(&req))
	require.NotNil(t, req.HostConfigModifier)
}
