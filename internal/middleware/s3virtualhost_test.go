package middleware

import "testing"

func TestExtractS3BucketFromHost(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		// Path-style: no subdomain → no rewrite.
		{"localhost", ""},
		{"localhost:4566", ""},
		{"127.0.0.1", ""},
		{"127.0.0.1:4566", ""},
		{"[::1]:4566", ""},

		// Virtual-hosted: {bucket}.localhost
		{"mybucket.localhost", "mybucket"},
		{"mybucket.localhost:4566", "mybucket"},
		{"cdk-hnb659fds-assets-050972292291-ap-southeast-2.localhost", "cdk-hnb659fds-assets-050972292291-ap-southeast-2"},
		{"cdk-hnb659fds-assets-050972292291-ap-southeast-2.localhost:4566", "cdk-hnb659fds-assets-050972292291-ap-southeast-2"},

		// Virtual-hosted: {bucket}.s3.localhost
		{"mybucket.s3.localhost", "mybucket"},
		{"mybucket.s3.localhost:4566", "mybucket"},

		// Virtual-hosted: {bucket}.s3.{region}.localhost
		{"mybucket.s3.us-east-1.localhost", "mybucket"},
		{"mybucket.s3.ap-southeast-2.localhost:4566", "mybucket"},

		// Virtual-hosted: {bucket}.s3.amazonaws.com (standard AWS)
		{"mybucket.s3.amazonaws.com", "mybucket"},
		{"mybucket.s3.us-west-2.amazonaws.com", "mybucket"},

		// S3 service endpoint — NOT a virtual-hosted bucket request.
		// s3.localhost is the S3 service subdomain, not {bucket}.localhost.
		{"s3.localhost", ""},
		{"s3.localhost:4566", ""},
		{"s3.us-east-1.localhost", ""},
		{"s3.ap-southeast-2.localhost:4566", ""},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := extractS3BucketFromHost(tt.host)
			if got != tt.want {
				t.Errorf("extractS3BucketFromHost(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

// TestExtractS3BucketFromHost_withExtraBase covers wildcard-DNS hostnames set
// via OVERCAST_HOSTNAME (e.g. "localhost.localstack.cloud"). CDK's asset
// publisher constructs URLs like:
//
//	cdk-hnb659fds-assets-<account>-<region>.localhost.localstack.cloud:<port>
//
// which must be recognised and rewritten to path-style so the bucket is found.
func TestExtractS3BucketFromHost_withExtraBase(t *testing.T) {
	const base = "localhost.localstack.cloud"

	tests := []struct {
		host string
		want string
	}{
		// The base domain itself is not a bucket.
		{base, ""},
		{base + ":4566", ""},

		// {bucket}.{base} — plain virtual-host (SDK v3 / CDK asset publisher)
		{"mybucket." + base, "mybucket"},
		{"mybucket." + base + ":4566", "mybucket"},

		// CDK bootstrap bucket hostname — the real-world trigger for this fix
		{"cdk-hnb659fds-assets-000000000000-ap-southeast-2." + base, "cdk-hnb659fds-assets-000000000000-ap-southeast-2"},
		{"cdk-hnb659fds-assets-000000000000-ap-southeast-2." + base + ":4566", "cdk-hnb659fds-assets-000000000000-ap-southeast-2"},

		// {bucket}.s3.{base} — s3-subdomain style still handled by .s3. rule
		{"mybucket.s3." + base, "mybucket"},
		{"mybucket.s3." + base + ":4566", "mybucket"},

		// {bucket}.s3.{region}.{base}
		{"mybucket.s3.us-east-1." + base, "mybucket"},
		{"mybucket.s3.ap-southeast-2." + base + ":4566", "mybucket"},

		// Existing localhost patterns still work when an extra base is present.
		{"mybucket.localhost", "mybucket"},
		{"mybucket.localhost:4566", "mybucket"},

		// S3 service endpoint with extra base — NOT a virtual-hosted bucket.
		{"s3." + base, ""},
		{"s3." + base + ":4566", ""},
		{"s3.us-east-1." + base, ""},
		{"s3.ap-southeast-2." + base + ":4566", ""},

		// IPs still pass through untouched.
		{"127.0.0.1:4566", ""},
		{"[::1]:4566", ""},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := extractS3BucketFromHost(tt.host, base)
			if got != tt.want {
				t.Errorf("extractS3BucketFromHost(%q, %q) = %q, want %q", tt.host, base, got, tt.want)
			}
		})
	}
}
